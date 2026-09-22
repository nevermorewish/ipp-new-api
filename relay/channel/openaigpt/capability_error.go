package openaigpt

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// upstreamRequestFailedPrefix is the opaque message some providers return in
// place of a real reason.
//
// It must be matched by prefix, not equality: real responses append an upstream
// request id, e.g. `Upstream request failed (request id: abc123)`. The previous
// implementation compared for exact equality, so it matched only the id-less
// form and silently stopped working the moment a provider added one.
const upstreamRequestFailedPrefix = "upstream request failed"

// capabilityMismatchMarkers are upstream messages that describe something the
// provider does not implement, rather than something wrong with the request.
// Each of these corresponds to a parameter or tool that is valid in the
// official contract, so the right response is to try a provider that supports
// it -- not to delete the caller's intent or call their request malformed.
var capabilityMismatchMarkers = []string{
	"unsupported parameter:",
	"unsupported tool type:",
	"not supported on this model",
	"logprobs are not supported with reasoning models",
}

var chatCapabilityMismatchMarkers = []string{
	"audio input is not available",
	"n>1 is not supported in responses compatibility mode",
	// Some OpenAI-compatible gateways expose GPT-5/6 through Chat
	// Completions but do not implement function tools when reasoning is on.
	// The request is valid; retrying a channel that supports the capability is
	// the safe behavior.
	"function tools with reasoning_effort are not supported",
}

// ClassifyResponsesError decides whether an upstream 400 means "this provider
// cannot do this" (retry elsewhere) or "this request is wrong" (fail now).
//
// requestWasLocallyValid must be the result of ValidateResponsesRequest on the
// outbound request. It is what makes the opaque-failure branch safe: a provider
// returns the same "Upstream request failed" for a permanently invalid request
// as for one it merely cannot serve, so without local validation as a
// tie-breaker, treating that message as retryable burns every channel in the
// pool on a request that can never succeed.
//
// Returns true when the error was reclassified as retryable.
func ClassifyResponsesError(apiErr *types.NewAPIError, requestWasLocallyValid bool) bool {
	if apiErr == nil || apiErr.StatusCode != 400 {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(apiErr.ToOpenAIError().Message))
	if !isResponsesCapabilityMismatch(message, requestWasLocallyValid) {
		return false
	}
	apiErr.SetErrorCode(types.ErrorCodeChannelOpenAIResponsesUnsupported)
	return true
}

// ClassifyResponsesNamespaceError handles upstreams that lose a namespace
// internally despite receiving it. Only retry if every matching outbound call
// actually has a namespace; genuine missing/ambiguous client state stays a 400.
func ClassifyResponsesNamespaceError(apiErr *types.NewAPIError, request *dto.OpenAIResponsesRequest) bool {
	if apiErr == nil || apiErr.StatusCode != 400 || request == nil {
		return false
	}
	message := apiErr.ToOpenAIError().Message
	const prefix = "Missing namespace for function_call '"
	if !strings.HasPrefix(message, prefix) {
		return false
	}
	name, _, found := strings.Cut(strings.TrimPrefix(message, prefix), "'")
	if !found || name == "" {
		return false
	}
	var items []map[string]any
	if err := common.Unmarshal(request.Input, &items); err != nil {
		return false
	}
	matched := false
	for _, item := range items {
		if item["type"] != "function_call" || item["name"] != name {
			continue
		}
		namespace, _ := item["namespace"].(string)
		if strings.TrimSpace(namespace) == "" {
			return false
		}
		matched = true
	}
	if matched {
		apiErr.SetErrorCode(types.ErrorCodeChannelOpenAIResponsesUnsupported)
	}
	return matched
}

// ClassifyChatError handles provider gaps observed on Chat Completions. Opaque
// failures are retryable only after the exact outbound body passed local
// contract validation.
func ClassifyChatError(apiErr *types.NewAPIError, requestWasLocallyValid bool) bool {
	if apiErr == nil || apiErr.StatusCode != 400 {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(apiErr.ToOpenAIError().Message))
	matched := false
	for _, marker := range chatCapabilityMismatchMarkers {
		if strings.Contains(message, marker) {
			matched = true
			break
		}
	}
	// Some aggregators route /v1/chat/completions into their Responses
	// implementation and then complain that `input` is missing. That is the
	// provider mis-routing a well-formed Chat request, so it is only a retry
	// signal once local validation has proved the request itself is fine.
	if requestWasLocallyValid &&
		strings.Contains(message, "one of \"input\" or \"previous_response_id\"") &&
		strings.Contains(message, "must be provided") {
		matched = true
	}
	// Official Chat custom tools put the name under tools[i].custom.name.
	// Some Responses-compatible providers incorrectly require tools[i].name,
	// and others report the mirror-image complaint that tools[i].custom is
	// missing when it was supplied. Both are the same provider-side shape
	// mismatch, and the quoting of the parameter path varies between
	// providers, so match the path loosely. Only treat either as a provider gap
	// after local validation has proved the submitted Chat tool shape is valid.
	if requestWasLocallyValid && isChatToolShapeComplaint(message) {
		matched = true
	}
	if !matched {
		matched = requestWasLocallyValid && strings.HasPrefix(message, upstreamRequestFailedPrefix)
	}
	if !matched {
		return false
	}
	apiErr.SetErrorCode(types.ErrorCodeChannelOpenAIResponsesUnsupported)
	return true
}

// isChatToolShapeComplaint reports whether an upstream message is a provider
// objecting to the shape of a tools[N] entry rather than to its contents.
//
// The member path is deliberately not matched. Providers disagree about which
// member they want (tools[i].name, tools[i].custom, tools[i].custom.name) and
// about quoting, and the gateway's own URL masker rewrites a dotted tail that
// happens to look like a hostname -- "custom.name" becomes "***.name" before
// this ever sees it, because .name is a real TLD. Anchoring on the member would
// make classification depend on that accident.
//
// Callers only reach this after local validation has proved the submitted tool
// list satisfies the official Chat contract, so an upstream still calling a
// tools[N] member missing is describing its own shape expectation.
func isChatToolShapeComplaint(message string) bool {
	return strings.Contains(message, "missing required parameter") && strings.Contains(message, "tools[")
}

func isResponsesCapabilityMismatch(message string, requestWasLocallyValid bool) bool {
	for _, marker := range capabilityMismatchMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	// previous_response_id state lives with whichever provider (and key)
	// created it. A provider that refuses it is telling us to go back there.
	if strings.Contains(message, "previous_response_id") &&
		(strings.Contains(message, "requires an openai api-key") ||
			strings.Contains(message, "unsupported") ||
			strings.Contains(message, "not available for this user")) {
		return true
	}
	// An opaque failure is only a capability signal if the request itself
	// passed local contract validation. Otherwise it is far more likely to be
	// the provider's unhelpful way of reporting a request we should reject.
	return requestWasLocallyValid && strings.HasPrefix(message, upstreamRequestFailedPrefix)
}

// ClassifyImageDataError handles the one upstream complaint that is genuinely
// ambiguous: "the image data you provided does not represent a valid image".
//
// If the gateway cannot decode the image either, the client sent something
// broken and retrying every channel just multiplies the failure. Only when the
// image decodes locally is this a provider-specific pipeline difference worth
// retrying elsewhere.
func ClassifyImageDataError(apiErr *types.NewAPIError, request *dto.OpenAIResponsesRequest) bool {
	if apiErr == nil || apiErr.StatusCode != 400 || request == nil {
		return false
	}
	message := strings.ToLower(apiErr.ToOpenAIError().Message)
	if !strings.Contains(message, "image data you provided does not represent a valid image") {
		return false
	}
	if !allInlineImagesDecode(request) {
		return false
	}
	apiErr.SetErrorCode(types.ErrorCodeChannelOpenAIResponsesUnsupported)
	return true
}

// allInlineImagesDecode reports whether every inline data: image in the request
// decodes locally. A request carrying no inline images counts as decodable:
// the upstream is then complaining about a remote URL we cannot inspect, which
// is a provider-side fetch difference rather than malformed client input.
func allInlineImagesDecode(request *dto.OpenAIResponsesRequest) bool {
	if len(request.Input) == 0 {
		return true
	}
	var input any
	if err := common.Unmarshal(request.Input, &input); err != nil {
		return true
	}
	decodable := true
	_ = walkJSON(input, func(item map[string]any) error {
		if typeName, _ := item["type"].(string); typeName != "input_image" {
			return nil
		}
		imageURL, _ := item["image_url"].(string)
		if !strings.HasPrefix(strings.ToLower(imageURL), "data:image/") {
			return nil
		}
		if err := ValidateDataImage(imageURL); err != nil {
			decodable = false
		}
		return nil
	})
	return decodable
}
