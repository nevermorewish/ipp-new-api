package moonshot

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// KVVEnabled scopes the opt-in compatibility contract to Moonshot K3 chat.
func KVVEnabled(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil &&
		info.RelayMode == relayconstant.RelayModeChatCompletions &&
		info.ChannelType == constant.ChannelTypeMoonshot &&
		info.ChannelSetting.MoonshotKVVEnabled &&
		isKimiK3FamilyModel(info.UpstreamModelName)
}

// isKimiK3FamilyModel reports the models that share the K3 parameter contract:
// fixed sampling values, preserved thinking and the 1M token context window.
// The list itself lives in dto because the Claude Messages relay applies the
// same cache contract to the same models.
func isKimiK3FamilyModel(model string) bool {
	return dto.IsKimiK3FamilyModel(model)
}

// isKimiK31Model reports the K3.1 releases, which have their own parameter
// compatibility switch.
func isKimiK31Model(model string) bool {
	return dto.IsKimiK31Model(model)
}

// kvvParamCompat reports whether the legacy rewrite switch is on: an
// unsupported sampling value or reasoning effort is translated to the value the
// upstream understands instead of being rejected.
func kvvParamCompat(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil &&
		info.ChannelSetting.MoonshotKVVParamCompat
}

// kvvOfficialCompat reports whether the K3.1 switch is on. It makes the
// gateway answer exactly like the official API, including the cases where the
// legacy behavior accepted a request the official API rejects, or rejected one
// it accepts.
func kvvOfficialCompat(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil &&
		isKimiK31Model(info.UpstreamModelName) &&
		info.ChannelSetting.MoonshotK31ParamCompat
}

// kvvMediaStringShorthand reports whether the image_url and video_url string
// shorthand of the official API should be rewritten into the object form.
func kvvMediaStringShorthand(info *relaycommon.RelayInfo) bool {
	return KVVEnabled(info) && kvvOfficialCompat(info)
}

var kvvToolName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,255}$`)

func adaptKVVRequest(request *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) error {
	// Three tiers, each opt-in and each strictly additive over the one before:
	//
	//   1. baseline (no switch): the behavior the gateway has always had.
	//   2. legacy compat: unsupported sampling values and reasoning efforts are
	//      rewritten to the nearest value the upstream understands.
	//   3. K3.1 compat: the gateway answers exactly like the official API,
	//      including the cases where tiers 1 and 2 accepted what the official
	//      API rejects, or rejected what it accepts.
	//
	// Nothing in tier 3 leaks into an existing channel; it runs only when the
	// K3.1 switch is on for a K3.1 model.
	legacyCompat := kvvParamCompat(info)
	official := kvvOfficialCompat(info)
	paramCompat := legacyCompat || official

	if official {
		if err := validateKVVPrediction(request.Prediction); err != nil {
			return err
		}
		if err := validateKVVPromptCacheOptions(request.PromptCacheOptions); err != nil {
			return err
		}
		request.PromptCacheOptions = rewriteKVVPromptCacheOptionsMode(request.PromptCacheOptions)
		if err := clampKVVOutputBudget(request, info); err != nil {
			return err
		}
		// A tool without parameters is valid for K3.1. Fill an empty schema so
		// the upstream's stricter structure check accepts the same request.
		normalizeKimiFunctionParameterSchemas(request.Tools)
	}

	// OpenAI clients may send developer instructions and empty system
	// placeholders. Kimi uses system for both instruction roles. Dropping the
	// placeholder is a compatibility behavior, so it does not run while a
	// compatibility switch is on: the official contract rejects an empty
	// content, and the check below must see the message to do that.
	messages := make([]dto.Message, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "developer" {
			message.Role = "system"
		}
		emptyContent := message.Content == nil
		if content, ok := message.Content.(string); ok {
			emptyContent = content == ""
		}
		if !paramCompat && message.Role == "system" && emptyContent && len(request.Messages) > 1 &&
			len(message.Tools) == 0 && len(message.ToolCalls) == 0 && message.ToolCallId == "" &&
			message.Name == nil && message.Prefix == nil && message.ReasoningContent == nil &&
			message.Reasoning == nil && len(message.Annotations) == 0 {
			continue
		}
		messages = append(messages, message)
	}
	if len(messages) > 0 {
		request.Messages = messages
	}

	names := make(map[string]bool)
	for _, tool := range request.Tools {
		names[tool.Function.Name] = true
	}
	for i := range request.Messages {
		message := &request.Messages[i]
		// Not every Kimi upstream rejects this, so enforce the official 400 here.
		if message.Role == "tool" && message.ToolCallId == "" {
			return fmt.Errorf("messages[%d].tool_call_id is required for role tool", i)
		}
		if len(message.Tools) == 0 {
			// The official API rejects an empty message content; the gateway
			// only enforces it while the compatibility switch is on.
			if paramCompat {
				if err := validateKVVMessageContent(message, i); err != nil {
					return err
				}
			}
			continue
		}
		if message.Role != "system" {
			return fmt.Errorf("messages[%d].tools requires role system", i)
		}
		if message.Content != nil {
			if content, ok := message.Content.(string); !ok || content != "" {
				return fmt.Errorf("messages[%d].tools cannot be combined with non-empty content", i)
			}
		}
		var tools []dto.ToolCallRequest
		if err := common.Unmarshal(message.Tools, &tools); err != nil || tools == nil {
			return fmt.Errorf("messages[%d].tools must be an array of function tools", i)
		}
		for _, tool := range tools {
			if tool.Type != "function" || !kvvToolName.MatchString(tool.Function.Name) {
				return fmt.Errorf("messages[%d].tools requires function tools with valid names (1-256 characters)", i)
			}
			if names[tool.Function.Name] {
				return fmt.Errorf("messages[%d].tools contains a duplicate function name", i)
			}
			names[tool.Function.Name] = true
		}
		// A dynamically declared tool may omit parameters just like a top-level
		// one; fill the empty schema so upstream accepts it.
		if paramCompat && normalizeKimiFunctionParameterSchemas(tools) {
			if normalized, err := common.Marshal(tools); err == nil {
				message.Tools = normalized
			}
		}
		// The DTO omits content for tool declarations when Content is nil.
		// Keep the original raw tools so strict and schema extensions survive.
		message.Content = nil
	}
	if err := adaptKVVThinking(request, legacyCompat, official); err != nil {
		return err
	}
	// K3 fixes sampling server-side. The fixed temperature depends on the
	// thinking mode: 1 with thinking enabled and 0.6 with it disabled.
	kvvFixedTemperature := 1.0
	if request.ReasoningEffort == "none" {
		kvvFixedTemperature = 0.6
	}
	if official {
		// The official API rejects anything but the fixed value instead of
		// silently lowering it. This is stricter than the baseline, so it
		// stays behind the K3.1 switch.
		if v := request.Temperature; v != nil && (math.IsNaN(*v) || *v != kvvFixedTemperature) {
			return fmt.Errorf("temperature must be %v", kvvFixedTemperature)
		}
	} else if legacyCompat {
		// The legacy switch rewrites: any temperature in [0,1] is accepted and
		// then replaced with the value the backend actually uses.
		if request.Temperature != nil && (math.IsNaN(*request.Temperature) ||
			*request.Temperature < 0 || *request.Temperature > 1) {
			return fmt.Errorf("temperature must be between 0 and 1")
		}
		if request.Temperature != nil {
			request.Temperature = common.GetPointer(kvvFixedTemperature)
		}
	} else if request.Temperature != nil && (math.IsNaN(*request.Temperature) ||
		*request.Temperature < 0 || *request.Temperature > 1) {
		// Unchanged behavior: any temperature in [0,1] is accepted, and
		// thinking requests are pinned to 1 because the backend fixes it.
		return fmt.Errorf("temperature must be between 0 and 1")
	}
	if !paramCompat && request.Temperature != nil && request.ReasoningEffort != "none" {
		request.Temperature = common.GetPointer(1.0)
	}

	if v := request.TopP; v != nil && *v != 0.95 {
		return fmt.Errorf("top_p must be 0.95")
	}
	if v := request.PresencePenalty; v != nil && *v != 0 {
		return fmt.Errorf("presence_penalty must be 0")
	}
	if v := request.FrequencyPenalty; v != nil && *v != 0 {
		return fmt.Errorf("frequency_penalty must be 0")
	}
	if v := request.N; v != nil && *v != 1 {
		return fmt.Errorf("n must be 1")
	}
	return nil
}

// validateKVVMessageContent enforces the official "content must not be empty"
// rule. Tool results may stay blank, and an assistant turn may carry only tool
// calls, so both are accepted without content.
func validateKVVMessageContent(message *dto.Message, index int) error {
	switch message.Role {
	case "assistant":
		// Preserved thinking replays keep an empty content with the assistant's
		// reasoning, which the official API accepts.
		if hasKimiAssistantToolCalls(message.ToolCalls) ||
			(message.ReasoningContent != nil && strings.TrimSpace(*message.ReasoningContent) != "") {
			return nil
		}
	case "tool":
		return nil
	case "system":
		// Dynamic tool declarations are carried without content.
		if len(message.Tools) > 0 {
			return nil
		}
	case "user":
	default:
		return nil
	}
	if hasMeaningfulKimiMessageContent(message.Content) {
		return nil
	}
	return fmt.Errorf("messages[%d] with role %q must not have empty content", index, message.Role)
}

// validateKVVPrediction enforces Predicted Output's closed enum. The official
// API rejects any other type, so an illegal value must never reach upstream.
func validateKVVPrediction(raw json.RawMessage) error {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var prediction struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if err := common.Unmarshal(raw, &prediction); err != nil {
		return fmt.Errorf("prediction must be an object")
	}
	if prediction.Type != "content" {
		return fmt.Errorf("prediction.type must be content")
	}
	var parts []map[string]any
	if err := common.Unmarshal(prediction.Content, &parts); err == nil {
		for i, part := range parts {
			if kind, _ := part["type"].(string); kind != dto.ContentTypeText {
				return fmt.Errorf("prediction.content[%d].type must be text", i)
			}
		}
	}
	return nil
}

// validateKVVPromptCacheOptions enforces the closed enums of the cache write
// options. The official contract accepts only implicit mode and the 5m/1h
// lifetimes; a value outside them is a client error, not something upstream
// should discover after the request is already billed.
func validateKVVPromptCacheOptions(raw json.RawMessage) error {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var options struct {
		Mode string `json:"mode"`
		TTL  string `json:"ttl"`
	}
	if err := common.Unmarshal(raw, &options); err != nil {
		return fmt.Errorf("prompt_cache_options must be an object")
	}
	if options.Mode != "" && options.Mode != "implicit" && options.Mode != "explicit" {
		return fmt.Errorf("prompt_cache_options.mode must be implicit or explicit")
	}
	switch options.TTL {
	case "", "5m", "1h":
	default:
		return fmt.Errorf("prompt_cache_options.ttl must be 5m or 1h")
	}
	return nil
}

// rewriteKVVPromptCacheOptionsMode lowers explicit mode to implicit for the
// upstream wire. The official API accepts both, but the Moonshot upstream
// rejects "explicit" with a generic format error, so the write hint would fail
// the whole request instead of enabling caching. The lifetime is preserved.
func rewriteKVVPromptCacheOptionsMode(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || common.GetJsonType(raw) != "object" {
		return raw
	}
	var options map[string]json.RawMessage
	if err := common.Unmarshal(raw, &options); err != nil {
		return raw
	}
	var mode string
	if err := common.Unmarshal(options["mode"], &mode); err != nil || mode != "explicit" {
		return raw
	}
	options["mode"], _ = common.Marshal("implicit")
	rewritten, err := common.Marshal(options)
	if err != nil {
		return raw
	}
	return rewritten
}

// validateKVVMessagesCacheControl enforces the official Messages cache_control
// shape: ephemeral type with an optional 5m/1h lifetime. The official endpoint
// reads only the request-level field and ignores the same mark inside messages,
// so an unknown member such as "mode" silently changed nothing; reporting it as
// a client error is part of the compatibility contract.
func validateKVVMessagesCacheControl(raw json.RawMessage, path string) error {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var control map[string]json.RawMessage
	if err := common.Unmarshal(raw, &control); err != nil {
		return fmt.Errorf("%s must be an object", path)
	}
	for key := range control {
		switch key {
		case "type", "ttl":
		default:
			return fmt.Errorf("%s.%s is not supported", path, key)
		}
	}
	if rawType, ok := control["type"]; ok {
		var kind string
		if err := common.Unmarshal(rawType, &kind); err != nil || kind != "ephemeral" {
			return fmt.Errorf("%s.type must be ephemeral", path)
		}
	}
	if rawTTL, ok := control["ttl"]; ok {
		var ttl string
		if err := common.Unmarshal(rawTTL, &ttl); err != nil || (ttl != "5m" && ttl != "1h") {
			return fmt.Errorf("%s.ttl must be 5m or 1h", path)
		}
	}
	return nil
}

// claudeContentCacheControls returns the cache_control mark of every content
// block, keyed by block index. A decoded Messages body carries blocks as
// []any of map[string]any, while an in-process request may already hold the
// typed []dto.ClaudeMediaMessage, so both shapes are read.
func claudeContentCacheControls(content any) map[int]json.RawMessage {
	controls := make(map[int]json.RawMessage)
	switch blocks := content.(type) {
	case []dto.ClaudeMediaMessage:
		for index := range blocks {
			if len(blocks[index].CacheControl) > 0 {
				controls[index] = blocks[index].CacheControl
			}
		}
	case []any:
		for index, rawBlock := range blocks {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			raw, ok := block["cache_control"]
			if !ok || raw == nil {
				continue
			}
			encoded, err := common.Marshal(raw)
			if err != nil {
				continue
			}
			controls[index] = encoded
		}
	}
	return controls
}

// kimiMessagesCacheTTL returns the TTL a Claude Messages request asks for.
// The official endpoint honours only the request-level cache_control and
// ignores body marks, so a client that marks a content block would silently
// fall back to the default 5m lifetime. Under the compatibility switch the
// block mark is lifted to the request level so the requested lifetime wins.
func kimiMessagesCacheTTL(request *dto.ClaudeRequest) (string, bool) {
	if request == nil {
		return "", false
	}
	if ttl, ok := claudeCacheControlTTL(request.CacheControl); ok {
		return ttl, true
	}
	for messageIndex := range request.Messages {
		controls := claudeContentCacheControls(request.Messages[messageIndex].Content)
		// Block order decides which TTL wins, so iterate by index rather than
		// over the map.
		for blockIndex := 0; blockIndex < len(controls); blockIndex++ {
			if ttl, ok := claudeCacheControlTTL(controls[blockIndex]); ok {
				return ttl, true
			}
		}
	}
	return "", false
}

func claudeCacheControlTTL(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var control struct {
		TTL string `json:"ttl"`
	}
	if err := common.Unmarshal(raw, &control); err != nil {
		return "", false
	}
	if control.TTL != "5m" && control.TTL != "1h" {
		return "", false
	}
	return control.TTL, true
}

// clampKVVOutputBudget reproduces the official behavior for an output budget
// that does not fit the remaining context: the API lowers it and continues
// instead of rejecting the request. A value above the model maximum is a
// different matter, and the official API answers it with 400 rather than
// lowering it, so that check runs first. The prompt estimate is already
// available because billing preparation runs before request conversion.
func clampKVVOutputBudget(request *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) error {
	for _, field := range []struct {
		name  string
		value *uint
	}{
		{"max_completion_tokens", request.MaxCompletionTokens},
		{"max_tokens", request.MaxTokens},
	} {
		if field.value != nil && *field.value > kimiK3ContextWindowTokens {
			return fmt.Errorf("%s must not exceed %d", field.name, kimiK3ContextWindowTokens)
		}
	}
	if info == nil {
		return nil
	}
	budget := uint64(kimiK3ContextWindowTokens)
	if prompt := info.GetEstimatePromptTokens(); prompt > 0 && uint64(prompt) < budget {
		budget -= uint64(prompt)
	}
	if request.MaxCompletionTokens != nil && uint64(*request.MaxCompletionTokens) > budget {
		request.MaxCompletionTokens = common.GetPointer(uint(budget))
	}
	if request.MaxTokens != nil && uint64(*request.MaxTokens) > budget {
		request.MaxTokens = common.GetPointer(uint(budget))
	}
	return nil
}

// K3 accepts only low, high and max. The other OpenAI and Anthropic levels are
// accepted by the official API, so map them onto the nearest level rather than
// rejecting clients that legitimately send them.
func compatKVVEffort(effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "minimal":
		return "low"
	case "medium":
		return "high"
	case "xhigh":
		return "max"
	}
	return effort
}

// isOfficialKVVEffort reports whether the official K3.1 API accepts the level.
// The official enum is exactly low, high and max; the wider OpenAI and
// Anthropic levels stay a legacy-compat rewrite, so a client sending medium
// gets the official 400 instead of a silent translation.
func isOfficialKVVEffort(effort string) bool {
	switch effort {
	case "low", "high", "max":
		return true
	default:
		return false
	}
}

func adaptKVVThinking(request *dto.GeneralOpenAIRequest, legacyCompat, official bool) error {
	thinking := make(map[string]json.RawMessage)
	if len(request.THINKING) > 0 {
		if err := common.Unmarshal(request.THINKING, &thinking); err != nil || thinking == nil {
			return fmt.Errorf("thinking must be an object")
		}
	}
	kwargs := make(map[string]json.RawMessage)
	if len(request.ChatTemplateKwargs) > 0 {
		if err := common.Unmarshal(request.ChatTemplateKwargs, &kwargs); err != nil || kwargs == nil {
			return fmt.Errorf("chat_template_kwargs must be an object")
		}
	}

	// The legacy switch translates the wider OpenAI and Anthropic level set
	// onto low, high and max. The official contract must not inherit that
	// rewrite even when both switches are on: it forwards the level untouched
	// so the enum check below answers the official 400 for anything outside
	// low, high and max.
	if legacyCompat && !official {
		request.ReasoningEffort = compatKVVEffort(request.ReasoningEffort)
	}
	// Explicit thinking.type wins, followed by reasoning_effort=none, then
	// the open-source template switch. All three dialects agree on the wire.
	enabled := request.ReasoningEffort != "none"
	for _, key := range []string{"enable_thinking", "thinking"} {
		if raw, ok := kwargs[key]; ok {
			var value *bool
			if err := common.Unmarshal(raw, &value); err != nil || value == nil {
				return fmt.Errorf("chat_template_kwargs.%s must be a boolean", key)
			}
			if request.ReasoningEffort != "none" {
				enabled = *value
			}
		}
	}
	if raw, ok := thinking["type"]; ok {
		var kind string
		if err := common.Unmarshal(raw, &kind); err != nil || (kind != "enabled" && kind != "disabled") {
			return fmt.Errorf("thinking.type must be enabled or disabled")
		}
		enabled = kind == "enabled"
	}

	if enabled {
		effort := request.ReasoningEffort
		if effort == "" || effort == "none" {
			effort = "max"
		}
		if raw, ok := thinking["effort"]; ok {
			fallback := effort
			if err := common.Unmarshal(raw, &effort); err != nil {
				return fmt.Errorf("thinking.effort must be low, high or max")
			}
			// The legacy compatibility switch treats an empty thinking.effort as
			// omitted and rewrites the wider level set.
			if legacyCompat {
				if effort = compatKVVEffort(effort); effort == "" {
					effort = fallback
				}
			}
		}
		// The official API accepts the wider level set and forwards it, so the
		// gateway must accept it too rather than answering 400.
		if official && isOfficialKVVEffort(strings.ToLower(strings.TrimSpace(effort))) {
			effort = strings.ToLower(strings.TrimSpace(effort))
		} else {
			switch effort {
			case "low", "high", "max":
			default:
				return fmt.Errorf("reasoning effort must be low, high or max")
			}
		}
		if raw, ok := thinking["keep"]; ok {
			var keep string
			if err := common.Unmarshal(raw, &keep); err != nil || keep != "all" {
				return fmt.Errorf("thinking.keep must be all when thinking is enabled")
			}
		}
		thinking["type"] = json.RawMessage(`"enabled"`)
		thinking["keep"] = json.RawMessage(`"all"`)
		thinking["effort"], _ = common.Marshal(effort)
		request.ReasoningEffort = effort
	} else {
		thinking["type"] = json.RawMessage(`"disabled"`)
		delete(thinking, "effort")
		delete(thinking, "keep")
		request.ReasoningEffort = "none"
	}
	for _, key := range []string{"thinking", "enable_thinking"} {
		if _, ok := kwargs[key]; ok {
			kwargs[key], _ = common.Marshal(enabled)
		}
	}
	var err error
	request.THINKING, err = common.Marshal(thinking)
	if err != nil {
		return err
	}
	if len(request.ChatTemplateKwargs) > 0 {
		request.ChatTemplateKwargs, err = common.Marshal(kwargs)
	}
	return err
}
