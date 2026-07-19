package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEnterpriseModelAliasRouteEnforcesRealAuthenticationChain(t *testing.T) {
	db := setupEnterpriseAliasRouteTestDB(t)
	configureEnterpriseAliasRoutePricing(t, `{"real-model":1}`)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "real-model", ChannelId: 700, Enabled: true}).Error)

	owner := createEnterpriseAliasRouteUser(t, db, 101, "route-owner", 1, 1, "owner-route-token")
	require.NoError(t, db.Create(&model.Enterprise{Id: 1, Name: "Route Enterprise", OwnerUserId: owner.Id, Status: model.EnterpriseStatusEnabled}).Error)
	normal := createEnterpriseAliasRouteUser(t, db, 102, "route-normal", 0, 0, "normal-route-token")
	sub := createEnterpriseAliasRouteUser(t, db, 103, "route-sub", 2, 1, "sub-route-token")
	disabledOwner := createEnterpriseAliasRouteUser(t, db, 104, "route-disabled-owner", 1, 2, "disabled-route-token")
	require.NoError(t, db.Create(&model.Enterprise{Id: 2, Name: "Disabled Route Enterprise", OwnerUserId: disabledOwner.Id, Status: model.EnterpriseStatusDisabled}).Error)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("enterprise-alias-route-test-secret"))))
	SetApiRouter(engine)

	tests := []struct {
		name       string
		user       *model.User
		headerID   int
		sourceID   string
		wantStatus int
	}{
		{name: "enterprise owner", user: owner, headerID: owner.Id, sourceID: "mdl_route_owner", wantStatus: http.StatusOK},
		{name: "normal user", user: normal, headerID: normal.Id, sourceID: "mdl_route_normal", wantStatus: http.StatusForbidden},
		{name: "sub account", user: sub, headerID: sub.Id, sourceID: "mdl_route_sub", wantStatus: http.StatusForbidden},
		{name: "forged user header", user: owner, headerID: normal.Id, sourceID: "mdl_route_forged", wantStatus: http.StatusUnauthorized},
		{name: "disabled enterprise", user: disabledOwner, headerID: disabledOwner.Id, sourceID: "mdl_route_disabled", wantStatus: http.StatusForbidden},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPut,
				"/api/user/workbuddy/model-aliases/"+testCase.sourceID,
				strings.NewReader(`{"alias":"corp-`+testCase.sourceID+`","upstream_model_id":"real-model","expected_version":0}`),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+testCase.user.GetAccessToken())
			request.Header.Set("New-Api-User", fmt.Sprintf("%d", testCase.headerID))
			recorder := httptest.NewRecorder()

			engine.ServeHTTP(recorder, request)

			require.Equal(t, testCase.wantStatus, recorder.Code, recorder.Body.String())
		})
	}

	var ownerRows int64
	require.NoError(t, db.Model(&model.EnterpriseModelAlias{}).Where("owner_user_id = ?", owner.Id).Count(&ownerRows).Error)
	require.EqualValues(t, 1, ownerRows)
}

func TestEnterpriseModelAliasGetRouteIsAuthenticatedAndTenantScoped(t *testing.T) {
	db := setupEnterpriseAliasRouteTestDB(t)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "real-model", ChannelId: 701, Enabled: true}).Error)
	owner := createEnterpriseAliasRouteUser(t, db, 201, "get-owner", 1, 11, "get-owner-token")
	otherOwner := createEnterpriseAliasRouteUser(t, db, 202, "get-other-owner", 1, 12, "get-other-owner-token")
	normal := createEnterpriseAliasRouteUser(t, db, 203, "get-normal", 0, 0, "get-normal-token")
	require.NoError(t, db.Create(&model.Enterprise{Id: 11, Name: "GET Owner Enterprise", OwnerUserId: owner.Id, Status: model.EnterpriseStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Enterprise{Id: 12, Name: "GET Other Enterprise", OwnerUserId: otherOwner.Id, Status: model.EnterpriseStatusEnabled}).Error)
	created, err := model.UpsertEnterpriseModelAlias(owner.Id, model.EnterpriseModelAliasMutation{
		SourceID:        "mdl_get_route",
		Alias:           "corp-chat",
		UpstreamModelID: "real-model",
	})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("enterprise-alias-get-route-test-secret"))))
	SetApiRouter(engine)
	request := func(user *model.User, headerID int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/user/workbuddy/model-aliases/"+created.SourceID, nil)
		if user != nil {
			req.Header.Set("Authorization", "Bearer "+user.GetAccessToken())
			req.Header.Set("New-Api-User", fmt.Sprintf("%d", headerID))
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		return recorder
	}

	current := request(owner, owner.Id)
	require.Equal(t, http.StatusOK, current.Code, current.Body.String())
	require.Contains(t, current.Header().Get("Cache-Control"), "no-store")
	var currentResponse struct {
		Success bool                       `json:"success"`
		Data    model.EnterpriseModelAlias `json:"data"`
	}
	require.NoError(t, common.Unmarshal(current.Body.Bytes(), &currentResponse))
	require.True(t, currentResponse.Success)
	require.Equal(t, created, currentResponse.Data)

	other := request(otherOwner, otherOwner.Id)
	require.Equal(t, http.StatusOK, other.Code, other.Body.String())
	var absence struct {
		Success bool `json:"success"`
		Data    struct {
			OwnerUserID int    `json:"owner_user_id"`
			SourceID    string `json:"source_id"`
			Status      int    `json:"status"`
			Version     uint64 `json:"version"`
			Absent      bool   `json:"absent"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(other.Body.Bytes(), &absence))
	require.True(t, absence.Success)
	require.Equal(t, otherOwner.Id, absence.Data.OwnerUserID)
	require.Equal(t, created.SourceID, absence.Data.SourceID)
	require.Equal(t, model.EnterpriseModelAliasStatusTombstone, absence.Data.Status)
	require.Zero(t, absence.Data.Version)
	require.True(t, absence.Data.Absent)
	require.NotContains(t, other.Body.String(), created.Alias)
	require.NotContains(t, other.Body.String(), created.UpstreamModelID)

	require.Equal(t, http.StatusForbidden, request(normal, normal.Id).Code)
	require.Equal(t, http.StatusUnauthorized, request(owner, otherOwner.Id).Code)
	require.Equal(t, http.StatusUnauthorized, request(nil, 0).Code)
	var logs int64
	require.NoError(t, db.Model(&model.Log{}).Count(&logs).Error)
	require.Zero(t, logs)
}

func setupEnterpriseAliasRouteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	dsn := fmt.Sprintf("file:enterprise-alias-route-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Enterprise{},
		&model.Ability{},
		&model.EnterpriseModelAlias{},
		&model.Log{},
	))
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.RedisEnabled = previousRedisEnabled
	})
	return db
}

func createEnterpriseAliasRouteUser(t *testing.T, db *gorm.DB, id int, username string, userType int, enterpriseID int, accessToken string) *model.User {
	t.Helper()
	user := &model.User{
		Id:           id,
		Username:     username,
		Password:     "hashed-password",
		DisplayName:  username,
		Role:         common.RoleCommonUser,
		Status:       common.UserStatusEnabled,
		Group:        "default",
		Type:         userType,
		EnterpriseId: enterpriseID,
		AffCode:      fmt.Sprintf("route-aff-%d", id),
		AccessToken:  &accessToken,
	}
	if userType == 2 {
		user.Topid = 101
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func configureEnterpriseAliasRoutePricing(t *testing.T, value string) {
	t.Helper()
	original := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(value))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(original)) })
}
