package openaigpt

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// legacyFunctionCallContextKey marks a Chat request that used the deprecated
// functions/function_call contract, so the response can be returned in the
// shape that request asked for.
//
// The literal is defined in relay/common so the Chat-via-Responses path in
// relay/channel/openai can read the same key without importing this package.
const legacyFunctionCallContextKey = relaycommon.LegacyFunctionCallContextKey

// UsesLegacyFunctionCall reports whether a Chat request was written against the
// deprecated functions contract rather than tools.
//
// A request carrying `tools` is a modern request even if it also sets
// `functions`, because the caller demonstrably knows the current contract and
// expects tool_calls back.
func UsesLegacyFunctionCall(body []byte) bool {
	var request struct {
		Tools        json.RawMessage `json:"tools"`
		Functions    json.RawMessage `json:"functions"`
		FunctionCall json.RawMessage `json:"function_call"`
	}
	if err := common.Unmarshal(body, &request); err != nil {
		return false
	}
	if isPresentJSON(request.Tools) {
		return false
	}
	return isPresentJSON(request.Functions) || isPresentJSON(request.FunctionCall)
}

func isPresentJSON(raw json.RawMessage) bool {
	return len(raw) > 0 && common.GetJsonType(raw) != "null"
}

// MarkLegacyFunctionCall records the legacy-contract decision for the response
// stage. It takes a minimal setter so the relay handler owns the gin context
// and this package stays free of a web-framework dependency.
func MarkLegacyFunctionCall(set func(key string, value any), body []byte) {
	if UsesLegacyFunctionCall(body) {
		set(legacyFunctionCallContextKey, true)
	}
}

// LegacyFunctionCallContextKey exposes the key so the relay handler can read
// back what MarkLegacyFunctionCall stored.
func LegacyFunctionCallContextKey() string {
	return legacyFunctionCallContextKey
}

// RestoreLegacyFunctionCall rewrites a Chat Completions response body from the
// current tool_calls shape back into the deprecated function_call shape.
//
// The implementation lives in relay/common because the Chat-via-Responses path
// in relay/channel/openai needs it as well, and that package cannot import this
// one: openaigpt embeds openai.Adaptor, so the dependency only runs one way.
// This wrapper keeps the concept addressable from the channel that owns the
// policy for it.
func RestoreLegacyFunctionCall(body []byte) ([]byte, bool) {
	return relaycommon.RestoreLegacyFunctionCallResponse(body)
}
