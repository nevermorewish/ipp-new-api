package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEnterpriseModelErrorLogKeepsRequestedAliasAndBillingModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyRequestedModel, "corp-chat")
	other := map[string]interface{}{}

	logModel := enterpriseModelLogName(ctx, "canonical-model", other)

	require.Equal(t, "corp-chat", logModel)
	require.Equal(t, "corp-chat", other["requested_model_name"])
	require.Equal(t, "canonical-model", other["billing_model_name"])
}

func TestEnterpriseModelErrorLogLeavesOrdinaryModelsUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	other := map[string]interface{}{}

	require.Equal(t, "canonical-model", enterpriseModelLogName(ctx, "canonical-model", other))
	require.Empty(t, other)
}
