package moonshot

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

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
