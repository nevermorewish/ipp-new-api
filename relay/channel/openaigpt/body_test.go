package openaigpt

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareResponsesBodyValidatesPassThroughFields(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-luna","input":"hello","store":"false"}`)
	_, _, _, err := PrepareResponsesBody(body, "gpt-5.6-luna", dto.ChannelOtherSettings{})
	require.ErrorContains(t, err, "store must be a boolean")
}

func TestPrepareResponsesBodyValidatesFinalOverrides(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6","input":"hello","parallel_tool_calls":"true"}`)
	_, _, _, err := PrepareResponsesBody(body, "gpt-5.6", dto.ChannelOtherSettings{})
	require.ErrorContains(t, err, "parallel_tool_calls must be a boolean")
}

func TestPrepareResponsesBodyNormalizesMappedGPT56WithoutDroppingUnknownFields(t *testing.T) {
	body := []byte(`{"model":"internal-alias","input":"hello","provider_extension":{"keep":true},"tools":[{"type":"web_search_preview"}]}`)
	prepared, request, changed, err := PrepareResponsesBody(body, "gpt-5.6-sol", dto.ChannelOtherSettings{})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "gpt-5.6-sol", request.Model)
	require.Equal(t, "web_search", gjson.GetBytes(prepared, "tools.0.type").String())
	require.True(t, gjson.GetBytes(prepared, "provider_extension.keep").Bool())
	// Body pass-through semantics are preserved: effectiveModel is used for
	// validation and normalization, not written over the caller's model field.
	require.Equal(t, "internal-alias", gjson.GetBytes(prepared, "model").String())
}

func TestPrepareResponsesBodyNormalizesBodyGPT56ModelWithoutOverride(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6","input":"hello","provider_extension":{"keep":true},"tools":[{"type":"web_search_preview"},{"type":"computer_use_preview","display_width":1024,"display_height":768,"environment":"browser"}]}`)

	prepared, request, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{})

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "gpt-5.6", request.Model)
	require.Equal(t, "web_search", gjson.GetBytes(prepared, "tools.0.type").String())
	require.Equal(t, "computer", gjson.GetBytes(prepared, "tools.1.type").String())
	require.False(t, gjson.GetBytes(prepared, "tools.1.display_width").Exists())
	require.False(t, gjson.GetBytes(prepared, "tools.1.display_height").Exists())
	require.False(t, gjson.GetBytes(prepared, "tools.1.environment").Exists())
	require.True(t, gjson.GetBytes(prepared, "provider_extension.keep").Bool())
}

func TestPrepareResponsesBodyDropsUnsupportedGPT5Temperature(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-terra","input":"hello","temperature":0.2,"top_p":0.9,"provider_extension":{"keep":true}}`)
	prepared, request, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{RemoveGPTTemperature: true})
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(prepared, "temperature").Exists())
	require.Equal(t, 0.9, gjson.GetBytes(prepared, "top_p").Float())
	require.True(t, gjson.GetBytes(prepared, "provider_extension.keep").Bool())
	require.Nil(t, request.Temperature)
	require.NotNil(t, request.TopP)
}

func TestPrepareResponsesBodyPreservesGPT4SamplingParameters(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","input":"hello","temperature":0.2,"top_p":0.9}`)
	prepared, request, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{RemoveGPTTemperature: true})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, prepared)
	require.NotNil(t, request.Temperature)
	require.InDelta(t, 0.2, *request.Temperature, 1e-9)
	require.NotNil(t, request.TopP)
}

func TestPrepareResponsesBodyClosesNestedNonStrictSchemaWithoutStrictValidation(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","text":{"format":{"type":"json_schema","name":"qa","schema":{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"value":{"type":"string"}}}}},"required":[]}}}}`)

	prepared, _, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{})

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(prepared, "text.format.strict").Exists())
	require.False(t, gjson.GetBytes(prepared, "text.format.schema.additionalProperties").Bool())
	require.True(t, gjson.GetBytes(prepared, "text.format.schema.additionalProperties").Exists())
	require.False(t, gjson.GetBytes(prepared, "text.format.schema.properties.items.items.additionalProperties").Bool())
	require.True(t, gjson.GetBytes(prepared, "text.format.schema.properties.items.items.additionalProperties").Exists())
	// required intentionally omits the declared property. Non-strict mode must
	// not turn this compatibility rewrite into strict validation.
	require.Empty(t, gjson.GetBytes(prepared, "text.format.schema.required").Array())
}

func TestPrepareResponsesBodyClampsSmallMaxOutputTokens(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","max_output_tokens":1}`)

	prepared, request, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{})

	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, request.MaxOutputTokens)
	require.Equal(t, uint(16), *request.MaxOutputTokens)
	require.Equal(t, int64(16), gjson.GetBytes(prepared, "max_output_tokens").Int())
}

func TestNormalizeNonStrictChatResponseSchemaClosesObjectsOnly(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","messages":[],"response_format":{"type":"json_schema","json_schema":{"name":"qa","strict":false,"schema":{"type":"object","properties":{"result":{"type":["object","null"],"properties":{"ok":{"type":"boolean"}}}},"required":[]}}}}`)

	prepared, changed, err := NormalizeNonStrictChatResponseSchema(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(prepared, "response_format.json_schema.strict").Bool())
	require.True(t, gjson.GetBytes(prepared, "response_format.json_schema.schema.additionalProperties").Exists())
	require.False(t, gjson.GetBytes(prepared, "response_format.json_schema.schema.additionalProperties").Bool())
	require.True(t, gjson.GetBytes(prepared, "response_format.json_schema.schema.properties.result.additionalProperties").Exists())
	require.False(t, gjson.GetBytes(prepared, "response_format.json_schema.schema.properties.result.additionalProperties").Bool())

	request, validated, err := ValidateChatBody(prepared, "")
	require.NoError(t, err)
	require.True(t, validated)
	require.NotNil(t, request)
}

func TestNonStrictSchemaNormalizerDoesNotRepairStrictSchemas(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"hello","text":{"format":{"type":"json_schema","name":"qa","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}}}}`)

	prepared, _, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{})

	require.ErrorContains(t, err, "must set additionalProperties to false when strict is true")
	require.False(t, changed)
	require.Nil(t, prepared)
}

func TestValidateChatBodyAcceptsOfficialCustomToolShape(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":"required","tools":[{"type":"custom","custom":{"name":"code_exec","format":{"type":"text"}}}]}`)
	request, validated, err := ValidateChatBody(body, "gpt-5.4")
	require.NoError(t, err)
	require.True(t, validated)
	require.Len(t, request.Tools, 1)
}

// TestValidateChatBodyPassesThroughUnmodelledBodies guards the pass-through
// contract: dto.GeneralOpenAIRequest types max_tokens as uint and stream as
// bool, but providers routinely accept the JSON forms below. Rejecting them
// locally would fail traffic that previously succeeded, so an unparseable body
// reports validated=false and is forwarded untouched.
func TestValidateChatBodyPassesThroughUnmodelledBodies(t *testing.T) {
	bodies := map[string]string{
		"float max_tokens":         `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"max_tokens":100.0}`,
		"negative completion cap":  `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"max_completion_tokens":-1}`,
		"stringly typed stream":    `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"stream":"true"}`,
		"stringly typed n":         `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"n":"2"}`,
		"stringly typed temp":      `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"temperature":"0.5"}`,
		"provider tool collection": `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tools":{"named":"map"}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			request, validated, err := ValidateChatBody([]byte(body), "")
			require.NoError(t, err, "an unmodelled body must reach upstream rather than 400 locally")
			require.False(t, validated, "validation did not run, so opaque 400s must not count as capability gaps")
			require.Nil(t, request)
		})
	}
}

func TestFinalBodyModelControlsValidationWithoutPassThroughOverride(t *testing.T) {
	responsesBody := []byte(`{"model":"gpt-5.6","input":"hello","reasoning":{"effort":"minimal"}}`)
	_, _, _, err := PrepareResponsesBody(responsesBody, "", dto.ChannelOtherSettings{})
	require.ErrorContains(t, err, "not supported by GPT-5.6")

	chatBody := []byte(`{"model":"gpt-5.6","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"minimal"}`)
	_, _, err = ValidateChatBody(chatBody, "")
	require.ErrorContains(t, err, "not supported by GPT-5.6")
}

// Deprecated message IDs are normalized before validating the final body.
func TestPrepareResponsesBodyNormalizesDeprecatedMessageID(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-luna","input":[{"type":"message","id":"item_legacy","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)

	prepared, request, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{})

	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, request)
	// Every item_*-prefixed message id in the fixture must be gone from the
	// final body, while non-offending items (including the official msg_*
	// ids) are untouched.
	var input []map[string]any
	require.NoError(t, common.Unmarshal([]byte(gjson.GetBytes(prepared, "input").Raw), &input))
	for _, item := range input {
		id, _ := item["id"].(string)
		require.False(t, strings.HasPrefix(id, "item_"), "item_* id must have been dropped: %+v", item)
	}
}

// TestPrepareResponsesBodyNormalizesLegacyMaxTokensAndReasoningEffort is the
// end-to-end counterpart to the alias normalizer tests: a Codex-shaped
// request using the retired top-level max_tokens/reasoning_effort fields must
// pass through PrepareResponsesBody with both values preserved on their
// canonical fields, instead of failing local contract validation.
func TestPrepareResponsesBodyNormalizesLegacyMaxTokensAndReasoningEffort(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":"hi","max_tokens":512,"reasoning_effort":"high"}`)

	prepared, request, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{})

	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, request.MaxOutputTokens)
	require.EqualValues(t, 512, *request.MaxOutputTokens)
	require.NotNil(t, request.Reasoning)
	require.Equal(t, "high", request.Reasoning.Effort)
	require.False(t, gjson.GetBytes(prepared, "max_tokens").Exists())
	require.False(t, gjson.GetBytes(prepared, "reasoning_effort").Exists())
}
