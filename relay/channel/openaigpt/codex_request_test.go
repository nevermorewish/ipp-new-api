package openaigpt

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexStreamTurnStateReachesClientAndNextRequest(t *testing.T) {
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	for _, channelType := range []int{constant.ChannelTypeOpenAIGPT, constant.ChannelTypeOpenAI} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeResponses, IsStream: true,
			RelayFormat: types.RelayFormatOpenAIResponses, OriginModelName: "gpt-6-astra", DisablePing: true}
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Codex-Turn-State": []string{"opaque-next-turn"}},
			Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))}
		if channelType == constant.ChannelTypeOpenAI {
			_, apiErr := (&openai.Adaptor{}).DoResponse(ctx, resp, info)
			require.Nil(t, apiErr)
			continue
		}
		_, apiErr := (&Adaptor{}).DoResponse(ctx, resp, info)
		require.Nil(t, apiErr)
		require.Contains(t, recorder.Body.String(), "response.completed")
		state := recorder.Result().Header.Get("X-Codex-Turn-State")
		require.Equal(t, "opaque-next-turn", state)
		ctx.Request.Header.Set("X-Codex-Turn-State", state)
		headers := make(http.Header)
		forwardCodexProtocolHeaders(ctx, &headers, info)
		require.Equal(t, state, headers.Get("X-Codex-Turn-State"))
	}
}

func TestCodexResponsesStreamOptionsSurviveDTORoundTrip(t *testing.T) {
	for _, options := range []string{
		`{"reasoning_summary_delivery":"auto"}`,
		`{"reasoning_summary_delivery":"continuous","include_obfuscation":false}`,
		`{"future_delivery":{"enabled":false,"count":0,"large_id":9007199254740993}}`,
	} {
		body := []byte(`{"model":"gpt-6-astra","input":"OK","stream_options":` + options + `}`)
		require.NoError(t, ValidateInboundResponsesBody(body))
		var request dto.OpenAIResponsesRequest
		require.NoError(t, common.Unmarshal(body, &request))
		roundTrip, err := common.Marshal(request)
		require.NoError(t, err)
		restored, err := PreserveResponsesStreamOptions(roundTrip, body)
		require.NoError(t, err)
		require.Equal(t, options, gjson.GetBytes(restored, "stream_options").Raw)
	}
	for _, options := range []string{`true`, `[]`, `{"include_obfuscation":"false"}`} {
		require.Error(t, ValidateInboundResponsesBody([]byte(`{"stream_options":`+options+`}`)))
	}
}

func TestCodexToolContinuationKeepsReferencesAndRejectsMissingContext(t *testing.T) {
	for _, input := range []string{
		`[{"type":"item_reference","id":"call_1"},{"type":"function_call_output","call_id":"call_1","output":"original output"}]`,
		`[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"original output"}]`,
	} {
		body := []byte(`{"model":"gpt-6-astra","input":` + input + `}`)
		prepared, request, _, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{})
		require.NoError(t, err)
		require.NotEmpty(t, prepared)
		require.JSONEq(t, input, string(request.Input))
	}
	_, _, _, err := PrepareResponsesBody([]byte(`{"model":"gpt-6-astra","input":[{"type":"function_call_output","call_id":"call_missing","output":"cannot invent its call"}]}`), "", dto.ChannelOtherSettings{})
	require.ErrorContains(t, err, "has no matching function_call context")
}
