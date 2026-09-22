package openaigpt

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	_ "golang.org/x/image/webp"
)

// NormalizeChatImageDataURLs converts a bare base64 image placed in a Chat
// Completions image_url field into the data URL form expected by OpenAI-GPT:
// data:<mime>;base64,<payload>. Existing HTTP(S) and data URLs are preserved.
// The conversion is deliberately conservative: a value is changed only when
// it decodes as a supported image, so arbitrary invalid URLs still reach the
// normal request validator/upstream error path.
func NormalizeChatImageDataURLs(body []byte) ([]byte, bool, error) {
	var object map[string]any
	if err := common.Unmarshal(body, &object); err != nil {
		return body, false, nil
	}
	messages, ok := object["messages"].([]any)
	if !ok {
		return body, false, nil
	}
	changed := false
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, rawContent := range content {
			item, ok := rawContent.(map[string]any)
			if !ok || item["type"] != dto.ContentTypeImageURL {
				continue
			}
			imageURL, ok := item["image_url"]
			if !ok {
				continue
			}
			switch value := imageURL.(type) {
			case string:
				if normalized, ok := normalizeBareImageData(value); ok {
					item["image_url"] = normalized
					changed = true
				}
			case map[string]any:
				url, ok := value["url"].(string)
				if !ok {
					continue
				}
				if normalized, ok := normalizeBareImageData(url); ok {
					value["url"] = normalized
					changed = true
				}
			}
		}
	}
	if !changed {
		return body, false, nil
	}
	normalized, err := common.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

// ValidateChatImageURLs validates image_url values before they reach the
// OpenAI-GPT upstream. Other channels intentionally do not use this helper.
// Remote URLs are syntax-checked only; the upstream remains responsible for
// fetching them and validating their contents.
func ValidateChatImageURLs(body []byte) error {
	var object map[string]any
	if err := common.Unmarshal(body, &object); err != nil {
		return nil
	}
	messages, _ := object["messages"].([]any)
	for mi, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		if message == nil {
			continue
		}
		content, _ := message["content"].([]any)
		for ci, rawContent := range content {
			item, _ := rawContent.(map[string]any)
			if item == nil || item["type"] != dto.ContentTypeImageURL {
				continue
			}
			value := item["image_url"]
			var imageURL string
			switch v := value.(type) {
			case string:
				imageURL = v
			case map[string]any:
				imageURL, _ = v["url"].(string)
			}
			imageURL = strings.TrimSpace(imageURL)
			if imageURL == "" {
				return fmt.Errorf("messages[%d].content[%d].image_url must contain a URL", mi, ci)
			}
			lower := strings.ToLower(imageURL)
			if strings.HasPrefix(lower, "data:image/") {
				if err := ValidateDataImage(imageURL); err != nil {
					return fmt.Errorf("messages[%d].content[%d].image_url: %w", mi, ci, err)
				}
				continue
			}
			u, err := url.Parse(imageURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("messages[%d].content[%d].image_url must be a valid http(s) URL or image data URL", mi, ci)
			}
		}
	}
	return nil
}

func normalizeBareImageData(value string) (string, bool) {
	payload := strings.TrimSpace(value)
	if payload == "" || strings.HasPrefix(strings.ToLower(payload), "data:") || strings.HasPrefix(strings.ToLower(payload), "http://") || strings.HasPrefix(strings.ToLower(payload), "https://") {
		return value, false
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return value, false
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return value, false
	}
	mimeType := map[string]string{
		"jpeg": "image/jpeg",
		"png":  "image/png",
		"gif":  "image/gif",
		"webp": "image/webp",
	}[strings.ToLower(format)]
	if mimeType == "" {
		return value, false
	}
	return "data:" + mimeType + ";base64," + payload, true
}

// Responses rejects an explicitly supplied max_output_tokens below 16. Keep
// the inbound validator permissive (it is also used by compatibility tests and
// pass-through callers), but normalize the final OpenAI-GPT wire body so that
// both direct Responses requests and Chat-to-Responses requests satisfy the
// upstream contract.
const minResponsesOutputTokens uint = 16

// NormalizeResponsesRequest rewrites request shapes that the official contract
// has superseded but that clients still send, so a still-valid intent is not
// turned into a 400 the caller cannot act on.
//
// Today that means upgrading preview tool names to their current GPT-5.6
// equivalents. It returns whether anything changed so the caller can log it.
func NormalizeResponsesRequest(r *dto.OpenAIResponsesRequest) (bool, error) {
	if r == nil {
		return false, nil
	}
	changed, err := normalizeGPT56Tools(r)
	if err != nil {
		return false, err
	}
	if normalizeSmallMaxOutputTokens(r) {
		changed = true
	}
	return changed, nil
}

// NormalizeGPTTemperatureInBody removes temperature for GPT-5 and GPT-6
// families only when explicitly enabled for this OpenAI-GPT channel. Run on
// final bodies so pass-through requests and parameter overrides are covered.
func NormalizeGPTTemperatureInBody(body []byte, model string, enabled bool) ([]byte, bool, error) {
	if !enabled {
		return body, false, nil
	}
	var object map[string]json.RawMessage
	if err := common.Unmarshal(body, &object); err != nil {
		return body, false, nil
	}
	if strings.TrimSpace(model) == "" {
		_ = common.Unmarshal(object["model"], &model)
	}
	model = strings.ToLower(strings.TrimSpace(model))
	// Match model-family boundaries, not arbitrary names such as "openrouter".
	reasoningModel := false
	for _, family := range []string{"gpt-5", "gpt-6"} {
		if model == family || strings.HasPrefix(model, family+"-") ||
			strings.HasPrefix(model, family+".") {
			reasoningModel = true
			break
		}
	}
	if !reasoningModel {
		return body, false, nil
	}
	if _, exists := object["temperature"]; !exists {
		return body, false, nil
	}
	delete(object, "temperature")
	normalized, err := common.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

// NormalizeAzureGPTSamplingParametersInBody removes sampling parameters that
// Azure GPT reasoning deployments reject even though they are valid on the
// public OpenAI contract. The compatibility switch is opt-in because these
// fields are meaningful to other OpenAI-compatible providers. Temperature is
// handled by NormalizeGPTTemperatureInBody; this helper covers the remaining
// reasoning-only parameters that otherwise produce an upstream HTTP 400.
func NormalizeAzureGPTSamplingParametersInBody(body []byte, model string, enabled bool) ([]byte, bool, error) {
	if !enabled {
		return body, false, nil
	}
	var object map[string]json.RawMessage
	if err := common.Unmarshal(body, &object); err != nil {
		return body, false, nil
	}
	if strings.TrimSpace(model) == "" {
		_ = common.Unmarshal(object["model"], &model)
	}
	model = strings.ToLower(strings.TrimSpace(model))
	reasoningModel := model == "gpt-5" || model == "gpt-6" ||
		strings.HasPrefix(model, "gpt-5-") || strings.HasPrefix(model, "gpt-5.") ||
		strings.HasPrefix(model, "gpt-6-") || strings.HasPrefix(model, "gpt-6.")
	if !reasoningModel {
		return body, false, nil
	}

	changed := false
	for _, field := range []string{"top_p", "logprobs", "top_logprobs"} {
		if _, exists := object[field]; exists {
			delete(object, field)
			changed = true
		}
	}
	if !changed {
		return body, false, nil
	}
	normalized, err := common.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

func normalizeSmallMaxOutputTokens(r *dto.OpenAIResponsesRequest) bool {
	if r == nil || r.MaxOutputTokens == nil || *r.MaxOutputTokens >= minResponsesOutputTokens {
		return false
	}
	value := minResponsesOutputTokens
	r.MaxOutputTokens = &value
	return true
}

// NormalizeConvertedChatRequest applies OpenAI-GPT-only fixes after the
// shared Chat-to-Responses converter has run. The shared converter is used by
// other channel types too, so these OpenAI-specific semantics must not leak
// into their requests.
func NormalizeConvertedChatRequest(chat *dto.GeneralOpenAIRequest, responses *dto.OpenAIResponsesRequest) {
	if chat == nil || responses == nil {
		return
	}

	// Responses supports include_obfuscation; Chat's include_usage is not a
	// Responses member and must not be forwarded across this conversion.
	if chat.StreamOptions != nil && chat.StreamOptions.IncludeObfuscation {
		responses.StreamOptions = &dto.StreamOptions{IncludeObfuscation: true}
	} else {
		responses.StreamOptions = nil
	}

	// Preserve an explicitly supplied Responses-style reasoning object when a
	// Chat request is routed through this compatibility path. If the legacy
	// reasoning_effort field is present, it remains authoritative for effort.
	if len(chat.Reasoning) > 0 && common.GetJsonType(chat.Reasoning) != "null" {
		parsed := &dto.Reasoning{}
		if err := common.Unmarshal(chat.Reasoning, parsed); err == nil {
			if effort := strings.TrimSpace(chat.ReasoningEffort); effort != "" {
				parsed.Effort = effort
			}
			responses.Reasoning = parsed
			return
		}
	}
	if effort := strings.TrimSpace(chat.ReasoningEffort); effort != "" {
		responses.Reasoning = &dto.Reasoning{Effort: effort}
	}
}

// NormalizeSmallMaxOutputTokensInBody applies the same rule to a pass-through
// body while retaining fields that are not modeled by OpenAIResponsesRequest.
func NormalizeSmallMaxOutputTokensInBody(body []byte) ([]byte, bool, error) {
	var object map[string]json.RawMessage
	if err := common.Unmarshal(body, &object); err != nil {
		return nil, false, err
	}
	raw, exists := object["max_output_tokens"]
	if !exists || common.GetJsonType(raw) == "null" {
		return body, false, nil
	}
	var value uint
	if err := common.Unmarshal(raw, &value); err != nil {
		return nil, false, fmt.Errorf("max_output_tokens must be an integer: %w", err)
	}
	if value >= minResponsesOutputTokens {
		return body, false, nil
	}
	encoded, err := common.Marshal(minResponsesOutputTokens)
	if err != nil {
		return nil, false, err
	}
	object["max_output_tokens"] = encoded
	normalized, err := common.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

// NormalizeZeroChatTokenLimitsInBody fixes the legacy Chat Completions token
// aliases after all conversion and channel parameter overrides have run.
// OpenAI-GPT rejects an explicitly supplied zero (or fractional/negative)
// token limit with "must be at least 1". A zero max_tokens also bypasses the
// shared converter's max_tokens -> max_completion_tokens migration, so move it
// to the canonical field while ensuring the upstream receives a valid value.
func NormalizeZeroChatTokenLimitsInBody(body []byte) ([]byte, bool, error) {
	var object map[string]json.RawMessage
	if err := common.Unmarshal(body, &object); err != nil {
		return nil, false, err
	}
	changed := false

	if raw, exists := object["max_tokens"]; exists && common.GetJsonType(raw) != "null" {
		var value float64
		if err := common.Unmarshal(raw, &value); err == nil && value < 1 {
			delete(object, "max_tokens")
			changed = true
			if canonical, hasCanonical := object["max_completion_tokens"]; !hasCanonical || common.GetJsonType(canonical) == "null" {
				encoded, err := common.Marshal(uint(1))
				if err != nil {
					return nil, false, err
				}
				object["max_completion_tokens"] = encoded
			} else {
				var canonicalValue float64
				if err := common.Unmarshal(canonical, &canonicalValue); err == nil && canonicalValue < 1 {
					encoded, err := common.Marshal(uint(1))
					if err != nil {
						return nil, false, err
					}
					object["max_completion_tokens"] = encoded
				}
			}
		}
	}

	if raw, exists := object["max_completion_tokens"]; exists && common.GetJsonType(raw) != "null" {
		var value float64
		if err := common.Unmarshal(raw, &value); err == nil && value < 1 {
			encoded, err := common.Marshal(uint(1))
			if err != nil {
				return nil, false, err
			}
			object["max_completion_tokens"] = encoded
			changed = true
		}
	}

	if !changed {
		return body, false, nil
	}
	normalized, err := common.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

// normalizeGPT56Tools upgrades preview tool names to the current GPT-5.6
// contract. The legacy computer preview's display/environment fields are
// removed because the current computer tool accepts only its type.
func normalizeGPT56Tools(r *dto.OpenAIResponsesRequest) (bool, error) {
	if !IsGPT56Model(r.Model) || len(r.Tools) == 0 {
		return false, nil
	}
	normalized, changed, err := normalizeGPT56ToolsRaw(r.Tools)
	if err != nil {
		return false, err
	}
	if changed {
		r.Tools = normalized
	}
	return changed, nil
}

// NormalizeGPT56ToolsInBody applies the same rewrite to a pass-through body,
// retaining unknown top-level fields as RawMessage.
func NormalizeGPT56ToolsInBody(body []byte, effectiveModel string) ([]byte, bool, error) {
	if !IsGPT56Model(effectiveModel) {
		return body, false, nil
	}
	var request map[string]json.RawMessage
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, false, err
	}
	tools, ok := request["tools"]
	if !ok || len(tools) == 0 {
		return body, false, nil
	}
	normalized, changed, err := normalizeGPT56ToolsRaw(tools)
	if err != nil || !changed {
		return body, false, err
	}
	request["tools"] = normalized
	encoded, err := common.Marshal(request)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

// NormalizeLegacyResponsesAliasesInBody rewrites two Chat Completions-era
// aliases that Codex clients still send on /v1/responses even though the
// official Responses contract retired them: top-level max_tokens (superseded
// by max_output_tokens) and top-level reasoning_effort (superseded by
// reasoning.effort). Both previously made the gateway's unknown-field
// allowlist reject an otherwise well-formed request.
//
// Per Rule 6, an explicit alias value is moved onto its canonical field
// rather than dropped, and only when the canonical field was not already set
// to a non-null value -- an explicit canonical value always wins. The alias
// key is deleted in every case so it never reaches the allowlist.
func NormalizeLegacyResponsesAliasesInBody(body []byte) ([]byte, bool, error) {
	var object map[string]json.RawMessage
	if err := common.Unmarshal(body, &object); err != nil {
		return body, false, nil
	}
	changed := false

	if raw, exists := object["max_tokens"]; exists {
		canonical, hasCanonical := object["max_output_tokens"]
		if (!hasCanonical || common.GetJsonType(canonical) == "null") && common.GetJsonType(raw) != "null" {
			object["max_output_tokens"] = raw
		}
		delete(object, "max_tokens")
		changed = true
	}

	if raw, exists := object["reasoning_effort"]; exists {
		if common.GetJsonType(raw) == "string" {
			var effort string
			if err := common.Unmarshal(raw, &effort); err != nil {
				return nil, false, fmt.Errorf("reasoning_effort must be a string: %w", err)
			}
			if effort = strings.TrimSpace(effort); effort != "" {
				if err := mergeReasoningEffortAlias(object, effort); err != nil {
					return nil, false, err
				}
			}
		}
		delete(object, "reasoning_effort")
		changed = true
	}

	if !changed {
		return body, false, nil
	}
	normalized, err := common.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

// mergeReasoningEffortAlias moves a top-level reasoning_effort value into
// reasoning.effort, creating the reasoning object if needed. An explicit,
// non-null reasoning.effort already present takes precedence and is left
// untouched.
func mergeReasoningEffortAlias(object map[string]json.RawMessage, effort string) error {
	var reasoning map[string]json.RawMessage
	if rawReasoning, exists := object["reasoning"]; exists && common.GetJsonType(rawReasoning) == "object" {
		if err := common.Unmarshal(rawReasoning, &reasoning); err != nil {
			return fmt.Errorf("reasoning must be an object: %w", err)
		}
	} else {
		reasoning = map[string]json.RawMessage{}
	}
	if existing, hasEffort := reasoning["effort"]; hasEffort && common.GetJsonType(existing) != "null" {
		return nil
	}
	encodedEffort, err := common.Marshal(effort)
	if err != nil {
		return err
	}
	reasoning["effort"] = encodedEffort
	encodedReasoning, err := common.Marshal(reasoning)
	if err != nil {
		return err
	}
	object["reasoning"] = encodedReasoning
	return nil
}

// NormalizeInvalidMessageItemIDsInBody drops a message input item's id when
// it uses the deprecated item_* prefix instead of an official msg_* replay
// identifier. An id is not required to replay an input item, so dropping it
// preserves the item's content while satisfying the official contract,
// instead of failing the whole request over one stale identifier from a
// replayed conversation history.
func NormalizeInvalidMessageItemIDsInBody(body []byte) ([]byte, bool, error) {
	var object map[string]json.RawMessage
	if err := common.Unmarshal(body, &object); err != nil {
		return body, false, nil
	}
	rawInput, exists := object["input"]
	if !exists || common.GetJsonType(rawInput) != "array" {
		return body, false, nil
	}
	var items []json.RawMessage
	if err := common.Unmarshal(rawInput, &items); err != nil {
		return body, false, nil
	}
	changed := false
	for index, rawItem := range items {
		if common.GetJsonType(rawItem) != "object" {
			continue
		}
		var item map[string]json.RawMessage
		if err := common.Unmarshal(rawItem, &item); err != nil {
			continue
		}
		rawID, hasID := item["id"]
		if !hasID || common.GetJsonType(rawID) != "string" {
			continue
		}
		var id string
		if err := common.Unmarshal(rawID, &id); err != nil {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(id), "item_") {
			continue
		}
		if _, messageLike := item["role"]; !messageLike {
			continue
		}
		delete(item, "id")
		encodedItem, err := common.Marshal(item)
		if err != nil {
			return nil, false, err
		}
		items[index] = encodedItem
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	encodedInput, err := common.Marshal(items)
	if err != nil {
		return nil, false, err
	}
	object["input"] = encodedInput
	normalized, err := common.Marshal(object)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

func normalizeGPT56ToolsRaw(raw json.RawMessage) (json.RawMessage, bool, error) {
	var tools []map[string]json.RawMessage
	if err := common.Unmarshal(raw, &tools); err != nil {
		return nil, false, fmt.Errorf("tools must be an array of objects: %w", err)
	}
	changed := false
	for _, tool := range tools {
		var typeName string
		if err := common.Unmarshal(tool["type"], &typeName); err != nil {
			return nil, false, fmt.Errorf("tool type must be a string: %w", err)
		}
		switch typeName {
		case "web_search_preview", "web_search_preview_2025_03_11":
			encodedType, err := common.Marshal("web_search")
			if err != nil {
				return nil, false, err
			}
			tool["type"] = encodedType
			changed = true
		case "computer_use_preview":
			encodedType, err := common.Marshal("computer")
			if err != nil {
				return nil, false, err
			}
			tool["type"] = encodedType
			delete(tool, "display_width")
			delete(tool, "display_height")
			delete(tool, "environment")
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	normalized, err := common.Marshal(tools)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}
