package relay

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGPTResponsesCompatibilityOutbound(t *testing.T) {
	service.InitHttpClient()
	for _, channelType := range []int{constant.ChannelTypeOpenAIGPT, constant.ChannelTypeOpenAI} {
		for _, enabled := range []bool{false, true} {
			for _, passthrough := range []bool{false, true} {
				t.Run(fmt.Sprintf("channel_%d/enabled_%t/passthrough_%t", channelType, enabled, passthrough), func(t *testing.T) {
					var outbound []byte
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						outbound, _ = io.ReadAll(r.Body)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, `{"error":{"message":"Missing namespace for function_call 'js'. It does not exist in the default namespace.","type":"invalid_request_error"}}`)
					}))
					defer upstream.Close()
					body := `{"model":"gpt-6-astra","temperature":0,"input":[{"type":"additional_tools","tools":[{"name":"functions","tools":[{"type":"function","name":"js","parameters":{"type":"object","properties":{"code":{"type":"string","encrypted":true}}}}]}]},{"type":"function_call","id":"item_legacy","name":"js","call_id":"c1","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"ok"},{"type":"agent_message","content":[{"type":"input_text","text":"keep"},{"type":"encrypted_content","encrypted_content":"opaque"}]}],"store":false}`
					ctx := newOpenAIGPTTestContext(t, "/v1/responses", body, upstream.URL, passthrough)
					common.SetContextKey(ctx, constant.ContextKeyChannelType, channelType)
					common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-6-astra")
					common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{RemoveAzureGPTEncryption: enabled})
					var request dto.OpenAIResponsesRequest
					require.NoError(t, common.Unmarshal([]byte(body), &request))
					info := &relaycommon.RelayInfo{Request: &request, OriginModelName: request.Model,
						RelayMode: relayconstant.RelayModeResponses, RelayFormat: types.RelayFormatOpenAIResponses, RequestURLPath: "/v1/responses"}
					apiErr := ResponsesHelper(ctx, info)
					require.NotNil(t, apiErr)
					require.NotEmpty(t, outbound, "must reach upstream")
					removed := channelType == constant.ChannelTypeOpenAIGPT && enabled
					require.Equal(t, !removed, gjson.GetBytes(outbound, "temperature").Exists())
					require.Equal(t, !removed, gjson.GetBytes(outbound, "input.1.id").Exists())
					require.Equal(t, "c1", gjson.GetBytes(outbound, "input.1.call_id").String())
					require.True(t, gjson.GetBytes(outbound, "input.0.tools.0.tools.0.parameters.properties.code.encrypted").Bool(), "compatibility must preserve tool schemas")
					require.Equal(t, !removed, gjson.GetBytes(outbound, "input.3.content.1.encrypted_content").Exists())
					require.Equal(t, "keep", gjson.GetBytes(outbound, "input.3.content.0.text").String())
					require.Equal(t, "ok", gjson.GetBytes(outbound, "input.2.output").String())
					require.Equal(t, "false", gjson.GetBytes(outbound, "store").Raw)
					if channelType == constant.ChannelTypeOpenAIGPT {
						require.Equal(t, "functions", gjson.GetBytes(outbound, "input.1.namespace").String())
						require.Equal(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
					} else {
						require.False(t, gjson.GetBytes(outbound, "input.1.namespace").Exists())
						require.NotEqual(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
					}
				})
			}
		}
	}
}

func TestOpenAIGPTAzureChatCompatibilityOutbound(t *testing.T) {
	service.InitHttpClient()
	for _, channelType := range []int{constant.ChannelTypeOpenAIGPT, constant.ChannelTypeOpenAI} {
		for _, enabled := range []bool{false, true} {
			for _, passthrough := range []bool{false, true} {
				t.Run(fmt.Sprintf("channel_%d/enabled_%t/passthrough_%t", channelType, enabled, passthrough), func(t *testing.T) {
					var outbound []byte
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						outbound, _ = io.ReadAll(r.Body)
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, `{"error":{"message":"test upstream reached","type":"invalid_request_error"}}`)
					}))
					defer upstream.Close()
					body := `{"model":"gpt-6-astra","temperature":0,"messages":[{"role":"user","content":[{"type":"text","text":"keep"},{"type":"encrypted_content","encrypted_content":"opaque"}]}],"tools":[{"type":"function","function":{"name":"followup_task","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true}},"additionalProperties":false}}}]}`
					ctx := newOpenAIGPTTestContext(t, "/v1/chat/completions", body, upstream.URL, passthrough)
					common.SetContextKey(ctx, constant.ContextKeyChannelType, channelType)
					common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-6-astra")
					common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{RemoveAzureGPTEncryption: enabled})
					var request dto.GeneralOpenAIRequest
					require.NoError(t, common.Unmarshal([]byte(body), &request))
					info := &relaycommon.RelayInfo{Request: &request, OriginModelName: request.Model,
						RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI, RequestURLPath: "/v1/chat/completions"}
					require.NotNil(t, TextHelper(ctx, info))
					require.NotEmpty(t, outbound, "must reach upstream")
					compat := channelType == constant.ChannelTypeOpenAIGPT && enabled
					require.Equal(t, !compat, gjson.GetBytes(outbound, "temperature").Exists())
					require.Equal(t, !compat, gjson.GetBytes(outbound, "messages.0.content.1").Exists())
					require.Equal(t, "keep", gjson.GetBytes(outbound, "messages.0.content.0.text").String())
					require.True(t, gjson.GetBytes(outbound, "tools.0.function.parameters.properties.message.encrypted").Bool())
				})
			}
		}
	}
}

func newOpenAIGPTTestContext(t *testing.T, path, body, upstreamURL string, passThrough bool) *gin.Context {
	t.Helper()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, upstreamURL)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "test-key")
	common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passThrough})
	t.Cleanup(func() { common.CleanupBodyStorage(ctx) })
	return ctx
}

func TestOpenAIGPTChatViaResponsesAppliesCompatibilityAfterOverrides(t *testing.T) {
	service.InitHttpClient()
	var outbound []byte
	var organization, outboundPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outbound, _ = io.ReadAll(r.Body)
		organization = r.Header.Get("OpenAI-Organization")
		outboundPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"test upstream reached","type":"invalid_request_error"}}`)
	}))
	defer upstream.Close()
	body := `{"model":"gpt-6-astra","temperature":0,"messages":[{"role":"user","content":[{"type":"text","text":"keep"},{"type":"encrypted_content","encrypted_content":"opaque"}]}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true}}}}}]}`
	ctx := newOpenAIGPTTestContext(t, "/v1/chat/completions", body, upstream.URL, false)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAIGPT)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-6-astra")
	common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{RemoveAzureGPTEncryption: true})
	common.SetContextKey(ctx, constant.ContextKeyChannelParamOverride, map[string]any{"temperature": 0.75})
	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal([]byte(body), &request))
	info := &relaycommon.RelayInfo{Request: &request, OriginModelName: request.Model, RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI, RequestURLPath: "/v1/chat/completions"}
	info.InitChannelMeta(ctx)
	info.Organization = "org-test"
	adaptor := GetAdaptor(info.ApiType)
	adaptor.Init(info)
	_, apiErr := textRequestViaResponses(ctx, info, adaptor, &request)
	require.NotNil(t, apiErr)
	require.NotEmpty(t, outbound)
	require.Equal(t, "org-test", organization)
	require.Equal(t, "/v1/responses", outboundPath)
	require.False(t, gjson.GetBytes(outbound, "temperature").Exists())
	require.NotContains(t, string(outbound), "opaque")
	require.Contains(t, string(outbound), "keep")
	require.True(t, gjson.GetBytes(outbound, "tools.0.parameters.properties.message.encrypted").Bool())
}

func TestOpenAIGPTCompactCompatibilityRejectsUnsafeEncryptedCompaction(t *testing.T) {
	service.InitHttpClient()
	for _, passThrough := range []bool{false, true} {
		body := `{"model":"gpt-6-astra","input":[{"type":"compaction","encrypted_content":"opaque"}]}`
		ctx := newOpenAIGPTTestContext(t, "/v1/responses/compact", body, "http://unused.invalid", passThrough)
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAIGPT)
		common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-6-astra")
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{RemoveAzureGPTEncryption: true})
		var request dto.OpenAIResponsesCompactionRequest
		require.NoError(t, common.Unmarshal([]byte(body), &request))
		info := &relaycommon.RelayInfo{Request: &request, OriginModelName: request.Model, RelayMode: relayconstant.RelayModeResponsesCompact, RelayFormat: types.RelayFormatOpenAIResponses, RequestURLPath: "/v1/responses/compact"}
		apiErr := ResponsesHelper(ctx, info)
		require.NotNil(t, apiErr)
		require.Equal(t, 400, apiErr.StatusCode)
		require.True(t, types.IsSkipRetryError(apiErr))
		require.Contains(t, apiErr.Error(), "compaction")
	}
}
