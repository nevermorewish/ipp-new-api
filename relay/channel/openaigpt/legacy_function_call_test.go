package openaigpt

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestDetectsLegacyFunctionCallRequests covers the request half of feedback
// item 9.
func TestDetectsLegacyFunctionCallRequests(t *testing.T) {
	legacy := []byte(`{"model":"gpt-5.6-sol","messages":[],"functions":[{"name":"get_weather"}],` +
		`"function_call":{"name":"get_weather"}}`)
	require.True(t, UsesLegacyFunctionCall(legacy))

	// functions alone is still the legacy contract.
	require.True(t, UsesLegacyFunctionCall([]byte(
		`{"model":"gpt-5.6-sol","messages":[],"functions":[{"name":"get_weather"}]}`)))

	// A caller that sends tools knows the current contract and expects
	// tool_calls back, even if it also carries functions for compatibility.
	require.False(t, UsesLegacyFunctionCall([]byte(
		`{"model":"gpt-5.6-sol","messages":[],"tools":[{"type":"function","function":{"name":"x"}}],`+
			`"functions":[{"name":"get_weather"}]}`)))

	require.False(t, UsesLegacyFunctionCall([]byte(`{"model":"gpt-5.6-sol","messages":[]}`)))
	require.False(t, UsesLegacyFunctionCall([]byte(`{"functions":null}`)))
	require.False(t, UsesLegacyFunctionCall([]byte(`not json`)))
}

// TestRestoreLegacyFunctionCall covers the response half of feedback item 9:
// a legacy request received message.tool_calls instead of message.function_call.
func TestRestoreLegacyFunctionCall(t *testing.T) {
	upstream := []byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-5.6-sol",
		"choices":[{"index":0,"finish_reason":"tool_calls","message":{
			"role":"assistant","content":null,
			"tool_calls":[{"id":"call_1","type":"function","function":{
				"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]}}],
		"usage":{"prompt_tokens":9,"completion_tokens":5,"total_tokens":14}}`)

	restored, changed := RestoreLegacyFunctionCall(upstream)
	require.True(t, changed)

	require.Equal(t, "get_weather", gjson.GetBytes(restored, "choices.0.message.function_call.name").String())
	require.Equal(t, `{"city":"Paris"}`, gjson.GetBytes(restored, "choices.0.message.function_call.arguments").String())
	require.False(t, gjson.GetBytes(restored, "choices.0.message.tool_calls").Exists())
	require.Equal(t, "function_call", gjson.GetBytes(restored, "choices.0.finish_reason").String())

	// Unrelated members survive the rewrite.
	require.Equal(t, "chatcmpl-1", gjson.GetBytes(restored, "id").String())
	require.Equal(t, int64(14), gjson.GetBytes(restored, "usage.total_tokens").Int())
	require.Equal(t, "assistant", gjson.GetBytes(restored, "choices.0.message.role").String())
}

// The legacy contract has room for exactly one call, so a parallel call set is
// left alone rather than silently losing calls the model asked for.
func TestRestoreLegacyFunctionCallLeavesParallelCallsAlone(t *testing.T) {
	upstream := []byte(`{"choices":[{"index":0,"finish_reason":"tool_calls","message":{
		"role":"assistant","tool_calls":[
			{"id":"a","type":"function","function":{"name":"f","arguments":"{}"}},
			{"id":"b","type":"function","function":{"name":"g","arguments":"{}"}}]}}]}`)

	restored, changed := RestoreLegacyFunctionCall(upstream)
	require.False(t, changed)
	require.Equal(t, upstream, restored)
}

func TestRestoreLegacyFunctionCallIgnoresPlainResponses(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}]}`,
		`{"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[]}}]}`,
		`{"choices":[{"index":0,"message":{"role":"assistant","tool_calls":null}}]}`,
		`{"choices":[]}`,
		`{"error":{"message":"boom"}}`,
		`not json`,
	} {
		restored, changed := RestoreLegacyFunctionCall([]byte(body))
		require.False(t, changed, body)
		require.Equal(t, []byte(body), restored, body)
	}
}

// A custom tool call has no legacy equivalent, so it is left as tool_calls.
func TestRestoreLegacyFunctionCallSkipsNonFunctionCalls(t *testing.T) {
	upstream := []byte(`{"choices":[{"index":0,"finish_reason":"tool_calls","message":{
		"role":"assistant","tool_calls":[{"id":"a","type":"custom","custom":{"name":"sql_writer"}}]}}]}`)
	restored, changed := RestoreLegacyFunctionCall(upstream)
	require.False(t, changed)
	require.Equal(t, upstream, restored)
}
