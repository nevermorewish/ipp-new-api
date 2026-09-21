package common

import (
	"encoding/json"

	appcommon "github.com/QuantumNous/new-api/common"
)

// LegacyFunctionCallContextKey marks a Chat request that used the deprecated
// functions/function_call contract, so the response can be returned in the
// shape that request asked for.
//
// The policy behind the flag belongs to the OpenAI-GPT channel, which is what
// sets it. The key and the response rewrite live here because
// relay/channel/openai reads them on the Chat-via-Responses path too, and
// openaigpt already imports openai — importing back would be a cycle.
const LegacyFunctionCallContextKey = "openaigpt_legacy_function_call"

// RestoreLegacyFunctionCallResponse rewrites a Chat Completions response body
// from the current tool_calls shape back into the deprecated function_call
// shape.
//
// A client that sent `functions` and `function_call` is running against the
// legacy contract, and every SDK built on it reads message.function_call. The
// upstream aggregator converts such requests to tools internally and answers
// with tool_calls, which silently migrates the response structure out from
// under the caller. Restoring it here is the same class of work as the
// preview-tool normalization the OpenAI-GPT channel already does: the caller's
// intent is still valid, so it is honoured rather than turned into a surprise.
//
// The transformation works on raw JSON so unmodeled fields survive. A body that
// does not parse, or that carries no tool call, is returned untouched -- this
// only ever narrows a response it fully understands.
//
// Scope: non-streaming responses only. The streaming delta shape carries no
// function_call member in this gateway's response model, so a stream rewrite
// would have to drop the tool call to express it. Leaving the stream alone is
// the honest behaviour.
func RestoreLegacyFunctionCallResponse(body []byte) ([]byte, bool) {
	var response map[string]json.RawMessage
	if err := appcommon.Unmarshal(body, &response); err != nil {
		return body, false
	}
	rawChoices, exists := response["choices"]
	if !exists {
		return body, false
	}
	var choices []map[string]json.RawMessage
	if err := appcommon.Unmarshal(rawChoices, &choices); err != nil {
		return body, false
	}

	changed := false
	for _, choice := range choices {
		converted, err := restoreLegacyChoice(choice)
		if err != nil {
			return body, false
		}
		changed = changed || converted
	}
	if !changed {
		return body, false
	}

	encodedChoices, err := appcommon.Marshal(choices)
	if err != nil {
		return body, false
	}
	response["choices"] = encodedChoices
	encoded, err := appcommon.Marshal(response)
	if err != nil {
		return body, false
	}
	return encoded, true
}

func restoreLegacyChoice(choice map[string]json.RawMessage) (bool, error) {
	rawMessage, exists := choice["message"]
	if !exists {
		return false, nil
	}
	var message map[string]json.RawMessage
	if err := appcommon.Unmarshal(rawMessage, &message); err != nil {
		return false, err
	}
	rawToolCalls, exists := message["tool_calls"]
	if !exists || !isPresentJSON(rawToolCalls) {
		return false, nil
	}

	var toolCalls []struct {
		Type     string          `json:"type"`
		Function json.RawMessage `json:"function"`
	}
	if err := appcommon.Unmarshal(rawToolCalls, &toolCalls); err != nil {
		return false, err
	}
	// The legacy contract has room for exactly one call. Leaving a parallel
	// call set as tool_calls is better than silently discarding calls the model
	// asked for.
	if len(toolCalls) != 1 || toolCalls[0].Type != "function" || !isPresentJSON(toolCalls[0].Function) {
		return false, nil
	}

	// The legacy function_call object is the tool call's function object without
	// the surrounding id and type, so it can be reused as-is.
	message["function_call"] = toolCalls[0].Function
	delete(message, "tool_calls")

	encodedMessage, err := appcommon.Marshal(message)
	if err != nil {
		return false, err
	}
	choice["message"] = encodedMessage

	// The legacy contract reports finish_reason "function_call" for this case.
	if finishReason, exists := choice["finish_reason"]; exists {
		var reason string
		if err := appcommon.Unmarshal(finishReason, &reason); err == nil && reason == "tool_calls" {
			encodedReason, err := appcommon.Marshal("function_call")
			if err != nil {
				return false, err
			}
			choice["finish_reason"] = encodedReason
		}
	}
	return true, nil
}

func isPresentJSON(raw json.RawMessage) bool {
	return len(raw) > 0 && appcommon.GetJsonType(raw) != "null"
}
