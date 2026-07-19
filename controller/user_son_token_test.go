package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type sonTokenFixtures struct {
	adminOne *model.User
	sonOne   *model.User
	sonTwo   *model.User
}

func setupSonTokenControllerTestDB(t *testing.T) (*gorm.DB, sonTokenFixtures) {
	t.Helper()
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Enterprise{}, &model.Token{}, &model.Log{}))

	createAdmin := func(suffix string) (*model.User, *model.Enterprise) {
		admin := &model.User{
			Username:       "enterprise-admin-" + suffix,
			Password:       "hashed-password",
			DisplayName:    "Admin " + suffix,
			Role:           common.RoleCommonUser,
			Status:         common.UserStatusEnabled,
			Type:           1,
			Group:          "default",
			AffCode:        "admin-aff-" + suffix,
			EnterpriseName: "Enterprise " + suffix,
		}
		require.NoError(t, db.Create(admin).Error)
		enterprise := &model.Enterprise{
			Name:        admin.EnterpriseName,
			OwnerUserId: admin.Id,
			Status:      model.EnterpriseStatusEnabled,
		}
		require.NoError(t, db.Create(enterprise).Error)
		admin.EnterpriseId = enterprise.Id
		require.NoError(t, db.Model(admin).Update("enterprise_id", enterprise.Id).Error)
		return admin, enterprise
	}

	adminOne, enterpriseOne := createAdmin("one")
	adminTwo, enterpriseTwo := createAdmin("two")
	createSon := func(suffix string, admin *model.User, enterprise *model.Enterprise) *model.User {
		son := &model.User{
			Username:       "enterprise-son-" + suffix,
			Password:       "hashed-password",
			DisplayName:    "Son " + suffix,
			Role:           common.RoleCommonUser,
			Status:         common.UserStatusEnabled,
			Type:           2,
			Topid:          admin.Id,
			EnterpriseId:   enterprise.Id,
			EnterpriseName: enterprise.Name,
			Group:          "default",
			AffCode:        "son-aff-" + suffix,
		}
		require.NoError(t, db.Create(son).Error)
		return son
	}

	return db, sonTokenFixtures{
		adminOne: adminOne,
		sonOne:   createSon("one", adminOne, enterpriseOne),
		sonTwo:   createSon("two", adminTwo, enterpriseTwo),
	}
}

func setSonTokenParams(ctx *gin.Context, sonId int, tokenId int) {
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(sonId)}}
	if tokenId > 0 {
		ctx.Params = append(ctx.Params, gin.Param{Key: "token_id", Value: strconv.Itoa(tokenId)})
	}
}

func TestEnterpriseSonTokenEndpointsRejectCrossEnterpriseAccess(t *testing.T) {
	db, fixtures := setupSonTokenControllerTestDB(t)
	foreign := seedToken(t, db, fixtures.sonTwo.Id, "foreign-token", "foreign-secret-token-key")

	listCtx, listRecorder := newAuthenticatedContext(t, http.MethodGet, "/api/user/son/2/tokens", nil, fixtures.adminOne.Id)
	setSonTokenParams(listCtx, fixtures.sonTwo.Id, 0)
	GetSonTokens(listCtx)
	require.Equal(t, http.StatusForbidden, listRecorder.Code)
	assert.NotContains(t, listRecorder.Body.String(), foreign.Key)

	createCtx, createRecorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/son/2/tokens", map[string]any{
		"name":            "unauthorized-create",
		"remain_quota":    100,
		"unlimited_quota": false,
	}, fixtures.adminOne.Id)
	setSonTokenParams(createCtx, fixtures.sonTwo.Id, 0)
	CreateSonToken(createCtx)
	require.Equal(t, http.StatusForbidden, createRecorder.Code)

	updateCtx, updateRecorder := newAuthenticatedContext(t, http.MethodPatch, "/api/user/son/2/tokens/1", map[string]any{
		"remain_quota": 999,
	}, fixtures.adminOne.Id)
	setSonTokenParams(updateCtx, fixtures.sonTwo.Id, foreign.Id)
	UpdateSonToken(updateCtx)
	require.Equal(t, http.StatusForbidden, updateRecorder.Code)
	assert.NotContains(t, updateRecorder.Body.String(), foreign.Key)

	deleteCtx, deleteRecorder := newAuthenticatedContext(t, http.MethodDelete, "/api/user/son/2/tokens/1", nil, fixtures.adminOne.Id)
	setSonTokenParams(deleteCtx, fixtures.sonTwo.Id, foreign.Id)
	DeleteSonToken(deleteCtx)
	require.Equal(t, http.StatusForbidden, deleteRecorder.Code)
	assert.NotContains(t, deleteRecorder.Body.String(), foreign.Key)

	var unchanged model.Token
	require.NoError(t, db.First(&unchanged, foreign.Id).Error)
	assert.Equal(t, foreign.RemainQuota, unchanged.RemainQuota)
}

func TestEnterpriseSonTokenFiniteLifecycle(t *testing.T) {
	db, fixtures := setupSonTokenControllerTestDB(t)

	createCtx, createRecorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/son/1/tokens", map[string]any{
		"name":            "workbuddy-user-token",
		"remain_quota":    1234,
		"unlimited_quota": false,
		"model_limits":    []string{"team-gpt", "team-deepseek"},
	}, fixtures.adminOne.Id)
	setSonTokenParams(createCtx, fixtures.sonOne.Id, 0)
	CreateSonToken(createCtx)
	createResponse := decodeAPIResponse(t, createRecorder)
	require.True(t, createResponse.Success, createRecorder.Body.String())
	assert.Contains(t, createRecorder.Header().Get("Cache-Control"), "no-store")
	var created SonTokenResponse
	require.NoError(t, common.Unmarshal(createResponse.Data, &created))
	require.NotEmpty(t, created.Key)

	var stored model.Token
	require.NoError(t, db.First(&stored, created.Id).Error)
	assert.False(t, stored.UnlimitedQuota)
	assert.Equal(t, 1234, stored.RemainQuota)
	assert.True(t, stored.ModelLimitsEnabled)

	listCtx, listRecorder := newAuthenticatedContext(t, http.MethodGet, "/api/user/son/1/tokens", nil, fixtures.adminOne.Id)
	setSonTokenParams(listCtx, fixtures.sonOne.Id, 0)
	GetSonTokens(listCtx)
	assert.NotContains(t, listRecorder.Body.String(), created.Key)
	assert.NotContains(t, listRecorder.Body.String(), `"key"`)

	expiresAt := time.Now().Add(time.Hour).Unix()
	updateCtx, updateRecorder := newAuthenticatedContext(t, http.MethodPatch, fmt.Sprintf("/api/user/son/%d/tokens/%d", fixtures.sonOne.Id, stored.Id), map[string]any{
		"status":          common.TokenStatusDisabled,
		"remain_quota":    2222,
		"unlimited_quota": false,
		"expired_time":    expiresAt,
		"model_limits":    []string{"team-gpt"},
	}, fixtures.adminOne.Id)
	setSonTokenParams(updateCtx, fixtures.sonOne.Id, stored.Id)
	UpdateSonToken(updateCtx)
	updateResponse := decodeAPIResponse(t, updateRecorder)
	require.True(t, updateResponse.Success, updateRecorder.Body.String())
	assert.NotContains(t, updateRecorder.Body.String(), created.Key)
	assert.NotContains(t, updateRecorder.Body.String(), `"key"`)
	require.NoError(t, db.First(&stored, stored.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, stored.Status)
	assert.Equal(t, 2222, stored.RemainQuota)
	assert.Equal(t, expiresAt, stored.ExpiredTime)
	assert.Equal(t, "team-gpt", stored.ModelLimits)

	deleteCtx, deleteRecorder := newAuthenticatedContext(t, http.MethodDelete, fmt.Sprintf("/api/user/son/%d/tokens/%d", fixtures.sonOne.Id, stored.Id), nil, fixtures.adminOne.Id)
	setSonTokenParams(deleteCtx, fixtures.sonOne.Id, stored.Id)
	DeleteSonToken(deleteCtx)
	deleteResponse := decodeAPIResponse(t, deleteRecorder)
	require.True(t, deleteResponse.Success, deleteRecorder.Body.String())
	assert.NotContains(t, deleteRecorder.Body.String(), created.Key)
	require.ErrorIs(t, db.First(&model.Token{}, stored.Id).Error, gorm.ErrRecordNotFound)
}

func TestEnterpriseSonTokenSupportsExplicitDenyAllModelLimits(t *testing.T) {
	db, fixtures := setupSonTokenControllerTestDB(t)

	createCtx, createRecorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/son/1/tokens", map[string]any{
		"name":                 "workbuddy-deny-all",
		"remain_quota":         1234,
		"unlimited_quota":      false,
		"model_limits":         []string{},
		"model_limits_enabled": true,
	}, fixtures.adminOne.Id)
	setSonTokenParams(createCtx, fixtures.sonOne.Id, 0)
	CreateSonToken(createCtx)
	createResponse := decodeAPIResponse(t, createRecorder)
	require.True(t, createResponse.Success, createRecorder.Body.String())

	var created SonTokenResponse
	require.NoError(t, common.Unmarshal(createResponse.Data, &created))
	var stored model.Token
	require.NoError(t, db.First(&stored, created.Id).Error)
	assert.True(t, stored.ModelLimitsEnabled)
	assert.Empty(t, stored.ModelLimits)

	disableCtx, disableRecorder := newAuthenticatedContext(t, http.MethodPatch, fmt.Sprintf("/api/user/son/%d/tokens/%d", fixtures.sonOne.Id, stored.Id), map[string]any{
		"model_limits_enabled": false,
	}, fixtures.adminOne.Id)
	setSonTokenParams(disableCtx, fixtures.sonOne.Id, stored.Id)
	UpdateSonToken(disableCtx)
	disableResponse := decodeAPIResponse(t, disableRecorder)
	require.True(t, disableResponse.Success, disableRecorder.Body.String())
	require.NoError(t, db.First(&stored, stored.Id).Error)
	assert.False(t, stored.ModelLimitsEnabled)
	assert.Empty(t, stored.ModelLimits)
}

func TestEnterpriseSonTokenRejectsDisabledNonEmptyModelLimits(t *testing.T) {
	db, fixtures := setupSonTokenControllerTestDB(t)
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/son/1/tokens", map[string]any{
		"name":                 "invalid-workbuddy-token",
		"remain_quota":         1234,
		"unlimited_quota":      false,
		"model_limits":         []string{"corp-chat"},
		"model_limits_enabled": false,
	}, fixtures.adminOne.Id)
	setSonTokenParams(ctx, fixtures.sonOne.Id, 0)
	CreateSonToken(ctx)
	response := decodeAPIResponse(t, recorder)
	require.False(t, response.Success, recorder.Body.String())

	var count int64
	require.NoError(t, db.Model(&model.Token{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestCreateSonLegacyRequestAlwaysCreatesFiniteDefaultToken(t *testing.T) {
	db, fixtures := setupSonTokenControllerTestDB(t)
	oldQuota := constant.SubaccountDefaultTokenQuota
	oldGenerateDefault := constant.GenerateDefaultToken
	constant.SubaccountDefaultTokenQuota = 777
	constant.GenerateDefaultToken = true
	t.Cleanup(func() {
		constant.SubaccountDefaultTokenQuota = oldQuota
		constant.GenerateDefaultToken = oldGenerateDefault
	})

	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/createSon", map[string]any{
		"username":     "legacy-son-request",
		"password":     "password123",
		"display_name": "Legacy Son",
	}, fixtures.adminOne.Id)
	CreateSonUser(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, recorder.Body.String())
	assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")

	var child model.User
	require.NoError(t, db.First(&child, "username = ?", "legacy-son-request").Error)
	assert.Zero(t, child.Quota)
	tokens, err := model.GetAllUserTokens(child.Id, 0, 10)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	assert.False(t, tokens[0].UnlimitedQuota)
	assert.Equal(t, 777, tokens[0].RemainQuota)
}

func TestCreateSonAcceptsExplicitTokenConfiguration(t *testing.T) {
	db, fixtures := setupSonTokenControllerTestDB(t)
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/createSon", map[string]any{
		"username":           "configured-son",
		"password":           "password123",
		"token_name":         "configured-token",
		"token_quota":        4321,
		"token_unlimited":    false,
		"token_model_limits": []string{"team-gpt", "team-deepseek"},
	}, fixtures.adminOne.Id)
	CreateSonUser(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, recorder.Body.String())
	assert.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")

	var data struct {
		UserId int              `json:"user_id"`
		Token  SonTokenResponse `json:"token"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &data))
	require.NotZero(t, data.UserId)
	require.NotEmpty(t, data.Token.Key)

	var stored model.Token
	require.NoError(t, db.First(&stored, data.Token.Id).Error)
	assert.Equal(t, data.UserId, stored.UserId)
	assert.Equal(t, "configured-token", stored.Name)
	assert.Equal(t, 4321, stored.RemainQuota)
	assert.False(t, stored.UnlimitedQuota)
	assert.True(t, stored.ModelLimitsEnabled)
	assert.Equal(t, "team-gpt,team-deepseek", stored.ModelLimits)
}

func TestCreateSonRejectsUnlimitedInitialToken(t *testing.T) {
	db, fixtures := setupSonTokenControllerTestDB(t)
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/createSon", map[string]any{
		"username":        "unlimited-initial-token",
		"password":        "password123",
		"token_unlimited": true,
	}, fixtures.adminOne.Id)

	CreateSonUser(ctx)
	response := decodeAPIResponse(t, recorder)
	require.False(t, response.Success)

	var count int64
	require.NoError(t, db.Model(&model.User{}).Where("username = ?", "unlimited-initial-token").Count(&count).Error)
	assert.Zero(t, count)
}

func TestCreateSonRejectsInvalidInitialTokenInputAsClientError(t *testing.T) {
	tests := []struct {
		name     string
		username string
		token    map[string]any
	}{
		{name: "zero quota", username: "zero-quota-son", token: map[string]any{"token_quota": 0}},
		{name: "negative quota", username: "negative-quota-son", token: map[string]any{"token_quota": -1}},
		{name: "quota overflow", username: "overflow-quota-son", token: map[string]any{"token_quota": int64(maxEnterpriseTokenQuota) + 1}},
		{name: "long token name", username: "long-token-name-son", token: map[string]any{"token_name": strings.Repeat("t", 51)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, fixtures := setupSonTokenControllerTestDB(t)
			payload := map[string]any{
				"username": test.username,
				"password": "password123",
			}
			for key, value := range test.token {
				payload[key] = value
			}

			ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/createSon", payload, fixtures.adminOne.Id)
			CreateSonUser(ctx)
			response := decodeAPIResponse(t, recorder)
			require.False(t, response.Success)
			assert.NotContains(t, strings.ToLower(response.Message), "database")
			assert.NotContains(t, response.Message, "数据库")

			var userCount int64
			require.NoError(t, db.Model(&model.User{}).Where("username = ?", test.username).Count(&userCount).Error)
			assert.Zero(t, userCount)

			var tokenCount int64
			require.NoError(t, db.Model(&model.Token{}).Count(&tokenCount).Error)
			assert.Zero(t, tokenCount)
		})
	}
}

func TestCreateSonValidatesUserFieldLengths(t *testing.T) {
	tests := []struct {
		name        string
		username    string
		password    string
		displayName string
	}{
		{name: "username", username: strings.Repeat("u", 21), password: "password123", displayName: "valid"},
		{name: "password", username: "long-password-son", password: strings.Repeat("p", 21), displayName: "valid"},
		{name: "display name", username: "long-display-son", password: "password123", displayName: strings.Repeat("d", 21)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, fixtures := setupSonTokenControllerTestDB(t)
			ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/createSon", map[string]any{
				"username":     test.username,
				"password":     test.password,
				"display_name": test.displayName,
			}, fixtures.adminOne.Id)

			CreateSonUser(ctx)
			response := decodeAPIResponse(t, recorder)
			require.False(t, response.Success)

			var count int64
			require.NoError(t, db.Model(&model.User{}).Where("username = ?", test.username).Count(&count).Error)
			assert.Zero(t, count)
		})
	}
}

func TestEnterpriseSonTokenDatabaseErrorsAreSanitized(t *testing.T) {
	db, fixtures := setupSonTokenControllerTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/user/son/1/tokens", nil, fixtures.adminOne.Id)
	setSonTokenParams(ctx, fixtures.sonOne.Id, 0)
	GetSonTokens(ctx)

	response := decodeAPIResponse(t, recorder)
	require.False(t, response.Success, recorder.Body.String())
	assert.NotContains(t, strings.ToLower(response.Message), "sql")
	assert.NotContains(t, strings.ToLower(response.Message), "closed")
}
