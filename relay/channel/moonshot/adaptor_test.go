package moonshot

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
