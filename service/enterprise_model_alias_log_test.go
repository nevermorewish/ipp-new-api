package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGenerateTextOtherInfoRecordsRequestedAndBillingModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		OriginModelName:    "canonical-model",
		RequestedModelName: "corp-chat",
		StartTime:          time.Now(),
		FirstResponseTime:  time.Now(),
		ChannelMeta:        &relaycommon.ChannelMeta{},
	}

	other := GenerateTextOtherInfo(ctx, info, 1, 1, 1, 0, 0, 0, 1)
	require.Equal(t, "corp-chat", other["requested_model_name"])
	require.Equal(t, "canonical-model", other["billing_model_name"])
}
