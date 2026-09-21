package openaigpt

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

// TestChatToolChoiceCrossChecksToolNames mirrors the Responses-side rule for
// the Chat shape, where the chosen name nests under function.name/custom.name.
// Without this the gateway forwarded a choice naming a tool that is not in the
// list and paid an upstream round trip plus a retry to learn what it could
// already prove.
func TestChatToolChoiceCrossChecksToolNames(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		contains string
	}{
		{
			name:     "function choice missing from tools",
			body:     `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"function","function":{"name":"absent"}},"tools":[{"type":"function","function":{"name":"present"}}]}`,
			contains: `tool_choice "absent" is not present in tools`,
		},
		{
			name:     "custom choice missing from tools",
			body:     `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"custom","custom":{"name":"absent"}},"tools":[{"type":"custom","custom":{"name":"present"}}]}`,
			contains: `tool_choice "absent" is not present in tools`,
		},
		{
			name:     "named choice without a name",
			body:     `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"function","function":{}},"tools":[{"type":"function","function":{"name":"present"}}]}`,
			contains: "requires function.name",
		},
		{
			name:     "invalid tool_choice string",
			body:     `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":"always"}`,
			contains: `invalid tool_choice "always"`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.Unmarshal([]byte(testCase.body), &request))
			require.ErrorContains(t, ValidateChatRequest(&request), testCase.contains)
		})
	}
}

// TestChatToolChoicePreservesValidShapes is the counterweight: a matching name,
// and a hosted tool choice that names no function, must both survive.
func TestChatToolChoicePreservesValidShapes(t *testing.T) {
	bodies := map[string]string{
		"matching function choice": `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"function","function":{"name":"present"}},"tools":[{"type":"function","function":{"name":"present"}}]}`,
		"matching custom choice":   `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"custom","custom":{"name":"present"}},"tools":[{"type":"custom","custom":{"name":"present"}}]}`,
		"hosted tool choice":       `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"file_search"},"tools":[{"type":"function","function":{"name":"present"}}]}`,
		"auto with no tools":       `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":"auto"}`,
		"required with tools":      `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"tool_choice":"required","tools":[{"type":"function","function":{"name":"present"}}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			var request dto.GeneralOpenAIRequest
			require.NoError(t, common.Unmarshal([]byte(body), &request))
			require.NoError(t, ValidateChatRequest(&request))
		})
	}
}

// TestValidationIsScopedToThisChannel guards the regression that motivated the
// whole change: these vocabularies must never be applied globally again. A
// third-party provider is free to accept values OpenAI does not define, so the
// only thing that may enforce them is this channel's own validator.
func TestValidationIsScopedToThisChannel(t *testing.T) {
	// prompt_cache_options.ttl was previously pinned to the single documented
	// value "30m". OpenAI is expected to add more, so it is no longer enforced.
	request := dto.OpenAIResponsesRequest{
		Model:              "gpt-5.6-sol",
		Input:              []byte(`"hello"`),
		PromptCacheOptions: []byte(`{"mode":"explicit","ttl":"1h"}`),
	}
	require.NoError(t, ValidateResponsesRequest(&request))
}
