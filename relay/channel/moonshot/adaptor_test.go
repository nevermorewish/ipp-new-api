package moonshot

import (
	"bytes"
	"fmt"
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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestKVVKeepsRequiredToolChoice(t *testing.T) {
	service.InitHttpClient()
	const weather = `{"type":"function","function":{"name":"get_weather","description":"Get the weather of a city.","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}`
	const question = `{"role":"user","content":"what is the weather in beijing?"}`
	// Official KVV request shapes: test_dynamic_tool_as_last_message, and the
	// test_tool_choice cases whose 400 must come from the upstream.
	for _, tc := range []struct {
		name, tools, messages string
	}{
		{"dynamic tool as last message", "", `[` + question + `,{"role":"system","content":"","tools":[` + weather + `]}]`},
		{"required without tools", "", `[` + question + `]`},
		{"required with empty tools", `"tools":[],`, `[` + question + `]`},
	} {
		for _, model := range []string{"kimi-k3", "kimi-k3.1-pro"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", tc.name, model, stream), func(t *testing.T) {
					body := fmt.Sprintf(`{"model":%q,"stream":%t,%s"messages":%s,"tool_choice":"required"}`, model, stream, tc.tools, tc.messages)
					var received []byte
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						received, _ = io.ReadAll(r.Body)
						_, _ = io.WriteString(w, `{}`)
					}))
					defer upstream.Close()
					var request dto.GeneralOpenAIRequest
					require.NoError(t, common.UnmarshalJsonStr(body, &request))
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					// KVV keeps the official contract: required stays on the wire.
					info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
						ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ChannelType: constant.ChannelTypeMoonshot,
							UpstreamModelName: model, ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: true}}}
					adaptor := &Adaptor{}
					converted, err := adaptor.ConvertOpenAIRequest(ctx, info, &request)
					require.NoError(t, err)
					wire, err := common.Marshal(converted)
					require.NoError(t, err)
					response, err := adaptor.DoRequest(ctx, info, bytes.NewReader(wire))
					require.NoError(t, err)
					require.NoError(t, response.(*http.Response).Body.Close())
					assert.Equal(t, "required", gjson.GetBytes(received, "tool_choice").String())
					assert.False(t, gjson.GetBytes(received, "tools").Exists())
					assert.Equal(t, stream, gjson.GetBytes(received, "stream").Bool())
					if dynamic := gjson.Get(body, "messages.1.tools"); dynamic.Exists() {
						assert.Equal(t, "system", gjson.GetBytes(received, "messages.1.role").String())
						assert.JSONEq(t, dynamic.Raw, gjson.GetBytes(received, "messages.1.tools").Raw)
					}
				})
			}
		}
	}
}

func TestMessagesRequestURL(t *testing.T) {
	for _, base := range []string{"https://api.moonshot.cn", "https://api.moonshot.cn/", "https://proxy.example/moonshot/"} {
		t.Run(base, func(t *testing.T) {
			url, err := (&Adaptor{}).GetRequestURL(&relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: base},
			})
			require.NoError(t, err)
			assert.Equal(t, strings.TrimRight(base, "/")+"/v1/messages", url)
		})
	}
}

func TestMessagesNativeRoundTrip(t *testing.T) {
	service.InitHttpClient()
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	const responseBody = `{"id":"msg_kimi","type":"message","role":"assistant","model":"kimi-k3","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":3,"cache_read_input_tokens":8}}`
	const streamBody = "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_kimi","type":"message","role":"assistant","model":"kimi-k3","content":[],"usage":{"input_tokens":12,"output_tokens":0,"cache_read_input_tokens":8}}}` + "\n\n" +
		"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n\n" +
		"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}` + "\n\n" +
		"event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var received []byte
			var receivedPath, authorization string
			var headers http.Header
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received, _ = io.ReadAll(r.Body)
				receivedPath, authorization = r.URL.Path, r.Header.Get("Authorization")
				headers = r.Header.Clone()
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, streamBody)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, responseBody)
			}))
			defer upstream.Close()
			body := fmt.Sprintf(`{"model":"kimi-k3","max_tokens":4096,"stream":%t,"system":"Be concise","messages":[{"role":"user","content":"Hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]}`, stream)
			var request dto.ClaudeRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &request))
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
			ctx.Request.Header.Add("anthropic-beta", "beta-one")
			ctx.Request.Header.Add("anthropic-beta", "beta-two")
			if stream {
				ctx.Request.Header.Set("anthropic-version", "2023-01-01")
			}
			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatClaude, IsStream: stream, Request: &request,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL + "/", ApiKey: "test-key", UpstreamModelName: "kimi-k3"},
			}
			adaptor := &Adaptor{}
			converted, err := adaptor.ConvertClaudeRequest(ctx, info, &request)
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			response, err := adaptor.DoRequest(ctx, info, bytes.NewReader(wire))
			require.NoError(t, err)
			assert.Equal(t, "/v1/messages", receivedPath)
			assert.Equal(t, "Bearer test-key", authorization)
			version := "2023-06-01"
			if stream {
				version = "2023-01-01"
			}
			assert.Equal(t, version, headers.Get("anthropic-version"))
			assert.Equal(t, "beta-one,beta-two", headers.Get("anthropic-beta"))
			assert.JSONEq(t, body, string(received))
			usage, apiErr := adaptor.DoResponse(ctx, response.(*http.Response), info)
			require.Nil(t, apiErr)
			require.IsType(t, &dto.Usage{}, usage)
			assert.Equal(t, 12, usage.(*dto.Usage).PromptTokens)
			assert.Equal(t, 3, usage.(*dto.Usage).CompletionTokens)
			assert.Equal(t, 8, usage.(*dto.Usage).PromptTokensDetails.CachedTokens)
			if stream {
				assert.Contains(t, recorder.Body.String(), `"text":"Hello"`)
				assert.Contains(t, recorder.Body.String(), "event: message_stop")
			} else {
				assert.JSONEq(t, responseBody, recorder.Body.String())
			}
		})
	}
}

func TestKVVRequestContract(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantError string
	}{
		{"omitted sampling", `{}`, ""},
		{"explicit defaults and zeros", `{"temperature":0,"top_p":0.95,"n":1,"presence_penalty":0,"frequency_penalty":0}`, ""},
		{"temperature upper boundary", `{"temperature":1}`, ""},
		{"temperature below range", `{"temperature":-0.1}`, "temperature"},
		{"temperature above range", `{"temperature":1.1}`, "temperature"},
		{"temperature two", `{"temperature":2}`, "temperature"},
		{"top_p", `{"top_p":0.8}`, "top_p"},
		{"presence_penalty", `{"presence_penalty":0.5}`, "presence_penalty"},
		{"frequency_penalty", `{"frequency_penalty":0.5}`, "frequency_penalty"},
		{"multiple choices", `{"n":2}`, "n must"},
		{"zero choices", `{"n":0}`, "n must"},
		{"negative choices", `{"n":-1}`, "n must"},
		{"dynamic tools", `{"messages":[{"role":"system","content":"","tools":[{"type":"function","function":{"name":"weather","strict":false,"parameters":{"type":"object"}}}]}]}`, ""},
		{"wrong role", `{"messages":[{"role":"user","content":"","tools":[]}]}`, "role system"},
		{"nonempty content", `{"messages":[{"role":"system","content":"hello","tools":[]}]}`, "non-empty content"},
		{"content blocks", `{"messages":[{"role":"system","content":[],"tools":[]}]}`, "non-empty content"},
		{"null tools", `{"messages":[{"role":"system","tools":null}]}`, "array"},
		{"object tools", `{"messages":[{"role":"system","tools":{}}]}`, "array"},
		{"null item", `{"messages":[{"role":"system","tools":[null]}]}`, "valid names"},
		{"missing type", `{"messages":[{"role":"system","tools":[{"function":{"name":"weather"}}]}]}`, "valid names"},
		{"invalid name", `{"messages":[{"role":"system","tools":[{"type":"function","function":{"name":"1bad"}}]}]}`, "valid names"},
		{"duplicate across declarations", `{"messages":[{"role":"system","tools":[{"type":"function","function":{"name":"dup"}}]},{"role":"system","tools":[{"type":"function","function":{"name":"dup"}}]}]}`, "duplicate"},
		{"duplicate global and dynamic", `{"tools":[{"type":"function","function":{"name":"dup"}}],"messages":[{"role":"system","tools":[{"type":"function","function":{"name":"dup"}}]}]}`, "duplicate"},
		{"tool result without id", `{"messages":[{"role":"tool","content":"some tool result"},{"role":"user","content":"hello"}]}`, "tool_call_id"},
		{"thinking null", `{"thinking":null}`, "thinking must"},
		{"thinking array", `{"thinking":[]}`, "thinking must"},
		{"invalid type", `{"thinking":{"type":"invalid"}}`, "thinking.type"},
	} {
		for _, thinking := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/thinking=%t", tc.name, thinking), func(t *testing.T) {
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.UnmarshalJsonStr(tc.body, &request))
				request.Model = "kimi-k3"
				request.ChatTemplateKwargs = []byte(fmt.Sprintf(`{"thinking":%t}`, thinking))
				info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3",
					ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: true},
				}}
				converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
				if tc.wantError != "" {
					var apiErr *types.NewAPIError
					require.ErrorAs(t, err, &apiErr)
					assert.Equal(t, 400, apiErr.StatusCode)
					assert.True(t, types.IsSkipRetryError(apiErr))
					assert.ErrorContains(t, err, tc.wantError)
					return
				}
				require.NoError(t, err)
				wire, err := common.Marshal(converted)
				require.NoError(t, err)
				for _, field := range []string{"temperature", "top_p", "n", "presence_penalty", "frequency_penalty"} {
					if field == "temperature" && thinking && gjson.Get(tc.body, field).Exists() {
						assert.Equal(t, 1.0, gjson.GetBytes(wire, field).Float())
						continue
					}
					assert.Equal(t, gjson.Get(tc.body, field).Raw, gjson.GetBytes(wire, field).Raw)
				}
				if tc.name == "dynamic tools" {
					assert.False(t, gjson.GetBytes(wire, "messages.0.content").Exists())
					assert.JSONEq(t, gjson.Get(tc.body, "messages.0.tools").Raw, gjson.GetBytes(wire, "messages.0.tools").Raw)
				}
			})
		}
	}
}

func TestKVVThinkingAndSwitch(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind, effort string
	}{
		{"default", `{}`, "enabled", "max"},
		{"template false", `{"chat_template_kwargs":{"thinking":false,"custom":7}}`, "disabled", "none"},
		{"reasoning none", `{"reasoning_effort":"none"}`, "disabled", "none"},
		{"reasoning low", `{"reasoning_effort":"low"}`, "enabled", "low"},
		{"minimal alias", `{"reasoning_effort":"minimal"}`, "enabled", "low"},
		{"medium alias", `{"reasoning_effort":"medium"}`, "enabled", "high"},
		{"xhigh alias", `{"reasoning_effort":"xhigh"}`, "enabled", "max"},
		{"explicit medium alias", `{"thinking":{"effort":"medium"}}`, "enabled", "high"},
		{"explicit effort wins", `{"thinking":{"type":"enabled","effort":"low"},"reasoning_effort":"max"}`, "enabled", "low"},
		{"disabled ignores effort and keep", `{"thinking":{"type":"disabled","keep":"none","effort":"high"},"reasoning_effort":"max"}`, "disabled", "none"},
		{"explicit type wins", `{"thinking":{"type":"enabled"},"chat_template_kwargs":{"thinking":false}}`, "enabled", "max"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.UnmarshalJsonStr(tc.body, &request))
				request.Model = "kimi-k3"
				before, err := common.Marshal(request)
				require.NoError(t, err)
				info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3",
					ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: enabled},
				}}
				converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
				require.NoError(t, err)
				wire, err := common.Marshal(converted)
				require.NoError(t, err)
				if !enabled {
					assert.JSONEq(t, string(before), string(wire))
					continue
				}
				assert.Equal(t, tc.kind, gjson.GetBytes(wire, "thinking.type").String())
				assert.Equal(t, tc.effort, gjson.GetBytes(wire, "reasoning_effort").String())
				if tc.kind == "enabled" {
					assert.Equal(t, tc.effort, gjson.GetBytes(wire, "thinking.effort").String())
					assert.Equal(t, "all", gjson.GetBytes(wire, "thinking.keep").String())
				} else {
					assert.False(t, gjson.GetBytes(wire, "thinking.effort").Exists())
					assert.False(t, gjson.GetBytes(wire, "thinking.keep").Exists())
				}
				if gjson.Get(tc.body, "chat_template_kwargs.thinking").Exists() {
					assert.Equal(t, tc.kind == "enabled", gjson.GetBytes(wire, "chat_template_kwargs.thinking").Bool())
				}
				assert.Equal(t, gjson.Get(tc.body, "chat_template_kwargs.custom").Raw, gjson.GetBytes(wire, "chat_template_kwargs.custom").Raw)
			}
		})
	}
}

func TestKVVScopeAndDisabledRequests(t *testing.T) {
	for _, tc := range []struct {
		name, upstream string
		channel, mode  int
		enabled        bool
	}{
		{"disabled", "kimi-k3", constant.ChannelTypeMoonshot, relayconstant.RelayModeChatCompletions, false},
		{"different model", "kimi-k2.5", constant.ChannelTypeMoonshot, relayconstant.RelayModeChatCompletions, true},
		{"different channel", "kimi-k3", constant.ChannelTypeOpenAI, relayconstant.RelayModeChatCompletions, true},
		{"native Responses", "kimi-k3", constant.ChannelTypeMoonshot, relayconstant.RelayModeResponses, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const body = `{"model":"client-alias","temperature":2,"messages":[{"role":"system","content":"","tools":[{"type":"function","function":{"name":"weather"}}]}]}`
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &request))
			info := &relaycommon.RelayInfo{RelayMode: tc.mode, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: tc.channel, UpstreamModelName: tc.upstream, ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: tc.enabled},
			}}
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.JSONEq(t, body, string(wire))
		})
	}
}

func TestKVVOpenAIClientMessageCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, messages, want string
	}{
		{"developer instructions", `[{"role":"developer","content":"Be concise"},{"role":"user","content":"hello"}]`, `[{"role":"system","content":"Be concise"},{"role":"user","content":"hello"}]`},
		{"empty system placeholder", `[{"role":"system","content":""},{"role":"user","content":"hello"}]`, `[{"role":"user","content":"hello"}]`},
		{"null developer placeholder", `[{"role":"developer","content":null},{"role":"user","content":"hello"}]`, `[{"role":"user","content":"hello"}]`},
		{"empty user retained", `[{"role":"user","content":""}]`, `[{"role":"user","content":""}]`},
		{"tool result retained", `[{"role":"tool","content":"","tool_call_id":"call_1"}]`, `[{"role":"tool","content":"","tool_call_id":"call_1"}]`},
		{"named system retained", `[{"role":"system","content":"","name":"context"},{"role":"user","content":"hello"}]`, `[{"role":"system","content":"","name":"context"},{"role":"user","content":"hello"}]`},
		{"tool call retained", `[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]`, `[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.UnmarshalJsonStr(`{"model":"kimi-k3.1-pro","messages":`+tc.messages+`}`, &request))
				info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro", ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: enabled},
				}}
				converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
				require.NoError(t, err)
				wire, err := common.Marshal(converted)
				require.NoError(t, err)
				want := tc.messages
				if enabled {
					want = tc.want
				}
				assert.JSONEq(t, want, gjson.GetBytes(wire, "messages").Raw)
			}
		})
	}
}

func TestConvertOpenAIRequestKimiK26UsesOnlyAllowedTemperature(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:       "kimi-k2.6",
		Temperature: common.GetPointer[float64](0.7),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "kimi-k2.6",
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)

	require.NoError(t, err)
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, convertedRequest.Temperature)
	require.Equal(t, 1.0, *convertedRequest.Temperature)
}

func TestConvertOpenAIRequestKimiK26KeepsOmittedTemperatureOmitted(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "kimi-k2.6",
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "kimi-k2.6",
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)

	require.NoError(t, err)
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Nil(t, convertedRequest.Temperature)
}

func TestConvertOpenAIRequestOtherMoonshotModelKeepsTemperature(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:       "kimi-k2.5",
		Temperature: common.GetPointer[float64](0.7),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "kimi-k2.5",
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)

	require.NoError(t, err)
	convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, convertedRequest.Temperature)
	require.Equal(t, 0.7, *convertedRequest.Temperature)
}

// Kimi serves OpenAI Responses natively (kimi-k3); the body is forwarded
// unchanged and other models are rejected by the upstream.
func TestResponsesRequestPassesThroughToV1Responses(t *testing.T) {
	const body = `{"model":"kimi-k3","input":"Latest Go release?","tools":[{"type":"web_search"}],"stream":false}`
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeResponses,
		RelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.moonshot.invalid", UpstreamModelName: "kimi-k3"},
	}
	url, err := (&Adaptor{}).GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://api.moonshot.invalid/v1/responses", url)

	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &request))
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	require.NoError(t, err)
	wire, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.JSONEq(t, body, string(wire))
}

func TestSpecialPlanRoutesPreserveNativeProtocols(t *testing.T) {
	for _, tc := range []struct {
		format types.RelayFormat
		path   string
	}{
		{types.RelayFormatOpenAI, "/v1/chat/completions"},
		{types.RelayFormatOpenAIResponses, "/v1/responses"},
		{types.RelayFormatClaude, "/v1/messages"},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			url, err := (&Adaptor{}).GetRequestURL(&relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "kimi-coding-plan"},
				RelayFormat: tc.format,
			})
			require.NoError(t, err)
			assert.Equal(t, "https://api.kimi.com/coding"+tc.path, url)
		})
	}
}

func TestKVVK31ProDynamicToolsAndThinking(t *testing.T) {
	for _, tc := range []struct {
		name, thinking  string
		enabled, stream bool
		wantError       bool
	}{
		{name: "default thinking", enabled: true},
		{name: "streaming", enabled: true, stream: true},
		{name: "explicit thinking", enabled: true, thinking: `{"type":"enabled","effort":"low"}`},
		{name: "disabled thinking rejected", enabled: true, thinking: `{"type":"disabled"}`, wantError: true},
		{name: "compatibility disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Use an alias to verify that compatibility follows the mapped model.
			body := `{"model":"client-alias","temperature":0.5,"messages":[{"role":"system","content":"","tools":[{"type":"function","function":{"name":"lookup","strict":false,"parameters":{"type":"object"}}}]},{"role":"user","content":"hello"}]}`
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &request))
			request.THINKING = []byte(tc.thinking)
			request.Stream = common.GetPointer(tc.stream)
			info := &relaycommon.RelayInfo{
				RelayMode: relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType:       constant.ChannelTypeMoonshot,
					UpstreamModelName: "kimi-k3.1-pro",
					ChannelSetting:    dto.ChannelSettings{MoonshotKVVEnabled: tc.enabled},
				},
			}
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
			if tc.wantError {
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.ErrorContains(t, err, "requires thinking")
				return
			}
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.Equal(t, !tc.enabled, gjson.GetBytes(wire, "messages.0.content").Exists())
			assert.JSONEq(t, gjson.Get(body, "messages.0.tools").Raw, gjson.GetBytes(wire, "messages.0.tools").Raw)
			assert.Equal(t, tc.stream, gjson.GetBytes(wire, "stream").Bool())
			if tc.enabled {
				assert.Equal(t, "enabled", gjson.GetBytes(wire, "thinking.type").String())
				assert.Equal(t, 1.0, gjson.GetBytes(wire, "temperature").Float())
			} else {
				assert.False(t, gjson.GetBytes(wire, "thinking").Exists())
				assert.Equal(t, 0.5, gjson.GetBytes(wire, "temperature").Float())
			}
		})
	}
}

const kimiRefParameters = `{"type":"object","properties":{"value":{"$ref":"#/$defs/__schema0","description":"argument"}},"$defs":{"__schema0":{"enum":["a","b"]}}}`

func TestKimiToolSchemaRefsAcrossWireFormats(t *testing.T) {
	for name, tool := range map[string]string{
		"chat":      `{"type":"function","function":{"name":"lookup","parameters":` + kimiRefParameters + `}}`,
		"responses": `{"type":"function","name":"lookup","parameters":` + kimiRefParameters + `}`,
		"messages":  `{"name":"lookup","input_schema":` + kimiRefParameters + `}`,
		"namespace": `{"type":"namespace","name":"ns","tools":[{"type":"function","name":"lookup","parameters":` + kimiRefParameters + `}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"tools":[` + tool + `],"temperature":0,"stream":false,"extension":9007199254740993}`)
			fixed, changed, err := normalizeKimiToolSchemaRefsBody(body)
			require.NoError(t, err)
			require.True(t, changed)
			path := map[string]string{"chat": "tools.0.function.parameters", "responses": "tools.0.parameters", "messages": "tools.0.input_schema", "namespace": "tools.0.tools.0.parameters"}[name]
			value := gjson.GetBytes(fixed, path+".properties.value")
			require.False(t, value.Get("$ref").Exists())
			require.JSONEq(t, `{"type":"string","enum":["a","b"],"description":"argument"}`, value.Raw)
			require.Equal(t, "9007199254740993", gjson.GetBytes(fixed, "extension").Raw)
			require.Equal(t, "0", gjson.GetBytes(fixed, "temperature").Raw)
			require.Equal(t, "false", gjson.GetBytes(fixed, "stream").Raw)
			again, changed, err := normalizeKimiToolSchemaRefsBody(fixed)
			require.NoError(t, err)
			require.False(t, changed)
			require.Equal(t, fixed, again)
		})
	}
}

func TestKimiSchemaRefsPreserveConstraintsAndLiteralData(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","function":{"name":"lookup","parameters":{
		"type":"object",
		"properties":{
			"n":{"$ref":"#/$defs/alias","minimum":3},
			"escaped":{"$ref":"#/$defs/a~1b~0c%20d"},
			"nullable":{"anyOf":[{"$ref":"#/definitions/array"},{"type":"null"}]},
			"literal":{"type":"object","default":{"$ref":"#/$defs/missing"}},
			"$ref":{"type":"string"}
		},
		"$defs":{"alias":{"$ref":"#/$defs/integer"},"integer":{"type":"integer","minimum":0,"maximum":9007199254740993},"a/b~c d":{"type":"boolean"}},
		"definitions":{"array":{"type":"array","items":{"$ref":"#/$defs/integer"}}},
		"examples":[{"$ref":"#/$defs/missing"}],
		"const":{"$ref":"#/$defs/missing"}
	}}}]}`)
	fixed, changed, err := normalizeKimiToolSchemaRefsBody(body)
	require.NoError(t, err)
	require.True(t, changed)
	schema := gjson.GetBytes(fixed, "tools.0.function.parameters")
	require.Equal(t, "0", schema.Get("properties.n.allOf.0.minimum").Raw)
	require.Equal(t, "3", schema.Get("properties.n.allOf.1.minimum").Raw)
	require.Equal(t, "9007199254740993", schema.Get("properties.n.allOf.0.maximum").Raw)
	require.Equal(t, "boolean", schema.Get("properties.escaped.type").String())
	require.Equal(t, "integer", schema.Get("properties.nullable.anyOf.0.items.type").String())
	require.Equal(t, "string", schema.Get("properties.$ref.type").String())
	require.Equal(t, "#/$defs/missing", schema.Get("properties.literal.default.$ref").String())
	require.Equal(t, "#/$defs/missing", schema.Get("examples.0.$ref").String())
	require.Equal(t, "#/$defs/missing", schema.Get("const.$ref").String())
}

func TestKimiSchemaRefsLeaveRecursiveExternalAndLiteralSchemasUnchanged(t *testing.T) {
	for name, schema := range map[string]string{
		"root recursion":   `{"type":"object","properties":{"next":{"$ref":"#"}}}`,
		"mutual recursion": `{"type":"object","properties":{"next":{"$ref":"#/$defs/a"}},"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/a"}}}`,
		"remote reference": `{"$ref":"https://example.invalid/schema.json"}`,
		"literal":          `{"type":"object","default":{"$ref":"#/$defs/missing"},"examples":[{"$ref":"#/$defs/missing"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"tools":[{"type":"function","name":"test","parameters":` + schema + `}]}`)
			fixed, changed, err := normalizeKimiToolSchemaRefsBody(body)
			require.NoError(t, err)
			require.False(t, changed)
			require.Equal(t, body, fixed)
		})
	}
}

func TestKimiSchemaRefsDoNotBorrowAnotherToolsDefinition(t *testing.T) {
	tools := make([]any, 14)
	for i := range tools {
		tools[i] = map[string]any{"type": "function", "name": fmt.Sprintf("tool_%d", i), "parameters": map[string]any{"type": "object"}}
	}
	tools[0].(map[string]any)["parameters"] = map[string]any{"$defs": map[string]any{"__schema0": map[string]any{"type": "string"}}}
	tools[13].(map[string]any)["parameters"] = map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"$ref": "#/$defs/__schema0"}}}
	body, err := common.Marshal(map[string]any{"tools": tools})
	require.NoError(t, err)
	fixed, changed, err := normalizeKimiToolSchemaRefsBody(body)
	require.ErrorContains(t, err, "tools[13] (tool_13).parameters")
	require.ErrorContains(t, err, `unresolved local schema $ref "#/$defs/__schema0"`)
	require.False(t, changed)
	require.Equal(t, body, fixed)
}

func TestKimiSchemaRefsRecoverEmptyGeneratedDefinitions(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"mcp__codex_app__automation_update","parameters":{"$defs":{},"oneOf":[{"$ref":"#/$defs/__schema0"},{"$ref":"#/$defs/__schema3"}],"properties":{},"type":"object"}}]}`)
	fixed, changed, err := normalizeKimiToolSchemaRefsBody(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "object", gjson.GetBytes(fixed, "tools.0.parameters.oneOf.0.type").String())
	require.Equal(t, "object", gjson.GetBytes(fixed, "tools.0.parameters.oneOf.1.type").String())
	require.False(t, gjson.GetBytes(fixed, "tools.0.parameters.oneOf.0.$ref").Exists())
}

func TestKimiSchemaRefsDynamicToolsAndMalformedDefinitions(t *testing.T) {
	body := []byte(`{"messages":[{"role":"system","tools":[{"type":"function","function":{"name":"lookup","parameters":` + kimiRefParameters + `}}]}]}`)
	fixed, changed, err := normalizeKimiToolSchemaRefsBody(body)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "string", gjson.GetBytes(fixed, "messages.0.tools.0.function.parameters.properties.value.type").String())
	assert.False(t, gjson.GetBytes(fixed, "messages.0.content").Exists())
	for _, defs := range []string{`null`, `[]`, `{"other":{"type":"string"}}`} {
		t.Run(defs, func(t *testing.T) {
			body := []byte(`{"tools":[{"type":"function","name":"lookup","parameters":{"$defs":` + defs + `,"$ref":"#/$defs/__schema0"}}]}`)
			_, _, err := normalizeKimiToolSchemaRefsBody(body)
			assert.ErrorContains(t, err, "unresolved local schema")
		})
	}
}

func TestKimiSchemaRefsBoundExpansion(t *testing.T) {
	defs := map[string]any{"end": map[string]any{"type": "string"}}
	next := "end"
	for i := range 20 {
		name := fmt.Sprintf("s%d", i)
		defs[name] = map[string]any{"type": "object", "properties": map[string]any{
			"left": map[string]any{"$ref": "#/$defs/" + next}, "right": map[string]any{"$ref": "#/$defs/" + next},
		}}
		next = name
	}
	body, err := common.Marshal(map[string]any{"tools": []any{map[string]any{"type": "function", "name": "large", "parameters": map[string]any{"$ref": "#/$defs/" + next, "$defs": defs}}}})
	require.NoError(t, err)
	_, _, err = normalizeKimiToolSchemaRefsBody(body)
	require.ErrorContains(t, err, "expansion exceeds")
}

func TestKimiDoRequestResolvesFinalToolSchemas(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude} {
		t.Run(string(format), func(t *testing.T) {
			var received []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received, _ = io.ReadAll(r.Body)
				_, _ = io.WriteString(w, `{}`)
			}))
			defer upstream.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{RelayFormat: format, RelayMode: relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "test", UpstreamModelName: "kimi-k3"}}
			body := []byte(`{"model":"kimi-k3","messages":[],"tools":[{"type":"function","function":{"name":"lookup","parameters":` + kimiRefParameters + `}}]}`)
			path := "tools.0.function.parameters.properties.value.type"
			if format == types.RelayFormatClaude {
				body = []byte(`{"model":"kimi-k3","messages":[],"tools":[{"name":"lookup","input_schema":` + kimiRefParameters + `}]}`)
				path = "tools.0.input_schema.properties.value.type"
			} else if format == types.RelayFormatOpenAIResponses {
				info.RelayMode = relayconstant.RelayModeResponses
				request := dto.OpenAIResponsesRequest{Model: "kimi-k3", Input: []byte(`"hello"`), Tools: []byte(`[{"type":"function","name":"lookup","parameters":` + kimiRefParameters + `}]`)}
				converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(ctx, info, request)
				require.NoError(t, err)
				path = "tools.0.parameters.properties.value.type"
				body, err = common.Marshal(converted)
				require.NoError(t, err)
			}
			// Direct DoRequest also covers pass-through / post-override bodies.
			response, err := (&Adaptor{}).DoRequest(ctx, info, bytes.NewReader(body))
			require.NoError(t, err)
			require.NoError(t, response.(*http.Response).Body.Close())
			require.Equal(t, "string", gjson.GetBytes(received, path).String())
		})
	}
}

func TestKimiDoRequestRejectsDanglingRefsBeforeUpstream(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://127.0.0.1:1"}}
	_, err := (&Adaptor{}).DoRequest(ctx, info, bytes.NewBufferString(`{"tools":[{"name":"broken","input_schema":{"$ref":"#/$defs/__schema0"}}]}`))
	var apiErr *types.NewAPIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.True(t, types.IsSkipRetryError(apiErr))
	require.ErrorContains(t, err, "tools[0] (broken).input_schema")
	// Relay helpers preserve the status when wrapping DoRequest errors.
	wrapped := types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	require.Equal(t, http.StatusBadRequest, wrapped.StatusCode)
}

func TestDynamicToolsRequiredFallback(t *testing.T) {
	service.InitHttpClient()
	const tool = `{"type":"function","function":{"name":"calculate","strict":false,"parameters":{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"],"x-large":9007199254740993}}}`
	const body = `{"model":"kimi-k3.1-pro","tool_choice":"required","stream":false,"messages":[{"role":"user","content":"Calculate 23*47"},{"role":"system","content":"","tools":[` + tool + `]}],"extension":9007199254740993}`
	const rejection = `{"error":{"message":"tool_choice 'required' requires at least one tool","type":"invalid_request_error"}}`
	for _, tc := range []struct {
		name            string
		enabled, stream bool
		status          int
		response        string
		wantCalls       int
	}{
		{"native accepted", true, false, 200, `{}`, 1},
		{"known rejection", true, false, 400, rejection, 2},
		{"json retry success", true, false, 400, rejection, 2},
		{"stream rejection", true, true, 400, rejection, 2},
		{"quoted rejection", true, false, 400, `{"error":{"message":"tool_choice \"required\" requires at least one tool"}}`, 2},
		{"disabled", false, false, 400, rejection, 1},
		{"unrelated error", true, false, 400, `{"error":{"message":"invalid schema"}}`, 1},
		{"server error", true, false, 500, rejection, 1},
		{"oversized error", true, false, 400, rejection + strings.Repeat(" ", 65536), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var received [][]byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, _ := io.ReadAll(r.Body)
				received = append(received, wire)
				calls++
				if calls == 1 {
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.response)
					return
				}
				if tc.name == "json retry success" {
					_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"calculate","arguments":"{}"}}]}}]}`)
					return
				}
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				// A repeated rejection must not cause a third request.
				w.WriteHeader(400)
				_, _ = io.WriteString(w, rejection)
			}))
			defer upstream.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions, IsStream: tc.stream, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, ChannelBaseUrl: upstream.URL, UpstreamModelName: "kimi-k3.1-pro", ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: tc.enabled},
			}}
			input := strings.Replace(body, `"stream":false`, fmt.Sprintf(`"stream":%t`, tc.stream), 1)
			response, err := (&Adaptor{}).DoRequest(ctx, info, strings.NewReader(input))
			require.NoError(t, err)
			resp := response.(*http.Response)
			defer resp.Body.Close()
			result, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tc.wantCalls, calls)
			assert.JSONEq(t, tool, gjson.GetBytes(received[0], "messages.1.tools.0").Raw)
			assert.False(t, gjson.GetBytes(received[0], "tools").Exists())
			if tc.wantCalls == 1 {
				assert.Equal(t, tc.response, string(result))
				return
			}
			if tc.name == "json retry success" {
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equal(t, "calculate", gjson.GetBytes(result, "choices.0.message.tool_calls.0.function.name").String())
			} else if tc.stream {
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
				assert.Contains(t, string(result), "data: [DONE]")
			} else {
				assert.Equal(t, rejection, string(result))
			}
			assert.Equal(t, "required", gjson.GetBytes(received[1], "tool_choice").String())
			assert.JSONEq(t, tool, gjson.GetBytes(received[1], "tools.0").Raw)
			assert.Equal(t, 1, len(gjson.GetBytes(received[1], "messages").Array()))
			assert.Equal(t, "Calculate 23*47", gjson.GetBytes(received[1], "messages.0.content").String())
			assert.Equal(t, "9007199254740993", gjson.GetBytes(received[1], "extension").Raw)
			assert.Equal(t, tc.stream, gjson.GetBytes(received[1], "stream").Bool())
		})
	}
}

func TestPromoteRequiredDynamicToolsContract(t *testing.T) {
	const dynamic = `{"type":"function","function":{"name":"dynamic","parameters":{"type":"object"}}}`
	const global = `{"type":"function","function":{"name":"global","parameters":{"type":"object"}}}`
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"global and dynamic", `{"tool_choice":"required","tools":[` + global + `],"messages":[{"role":"user","content":"hello"},{"role":"system","tools":[` + dynamic + `]}]}`, true},
		{"auto", `{"tool_choice":"auto","messages":[{"role":"user","content":"hello"},{"role":"system","tools":[` + dynamic + `]}]}`, false},
		{"duplicate", `{"tool_choice":"required","tools":[` + dynamic + `],"messages":[{"role":"user","content":"hello"},{"role":"system","tools":[` + dynamic + `]}]}`, false},
		{"invalid role", `{"tool_choice":"required","messages":[{"role":"user","tools":[` + dynamic + `]}]}`, false},
		{"nonempty content", `{"tool_choice":"required","messages":[{"role":"system","content":"instructions","tools":[` + dynamic + `]}]}`, false},
		{"null", `{"tool_choice":"required","messages":[{"role":"system","tools":null}]}`, false},
		{"object", `{"tool_choice":"required","messages":[{"role":"system","tools":{}}]}`, false},
		{"null item", `{"tool_choice":"required","messages":[{"role":"system","tools":[null]}]}`, false},
		{"no conversational message", `{"tool_choice":"required","messages":[{"role":"system","tools":[` + dynamic + `]}]}`, false},
		{"metadata", `{"tool_choice":"required","messages":[{"role":"user","content":"hello"},{"role":"system","name":"declaration","tools":[` + dynamic + `]}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, ok := promoteRequiredDynamicTools([]byte(tc.body))
			require.Equal(t, tc.want, ok)
			if !ok {
				return
			}
			assert.Equal(t, "required", gjson.GetBytes(result, "tool_choice").String())
			if tc.name == "global and dynamic" {
				assert.JSONEq(t, `[`+global+`,`+dynamic+`]`, gjson.GetBytes(result, "tools").Raw)
			}
			if tc.name == "metadata" {
				assert.Equal(t, "declaration", gjson.GetBytes(result, "messages.1.name").String())
				assert.False(t, gjson.GetBytes(result, "messages.1.tools").Exists())
			}
		})
	}
}
