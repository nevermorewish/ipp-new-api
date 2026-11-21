package moonshot

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestMoonshotResponseTraceHeaders(t *testing.T) {
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Header(common.RequestIdKey, "local-request")
			body := `{"id":"chatcmpl-kimi","model":"kimi-k3.1-pro","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
			headers := http.Header{
				"Content-Type": {"application/json"},
				"X-Trace-Id":   {"trace-123"}, "Msh-Uid": {"msh-123"},
				"X-Provider-Extension": {"extension"},
				"X-Reasoning-Included": {"true"}, "X-Codex-Turn-State": {"turn"},
				common.RequestIdKey: {"upstream-request"},
			}
			if stream {
				body = "data: " + `{"id":"chatcmpl-kimi","model":"kimi-k3.1-pro","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}` + "\n\ndata: [DONE]\n\n"
				headers.Set("Content-Type", "text/event-stream")
				headers["X-Provider-Extension"] = []string{"first", "second"}
				headers["Connection"] = []string{"keep-alive, X-Hop", "X-Other-Hop"}
				for _, name := range []string{"X-Hop", "X-Other-Hop", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Upgrade"} {
					headers.Set(name, "must-not-forward")
				}
				headers.Set("Content-Length", "999999")
				headers.Set("Content-Encoding", "gzip")
				headers.Set("Transfer-Encoding", "upstream-framing")
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
				IsStream: stream, DisablePing: true,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro"},
			}
			_, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
			require.Nil(t, apiErr)
			result := recorder.Result()
			defer result.Body.Close()
			assert.Equal(t, http.StatusOK, result.StatusCode)
			assert.Equal(t, "trace-123", result.Header.Get("X-Trace-Id"))
			assert.Equal(t, "msh-123", result.Header.Get("Msh-Uid"))
			assert.Equal(t, headers.Values("X-Provider-Extension"), result.Header.Values("X-Provider-Extension"))
			assert.Equal(t, "true", result.Header.Get("X-Reasoning-Included"))
			assert.Equal(t, "turn", result.Header.Get("X-Codex-Turn-State"))
			assert.Equal(t, "local-request", result.Header.Get(common.RequestIdKey))
			assert.Equal(t, "upstream-request", c.GetString(common.UpstreamRequestIdKey))
			assert.Contains(t, recorder.Body.String(), "OK")
			if stream {
				assert.Contains(t, recorder.Body.String(), "data: [DONE]")
				assert.Equal(t, "text/event-stream", result.Header.Get("Content-Type"))
				assert.Equal(t, "chunked", result.Header.Get("Transfer-Encoding"))
				for _, name := range []string{"X-Hop", "X-Other-Hop", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Upgrade", "Content-Length", "Content-Encoding"} {
					assert.Empty(t, result.Header.Get(name), name)
				}
			}
		})
	}
}

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
	// The wider OpenAI and Anthropic levels are the official 400 on the
	// baseline and are rewritten only while KVV parameter compatibility is on.
	for _, alias := range []struct {
		name, body, forceParamCompat string
	}{
		{"minimal alias", `{"reasoning_effort":"minimal"}`, "low"},
		{"medium alias", `{"reasoning_effort":"medium"}`, "high"},
		{"xhigh alias", `{"reasoning_effort":"xhigh"}`, "max"},
		{"explicit medium alias", `{"thinking":{"effort":"medium"}}`, "high"},
	} {
		t.Run(alias.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(alias.body, &request))
			request.Model = "kimi-k3"
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3",
				ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: true},
			}}
			_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, 400, apiErr.StatusCode)

			var compatRequest dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(alias.body, &compatRequest))
			compatRequest.Model = "kimi-k3"
			info.ChannelSetting.MoonshotKVVParamCompat = true
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &compatRequest)
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.Equal(t, alias.forceParamCompat, gjson.GetBytes(wire, "reasoning_effort").String())
		})
	}

	for _, tc := range []struct {
		name, body, kind, effort string
	}{
		{"default", `{}`, "enabled", "max"},
		{"template false", `{"chat_template_kwargs":{"thinking":false,"custom":7}}`, "disabled", "none"},
		{"reasoning none", `{"reasoning_effort":"none"}`, "disabled", "none"},
		{"reasoning low", `{"reasoning_effort":"low"}`, "enabled", "low"},
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
				assert.EqualError(t, err, "thinking must be enabled")
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
		// The fallback follows the upstream rejection, not the KVV channel
		// switch, so a channel without KVV still gets the promoted retry.
		{"channel without kvv", false, false, 400, rejection, 2},
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

func TestDynamicToolsHoistSwitch(t *testing.T) {
	service.InitHttpClient()
	const tool = `{"type":"function","function":{"name":"calculate","parameters":{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}}}`
	const user = `{"role":"user","content":"Calculate 23*47"}`
	for _, tc := range []struct {
		name, choice    string
		stream, hoisted bool
	}{
		{"required", "required", false, true},
		{"required stream", "required", true, true},
		// auto leaves the call to the model, so the declaration keeps its
		// official place in the conversation.
		{"auto", "auto", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received [][]byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, _ := io.ReadAll(r.Body)
				received = append(received, wire)
				_, _ = io.WriteString(w, `{}`)
			}))
			defer upstream.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions, IsStream: tc.stream, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, ChannelBaseUrl: upstream.URL, UpstreamModelName: "kimi-k3.1-pro", ChannelSetting: dto.ChannelSettings{MoonshotHoistDynamicTools: true},
			}}
			body := fmt.Sprintf(`{"model":"kimi-k3.1-pro","tool_choice":%q,"stream":%t,"messages":[%s,{"role":"system","tools":[%s]}]}`, tc.choice, tc.stream, user, tool)
			response, err := (&Adaptor{}).DoRequest(ctx, info, strings.NewReader(body))
			require.NoError(t, err)
			resp := response.(*http.Response)
			defer resp.Body.Close()
			// The promoted body is the first and only request, not a retry.
			require.Len(t, received, 1)
			want := body
			if tc.hoisted {
				want = fmt.Sprintf(`{"model":"kimi-k3.1-pro","tool_choice":"required","stream":%t,"messages":[%s],"tools":[%s]}`, tc.stream, user, tool)
			}
			assert.JSONEq(t, want, string(received[0]))
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
		{"declaration without text elsewhere", `{"tool_choice":"required","messages":[{"role":"user","tools":[` + dynamic + `]}]}`, false},
		{"declaration with instructions", `{"tool_choice":"required","messages":[{"role":"user","content":"hello"},{"role":"system","content":"instructions","tools":[` + dynamic + `]}]}`, true},
		{"declaration on user message", `{"tool_choice":"required","messages":[{"role":"user","content":"hello","tools":[` + dynamic + `]}]}`, true},
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

func TestKimiToolResultPairing(t *testing.T) {
	info := func(relayFormat types.RelayFormat, model string) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			RelayFormat: relayFormat, RelayMode: relayconstant.RelayModeChatCompletions,
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: model},
		}
	}
	assistantCall := `{"role":"assistant","content":null,"tool_calls":[{"id":"call_abc123","type":"function","function":{"name":"get_weather","arguments":"{}"}}]}`
	for _, tc := range []struct {
		name, body string
		info       *relaycommon.RelayInfo
		wantErr    string
	}{
		{
			name: "orphan result is rejected",
			body: `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":"weather?"},{"role":"tool","tool_call_id":"call_abc123","content":"sunny"}]}`,
			info: info(types.RelayFormatOpenAI, "kimi-k3.1-pro"), wantErr: "does not match any preceding assistant tool_calls",
		},
		{
			name: "paired result is accepted",
			body: `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":"weather?"},` + assistantCall + `,{"role":"tool","tool_call_id":"call_abc123","content":"sunny"}]}`,
			info: info(types.RelayFormatOpenAI, "kimi-k3.1-pro"),
		},
		{
			name: "second result for one call is rejected",
			body: `{"model":"kimi-k3","messages":[` + assistantCall + `,{"role":"tool","tool_call_id":"call_abc123","content":"sunny"},{"role":"tool","tool_call_id":"call_abc123","content":"rainy"}]}`,
			info: info(types.RelayFormatOpenAI, "kimi-k3"), wantErr: "submits a second result",
		},
		{
			name: "empty tool_call_id is rejected",
			body: `{"model":"kimi-k3","messages":[{"role":"user","content":"weather?"},{"role":"tool","content":"sunny"}]}`,
			info: info(types.RelayFormatOpenAI, "kimi-k3"), wantErr: "tool_call_id is required",
		},
		{
			name: "claude format is not checked",
			body: `{"model":"kimi-k3","messages":[{"role":"tool","tool_call_id":"call_abc123","content":"sunny"}]}`,
			info: info(types.RelayFormatClaude, "kimi-k3"),
		},
		{
			name: "older kimi models keep pass-through",
			body: `{"model":"kimi-k2.5","messages":[{"role":"user","content":"weather?"},{"role":"tool","tool_call_id":"call_abc123","content":"sunny"}]}`,
			info: info(types.RelayFormatOpenAI, "kimi-k2.5"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateKimiToolResultPairing([]byte(tc.body), tc.info)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestKVVK31ParamCompat covers the kimi-k3.1 switch. With it on the gateway
// answers exactly like the official API, including the cases where the legacy
// behavior accepted a value the official API rejects.
func TestKVVK31ParamCompat(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantError string
		compat                bool
		legacy                bool
		wantTemperature       float64
	}{
		{name: "off accepts unsupported temperature", body: `{"temperature":0.99}`, wantTemperature: 1},
		{name: "on rejects unsupported temperature", compat: true, body: `{"temperature":0.99}`, wantError: "temperature"},
		{name: "on accepts fixed temperature", compat: true, body: `{"temperature":1}`, wantTemperature: 1},
		{name: "off maps medium to nothing", body: `{"reasoning_effort":"medium"}`, wantError: "reasoning effort"},
		{name: "on accepts low", compat: true, body: `{"reasoning_effort":"low"}`},
		{name: "on accepts medium", compat: true, body: `{"reasoning_effort":"medium"}`},
		{name: "on accepts minimal", compat: true, body: `{"reasoning_effort":"minimal"}`},
		{name: "on accepts xhigh", compat: true, body: `{"reasoning_effort":"xhigh"}`},
		{name: "official wins over legacy rewrite", compat: true, legacy: true, body: `{"reasoning_effort":"medium"}`},
		{name: "both reject top_p", body: `{"top_p":0.5}`, wantError: "top_p"},
		{name: "on rejects top_p", compat: true, body: `{"top_p":0.5}`, wantError: "top_p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &request))
			request.Model = "kimi-k3.1-pro"
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro",
				ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: true, MoonshotK31ParamCompat: tc.compat, MoonshotKVVParamCompat: tc.legacy},
			}}
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
			if tc.wantError != "" {
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, 400, apiErr.StatusCode)
				assert.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.Equal(t, tc.wantTemperature, gjson.GetBytes(wire, "temperature").Float())
			if gjson.Get(tc.body, "top_p").Exists() {
				assert.Equal(t, 0.95, gjson.GetBytes(wire, "top_p").Float())
			}
		})
	}
}

// TestKVVK31RequestValidation covers the request shapes the official API
// rejects or accepts, which the gateway must answer the same way.
func TestKVVK31RequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantError string
		promptTokens          int
		wantBudget            int64
	}{
		{name: "empty user content", body: `{"messages":[{"role":"user","content":""}]}`, wantError: "empty content"},
		{name: "empty system content", body: `{"messages":[{"role":"system","content":""},{"role":"user","content":"hi"}]}`, wantError: "empty content"},
		{name: "assistant tool call without content", body: `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}]}`},
		{name: "empty tool result", body: `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":""}]}`},
		{name: "illegal prediction type", body: `{"messages":[{"role":"user","content":"hi"}],"prediction":{"type":"bogus"}}`, wantError: "prediction.type"},
		{name: "prediction text content", body: `{"messages":[{"role":"user","content":"hi"}],"prediction":{"type":"content","content":[{"type":"text","text":"a"}]}}`},
		{name: "illegal cache ttl", body: `{"messages":[{"role":"user","content":"hi"}],"prompt_cache_options":{"ttl":"2h"}}`, wantError: "ttl"},
		{name: "illegal cache mode", body: `{"messages":[{"role":"user","content":"hi"}],"prompt_cache_options":{"mode":"bogus"}}`, wantError: "mode"},
		{name: "official cache option", body: `{"messages":[{"role":"user","content":"hi"}],"prompt_cache_options":{"mode":"explicit","ttl":"1h"}}`},
		{name: "tool without parameters", body: `{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f"}}]}`},
		{name: "budget lowered to context window", body: `{"messages":[{"role":"user","content":"hi"}],"max_completion_tokens":1048576}`, promptTokens: 9, wantBudget: 1048567},
		{name: "budget lowered to context without estimate", body: `{"messages":[{"role":"user","content":"hi"}],"max_completion_tokens":1048576}`, wantBudget: 1048576},
		{name: "budget above model maximum", body: `{"messages":[{"role":"user","content":"hi"}],"max_completion_tokens":1048577}`, wantError: "max_completion_tokens"},
		{name: "deprecated max_tokens above model maximum", body: `{"messages":[{"role":"user","content":"hi"}],"max_tokens":1048577}`, wantError: "max_tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &request))
			request.Model = "kimi-k3.1-pro"
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro",
				ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: true, MoonshotK31ParamCompat: true},
			}}
			info.SetEstimatePromptTokens(tc.promptTokens)
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
			if tc.wantError != "" {
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, 400, apiErr.StatusCode)
				assert.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			if tc.wantBudget > 0 {
				assert.Equal(t, tc.wantBudget, gjson.GetBytes(wire, "max_completion_tokens").Int())
			}
			if gjson.Get(tc.body, "tools").Exists() {
				assert.JSONEq(t, `{"type":"object","properties":{}}`, gjson.GetBytes(wire, "tools.0.function.parameters").Raw)
			}
		})
	}
}

// TestKVVK31DisabledThinking covers disabled thinking on K3.1. The official API
// accepts it, so the K3.1 switch forwards it in the official shape; without the
// switch the gateway still rejects it.
func TestKVVK31DisabledThinking(t *testing.T) {
	const message = `"messages":[{"role":"user","content":"北京天气怎么样？"}]`
	for _, tc := range []struct {
		name, model, body, wantError string
		compat                       bool
	}{
		{name: "switch off rejects", model: "kimi-k3.1-pro", body: `{"thinking":{"type":"disabled"},` + message + `}`, wantError: "thinking must be enabled"},
		{name: "thinking type", model: "kimi-k3.1-pro", compat: true, body: `{"thinking":{"type":"disabled"},` + message + `}`},
		{name: "reasoning effort none", model: "kimi-k3.1-pro", compat: true, body: `{"reasoning_effort":"none",` + message + `}`},
		{name: "non-thinking temperature", model: "kimi-k3.1-pro", compat: true, body: `{"thinking":{"type":"disabled"},"temperature":0.6,` + message + `}`},
		{name: "highspeed", model: "kimi-k3.1-pro-highspeed", compat: true, body: `{"thinking":{"type":"disabled","keep":"all"},` + message + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &request))
			request.Model = tc.model
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: tc.model,
				ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: true, MoonshotK31ParamCompat: tc.compat},
			}}
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
			if tc.wantError != "" {
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				assert.EqualError(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.JSONEq(t, `{"type":"disabled"}`, gjson.GetBytes(wire, "thinking").Raw)
			assert.False(t, gjson.GetBytes(wire, "reasoning_effort").Exists())
			assert.Equal(t, gjson.Get(tc.body, "temperature").Raw, gjson.GetBytes(wire, "temperature").Raw)
		})
	}
}

// TestKVVK31NamedToolChoice covers a named tool_choice with thinking disabled.
// Kimi upstreams reject it as if thinking were on, so the K3.1 switch asks for
// the function's parameters as a json_schema response format and turns the
// answer back into a call to that function.
func TestKVVK31NamedToolChoice(t *testing.T) {
	const weather = `{"type":"function","function":{"name":"get_weather","description":"Get the weather of a city.","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}`
	const weatherFormat = `{"type":"json_schema","json_schema":{"name":"get_weather","description":"Get the weather of a city.","schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}`
	const named = `"tool_choice":{"type":"function","function":{"name":"get_weather"}}`
	const user = `{"role":"user","content":"北京天气怎么样？"}`
	k31Compat := dto.ChannelSettings{MoonshotKVVEnabled: true, MoonshotK31ParamCompat: true}
	newInfo := func(stream bool, settings dto.ChannelSettings) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
			IsStream: stream, DisablePing: true,
			ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro",
				ChannelSetting: settings,
			},
		}
	}

	for _, tc := range []struct {
		name, body string
		wantFormat string // empty: the tool_choice is forwarded unchanged
	}{
		{name: "top-level tool replaces the client response format", wantFormat: weatherFormat,
			body: `{"thinking":{"type":"disabled"},"tools":[` + weather + `],` + named + `,"response_format":{"type":"json_object"},"messages":[` + user + `]}`},
		{name: "dynamic tool", wantFormat: weatherFormat,
			body: `{"thinking":{"type":"disabled"},` + named + `,"messages":[{"role":"system","content":"","tools":[` + weather + `]},` + user + `]}`},
		{name: "tool without parameters", wantFormat: `{"type":"json_schema","json_schema":{"name":"get_weather","schema":{"type":"object","properties":{}}}}`,
			body: `{"thinking":{"type":"disabled"},"tools":[{"type":"function","function":{"name":"get_weather"}}],` + named + `,"messages":[` + user + `]}`},
		{name: "undeclared function",
			body: `{"thinking":{"type":"disabled"},"tools":[` + weather + `],"tool_choice":{"type":"function","function":{"name":"get_time"}},"messages":[` + user + `]}`},
		{name: "thinking enabled",
			body: `{"tools":[` + weather + `],` + named + `,"messages":[` + user + `]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &request))
			request.Model = "kimi-k3.1-pro"
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, newInfo(false, k31Compat), &request)
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			if tc.wantFormat == "" {
				assert.JSONEq(t, gjson.Get(tc.body, "tool_choice").Raw, gjson.GetBytes(wire, "tool_choice").Raw)
				assert.False(t, gjson.GetBytes(wire, "response_format").Exists())
				return
			}
			assert.Equal(t, "none", gjson.GetBytes(wire, "tool_choice").String())
			assert.JSONEq(t, tc.wantFormat, gjson.GetBytes(wire, "response_format").Raw)
			assert.JSONEq(t, `{"type":"disabled"}`, gjson.GetBytes(wire, "thinking").Raw)
		})
	}

	// The upstream bodies below are captured answers to the rewritten request.
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	const usage = `"usage":{"prompt_tokens":158,"completion_tokens":34,"total_tokens":192,"cached_tokens":0,"completion_tokens_details":{},"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0}}`
	chunk := func(choice string) string {
		return `data: {"choices":[` + choice + `],"created":1794743397,"id":"chatcmpl-7a7b","model":"kimi-k3.1-pro","object":"chat.completion.chunk"}` + "\n\n"
	}
	upstreamStream := chunk(`{"index":0,"delta":{"role":"assistant"}}`) + chunk(`{"index":0,"delta":{}}`)
	for _, fragment := range []string{`{\"`, `city`, `\":`, ` \"`, `北京`, `\"}`} {
		upstreamStream += chunk(`{"index":0,"delta":{"content":"` + fragment + `"}}`)
	}
	upstreamStream += `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":"stop"}],"created":1794743397,"id":"chatcmpl-7a7b","model":"kimi-k3.1-pro","object":"chat.completion.chunk",` + usage + "}\n\ndata: [DONE]\n\n"
	upstreamJSON := `{"choices":[{"index":0,"message":{"role":"assistant","content":"{\"city\": \"北京\"}"},"finish_reason":"stop"}],"created":1794743400,"id":"chatcmpl-fcbf","model":"kimi-k3.1-pro","object":"chat.completion",` + usage + `}`

	for _, path := range []struct {
		name, thinking string
		settings       dto.ChannelSettings
	}{
		{name: "client disables thinking", thinking: `"thinking":{"type":"disabled"},`, settings: k31Compat},
		{name: "allow switch", thinking: `"thinking":{"type":"disabled"},`, settings: dto.ChannelSettings{MoonshotKVVEnabled: true, MoonshotDisableThinking: true}},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("response %s stream=%t", path.name, stream), func(t *testing.T) {
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.UnmarshalJsonStr(`{`+path.thinking+`"tools":[`+weather+`],`+named+`,"messages":[`+user+`]}`, &request))
				request.Model = "kimi-k3.1-pro"
				request.Stream = common.GetPointer(stream)
				info := newInfo(stream, path.settings)
				adaptor := &Adaptor{}
				converted, err := adaptor.ConvertOpenAIRequest(nil, info, &request)
				require.NoError(t, err)
				// TextHelper converts a KVV request again on the final body, which
				// is already rewritten; the call must survive that pass.
				wire, err := common.Marshal(converted)
				require.NoError(t, err)
				var final dto.GeneralOpenAIRequest
				require.NoError(t, common.Unmarshal(wire, &final))
				_, err = adaptor.ConvertOpenAIRequest(nil, info, &final)
				require.NoError(t, err)

				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				upstream, contentType := upstreamJSON, "application/json"
				if stream {
					upstream, contentType = upstreamStream, "text/event-stream"
				}
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(upstream))}
				usageResult, apiErr := adaptor.DoResponse(c, resp, info)
				require.Nil(t, apiErr)
				// Usage is the upstream's own; only the answer's shape changes.
				assert.Equal(t, 158, usageResult.(*dto.Usage).PromptTokens)

				var id, name, arguments, content, finish string
				if !stream {
					body := recorder.Body.Bytes()
					id = gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String()
					name = gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String()
					arguments = gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String()
					content = gjson.GetBytes(body, "choices.0.message.content").String()
					finish = gjson.GetBytes(body, "choices.0.finish_reason").String()
					assert.Equal(t, "function", gjson.GetBytes(body, "choices.0.message.tool_calls.0.type").String())
				}
				for line := range strings.SplitSeq(recorder.Body.String(), "\n") {
					event, ok := strings.CutPrefix(line, "data: ")
					if !stream || !ok || !strings.HasPrefix(event, "{") {
						continue
					}
					delta := gjson.Get(event, "choices.0.delta")
					id += delta.Get("tool_calls.0.id").String()
					name += delta.Get("tool_calls.0.function.name").String()
					arguments += delta.Get("tool_calls.0.function.arguments").String()
					content += delta.Get("content").String()
					if reason := gjson.Get(event, "choices.0.finish_reason").String(); reason != "" {
						finish = reason
					}
				}
				assert.Regexp(t, `^get_weather_0_[0-9a-f]{8}$`, id)
				assert.Equal(t, "get_weather", name)
				assert.Equal(t, `{"city": "北京"}`, arguments)
				assert.Empty(t, content)
				assert.Equal(t, "tool_calls", finish)
			})
		}
	}
}

// TestMoonshotAllowDisableThinking covers the channel switch that lets a
// client turn thinking off. Thinking stays on by default; only a request that
// asks for it goes upstream with thinking disabled.
func TestMoonshotAllowDisableThinking(t *testing.T) {
	const weather = `"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]`
	const named = `"tool_choice":{"type":"function","function":{"name":"get_weather"}}`
	const disabled = `"thinking":{"type":"disabled"}`
	const kvvDefault = `{"type":"enabled","keep":"all","effort":"max"}`
	for _, tc := range []struct {
		name, model, body, wantThinking string // empty wantThinking: no thinking field
		kvv, wantEmulated               bool
	}{
		{name: "default keeps thinking", model: "kimi-k3.1-pro", kvv: true, body: `{}`, wantThinking: kvvDefault},
		{name: "default keeps thinking without KVV", model: "kimi-k3.1-pro", body: `{}`},
		{name: "client disables thinking", model: "kimi-k3.1-pro", kvv: true, body: `{` + disabled + `}`, wantThinking: `{"type":"disabled"}`},
		{name: "client disables thinking with a named tool", model: "kimi-k3.1-pro", kvv: true, body: `{` + disabled + `,` + weather + `,` + named + `}`, wantThinking: `{"type":"disabled"}`, wantEmulated: true},
		{name: "named tool without KVV", model: "kimi-k3", body: `{` + disabled + `,` + weather + `,` + named + `}`, wantThinking: `{"type":"disabled"}`, wantEmulated: true},
		{name: "thinking on keeps a named tool", model: "kimi-k3.1-pro", kvv: true, body: `{` + weather + `,` + named + `}`, wantThinking: kvvDefault},
		{name: "other model keeps a named tool", model: "kimi-k2.6", body: `{` + disabled + `,` + weather + `,` + named + `}`, wantThinking: `{"type":"disabled"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &request))
			request.Model = tc.model
			request.Messages = []dto.Message{{Role: "user", Content: "北京天气怎么样？"}}
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: tc.model,
				ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: tc.kvv, MoonshotDisableThinking: true},
			}}
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			if tc.wantThinking == "" {
				assert.False(t, gjson.GetBytes(wire, "thinking").Exists())
			} else {
				assert.JSONEq(t, tc.wantThinking, gjson.GetBytes(wire, "thinking").Raw)
			}
			if tc.wantThinking == `{"type":"disabled"}` {
				assert.False(t, gjson.GetBytes(wire, "reasoning_effort").Exists())
			}
			if tc.wantEmulated {
				assert.Equal(t, "none", gjson.GetBytes(wire, "tool_choice").String())
				assert.Equal(t, "get_weather", gjson.GetBytes(wire, "response_format.json_schema.name").String())
			} else if gjson.Get(tc.body, "tool_choice").Exists() {
				assert.JSONEq(t, gjson.Get(tc.body, "tool_choice").Raw, gjson.GetBytes(wire, "tool_choice").Raw)
			}
		})
	}
}

// TestKVVK31MediaStringShorthand covers the official string shorthand for
// image_url and video_url. Kimi upstreams answer the string form with a 400, so
// the K3.1 switch rewrites it into the object form; without the switch the body
// is forwarded unchanged.
func TestKVVK31MediaStringShorthand(t *testing.T) {
	const body = `{"messages":[{"role":"user","content":[` +
		`{"type":"text","text":"describe"},` +
		`{"type":"image_url","image_url":"https://example.com/a.png","detail_hint":"keep"},` +
		`{"type":"video_url","video_url":"https://example.com/v.mp4"},` +
		`{"type":"image_url","image_url":{"url":"https://example.com/b.png"}}]}]}`
	for _, tc := range []struct {
		name                 string
		compat               bool
		wantImage, wantVideo string
	}{
		{name: "switch off forwards the string form", wantImage: `"https://example.com/a.png"`, wantVideo: `"https://example.com/v.mp4"`},
		{name: "switch on rewrites into the object form", compat: true, wantImage: `{"url":"https://example.com/a.png"}`, wantVideo: `{"url":"https://example.com/v.mp4"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &request))
			request.Model = "kimi-k3.1-pro"
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro",
				ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: true, MoonshotK31ParamCompat: tc.compat},
			}}
			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.JSONEq(t, tc.wantImage, gjson.GetBytes(wire, "messages.0.content.1.image_url").Raw)
			assert.JSONEq(t, tc.wantVideo, gjson.GetBytes(wire, "messages.0.content.2.video_url").Raw)
			// Sibling members survive the rewrite, and an object form is never touched.
			assert.Equal(t, "keep", gjson.GetBytes(wire, "messages.0.content.1.detail_hint").String())
			assert.JSONEq(t, `{"url":"https://example.com/b.png"}`, gjson.GetBytes(wire, "messages.0.content.3.image_url").Raw)
		})
	}
}

// kimiCacheTail is the per-request end of the vendor cache test: a fixed reply
// instruction and an identifier that changes on every request.
func kimiCacheTail(id string) string {
	return "\n请只回复：已收到。\n请求编号：" + id + "；请求标识：" + strings.Repeat("a1", 32) + "。"
}

func TestKimiCacheTextPieces(t *testing.T) {
	material := strings.Repeat("缓存资料。", 940) // 4700 runes without a line break
	for _, tc := range []struct {
		name string
		text string
		cuts []int // rune offsets where a new part starts; nil keeps the text whole
	}{
		{"below the minimum", strings.Repeat("a", 1023), nil},
		{"no checkpoint below the length", strings.Repeat("a", 1024), nil},
		{"checkpoints", strings.Repeat("a", 5000), []int{1024, 2048, 4096}},
		{"checkpoints count runes", strings.Repeat("中", 3000), []int{1024, 2048}},
		{"identifier on the last line", material + kimiCacheTail("1"), []int{1024, 2048, 4096, 4701, 4711}},
		{"CRLF and a blank line", material + "\r\n\r\n请只回复：已收到。\r\n请求编号：1", []int{1024, 2048, 4096, 4704, 4715}},
		{"trailing blank lines stay in the last part", material + "\n问题\n\n", []int{1024, 2048, 4096, 4701}},
		{"line start on a checkpoint", strings.Repeat("a", 2047) + "\n" + strings.Repeat("b", 10), []int{1024, 2048}},
		{"no whitespace-only part", strings.Repeat("a", 4095) + "\n\n问题", []int{1024, 2048, 4096}},
		{"invalid UTF-8", strings.Repeat("a", 2000) + "\xff", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pieces := kimiCacheTextPieces(tc.text)
			if tc.cuts == nil {
				assert.Nil(t, pieces)
				return
			}
			assert.Equal(t, tc.text, strings.Join(pieces, ""))
			var cuts []int
			offset := 0
			for _, piece := range pieces[:len(pieces)-1] {
				offset += utf8.RuneCountInString(piece)
				cuts = append(cuts, offset)
			}
			assert.Equal(t, tc.cuts, cuts)
			for _, piece := range pieces {
				assert.True(t, utf8.ValidString(piece))
				assert.NotEmpty(t, strings.TrimSpace(piece))
			}
		})
	}

	// Only the part that holds the identifier differs, so every part before it
	// is read from the cache the previous request wrote.
	first := kimiCacheTextPieces(material + kimiCacheTail("1"))
	second := kimiCacheTextPieces(material + kimiCacheTail("20261115-000042"))
	require.Len(t, second, len(first))
	assert.Equal(t, first[:len(first)-1], second[:len(second)-1])
	assert.NotEqual(t, first[len(first)-1], second[len(second)-1])
}

func TestSplitKimiCacheTextBody(t *testing.T) {
	long := strings.Repeat("缓存资料。", 940) + kimiCacheTail("1")
	quoted, err := common.Marshal(long)
	require.NoError(t, err)
	text := string(quoted)
	joinParts := func(parts []gjson.Result) string {
		var joined strings.Builder
		for _, part := range parts {
			assert.Equal(t, dto.ContentTypeText, part.Get("type").String())
			joined.WriteString(part.Get("text").String())
		}
		return joined.String()
	}

	t.Run("only the message content changes", func(t *testing.T) {
		body := `{"model":"kimi-k3.1-pro","extension":9007199254740993,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","x-large":9007199254740993}}}],"messages":[{"role":"system","content":"short"},{"role":"user","content":` + text + `}]}`
		out := splitKimiCacheTextBody([]byte(body))
		content := gjson.GetBytes(out, "messages.1.content")
		require.True(t, content.IsArray())
		assert.Equal(t, strings.Replace(body, text, content.Raw, 1), string(out))
		assert.Len(t, content.Array(), 6)
		assert.Equal(t, long, joinParts(content.Array()))
	})

	t.Run("system text", func(t *testing.T) {
		out := splitKimiCacheTextBody([]byte(`{"messages":[{"role":"system","content":` + text + `}]}`))
		assert.Equal(t, long, joinParts(gjson.GetBytes(out, "messages.0.content").Array()))
	})

	t.Run("other parts keep their place", func(t *testing.T) {
		image := `{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}`
		out := splitKimiCacheTextBody([]byte(`{"messages":[{"role":"user","content":[` + image + `,{"type":"text","text":` + text + `}]}]}`))
		parts := gjson.GetBytes(out, "messages.0.content").Array()
		require.Len(t, parts, 7)
		assert.JSONEq(t, image, parts[0].Raw)
		assert.Equal(t, long, joinParts(parts[1:]))
	})

	shortParts := strings.Repeat(`{"type":"text","text":"x"},`, 15)
	for _, tc := range []struct{ name, body string }{
		{"assistant partial", `{"messages":[{"role":"assistant","content":` + text + `,"partial":true}]}`},
		{"tool result", `{"messages":[{"role":"tool","tool_call_id":"call_1","content":` + text + `}]}`},
		{"developer", `{"messages":[{"role":"developer","content":` + text + `}]}`},
		{"context cache", `{"messages":[{"role":"cache","content":` + text + `}]}`},
		{"dynamic tool declaration", `{"messages":[{"role":"system","content":` + text + `,"tools":[]}]}`},
		{"part with cache_control", `{"messages":[{"role":"user","content":[{"type":"text","text":` + text + `,"cache_control":{"type":"ephemeral"}}]}]}`},
		{"more parts than the cap", `{"messages":[{"role":"user","content":[` + shortParts + `{"type":"text","text":` + text + `}]}]}`},
		{"short text", `{"messages":[{"role":"user","content":"hello"}]}`},
		{"null content", `{"messages":[{"role":"user","content":null}]}`},
		{"messages object", `{"messages":{"role":"user","content":` + text + `}}`},
		{"not JSON", `{"messages":[`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.body, string(splitKimiCacheTextBody([]byte(tc.body))))
		})
	}
}

func TestKimiSplitCacheTextSwitch(t *testing.T) {
	service.InitHttpClient()
	long := strings.Repeat("缓存资料。", 940) + kimiCacheTail("1")
	quoted, err := common.Marshal(long)
	require.NoError(t, err)
	const tool = `{"type":"function","function":{"name":"calculate","parameters":{"type":"object"}}}`
	for _, tc := range []struct {
		name               string
		model              string
		format             types.RelayFormat
		on, hoist, splitIt bool
	}{
		{"on", "kimi-k3.1-pro", types.RelayFormatOpenAI, true, false, true},
		{"off", "kimi-k3.1-pro", types.RelayFormatOpenAI, false, false, false},
		{"other model", "kimi-k2.6", types.RelayFormatOpenAI, true, false, false},
		{"Messages format", "kimi-k3.1-pro", types.RelayFormatClaude, true, false, false},
		// The promoted dynamic tool body is built from the split body.
		{"promoted dynamic tools", "kimi-k3.1-pro", types.RelayFormatOpenAI, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received [][]byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, _ := io.ReadAll(r.Body)
				received = append(received, wire)
				_, _ = io.WriteString(w, `{}`)
			}))
			defer upstream.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{RelayFormat: tc.format, RelayMode: relayconstant.RelayModeChatCompletions, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, ChannelBaseUrl: upstream.URL, UpstreamModelName: tc.model,
				ChannelSetting: dto.ChannelSettings{MoonshotSplitCacheText: tc.on, MoonshotHoistDynamicTools: tc.hoist},
			}}
			body := `{"model":"` + tc.model + `","messages":[{"role":"user","content":` + string(quoted) + `}]}`
			if tc.hoist {
				body = `{"model":"` + tc.model + `","tool_choice":"required","messages":[{"role":"user","content":` + string(quoted) + `},{"role":"system","tools":[` + tool + `]}]}`
			}
			response, err := (&Adaptor{}).DoRequest(ctx, info, strings.NewReader(body))
			require.NoError(t, err)
			require.NoError(t, response.(*http.Response).Body.Close())
			require.Len(t, received, 1)
			content := gjson.GetBytes(received[0], "messages.0.content")
			if !tc.splitIt {
				assert.Equal(t, gjson.String, content.Type)
				assert.Equal(t, long, content.String())
				return
			}
			parts := content.Array()
			require.Len(t, parts, 6)
			assert.True(t, strings.HasPrefix(parts[5].Get("text").String(), "请求编号：1"))
			if tc.hoist {
				assert.JSONEq(t, `[`+tool+`]`, gjson.GetBytes(received[0], "tools").Raw)
				assert.Len(t, gjson.GetBytes(received[0], "messages").Array(), 1)
			}
		})
	}
}

// TestKimiToolSchemaRecursion covers the official answer for a tool schema whose
// recursion has no finite instance. The verdicts below were measured against
// api.moonshot.cn: a cycle is rejected only when every edge on it is mandatory.
func TestKimiToolSchemaRecursion(t *testing.T) {
	for _, tc := range []struct {
		name, parameters string
		wantError        bool
	}{
		{name: "self reference is required", wantError: true,
			parameters: `{"type":"object","properties":{"n":{"$ref":"#"}},"required":["n"]}`},
		{name: "self reference is optional",
			parameters: `{"type":"object","properties":{"n":{"$ref":"#"}}}`},
		{name: "required names another property",
			parameters: `{"type":"object","properties":{"n":{"$ref":"#"}},"required":["zzz"]}`},
		{name: "$defs cycle through required", wantError: true,
			parameters: `{"type":"object","properties":{"n":{"$ref":"#/$defs/N"}},"required":["n"],"$defs":{"N":{"type":"object","properties":{"x":{"$ref":"#/$defs/N"}},"required":["x"]}}}`},
		{name: "$defs cycle through optional",
			parameters: `{"type":"object","properties":{"n":{"$ref":"#/$defs/N"}},"$defs":{"N":{"type":"object","properties":{"x":{"$ref":"#/$defs/N"}}}}}`},
		{name: "mutual cycle closed on required", wantError: true,
			parameters: `{"type":"object","properties":{"a":{"$ref":"#/$defs/A"}},"required":["a"],"$defs":{"A":{"type":"object","properties":{"b":{"$ref":"#/$defs/B"}},"required":["b"]},"B":{"type":"object","properties":{"a":{"$ref":"#/$defs/A"}},"required":["a"]}}}`},
		{name: "mutual cycle with one free edge",
			parameters: `{"type":"object","properties":{"a":{"$ref":"#/$defs/A"}},"required":["a"],"$defs":{"A":{"type":"object","properties":{"b":{"$ref":"#/$defs/B"}},"required":["b"]},"B":{"type":"object","properties":{"a":{"$ref":"#/$defs/A"}}}}}`},
		{name: "array items without minItems",
			parameters: `{"type":"object","properties":{"n":{"type":"array","items":{"$ref":"#"}}},"required":["n"]}`},
		{name: "array items with minItems", wantError: true,
			parameters: `{"type":"object","properties":{"n":{"type":"array","items":{"$ref":"#"},"minItems":1}},"required":["n"]}`},
		{name: "anyOf with one free branch",
			parameters: `{"type":"object","properties":{"n":{"anyOf":[{"$ref":"#"},{"type":"string"}]}},"required":["n"]}`},
		{name: "anyOf with every branch cycling", wantError: true,
			parameters: `{"type":"object","properties":{"n":{"anyOf":[{"$ref":"#"},{"$ref":"#"}]}},"required":["n"]}`},
		{name: "allOf is not mandatory",
			parameters: `{"type":"object","properties":{"n":{"allOf":[{"$ref":"#"}]}},"required":["n"]}`},
		{name: "not is mandatory", wantError: true,
			parameters: `{"type":"object","properties":{"n":{"not":{"$ref":"#"}}},"required":["n"]}`},
		{name: "then is mandatory", wantError: true,
			parameters: `{"type":"object","properties":{"n":{"if":{"type":"object"},"then":{"$ref":"#"}}},"required":["n"]}`},
		// A $ref in a data keyword is not a constraint.
		{name: "reference inside default",
			parameters: `{"type":"object","properties":{"n":{"type":"string","default":{"$ref":"#"}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"tools":[{"type":"function","function":{"name":"f","description":"f","parameters":` + tc.parameters + `}}]}`
			err := validateKimiToolSchemaRecursion([]byte(body))
			if !tc.wantError {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, "infinite recursion without termination condition")
		})
	}
}

// TestKimiToolSchemaRecursionWorkspace checks that tools declared inside a
// system message and nested tool containers are searched too.
func TestKimiToolSchemaRecursionWorkspace(t *testing.T) {
	const recursive = `{"type":"object","properties":{"n":{"$ref":"#"}},"required":["n"]}`
	for _, tc := range []struct{ name, body string }{
		{name: "system message tools",
			body: `{"messages":[{"role":"system","content":"","tools":[{"type":"function","function":{"name":"f","parameters":` + recursive + `}}]}]}`},
		{name: "nested tool container",
			body: `{"tools":[{"type":"namespace","tools":[{"type":"function","function":{"name":"f","parameters":` + recursive + `}}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, validateKimiToolSchemaRecursion([]byte(tc.body)),
				"infinite recursion without termination condition")
		})
	}
}

// TestKimiToolSchemaRecursionGatedByK31Switch pins the compatibility switch: the
// baseline still forwards a recursive schema for the upstream to judge.
func TestKimiToolSchemaRecursionGatedByK31Switch(t *testing.T) {
	const recursive = `{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"n":{"$ref":"#"}},"required":["n"]}}}`
	for _, tc := range []struct {
		name   string
		compat bool
	}{
		{name: "switch off leaves the schema to the upstream"},
		{name: "switch on validates locally", compat: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro",
					ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: true, MoonshotK31ParamCompat: tc.compat},
				},
			}
			assert.Equal(t, tc.compat, kvvOfficialCompat(info))
		})
	}
}

// TestKimiToolSchemaEnumTypes covers the official 400 for an enum node without a
// type. Baseline inference fills that type in, so the check only runs under the
// K3.1 switch and must not weaken the cases the official API accepts.
func TestKimiToolSchemaEnumTypes(t *testing.T) {
	for _, tc := range []struct {
		name, parameters string
		wantError        bool
	}{
		{name: "enum without type", wantError: true,
			parameters: `{"type":"object","properties":{"v":{"enum":["a","b"]}},"required":["v"]}`},
		{name: "enum inside anyOf branch", wantError: true,
			parameters: `{"type":"object","properties":{"v":{"anyOf":[{"type":"string"},{"enum":["a"]}]}},"required":["v"]}`},
		{name: "enum with type",
			parameters: `{"type":"object","properties":{"v":{"type":"string","enum":["a","b"]}},"required":["v"]}`},
		{name: "empty type string", wantError: true,
			parameters: `{"type":"object","properties":{"v":{"type":"","enum":["a"]}},"required":["v"]}`},
		{name: "const without type",
			parameters: `{"type":"object","properties":{"v":{"const":"a"}},"required":["v"]}`},
		{name: "oneOf without type",
			parameters: `{"type":"object","properties":{"v":{"oneOf":[{"type":"string"},{"type":"integer"}]}},"required":["v"]}`},
		{name: "structural keyword without type",
			parameters: `{"type":"object","properties":{"v":{"properties":{"x":{"type":"string"}}}},"required":["v"]}`},
		{name: "enum inside $defs", wantError: true,
			parameters: `{"type":"object","properties":{"v":{"$ref":"#/$defs/E"}},"required":["v"],"$defs":{"E":{"enum":["a"]}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tools []dto.ToolCallRequest
			require.NoError(t, common.UnmarshalJsonStr(`[{"type":"function","function":{"name":"f","description":"f","parameters":`+tc.parameters+`}}]`, &tools))
			err := validateKimiToolEnumTypes(tools, "tools")
			if !tc.wantError {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, "enum without type")
		})
	}
}

func TestMoonshotMessagesCacheControlFallback(t *testing.T) {
	service.InitHttpClient()
	const marked = `{"model":"kimi-k3.1-pro","cache_control":{"type":"ephemeral"},"tools":[{"name":"lookup","cache_control":{"type":"ephemeral"},"input_schema":{"type":"object","properties":{"cache_control":{"type":"integer"}}}}],"system":[{"type":"text","text":"cache_control","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"lookup","id":"a","input":{"cache_control":9007199254740993},"cache_control":{"type":"ephemeral"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":[{"type":"text","text":"ok","cache_control":{"type":"ephemeral"}}]}]}]}`
	const unmarked = `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":"cache_control"}],"tools":[{"name":"test","input_schema":{"properties":{"cache_control":{}}}}]}`
	const rejected = `{"error":{"message":"cache_control is not supported on Chat Completions; use prompt_cache_options"}}`
	const alternate = `{"error":{"message":"This endpoint does not support the cache_control field"}}`
	for _, tc := range []struct {
		name, body, reply               string
		firstStatus, finalStatus, calls int
		stream                          bool
	}{
		{"accepted breakpoint", marked, `{"ok":true}`, 200, 200, 1, false},
		{"known rejection", marked, rejected, 400, 200, 2, false},
		{"stream", marked, alternate, 400, 200, 2, true},
		{"second rejection", marked, rejected, 400, 400, 2, false},
		{"no breakpoints", unmarked, rejected, 400, 400, 1, false},
		{"unrelated bad request", marked, `{"error":{"message":"cache_control.ttl is not supported"}}`, 400, 400, 1, false},
		{"server error", marked, rejected, 500, 500, 1, false},
		{"oversized response", marked, rejected + strings.Repeat(" ", 65536), 400, 400, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests [][]byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/messages", r.URL.Path)
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				requests = append(requests, body)
				if len(requests) == 1 {
					w.WriteHeader(tc.firstStatus)
					_, _ = io.WriteString(w, tc.reply)
					return
				}
				if tc.stream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				w.WriteHeader(tc.finalStatus)
				if tc.finalStatus == 400 {
					_, _ = io.WriteString(w, rejected)
				} else if tc.stream {
					_, _ = io.WriteString(w, "event: message_stop\n\n")
				} else {
					_, _ = io.WriteString(w, `{"type":"message"}`)
				}
			}))
			defer upstream.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, IsStream: tc.stream, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot, ChannelBaseUrl: upstream.URL, UpstreamModelName: "kimi-k3.1-pro"}}
			input := tc.body
			if tc.stream {
				input = strings.Replace(input, `{"model"`, `{"stream":true,"model"`, 1)
			}
			response, err := (&Adaptor{}).DoRequest(ctx, info, strings.NewReader(input))
			require.NoError(t, err)
			resp := response.(*http.Response)
			defer resp.Body.Close()
			result, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Len(t, requests, tc.calls)
			assert.Equal(t, input, string(requests[0]))
			assert.Equal(t, tc.finalStatus, resp.StatusCode)
			if tc.calls == 1 {
				assert.Equal(t, tc.reply, string(result))
				return
			}
			// Compare the whole request: schema property, tool argument, large
			// integer, literal text and stream flag all survive the second send.
			expected := strings.ReplaceAll(input, `,"cache_control":{"type":"ephemeral"}`, "")
			assert.JSONEq(t, expected, string(requests[1]))
			assert.Equal(t, "9007199254740993", gjson.GetBytes(requests[1], "messages.0.content.0.input.cache_control").Raw)
			if tc.stream {
				assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
				assert.Equal(t, "event: message_stop\n\n", string(result))
			}
			if tc.finalStatus == 400 {
				assert.Equal(t, rejected, string(result))
			}
		})
	}
}

// TestKVVK31MessagesCacheControl covers the official Messages cache contract. A
// decoded body carries content blocks as []any, which is the shape that
// actually reaches the adapter, so the block-level mark must be validated from
// that shape and not only from the typed one.
//
// The upstream answers a Chat Completions content part carrying cache_control
// with a 400, so a lifted mark must leave the body entirely: the lifetime moves
// to the request level and no block keeps the member.
func TestKVVK31MessagesCacheControl(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantError string
		compat                bool
		wantTTL               string
		wantBlockMark         bool
	}{
		{
			name:      "unsupported member is rejected",
			compat:    true,
			body:      `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","mode":"readonly"}}]}]}`,
			wantError: "cache_control.mode",
		},
		{
			name:    "clean block mark is lifted and removed",
			compat:  true,
			body:    `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`,
			wantTTL: "1h",
		},
		{
			name:    "block mark without a lifetime still lifts",
			compat:  true,
			body:    `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`,
			wantTTL: "5m",
		},
		{
			name:    "system block mark is lifted and removed",
			compat:  true,
			body:    `{"model":"kimi-k3.1-pro","system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"hi"}]}`,
			wantTTL: "1h",
		},
		{
			name:    "request level mark wins over a block mark",
			compat:  true,
			body:    `{"model":"kimi-k3.1-pro","cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`,
			wantTTL: "1h",
		},
		{
			name:      "bad type is rejected",
			compat:    true,
			body:      `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"persistent"}}]}]}`,
			wantError: "cache_control.type",
		},
		{
			name:          "switch off leaves the mark alone",
			body:          `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","mode":"readonly"}}]}]}`,
			wantBlockMark: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request dto.ClaudeRequest
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &request))
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, Request: &request, ChannelMeta: &relaycommon.ChannelMeta{
				ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro",
				ChannelSetting: dto.ChannelSettings{MoonshotKVVEnabled: true, MoonshotK31ParamCompat: tc.compat},
			}}
			converted, err := (&Adaptor{}).ConvertClaudeRequest(nil, info, &request)
			if tc.wantError != "" {
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, 400, apiErr.StatusCode)
				assert.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			wire, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.Equal(t, tc.wantTTL, gjson.GetBytes(wire, "cache_control.ttl").String())
			assert.Equal(t, tc.wantBlockMark, strings.Contains(string(wire), "messages\":[{\"role\":\"user\",\"content\":[{\"cache_control\""))
		})
	}
}

// A cache write is a subset of the prompt it belongs to, so the relay must
// never forward a write count larger than prompt_tokens. Upstream accounting
// drifts; forwarding it unchanged makes clients report "write > input".
func TestMoonshotCacheWriteBoundedByPrompt(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			originalTimeout := constant.StreamingTimeout
			constant.StreamingTimeout = 30
			t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			usage := `"usage":{"prompt_tokens":1024,"completion_tokens":1,"total_tokens":1025,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":4096,"cache_write_tokens_details":{"ttl_5m_tokens":4096}}}`
			headers := http.Header{"Content-Type": {"application/json"}}
			body := `{"id":"chatcmpl-kimi","model":"kimi-k3.1-pro","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],` + usage + `}`
			if stream {
				body = "data: " + `{"id":"chatcmpl-kimi","model":"kimi-k3.1-pro","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}],` + usage + "}\n\ndata: [DONE]\n\n"
				headers.Set("Content-Type", "text/event-stream")
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
				IsStream: stream, DisablePing: true, ShouldIncludeUsage: true,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro"},
			}
			_, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
			require.Nil(t, apiErr)

			forwarded := recorder.Body.String()
			prompt := gjson.Get(forwarded, "usage.prompt_tokens").Int()
			written := gjson.Get(forwarded, "usage.prompt_tokens_details.cache_write_tokens").Int()
			if !stream {
				require.NotZero(t, prompt, "forwarded body: %s", forwarded)
			}
			require.LessOrEqual(t, written, prompt, "cache_write_tokens 不能大于 prompt_tokens: %s", forwarded)
		})
	}
}

func TestMoonshotStreamUsagePosition(t *testing.T) {
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })

	const report = `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"vendor":{"zero":0},"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0,"cache_write_tokens_details":{"ttl_5m_tokens":0,"ttl_1h_tokens":0}}}`
	const fields = `"id":"chatcmpl-kimi","created":123,"object":"chat.completion.chunk","model":"kimi-k3.1-pro","system_fingerprint":"fp_test"`
	const choice = `"index":0,"delta":{"content":"OK"},"finish_reason":"stop"`
	const stop = `{` + fields + `,"choices":[{` + choice + `}]}`
	const root = `{` + fields + `,"choices":[{` + choice + `}],"usage":` + report + `}`
	const nested = `{` + fields + `,"choices":[{` + choice + `,"usage":` + report + `}]}`
	const standalone = `{` + fields + `,"choices":[],"usage":` + report + `}`
	const trailer = `{` + fields + `,"choices":[]}`
	// A real standalone event owns its metadata and extensions.
	const official = `{` + fields + `,"choices":[],"upstream":true,"usage":` + report + `}`
	on, off := common.GetPointer(true), common.GetPointer(false)
	for _, tc := range []struct {
		name     string
		fix      *bool
		include  bool
		in, want []string
	}{
		{"legacy root", nil, true, []string{root}, []string{stop, standalone}},
		{"root", on, true, []string{root}, []string{stop, standalone}},
		{"choice only", on, true, []string{nested}, []string{stop, standalone}},
		{"official dual report", on, true, []string{nested, official}, []string{stop, official}},
		{"already separate", on, true, []string{stop, official}, []string{stop, official}},
		{"trailing event", on, true, []string{root, trailer}, []string{stop, trailer, standalone}},
		{"off keeps root", off, true, []string{root}, []string{root}},
		{"off keeps choice", off, true, []string{nested}, []string{nested}},
		{"off keeps official", off, true, []string{nested, official}, []string{nested, official}},
		{"usage not requested", on, false, []string{nested, official}, []string{stop}},
		{"usage not requested with fix off", off, false, []string{root}, []string{stop}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			body := "data: " + strings.Join(tc.in, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			info := &relaycommon.RelayInfo{
				RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
				IsStream: true, DisablePing: true, ShouldIncludeUsage: tc.include,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro", ChannelSetting: dto.ChannelSettings{MoonshotFixUsagePosition: tc.fix}},
			}
			billed, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
			require.Nil(t, apiErr)
			assert.Equal(t, 12, billed.(*dto.Usage).TotalTokens)
			var events []string
			for line := range strings.SplitSeq(recorder.Body.String(), "\n") {
				if event, ok := strings.CutPrefix(line, "data: "); ok {
					events = append(events, event)
				}
			}
			require.Len(t, events, len(tc.want)+1)
			for i, want := range tc.want {
				assert.JSONEq(t, want, events[i])
			}
			assert.Equal(t, "[DONE]", events[len(tc.want)])
		})
	}
}
