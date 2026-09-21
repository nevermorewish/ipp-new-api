package openaigpt

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Reduced from the September 21 reserved-schema and item_* rejections. No
// production prompts, credentials or encrypted payloads belong in fixtures.
func TestAzureCompatibilityPreservesReservedSchemaAndToolHistory(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","temperature":0,"store":false,"input":[
		{"type":"additional_tools","tools":[{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"followup_task","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true},"target":{"type":"string"}},"required":["target","message"],"additionalProperties":false},"strict":false}]}]},
		{"type":"reasoning","id":"item_old_reasoning","encrypted_content":"opaque","summary":[]},
		{"type":"function_call","id":"item_old_call","call_id":"call_pair","name":"followup_task","namespace":"collaboration","arguments":"{\"target\":\"worker\",\"message\":\"continue\"}"},
		{"type":"function_call_output","call_id":"call_pair","output":"accepted"}
	],"provider_extension":{"number":9007199254740993,"encrypted":true}}`)
	var settings dto.ChannelOtherSettings
	require.NoError(t, common.Unmarshal([]byte(`{"remove_azure_gpt_encryption":true}`), &settings), "the saved setting must still enable compatibility")
	prepared, request, changed, err := PrepareResponsesBody(body, "", settings)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(prepared, "temperature").Exists())
	require.Nil(t, request.Temperature)
	require.JSONEq(t, gjson.GetBytes(body, "input.0.tools").Raw, gjson.GetBytes(prepared, "input.0.tools").Raw)
	require.Len(t, gjson.GetBytes(prepared, "input").Array(), 3)
	require.False(t, gjson.GetBytes(prepared, "input.1.id").Exists())
	require.Equal(t, "call_pair", gjson.GetBytes(prepared, "input.1.call_id").String())
	require.Equal(t, "call_pair", gjson.GetBytes(prepared, "input.2.call_id").String())
	require.Equal(t, gjson.GetBytes(body, "input.2.arguments").String(), gjson.GetBytes(prepared, "input.1.arguments").String())
	require.Equal(t, "accepted", gjson.GetBytes(prepared, "input.2.output").String())
	require.Equal(t, "false", gjson.GetBytes(prepared, "store").Raw)
	require.JSONEq(t, gjson.GetBytes(body, "provider_extension").Raw, gjson.GetBytes(prepared, "provider_extension").Raw)
	again, _, changed, err := PrepareResponsesBody(prepared, "", settings)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, prepared, again)

	off, _, changed, err := PrepareResponsesBody(body, "", dto.ChannelOtherSettings{})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, off, "default mode must retain explicit zero and encrypted history")
}

func TestAzureCompatibilityOnlyRemovesOptionalStaleToolIDs(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"function_call","id":"item_stale","call_id":"call_1","name":"lookup","arguments":"{\"id\":\"item_user_data\"}"},
		{"type":"function_call_output","call_id":"call_1","output":[{"type":"function_call","id":"item_result_data"}]},
		{"type":"custom_tool_call","id":"item_custom","call_id":"call_2","name":"exec","input":"id=item_user_data"},
		{"type":"function_call","id":"fc_valid","call_id":"call_3","name":"lookup","arguments":"{}"},
		{"type":"reasoning","id":"rs_valid","summary":[]},
		{"type":"reasoning","id":"item_plain_reasoning","summary":[]},
		{"type":"provider_extension","id":"item_extension"}
	],"metadata":{"id":"item_metadata"}}`)
	got, changed, err := NormalizeResponsesCompatibility(body, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(got, "input.0.id").Exists())
	require.False(t, gjson.GetBytes(got, "input.2.id").Exists())
	for _, path := range []string{"input.0.arguments", "input.0.call_id", "input.1", "input.2.input", "input.2.call_id", "input.3", "input.4", "input.5", "input.6", "metadata"} {
		require.JSONEq(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(got, path).Raw, path)
	}
}

func TestAzureCompatibilityDoesNotBreakItemReferences(t *testing.T) {
	for _, item := range []string{
		`{"type":"function_call","id":"item_referenced","call_id":"call_pair","name":"lookup","arguments":"{}"}`,
		`{"type":"reasoning","id":"item_referenced","encrypted_content":"opaque"}`,
	} {
		body := []byte(`{"input":[` + item + `,{"type":"item_reference","id":"item_referenced"}]}`)
		_, _, err := NormalizeResponsesCompatibility(body, true)
		require.ErrorContains(t, err, "referenced history item")
		off, changed, err := NormalizeResponsesCompatibility(body, false)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, off)
	}
}

func TestAzureCompatibilityPreservesSchemaWithoutHistory(t *testing.T) {
	for _, body := range []string{
		`{"tools":[{"type":"namespace","name":"collaboration","tools":[{"type":"function","name":"followup_task","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true}}}}]}],"input":[]}`,
		`{"tools":[{"type":"function","function":{"name":"followup_task","parameters":{"type":"object","properties":{"message":{"type":"string","encrypted":true}}}}}],"messages":[]}`,
	} {
		var got []byte
		var changed bool
		var err error
		if gjson.Get(body, "messages").Exists() {
			got, changed, err = NormalizeAzureGPTChatEncryption([]byte(body), true)
		} else {
			got, changed, err = NormalizeResponsesCompatibility([]byte(body), true)
		}
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, string(got))
	}
}
