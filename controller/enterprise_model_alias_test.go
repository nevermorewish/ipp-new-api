package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpsertEnterpriseModelAliasDerivesOwnerAndIsIdempotent(t *testing.T) {
	db := newControllerEnterpriseAliasTestDB(t)
	configureControllerEnterpriseAliasPricing(t, `{"real-model":1}`)
	createControllerEnterpriseAliasAbility(t, db, "real-model")

	body := `{"owner_user_id":999,"alias":"corp-chat","upstream_model_id":"real-model","expected_version":0}`
	first := performEnterpriseAliasControllerRequest(t, http.MethodPut, "/api/user/workbuddy/model-aliases/mdl_stable", body, 101)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	firstResponse := decodeEnterpriseAliasControllerResponse(t, first)
	require.True(t, firstResponse.Success)
	require.Equal(t, 101, firstResponse.Data.OwnerUserID)
	require.Equal(t, "mdl_stable", firstResponse.Data.SourceID)
	require.EqualValues(t, 1, firstResponse.Data.Version)

	retry := performEnterpriseAliasControllerRequest(t, http.MethodPut, "/api/user/workbuddy/model-aliases/mdl_stable", body, 101)
	require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
	retryResponse := decodeEnterpriseAliasControllerResponse(t, retry)
	require.True(t, retryResponse.Success)
	require.Equal(t, firstResponse.Data.ID, retryResponse.Data.ID)
	require.EqualValues(t, 1, retryResponse.Data.Version)

	var forgedOwnerRows int64
	require.NoError(t, db.Model(&model.EnterpriseModelAlias{}).Where("owner_user_id = ?", 999).Count(&forgedOwnerRows).Error)
	require.Zero(t, forgedOwnerRows)
}

func TestUpsertEnterpriseModelAliasRejectsUnpricedOrImmutableRoutes(t *testing.T) {
	db := newControllerEnterpriseAliasTestDB(t)
	configureControllerEnterpriseAliasPricing(t, `{"priced-model":1}`)
	createControllerEnterpriseAliasAbility(t, db, "priced-model")
	createControllerEnterpriseAliasAbility(t, db, "unpriced-model")

	unpriced := performEnterpriseAliasControllerRequest(t, http.MethodPut, "/api/user/workbuddy/model-aliases/mdl_unpriced", `{"alias":"corp-unpriced","upstream_model_id":"unpriced-model"}`, 101)
	require.Equal(t, http.StatusBadRequest, unpriced.Code, unpriced.Body.String())
	require.NotContains(t, unpriced.Body.String(), "ModelRatio")

	created := performEnterpriseAliasControllerRequest(t, http.MethodPut, "/api/user/workbuddy/model-aliases/mdl_priced", `{"alias":"corp-chat","upstream_model_id":"priced-model"}`, 101)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	changed := performEnterpriseAliasControllerRequest(t, http.MethodPut, "/api/user/workbuddy/model-aliases/mdl_priced", `{"alias":"corp-chat-v2","upstream_model_id":"priced-model","expected_version":1}`, 101)
	require.Equal(t, http.StatusConflict, changed.Code, changed.Body.String())
	require.Contains(t, changed.Body.String(), "cannot be changed")

	var rows int64
	require.NoError(t, db.Model(&model.EnterpriseModelAlias{}).Count(&rows).Error)
	require.EqualValues(t, 1, rows)
}

func TestDeleteEnterpriseModelAliasTombstonesAndRetriesSafely(t *testing.T) {
	db := newControllerEnterpriseAliasTestDB(t)
	configureControllerEnterpriseAliasPricing(t, `{"real-model":1}`)
	createControllerEnterpriseAliasAbility(t, db, "real-model")
	created := performEnterpriseAliasControllerRequest(t, http.MethodPut, "/api/user/workbuddy/model-aliases/mdl_delete", `{"alias":"corp-chat","upstream_model_id":"real-model"}`, 101)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	missingVersion := performEnterpriseAliasControllerRequest(t, http.MethodDelete, "/api/user/workbuddy/model-aliases/mdl_delete", `{}`, 101)
	require.Equal(t, http.StatusConflict, missingVersion.Code, missingVersion.Body.String())

	wrongOwner := performEnterpriseAliasControllerRequest(t, http.MethodDelete, "/api/user/workbuddy/model-aliases/mdl_delete", `{"expected_version":1}`, 202)
	require.Equal(t, http.StatusForbidden, wrongOwner.Code, wrongOwner.Body.String())

	deleted := performEnterpriseAliasControllerRequest(t, http.MethodDelete, "/api/user/workbuddy/model-aliases/mdl_delete", `{"expected_version":1}`, 101)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	deletedResponse := decodeEnterpriseAliasControllerResponse(t, deleted)
	require.Equal(t, model.EnterpriseModelAliasStatusTombstone, deletedResponse.Data.Status)
	require.EqualValues(t, 2, deletedResponse.Data.Version)

	retry := performEnterpriseAliasControllerRequest(t, http.MethodDelete, "/api/user/workbuddy/model-aliases/mdl_delete", `{"expected_version":1}`, 101)
	require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
	retryResponse := decodeEnterpriseAliasControllerResponse(t, retry)
	require.EqualValues(t, 2, retryResponse.Data.Version)

	row, found, err := model.ResolveEnterpriseModelAlias(101, "corp-chat")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, model.EnterpriseModelAliasStatusTombstone, row.Status)
	var rows int64
	require.NoError(t, db.Model(&model.EnterpriseModelAlias{}).Count(&rows).Error)
	require.EqualValues(t, 1, rows)
}

func TestDeleteEnterpriseModelAliasReturnsVerifiableAbsenceForNeverCreatedSource(t *testing.T) {
	newControllerEnterpriseAliasTestDB(t)

	missing := performEnterpriseAliasControllerRequest(t, http.MethodDelete, "/api/user/workbuddy/model-aliases/mdl_never_created", `{}`, 101)
	require.Equal(t, http.StatusOK, missing.Code, missing.Body.String())
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			OwnerUserID int    `json:"owner_user_id"`
			SourceID    string `json:"source_id"`
			Status      int    `json:"status"`
			Version     uint64 `json:"version"`
			Absent      bool   `json:"absent"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(missing.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, 101, response.Data.OwnerUserID)
	require.Equal(t, "mdl_never_created", response.Data.SourceID)
	require.Equal(t, model.EnterpriseModelAliasStatusTombstone, response.Data.Status)
	require.Zero(t, response.Data.Version)
	require.True(t, response.Data.Absent)
}

func TestGetEnterpriseModelAliasReconcilesCurrentTombstoneAndAbsenceWithoutAudit(t *testing.T) {
	db := newControllerEnterpriseAliasTestDB(t)
	configureControllerEnterpriseAliasPricing(t, `{"real-model":1}`)
	createControllerEnterpriseAliasAbility(t, db, "real-model")
	require.NoError(t, db.Create(&model.User{Id: 101, Username: "enterprise-owner"}).Error)

	created := performEnterpriseAliasControllerRequest(t, http.MethodPut, "/api/user/workbuddy/model-aliases/mdl_reconcile", `{"alias":"corp-chat","upstream_model_id":"real-model"}`, 101)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	current := performEnterpriseAliasControllerRequest(t, http.MethodGet, "/api/user/workbuddy/model-aliases/mdl_reconcile", "", 101)
	require.Equal(t, http.StatusOK, current.Code, current.Body.String())
	require.Contains(t, current.Header().Get("Cache-Control"), "no-store")
	currentResponse := decodeEnterpriseAliasControllerResponse(t, current)
	require.Equal(t, 101, currentResponse.Data.OwnerUserID)
	require.Equal(t, "mdl_reconcile", currentResponse.Data.SourceID)
	require.Equal(t, "corp-chat", currentResponse.Data.Alias)
	require.Equal(t, "real-model", currentResponse.Data.UpstreamModelID)
	require.Equal(t, model.EnterpriseModelAliasStatusActive, currentResponse.Data.Status)
	require.EqualValues(t, 1, currentResponse.Data.Version)

	deleted := performEnterpriseAliasControllerRequest(t, http.MethodDelete, "/api/user/workbuddy/model-aliases/mdl_reconcile", `{"expected_version":1}`, 101)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	tombstone := performEnterpriseAliasControllerRequest(t, http.MethodGet, "/api/user/workbuddy/model-aliases/mdl_reconcile", "", 101)
	require.Equal(t, http.StatusOK, tombstone.Code, tombstone.Body.String())
	tombstoneResponse := decodeEnterpriseAliasControllerResponse(t, tombstone)
	require.Equal(t, model.EnterpriseModelAliasStatusTombstone, tombstoneResponse.Data.Status)
	require.EqualValues(t, 2, tombstoneResponse.Data.Version)

	for _, testCase := range []struct {
		name    string
		ownerID int
		source  string
	}{
		{name: "other owner", ownerID: 202, source: "mdl_reconcile"},
		{name: "never created", ownerID: 101, source: "mdl_never_created"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			missing := performEnterpriseAliasControllerRequest(t, http.MethodGet, "/api/user/workbuddy/model-aliases/"+testCase.source, "", testCase.ownerID)
			require.Equal(t, http.StatusOK, missing.Code, missing.Body.String())
			var response struct {
				Success bool `json:"success"`
				Data    struct {
					OwnerUserID int    `json:"owner_user_id"`
					SourceID    string `json:"source_id"`
					Status      int    `json:"status"`
					Version     uint64 `json:"version"`
					Absent      bool   `json:"absent"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(missing.Body.Bytes(), &response))
			require.True(t, response.Success)
			require.Equal(t, testCase.ownerID, response.Data.OwnerUserID)
			require.Equal(t, testCase.source, response.Data.SourceID)
			require.Equal(t, model.EnterpriseModelAliasStatusTombstone, response.Data.Status)
			require.Zero(t, response.Data.Version)
			require.True(t, response.Data.Absent)
			require.NotContains(t, missing.Body.String(), "corp-chat")
			require.NotContains(t, missing.Body.String(), "real-model")
		})
	}

	invalid := performEnterpriseAliasControllerRequest(t, http.MethodGet, "/api/user/workbuddy/model-aliases/unsafe%20source", "", 101)
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	var logs int64
	require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", 101, model.LogTypeManage).Count(&logs).Error)
	require.EqualValues(t, 2, logs)
}

func TestEnterpriseModelAliasMutationsWriteOperationAuditLogs(t *testing.T) {
	db := newControllerEnterpriseAliasTestDB(t)
	configureControllerEnterpriseAliasPricing(t, `{"real-model":1}`)
	createControllerEnterpriseAliasAbility(t, db, "real-model")
	require.NoError(t, db.Create(&model.User{Id: 101, Username: "enterprise-owner"}).Error)

	created := performEnterpriseAliasControllerRequest(t, http.MethodPut, "/api/user/workbuddy/model-aliases/mdl_audit", `{"alias":"corp-chat","upstream_model_id":"real-model"}`, 101)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	deleted := performEnterpriseAliasControllerRequest(t, http.MethodDelete, "/api/user/workbuddy/model-aliases/mdl_audit", `{"expected_version":1}`, 101)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())

	var logs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", 101, model.LogTypeManage).Order("id asc").Find(&logs).Error)
	require.Len(t, logs, 2)
	require.Contains(t, logs[0].Other, "enterprise_model_alias.upsert")
	require.Contains(t, logs[0].Other, "mdl_audit")
	require.Contains(t, logs[1].Other, "enterprise_model_alias.delete")
	require.Contains(t, logs[1].Other, "mdl_audit")
	for _, log := range logs {
		var other struct {
			AdminInfo map[string]interface{} `json:"admin_info"`
			AuditInfo map[string]interface{} `json:"audit_info"`
		}
		require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
		require.EqualValues(t, 101, other.AdminInfo["admin_id"])
		require.Equal(t, "enterprise-owner", other.AdminInfo["admin_username"])
		require.Equal(t, "access_token", other.AdminInfo["auth_method"])
		require.Equal(t, "success", other.AuditInfo["result"])
	}
}

type enterpriseAliasControllerResponse struct {
	Success bool                       `json:"success"`
	Message string                     `json:"message"`
	Data    model.EnterpriseModelAlias `json:"data"`
}

func decodeEnterpriseAliasControllerResponse(t *testing.T, recorder *httptest.ResponseRecorder) enterpriseAliasControllerResponse {
	t.Helper()
	var response enterpriseAliasControllerResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func performEnterpriseAliasControllerRequest(t *testing.T, method string, target string, body string, ownerUserID int) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "source_id", Value: strings.TrimPrefix(target, "/api/user/workbuddy/model-aliases/")}}
	ctx.Set("id", ownerUserID)
	ctx.Set("username", "enterprise-owner")
	ctx.Set("role", common.RoleCommonUser)
	ctx.Set("use_access_token", true)
	switch method {
	case http.MethodGet:
		GetEnterpriseModelAlias(ctx)
	case http.MethodDelete:
		DeleteEnterpriseModelAlias(ctx)
	default:
		UpsertEnterpriseModelAlias(ctx)
	}
	return recorder
}

func newControllerEnterpriseAliasTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	dsn := fmt.Sprintf("file:controller-enterprise-model-alias-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Ability{}, &model.EnterpriseModelAlias{}, &model.Log{}))
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.RedisEnabled = previousRedisEnabled
	})
	return db
}

func createControllerEnterpriseAliasAbility(t *testing.T, db *gorm.DB, modelName string) {
	t.Helper()
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     modelName,
		ChannelId: len(modelName) + 700,
		Enabled:   true,
	}).Error)
}

func configureControllerEnterpriseAliasPricing(t *testing.T, value string) {
	t.Helper()
	original := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(value))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(original)) })
}
