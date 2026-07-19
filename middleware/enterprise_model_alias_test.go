package middleware

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestResolveEnterpriseModelRequestUsesCurrentEnterpriseOwner(t *testing.T) {
	db := newMiddlewareEnterpriseAliasTestDB(t)
	createMiddlewareEnterpriseAlias(t, db, 101, "mdl_a", "corp-chat", "upstream-a")
	createMiddlewareEnterpriseAlias(t, db, 202, "mdl_b", "corp-chat", "upstream-b")

	t.Run("sub-account resolves through top owner", func(t *testing.T) {
		ctx := newEnterpriseAliasContext(t)
		ctx.Set("id", 1001)
		common.SetContextKey(ctx, constant.ContextKeyUserType, 2)
		common.SetContextKey(ctx, constant.ContextKeyUserTopId, 101)
		request := &ModelRequest{Model: "corp-chat"}

		require.NoError(t, resolveEnterpriseModelRequest(ctx, request))
		require.Equal(t, "upstream-a", request.Model)
		require.Equal(t, "corp-chat", common.GetContextKeyString(ctx, constant.ContextKeyRequestedModel))
	})

	t.Run("another enterprise gets its own target", func(t *testing.T) {
		ctx := newEnterpriseAliasContext(t)
		ctx.Set("id", 2001)
		common.SetContextKey(ctx, constant.ContextKeyUserType, 2)
		common.SetContextKey(ctx, constant.ContextKeyUserTopId, 202)
		request := &ModelRequest{Model: "corp-chat"}

		require.NoError(t, resolveEnterpriseModelRequest(ctx, request))
		require.Equal(t, "upstream-b", request.Model)
	})

	t.Run("enterprise owner can test the same alias", func(t *testing.T) {
		ctx := newEnterpriseAliasContext(t)
		ctx.Set("id", 101)
		common.SetContextKey(ctx, constant.ContextKeyUserType, 1)
		request := &ModelRequest{Model: "corp-chat"}

		require.NoError(t, resolveEnterpriseModelRequest(ctx, request))
		require.Equal(t, "upstream-a", request.Model)
	})
}

func TestResolveEnterpriseModelRequestLeavesUnmanagedUsersAndModelsUntouched(t *testing.T) {
	db := newMiddlewareEnterpriseAliasTestDB(t)
	createMiddlewareEnterpriseAlias(t, db, 101, "mdl_a", "corp-chat", "upstream-a")

	cases := []struct {
		name     string
		userType int
		topID    int
		model    string
	}{
		{name: "normal user", userType: 0, model: "corp-chat"},
		{name: "unmanaged model", userType: 2, topID: 101, model: "ordinary-model"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := newEnterpriseAliasContext(t)
			ctx.Set("id", 1001)
			common.SetContextKey(ctx, constant.ContextKeyUserType, testCase.userType)
			common.SetContextKey(ctx, constant.ContextKeyUserTopId, testCase.topID)
			request := &ModelRequest{Model: testCase.model}

			require.NoError(t, resolveEnterpriseModelRequest(ctx, request))
			require.Equal(t, testCase.model, request.Model)
			require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyRequestedModel))
		})
	}
}

func TestResolveEnterpriseModelRequestFailsClosedForTombstone(t *testing.T) {
	db := newMiddlewareEnterpriseAliasTestDB(t)
	row := createMiddlewareEnterpriseAlias(t, db, 101, "mdl_deleted", "corp-chat", "upstream-a")
	_, err := model.TombstoneEnterpriseModelAlias(101, row.SourceID, &row.Version)
	require.NoError(t, err)

	ctx := newEnterpriseAliasContext(t)
	ctx.Set("id", 1001)
	common.SetContextKey(ctx, constant.ContextKeyUserType, 2)
	common.SetContextKey(ctx, constant.ContextKeyUserTopId, 101)
	request := &ModelRequest{Model: "corp-chat"}

	err = resolveEnterpriseModelRequest(ctx, request)
	require.ErrorIs(t, err, errEnterpriseModelAliasInactive)
	require.Equal(t, "corp-chat", request.Model)
}

func TestResolveEnterpriseModelRequestRejectsInactiveAliasBeforeModelLimit(t *testing.T) {
	db := newMiddlewareEnterpriseAliasTestDB(t)

	for _, testCase := range []struct {
		name   string
		status int
	}{
		{name: "disabled", status: model.EnterpriseModelAliasStatusDisabled},
		{name: "tombstone", status: model.EnterpriseModelAliasStatusTombstone},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			row := createMiddlewareEnterpriseAlias(t, db, 101, "mdl_"+testCase.name, "corp-"+testCase.name, "upstream-"+testCase.name)
			require.NoError(t, db.Model(&model.EnterpriseModelAlias{}).
				Where("id = ?", row.ID).
				Update("status", testCase.status).Error)

			ctx := newEnterpriseAliasContext(t)
			ctx.Set("id", 1001)
			common.SetContextKey(ctx, constant.ContextKeyUserType, 2)
			common.SetContextKey(ctx, constant.ContextKeyUserTopId, 101)
			common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
			common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{row.Alias: true})
			request := &ModelRequest{Model: row.Alias}

			err := resolveEnterpriseModelRequest(ctx, request)

			require.ErrorIs(t, err, errEnterpriseModelAliasInactive)
			require.Equal(t, row.Alias, request.Model)
			require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyRequestedModel))
		})
	}
}

func TestResolveEnterpriseModelRequestPreservesResponsesCompactSuffix(t *testing.T) {
	db := newMiddlewareEnterpriseAliasTestDB(t)
	createMiddlewareEnterpriseAlias(t, db, 101, "mdl_compact", "corp-chat", "upstream-a")

	ctx := newEnterpriseAliasContext(t)
	ctx.Set("id", 1001)
	common.SetContextKey(ctx, constant.ContextKeyUserType, 2)
	common.SetContextKey(ctx, constant.ContextKeyUserTopId, 101)
	request := &ModelRequest{Model: ratio_setting.WithCompactModelSuffix("corp-chat")}

	require.NoError(t, resolveEnterpriseModelRequest(ctx, request))
	require.Equal(t, ratio_setting.WithCompactModelSuffix("upstream-a"), request.Model)
	require.Equal(t, "corp-chat", common.GetContextKeyString(ctx, constant.ContextKeyRequestedModel))
}

func newMiddlewareEnterpriseAliasTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	dsn := fmt.Sprintf("file:middleware-enterprise-model-alias-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "-"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(&model.Ability{}, &model.EnterpriseModelAlias{}))
	t.Cleanup(func() { model.DB = previousDB })
	return db
}

func createMiddlewareEnterpriseAlias(t *testing.T, db *gorm.DB, ownerID int, sourceID string, alias string, upstream string) model.EnterpriseModelAlias {
	t.Helper()
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     upstream,
		ChannelId: ownerID,
		Enabled:   true,
	}).Error)
	row, err := model.UpsertEnterpriseModelAlias(ownerID, model.EnterpriseModelAliasMutation{
		SourceID:        sourceID,
		Alias:           alias,
		UpstreamModelID: upstream,
	})
	require.NoError(t, err)
	return row
}

func newEnterpriseAliasContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return ctx
}
