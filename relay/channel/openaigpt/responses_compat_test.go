package openaigpt

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesNamespaceRepairRespectsDeclarationsAndOrder(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","store":false,"input":[
		{"type":"function_call","name":"js","call_id":"before","arguments":"{}"},
		{"type":"additional_tools","tools":[{"name":"functions","tools":[{"type":"function","name":"js"}]}]},
		{"type":"function_call","name":"js","call_id":"after","arguments":"{\"encrypted\":true}"},
		{"type":"function_call_output","call_id":"after","output":"result"},
		{"type":"tool_search_output","tools":[{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"send_message"}]}]},
		{"type":"function_call","name":"send_message","namespace":null,"call_id":"search","arguments":"{}"},
		{"type":"function_call","name":"js","namespace":"explicit","call_id":"keep","arguments":"{}"}
	],"provider_extension":{"number":9007199254740993,"flag":false}}`)
	prepared, request, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{})
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(prepared, "input.0.namespace").Exists(), "later declarations cannot repair earlier calls")
	require.Equal(t, "functions", gjson.GetBytes(prepared, "input.2.namespace").String())
	require.Equal(t, "collaboration", gjson.GetBytes(prepared, "input.5.namespace").String())
	require.Equal(t, "explicit", gjson.GetBytes(prepared, "input.6.namespace").String())
	require.Equal(t, "after", gjson.GetBytes(prepared, "input.3.call_id").String())
	require.Equal(t, `{"encrypted":true}`, gjson.GetBytes(prepared, "input.2.arguments").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(prepared, "provider_extension.number").Raw)
	require.Equal(t, "false", gjson.GetBytes(prepared, "store").Raw)
	require.Equal(t, "functions", gjson.GetBytes(request.Input, "2.namespace").String())
	again, changed, err := NormalizeResponsesCompatibility(prepared, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, prepared, again)
}

func TestResponsesNamespaceDoesNotGuess(t *testing.T) {
	for _, test := range []struct {
		name, tools, call string
		wantError         bool
	}{
		{"unknown", `[]`, `{"type":"function_call","name":"js"}`, false},
		{"default wins", `[{"type":"function","name":"js"},{"type":"namespace","name":"functions","tools":[{"type":"function","name":"js"}]}]`, `{"type":"function_call","name":"js"}`, false},
		{"ambiguous", `[{"type":"namespace","name":"a","tools":[{"type":"function","name":"js"}]},{"type":"namespace","name":"b","tools":[{"type":"function","name":"js"}]}]`, `{"type":"function_call","name":"js"}`, true},
		{"explicit", `[{"type":"namespace","name":"a","tools":[{"type":"function","name":"js"}]},{"type":"namespace","name":"b","tools":[{"type":"function","name":"js"}]}]`, `{"type":"function_call","name":"js","namespace":"b"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"tools":` + test.tools + `,"input":[` + test.call + `]}`)
			got, changed, err := NormalizeResponsesCompatibility(body, false)
			if test.wantError {
				require.ErrorContains(t, err, "matches multiple namespaces")
				return
			}
			require.NoError(t, err)
			require.False(t, changed)
			require.Equal(t, body, got)
		})
	}
}

func TestAzureEncryptionRemovalPreservesPlaintextAndToolPairs(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","include":["reasoning.encrypted_content","message.output_text.logprobs"],"input":[
		{"type":"additional_tools","tools":[{"name":"collaboration","tools":[{"type":"function","name":"send_message","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true},"encrypted":{"type":"boolean"}},"default":{"encrypted":true},"$defs":{"part":{"type":"string","encrypted":true}}}}]}]},
		{"type":"function_call","name":"send_message","namespace":"collaboration","call_id":"c1","arguments":"{\"message\":\"hello\"}"},
		{"type":"function_call_output","call_id":"c1","output":"plaintext result"},
		{"type":"agent_message","author":"agent","recipient":"root","content":[{"type":"input_text","text":"keep plaintext"},{"type":"encrypted_content","encrypted_content":"opaque"}]},
		{"type":"reasoning","id":"rs_history","encrypted_content":"opaque","summary":[]},
		{"type":"function_call","name":"send_message","namespace":"collaboration","call_id":"c2","arguments":"{}"},
		{"type":"function_call_output","call_id":"c2","output":[{"type":"encrypted_content","encrypted_content":"opaque"}]},
		{"role":"user","content":[{"type":"encrypted_content","encrypted_content":"opaque"}]}
	],"metadata":{"encrypted_content":"user data"},"provider_extension":{"encrypted":true,"n":9007199254740993}}`)
	off, changed, err := NormalizeResponsesCompatibility(body, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, off)
	prepared, request, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{RemoveAzureGPTEncryption: true})
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, gjson.GetBytes(prepared, "input").Array(), 6)
	require.JSONEq(t, gjson.GetBytes(body, "input.0.tools").Raw, gjson.GetBytes(prepared, "input.0.tools").Raw, "tool schemas are declarations, not encrypted history")
	require.True(t, gjson.GetBytes(prepared, "input.0.tools.0.tools.0.parameters.properties.message.encrypted").Bool())
	require.Equal(t, "boolean", gjson.GetBytes(prepared, "input.0.tools.0.tools.0.parameters.properties.encrypted.type").String())
	require.True(t, gjson.GetBytes(prepared, "input.0.tools.0.tools.0.parameters.default.encrypted").Bool())
	require.True(t, gjson.GetBytes(prepared, "input.0.tools.0.tools.0.parameters.$defs.part.encrypted").Bool())
	require.Equal(t, "plaintext result", gjson.GetBytes(prepared, "input.2.output").String())
	require.Len(t, gjson.GetBytes(prepared, "input.3.content").Array(), 1)
	require.Equal(t, "keep plaintext", gjson.GetBytes(prepared, "input.3.content.0.text").String())
	require.Equal(t, "c2", gjson.GetBytes(prepared, "input.5.call_id").String())
	require.Equal(t, `""`, gjson.GetBytes(prepared, "input.5.output").Raw)
	require.JSONEq(t, `["message.output_text.logprobs"]`, gjson.GetBytes(prepared, "include").Raw)
	require.Equal(t, "user data", gjson.GetBytes(prepared, "metadata.encrypted_content").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(prepared, "provider_extension.n").Raw)
	require.JSONEq(t, gjson.GetBytes(prepared, "input").Raw, string(request.Input))
	again, changed, err := NormalizeResponsesCompatibility(prepared, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, prepared, again)
}

func TestAzureEncryptionRemovalRejectsOpaqueCompaction(t *testing.T) {
	body := []byte(`{"input":[{"type":"compaction","encrypted_content":"entire history"}]}`)
	_, _, err := NormalizeResponsesCompatibility(body, true)
	require.ErrorContains(t, err, "uncompressed plaintext history")
	got, changed, err := NormalizeResponsesCompatibility(body, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
}

func TestAzureChatEncryptionIsOptIn(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","function":{"name":"send_message","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true}}}}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"encrypted_content","encrypted_content":"opaque"}]}],"temperature":0}`)
	got, changed, err := NormalizeAzureGPTChatEncryption(body, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
	got, changed, err = NormalizeAzureGPTChatEncryption(body, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, gjson.GetBytes(body, "tools").Raw, gjson.GetBytes(got, "tools").Raw)
	require.Len(t, gjson.GetBytes(got, "messages.0.content").Array(), 1)
	require.Equal(t, "0", gjson.GetBytes(got, "temperature").Raw)
}

func TestNamespaceUpstreamMismatchRequiresOutboundEvidence(t *testing.T) {
	for _, test := range []struct {
		input string
		retry bool
	}{
		{`[{"type":"function_call","name":"js","namespace":"functions"}]`, true},
		{`[{"type":"function_call","name":"js"}]`, false},
		{`[{"type":"function_call","name":"js","namespace":"functions"},{"type":"function_call","name":"js"}]`, false},
		{`[{"type":"function_call","name":"other","namespace":"functions"}]`, false},
	} {
		apiErr := makeResponsesError(400, "Missing namespace for function_call 'js'. It does not exist in the default namespace.")
		request := &dto.OpenAIResponsesRequest{Input: []byte(test.input)}
		require.Equal(t, test.retry, ClassifyResponsesNamespaceError(apiErr, request))
		if test.retry {
			require.Equal(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
		}
	}
}
