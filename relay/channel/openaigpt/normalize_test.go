package openaigpt

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeConvertedChatRequestIsOpenAIGPTSpecific(t *testing.T) {
	chat := &dto.GeneralOpenAIRequest{
		StreamOptions:   &dto.StreamOptions{IncludeUsage: true, IncludeObfuscation: true},
		Reasoning:       json.RawMessage(`{"summary":"concise","context":"current_turn"}`),
		ReasoningEffort: " high ",
	}
	responses := &dto.OpenAIResponsesRequest{Reasoning: &dto.Reasoning{Summary: "detailed"}}

	NormalizeConvertedChatRequest(chat, responses)

	require.Equal(t, &dto.StreamOptions{IncludeObfuscation: true}, responses.StreamOptions)
	require.NotNil(t, responses.Reasoning)
	require.Equal(t, "high", responses.Reasoning.Effort)
	require.Equal(t, "concise", responses.Reasoning.Summary)
	require.JSONEq(t, `"current_turn"`, string(responses.Reasoning.Context))
}

func TestNormalizeChatImageDataURLsAddsDetectedImagePrefix(t *testing.T) {
	const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	body := []byte(`{"model":"gpt-6-astra","messages":[{"role":"user","content":[` +
		`{"type":"text","text":"look"},` +
		`{"type":"image_url","image_url":{"url":"` + pngBase64 + `","detail":"high"}}]}]}`)

	normalized, changed, err := NormalizeChatImageDataURLs(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "data:image/png;base64,"+pngBase64, gjson.GetBytes(normalized, "messages.0.content.1.image_url.url").String())
	require.Equal(t, "high", gjson.GetBytes(normalized, "messages.0.content.1.image_url.detail").String())
}

func TestNormalizeChatImageDataURLsPreservesPrefixedAndInvalidValues(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","messages":[` +
		`{"role":"user","content":[` +
		`{"type":"image_url","image_url":"data:image/png;base64,AA=="},` +
		`{"type":"image_url","image_url":"not-a-base64-image"},` +
		`{"type":"image_url","image_url":"https://example.com/image.png"}` +
		`]}]}`)

	normalized, changed, err := NormalizeChatImageDataURLs(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, normalized)
}

func TestValidateChatImageURLsRejectsMalformedValue(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"not-a-url"}}]}]}`)
	if err := ValidateChatImageURLs(body); err == nil {
		t.Fatal("expected malformed image URL to be rejected")
	}
}

func TestValidateChatImageURLsAcceptsHTTPS(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}]}`)
	if err := ValidateChatImageURLs(body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeSmallMaxOutputTokensInBodyPreservesOtherFields(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","max_output_tokens":1,"provider_extension":{"keep":true}}`)

	normalized, changed, err := NormalizeSmallMaxOutputTokensInBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.EqualValues(t, 16, gjson.GetBytes(normalized, "max_output_tokens").Int())
	require.True(t, gjson.GetBytes(normalized, "provider_extension.keep").Bool())
}

func TestNormalizeZeroChatTokenLimitsInBody(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","max_tokens":0,"provider_extension":{"keep":true}}`)

	normalized, changed, err := NormalizeZeroChatTokenLimitsInBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "max_tokens").Exists())
	require.EqualValues(t, 1, gjson.GetBytes(normalized, "max_completion_tokens").Int())
	require.True(t, gjson.GetBytes(normalized, "provider_extension.keep").Bool())
}

func TestNormalizeZeroChatTokenLimitsPreservesValidCanonicalValue(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","max_tokens":0,"max_completion_tokens":128}`)

	normalized, changed, err := NormalizeZeroChatTokenLimitsInBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "max_tokens").Exists())
	require.EqualValues(t, 128, gjson.GetBytes(normalized, "max_completion_tokens").Int())
}

// TestNormalizeLegacyResponsesAliasesInBodyMovesMaxTokens covers the Codex
// client shape that made unrecognized request argument supplied: max_tokens
// the single largest category of production 400s on the OpenAI-GPT channel.
func TestNormalizeLegacyResponsesAliasesInBodyMovesMaxTokens(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":"hi","max_tokens":512,"provider_extension":{"keep":true}}`)

	normalized, changed, err := NormalizeLegacyResponsesAliasesInBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "max_tokens").Exists())
	require.EqualValues(t, 512, gjson.GetBytes(normalized, "max_output_tokens").Int())
	require.True(t, gjson.GetBytes(normalized, "provider_extension.keep").Bool())
}

// An explicit max_output_tokens already present is canonical and must win
// over the deprecated alias rather than being overwritten by it.
func TestNormalizeLegacyResponsesAliasesInBodyPrefersExplicitMaxOutputTokens(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":"hi","max_tokens":512,"max_output_tokens":128}`)

	normalized, changed, err := NormalizeLegacyResponsesAliasesInBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "max_tokens").Exists())
	require.EqualValues(t, 128, gjson.GetBytes(normalized, "max_output_tokens").Int())
}

// TestNormalizeLegacyResponsesAliasesInBodyMovesReasoningEffort covers the
// second-largest category: unrecognized request argument supplied:
// reasoning_effort. The value is merged into reasoning.effort rather than
// dropped.
func TestNormalizeLegacyResponsesAliasesInBodyMovesReasoningEffort(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"hi","reasoning_effort":"high"}`)

	normalized, changed, err := NormalizeLegacyResponsesAliasesInBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "reasoning_effort").Exists())
	require.Equal(t, "high", gjson.GetBytes(normalized, "reasoning.effort").String())
}

// An explicit reasoning.effort already present is canonical and must win
// over the deprecated top-level alias.
func TestNormalizeLegacyResponsesAliasesInBodyPrefersExplicitReasoningEffort(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"hi","reasoning_effort":"high","reasoning":{"effort":"low"}}`)

	normalized, changed, err := NormalizeLegacyResponsesAliasesInBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "reasoning_effort").Exists())
	require.Equal(t, "low", gjson.GetBytes(normalized, "reasoning.effort").String())
}

// reasoning_effort must merge into an existing reasoning object without
// clobbering its other members (e.g. summary).
func TestNormalizeLegacyResponsesAliasesInBodyMergesIntoExistingReasoning(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"hi","reasoning_effort":"high","reasoning":{"summary":"concise"}}`)

	normalized, changed, err := NormalizeLegacyResponsesAliasesInBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "high", gjson.GetBytes(normalized, "reasoning.effort").String())
	require.Equal(t, "concise", gjson.GetBytes(normalized, "reasoning.summary").String())
}

func TestNormalizeLegacyResponsesAliasesInBodyLeavesUnaffectedRequestsUntouched(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","input":"hi","max_output_tokens":128}`)

	normalized, changed, err := NormalizeLegacyResponsesAliasesInBody(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, normalized)
}

// TestNormalizeInvalidMessageItemIDsInBodyDropsStaleItemPrefix covers the
// input[N].id must use an official msg_* message identifier or be omitted;
// item_* is not valid category: a replayed conversation history carries a
// stale item_* id on a message-type input item. The id is not required to
// replay the item, so it is dropped instead of failing the whole request.
func TestNormalizeInvalidMessageItemIDsInBodyDropsStaleItemPrefix(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":[` +
		`{"role":"user","content":"hi"},` +
		`{"role":"assistant","id":"item_abc123","content":"reply"}` +
		`]}`)

	normalized, changed, err := NormalizeInvalidMessageItemIDsInBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(normalized, "input.1.id").Exists())
	require.Equal(t, "reply", gjson.GetBytes(normalized, "input.1.content").String())
	require.Equal(t, "hi", gjson.GetBytes(normalized, "input.0.content").String())
}

// An official msg_* id must survive untouched.
func TestNormalizeInvalidMessageItemIDsInBodyPreservesOfficialMsgID(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":[{"role":"assistant","id":"msg_abc123","content":"reply"}]}`)

	normalized, changed, err := NormalizeInvalidMessageItemIDsInBody(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, "msg_abc123", gjson.GetBytes(normalized, "input.0.id").String())
}

// A non-message item (e.g. a function_call) legitimately uses item_*-style
// ids and must not be touched by this normalizer.
func TestNormalizeInvalidMessageItemIDsInBodyLeavesNonMessageItemsAlone(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":[{"type":"function_call","id":"item_fc1","call_id":"call_1","name":"lookup","arguments":"{}"}]}`)

	normalized, changed, err := NormalizeInvalidMessageItemIDsInBody(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, "item_fc1", gjson.GetBytes(normalized, "input.0.id").String())
}
