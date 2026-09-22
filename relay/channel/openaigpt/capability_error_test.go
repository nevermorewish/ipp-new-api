package openaigpt

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

// TestClassifyResponsesErrorReclassifiesCapabilityMismatches covers the happy
// path: an upstream 400 that says "I do not implement this" should retry
// elsewhere rather than fail the request.
func TestClassifyResponsesErrorReclassifiesCapabilityMismatches(t *testing.T) {
	cases := map[string]string{
		"explicit unsupported parameter": "Unsupported parameter: 'top_logprobs' is not supported with reasoning models.",
		"explicit unsupported tool":      "Unsupported tool type: 'shell'. Supported types are function, web_search, file_search.",
		"not supported on this model":    "Reasoning effort is not supported on this model.",
		"logprobs reasoning conflict":    "logprobs are not supported with reasoning models.",
		"previous_response_id affinity":  "previous_response_id 'resp_xyz' requires an OpenAI API-key that created it.",
	}

	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			apiErr := makeResponsesError(400, message)
			require.True(t, ClassifyResponsesError(apiErr, true))
			require.Equal(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
		})
	}
}

// TestOpaqueFailureRequiresLocalValidation is Bug Fix #1: "Upstream request
// failed" was treated as universally retryable, but it is the same message
// providers return for a permanently invalid request. The fix: only treat it as
// retryable if the request passed local validation first.
func TestOpaqueFailureRequiresLocalValidation(t *testing.T) {
	const message = "Upstream request failed (request id: abc123)"

	t.Run("request was valid", func(t *testing.T) {
		apiErr := makeResponsesError(400, message)
		require.True(t, ClassifyResponsesError(apiErr, true))
		require.Equal(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
	})

	t.Run("request was invalid", func(t *testing.T) {
		apiErr := makeResponsesError(400, message)
		require.False(t, ClassifyResponsesError(apiErr, false))
		require.NotEqual(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
	})
}

// TestOpaqueFailureMatchesByPrefix is Bug Fix #2: the original code matched
// "Upstream request failed" by exact equality, which stopped working the moment
// a provider appended a request id. The fix: match by prefix.
func TestOpaqueFailureMatchesByPrefix(t *testing.T) {
	cases := []string{
		"Upstream request failed",
		"Upstream request failed (request id: abc123)",
		"upstream request failed", // case-insensitive
		"  Upstream request failed  (request id: xyz)  ",
	}

	for _, message := range cases {
		t.Run(message, func(t *testing.T) {
			apiErr := makeResponsesError(400, message)
			require.True(t, ClassifyResponsesError(apiErr, true))
		})
	}
}

func TestClassifyProductionChatCapabilityMismatches(t *testing.T) {
	cases := map[string]string{
		"audio input":                "Audio input is not available.",
		"multiple responses":         "n>1 is not supported in responses compatibility mode",
		"reasoning tools":            "Function tools with reasoning_effort are not supported for this model.",
		"custom tool shape mismatch": "Missing required parameter: 'tools[0].name'.",
		// The mirror-image complaint from the 2026-08-28 review: the provider
		// says tools[0].custom is missing on a request that supplied it.
		"custom tool container missing": "Missing required parameter: tools[0].custom",
		// The gateway's URL masker rewrites the dotted tail of this one to
		// "***.name" before classification sees it, because .name is a TLD.
		// Classification must not depend on the member surviving that.
		"custom tool name masked":  "Missing required parameter: tools[3].custom.name",
		"wrong upstream endpoint":  `One of "input" or "previous_response_id" or 'prompt' or 'conversation' must be provided.`,
		"validated opaque failure": "Upstream request failed",
	}
	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			apiErr := makeResponsesError(400, message)
			require.True(t, ClassifyChatError(apiErr, true))
			require.Equal(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
		})
	}
}

func TestClassifyChatOpaqueFailureRequiresValidation(t *testing.T) {
	apiErr := makeResponsesError(400, "Upstream request failed")
	require.False(t, ClassifyChatError(apiErr, false))
	require.NotEqual(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
}

func TestClassifyChatCustomShapeMismatchRequiresValidation(t *testing.T) {
	const message = "Missing required parameter: 'tools[2].name'."

	validRequestError := makeResponsesError(400, message)
	require.True(t, ClassifyChatError(validRequestError, true))
	require.Equal(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, validRequestError.GetErrorCode())

	invalidRequestError := makeResponsesError(400, message)
	require.False(t, ClassifyChatError(invalidRequestError, false))
	require.NotEqual(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, invalidRequestError.GetErrorCode())
}

// TestClassifyChatMisroutedEndpointRequiresValidation covers the aggregator
// that answers /v1/chat/completions out of its Responses implementation. That
// only means "retry elsewhere" once local validation has cleared the request:
// an unvalidated body could just as easily be permanently malformed, and
// retrying it burns every channel in the pool.
func TestClassifyChatMisroutedEndpointRequiresValidation(t *testing.T) {
	const message = `One of "input" or "previous_response_id" or 'prompt' or 'conversation' must be provided.`

	validRequestError := makeResponsesError(400, message)
	require.True(t, ClassifyChatError(validRequestError, true))
	require.Equal(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, validRequestError.GetErrorCode())

	invalidRequestError := makeResponsesError(400, message)
	require.False(t, ClassifyChatError(invalidRequestError, false))
	require.NotEqual(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, invalidRequestError.GetErrorCode())
}

// TestNon400ErrorsAreNeverReclassified asserts a 401/403/429/500 is never
// treated as a capability mismatch even if the message contains a marker.
func TestNon400ErrorsAreNeverReclassified(t *testing.T) {
	for _, statusCode := range []int{401, 403, 429, 500, 502, 503} {
		t.Run("status "+string(rune(statusCode+'0')), func(t *testing.T) {
			apiErr := makeResponsesError(statusCode, "Unsupported parameter: top_logprobs")
			require.False(t, ClassifyResponsesError(apiErr, true))
		})
	}
}

// TestClassifyImageDataErrorGuardsDoubleRetry is Bug Fix #3: the previous
// implementation treated every "invalid image data" complaint as a provider
// difference. If the image really is corrupt, retrying every channel just
// multiplies the failure. The fix: only reclassify if the gateway can decode
// the image too.
func TestClassifyImageDataErrorGuardsDoubleRetry(t *testing.T) {
	const message = "The image data you provided does not represent a valid image."

	// A synthetic 1x1 PNG distinguishes provider rejection from corrupt input.
	t.Run("provider rejects an image the gateway can decode", func(t *testing.T) {
		apiErr := makeResponsesError(400, message)
		request := &dto.OpenAIResponsesRequest{
			Input: []byte(`[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII="}]}]`),
		}
		require.True(t, ClassifyImageDataError(apiErr, request))
		require.Equal(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
	})

	// A genuinely corrupt payload must not be retried: every channel would
	// reject it identically, so retrying only multiplies latency and cost.
	t.Run("gateway cannot decode the image either", func(t *testing.T) {
		apiErr := makeResponsesError(400, message)
		request := &dto.OpenAIResponsesRequest{
			Input: []byte(`[{"role":"user","content":[{"type":"input_image",` +
				`"image_url":"data:image/png;base64,bm90LWFuLWltYWdl"}]}]`),
		}
		require.False(t, ClassifyImageDataError(apiErr, request))
		require.NotEqual(t, types.ErrorCodeChannelOpenAIResponsesUnsupported, apiErr.GetErrorCode())
	})

	// No inline image means the upstream is complaining about a remote URL the
	// gateway cannot inspect. That is a provider-side fetch difference.
	t.Run("no inline image to inspect", func(t *testing.T) {
		apiErr := makeResponsesError(400, message)
		request := &dto.OpenAIResponsesRequest{Input: []byte(`"just text"`)}
		require.True(t, ClassifyImageDataError(apiErr, request))
	})

	// An unrelated 400 must never be swallowed by this classifier.
	t.Run("unrelated error is untouched", func(t *testing.T) {
		apiErr := makeResponsesError(400, "Invalid value: 'always'.")
		request := &dto.OpenAIResponsesRequest{Input: []byte(`"text"`)}
		require.False(t, ClassifyImageDataError(apiErr, request))
	})
}

// TestPreservesOriginalErrorForCaller asserts that reclassification is strictly
// internal: the original upstream error payload is what the client sees, not an
// invented gateway message.
func TestPreservesOriginalErrorForCaller(t *testing.T) {
	apiErr := makeResponsesError(400, "Unsupported tool type: shell.")
	ClassifyResponsesError(apiErr, true)
	openAIErr := apiErr.ToOpenAIError()
	require.Equal(t, "Unsupported tool type: shell.", openAIErr.Message)
	require.Equal(t, "invalid_request_error", openAIErr.Type)
}

func makeResponsesError(statusCode int, message string) *types.NewAPIError {
	openAIErr := types.OpenAIError{
		Message: message,
		Type:    "invalid_request_error",
		Code:    "invalid_request",
	}
	return types.WithOpenAIError(openAIErr, statusCode)
}
