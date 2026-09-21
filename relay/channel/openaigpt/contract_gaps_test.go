package openaigpt

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

// responsesRequest builds a Responses request from a JSON literal so each case
// reads as the body a client would actually send.
func responsesRequest(t *testing.T, body string) *dto.OpenAIResponsesRequest {
	t.Helper()
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &request))
	return &request
}

func chatRequest(t *testing.T, body string) *dto.GeneralOpenAIRequest {
	t.Helper()
	var request dto.GeneralOpenAIRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &request))
	return &request
}

// TestRejectsInvalidReasoningMode covers feedback item 17: reasoning.mode was
// accepted with any value and the request generated normally.
func TestRejectsInvalidReasoningMode(t *testing.T) {
	err := ValidateResponsesRequest(responsesRequest(t,
		`{"model":"gpt-5.6-sol","input":"Reply only OK.","reasoning":{"mode":"invalid"},"max_output_tokens":16}`))
	require.ErrorContains(t, err, "invalid reasoning.mode")
	require.ErrorContains(t, err, "pro, standard")
}

func TestAcceptsOfficialReasoningVocabularies(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-5.6-sol","input":"OK","reasoning":{"mode":"standard"},"max_output_tokens":16}`,
		`{"model":"gpt-5.6-sol","input":"OK","reasoning":{"mode":"pro"},"max_output_tokens":16}`,
		`{"model":"gpt-5.6-sol","input":"OK","reasoning":{"context":"all_turns"},"max_output_tokens":16}`,
		`{"model":"gpt-5.6-sol","input":"OK","reasoning":{"summary":"detailed"},"max_output_tokens":16}`,
		`{"model":"gpt-5.6-sol","input":"OK","reasoning":{"mode":null},"max_output_tokens":16}`,
	} {
		require.NoError(t, ValidateResponsesRequest(responsesRequest(t, body)), body)
	}
}

func TestRejectsInvalidReasoningContext(t *testing.T) {
	err := ValidateResponsesRequest(responsesRequest(t,
		`{"model":"gpt-5.6-sol","input":"OK","reasoning":{"context":"every_turn"},"max_output_tokens":16}`))
	require.ErrorContains(t, err, "invalid reasoning.context")
}

// The DTO-level validator remains permissive because it is also used before
// the final body normalizer. PrepareResponsesBody clamps values below the
// official Responses minimum before the request is sent upstream.
func TestAcceptsMaxOutputTokensBelowOfficialMinimum(t *testing.T) {
	for _, value := range []string{"0", "1", "5", "16"} {
		require.NoError(t, ValidateResponsesRequest(responsesRequest(t,
			`{"model":"gpt-5.6-sol","input":"Explain why the sky is blue.","max_output_tokens":`+value+`}`)), value)
	}
}

func TestRejectsSamplingParametersOutsideOfficialRange(t *testing.T) {
	require.ErrorContains(t,
		ValidateResponsesRequest(responsesRequest(t, `{"model":"gpt-5.6-sol","input":"OK","temperature":3}`)),
		"temperature must be between 0 and 2")
	require.ErrorContains(t,
		ValidateResponsesRequest(responsesRequest(t, `{"model":"gpt-5.6-sol","input":"OK","top_p":1.5}`)),
		"top_p must be between 0 and 1")
	// The values the QA report saw silently rewritten are perfectly valid and
	// must still pass through untouched.
	require.NoError(t,
		ValidateResponsesRequest(responsesRequest(t, `{"model":"gpt-5.6-sol","input":"OK","temperature":0.2,"top_p":0.9}`)))
}

func TestRejectsOversizedMetadata(t *testing.T) {
	require.ErrorContains(t,
		ValidateResponsesRequest(responsesRequest(t,
			`{"model":"gpt-5.6-sol","input":"OK","metadata":{"qa_case":42}}`)),
		"must be a string")
	// The exact metadata from the report is legal and must survive validation.
	require.NoError(t,
		ValidateResponsesRequest(responsesRequest(t,
			`{"model":"gpt-5.6-sol","input":"OK","metadata":{"qa_case":"hx-r06"}}`)))
}

// TestRejectsChatOnlyStreamOptionsOnResponses covers feedback item 16.
func TestRejectsChatOnlyStreamOptionsOnResponses(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		body := `{"model":"gpt-5.6-sol","input":"Reply only OK.","stream":true,` +
			`"stream_options":{"include_usage":` + value + `},"max_output_tokens":16}`
		err := ValidateInboundResponsesBody([]byte(body))
		require.ErrorContains(t, err, "stream_options.include_usage", value)
	}
	// include_obfuscation is the one official Responses member.
	require.NoError(t, ValidateInboundResponsesBody([]byte(
		`{"model":"gpt-5.6-sol","input":"OK","stream":true,"stream_options":{"include_obfuscation":true}}`)))
}

// TestRejectsUnknownFields covers feedback item 18.
func TestRejectsUnknownFields(t *testing.T) {
	require.ErrorContains(t,
		ValidateInboundResponsesBody([]byte(`{"model":"gpt-5.6-sol","input":"OK","qa_unknown_field":true}`)),
		"unrecognized request argument supplied: qa_unknown_field")
	require.ErrorContains(t,
		ValidateInboundResponsesBody([]byte(`{"model":"gpt-5.6-sol","input":"OK","ultra":true}`)),
		"ultra")
	require.ErrorContains(t,
		ValidateInboundResponsesBody([]byte(`{"model":"gpt-5.6-sol","input":"OK","reasoning":{"unknown":1}}`)),
		"reasoning.unknown")
	require.ErrorContains(t,
		ValidateInboundChatBody([]byte(`{"model":"gpt-5.6-sol","messages":[],"qa_unknown_field":true}`)),
		"qa_unknown_field")

	// Modeled fields, including vendor extensions, stay the upstream's call.
	require.NoError(t, ValidateInboundResponsesBody([]byte(
		`{"model":"gpt-5.6-sol","input":"OK","previous_response_id":"resp_1","prompt_cache_key":"k"}`)))
	require.NoError(t, ValidateInboundChatBody([]byte(
		`{"model":"gpt-5.6-sol","messages":[],"logprobs":true,"top_logprobs":2}`)))

	// client_metadata is a top-level member Codex sends on Responses calls. It
	// was rejected as unknown, which failed the call with 400 before any
	// retry, failover, or billing could run. Forwarding is asserted separately
	// in the relay package; passing this gate only means it was not rejected.
	require.NoError(t, ValidateInboundResponsesBody([]byte(
		`{"model":"gpt-5.6-sol","input":"OK","client_metadata":{"cli_version":"0.9.1","terminal_type":"iTerm.app"}}`)))
	// Chat keeps rejecting it on purpose: the Chat-through-Responses converter
	// rebuilds the request field by field and would drop the member, so
	// accepting it here would report success for a parameter that never
	// reaches the upstream.
	require.ErrorContains(t,
		ValidateInboundChatBody([]byte(`{"model":"gpt-5.6-sol","messages":[],"client_metadata":{"cli_version":"0.9.1"}}`)),
		"client_metadata")
	// A body that is not a JSON object is left for the request parser to report.
	require.NoError(t, ValidateInboundChatBody([]byte(`not json`)))
	require.NoError(t, ValidateInboundChatBody(nil))
}

// TestRejectsInvalidStrictSchemas covers feedback item 19: all three illegal
// strict schemas were executed with HTTP 200.
func TestRejectsInvalidStrictSchemas(t *testing.T) {
	cases := []struct {
		name     string
		schema   string
		contains string
	}{
		{
			name: "nested object missing additionalProperties",
			schema: `{"type":"object","properties":{"result":{"type":"object",` +
				`"properties":{"ok":{"type":"boolean"}},"required":["ok"]}},` +
				`"required":["result"],"additionalProperties":false}`,
			contains: "must set additionalProperties to false",
		},
		{
			name: "required omits a declared property",
			schema: `{"type":"object","properties":{"ok":{"type":"boolean"},"value":{"type":"integer"}},` +
				`"required":["ok"],"additionalProperties":false}`,
			contains: `"value" is missing`,
		},
		{
			name: "required names an undeclared property",
			schema: `{"type":"object","properties":{"ok":{"type":"boolean"}},` +
				`"required":["ok","ghost"],"additionalProperties":false}`,
			contains: `names "ghost"`,
		},
		{
			name: "additionalProperties is true",
			schema: `{"type":"object","properties":{"ok":{"type":"boolean"}},` +
				`"required":["ok"],"additionalProperties":true}`,
			contains: "additionalProperties must be false",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body := `{"model":"gpt-5.6-sol","input":"Return a result.","max_output_tokens":128,` +
				`"text":{"format":{"type":"json_schema","name":"qa","strict":true,"schema":` + testCase.schema + `}}}`
			require.ErrorContains(t, ValidateResponsesRequest(responsesRequest(t, body)), testCase.contains)

			chatBody := `{"model":"gpt-5.6-sol","messages":[],"response_format":{"type":"json_schema",` +
				`"json_schema":{"name":"qa","strict":true,"schema":` + testCase.schema + `}}}`
			require.ErrorContains(t, ValidateChatRequest(chatRequest(t, chatBody)), testCase.contains)
		})
	}
}

func TestAcceptsValidStrictSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"result":{"type":"object",` +
		`"properties":{"ok":{"type":"boolean"},"value":{"type":"integer"}},` +
		`"required":["ok","value"],"additionalProperties":false}},` +
		`"required":["result"],"additionalProperties":false}`
	body := `{"model":"gpt-5.6-sol","input":"Return a result.","max_output_tokens":128,` +
		`"text":{"format":{"type":"json_schema","name":"qa","strict":true,"schema":` + schema + `}}}`
	require.NoError(t, ValidateResponsesRequest(responsesRequest(t, body)))
}

// A loose schema is not held to strict-mode validation rules. The final-body
// compatibility normalizer may still close its object schemas for upstreams
// that require additionalProperties=false.
func TestDoesNotStrictlyValidateNonStrictSchemas(t *testing.T) {
	schema := `{"type":"object","properties":{"ok":{"type":"boolean"},"value":{"type":"integer"}},"required":["ok"]}`
	body := `{"model":"gpt-5.6-sol","input":"Return a result.","max_output_tokens":128,` +
		`"text":{"format":{"type":"json_schema","name":"qa","schema":` + schema + `}}}`
	require.NoError(t, ValidateResponsesRequest(responsesRequest(t, body)))
}

// Nullable objects are declared as "type": ["object","null"] and still carry
// the strict constraints.
func TestStrictSchemaCoversNullableObjects(t *testing.T) {
	schema := `{"type":["object","null"],"properties":{"ok":{"type":"boolean"}},"required":["ok"]}`
	require.ErrorContains(t, ValidateStrictSchema(decodeSchema(t, schema), "schema"),
		"must set additionalProperties to false")
}

// A $ref carries no constraints of its own; the definition it points at is
// validated where it is declared.
func TestStrictSchemaValidatesDefsAndSkipsRefs(t *testing.T) {
	schema := `{"type":"object","properties":{"node":{"$ref":"#/$defs/node"}},` +
		`"required":["node"],"additionalProperties":false,` +
		`"$defs":{"node":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}}`
	require.ErrorContains(t, ValidateStrictSchema(decodeSchema(t, schema), "schema"),
		"schema.$defs.node must set additionalProperties to false")
}

func TestStrictSchemaValidatesToolParameters(t *testing.T) {
	body := `{"model":"gpt-5.6-sol","input":"Use the tool.","max_output_tokens":128,` +
		`"tools":[{"type":"function","name":"get_weather","strict":true,` +
		`"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":[]}}]}`
	require.ErrorContains(t, ValidateResponsesRequest(responsesRequest(t, body)),
		"tools[0].parameters")

	chatBody := `{"model":"gpt-5.6-sol","messages":[],"tools":[{"type":"function","function":{` +
		`"name":"get_weather","strict":true,"parameters":{"type":"object",` +
		`"properties":{"city":{"type":"string"}},"required":[]}}}]}`
	require.ErrorContains(t, ValidateChatToolSchemas([]byte(chatBody)), "tools[0].function.parameters")
}

func decodeSchema(t *testing.T, raw string) any {
	t.Helper()
	var schema any
	require.NoError(t, common.UnmarshalJsonStr(raw, &schema))
	return schema
}

// TestRejectsConflictingToolResults covers feedback item 14: two conflicting
// results for one tool_call_id were both fed to the model.
func TestRejectsConflictingToolResults(t *testing.T) {
	body := `{"model":"gpt-5.6-sol","messages":[
		{"role":"user","content":"Call get_weather for Beijing."},
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_qa_duplicate","type":"function",
			"function":{"name":"get_weather","arguments":"{\"city\":\"Beijing\"}"}}]},
		{"role":"tool","tool_call_id":"call_qa_duplicate","content":"{\"weather\":\"sunny\"}"},
		{"role":"tool","tool_call_id":"call_qa_duplicate","content":"{\"weather\":\"rain\"}"},
		{"role":"user","content":"Report the tool result."}]}`
	err := ValidateChatRequest(chatRequest(t, body))
	require.ErrorContains(t, err, "second result for tool_call_id")
	require.ErrorContains(t, err, "call_qa_duplicate")
}

func TestRejectsOrphanToolResult(t *testing.T) {
	body := `{"model":"gpt-5.6-sol","messages":[
		{"role":"user","content":"hi"},
		{"role":"tool","tool_call_id":"call_missing","content":"{}"}]}`
	require.ErrorContains(t, ValidateChatRequest(chatRequest(t, body)),
		"does not match any preceding assistant tool_call")
}

func TestAcceptsWellFormedToolHistory(t *testing.T) {
	body := `{"model":"gpt-5.6-sol","messages":[
		{"role":"user","content":"Call get_weather for Beijing."},
		{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{}"}},
			{"id":"call_b","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_b","content":"{\"weather\":\"rain\"}"},
		{"role":"tool","tool_call_id":"call_a","content":"{\"weather\":\"sunny\"}"}]}`
	require.NoError(t, ValidateChatRequest(chatRequest(t, body)))
}
