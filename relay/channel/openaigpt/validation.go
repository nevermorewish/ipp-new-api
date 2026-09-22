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
	"regexp"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	_ "golang.org/x/image/webp"
)

// maxToolNestingDepth bounds recursion through container tools so a
// deeply-nested or cyclic-looking payload cannot exhaust the stack.
const maxToolNestingDepth = 64

var includeValues = map[string]struct{}{
	"code_interpreter_call.outputs":         {},
	"computer_call_output.output.image_url": {},
	"file_search_call.results":              {},
	"message.input_image.image_url":         {},
	"message.output_text.logprobs":          {},
	"reasoning.encrypted_content":           {},
	"web_search_call.action.sources":        {},
	"web_search_call.results":               {},
}

var reasoningEfforts = map[string]struct{}{
	"none": {}, "minimal": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
}

// reasoningModes is the official reasoning.mode vocabulary. Unlike
// prompt_cache_options.ttl, this one is a closed enum in the saved spec
// ("One of the following: standard, pro"), so a local check cannot go stale
// against a value OpenAI is about to add.
var reasoningModes = map[string]struct{}{
	"standard": {}, "pro": {},
}

// reasoningContexts is the official reasoning.context vocabulary.
var reasoningContexts = map[string]struct{}{
	"auto": {}, "current_turn": {}, "all_turns": {},
}

// reasoningSummaries is the vocabulary shared by reasoning.summary and the
// deprecated reasoning.generate_summary.
var reasoningSummaries = map[string]struct{}{
	"auto": {}, "concise": {}, "detailed": {},
}

// Official limits on the metadata map: at most 16 pairs, keys up to 64
// characters, values up to 512.
const (
	maxMetadataPairs       = 16
	maxMetadataKeyLength   = 64
	maxMetadataValueLength = 512
	// OpenAI rejects a request when the top-level tools array contains more
	// than 128 entries. Keep this check local so an oversized request does not
	// make a paid upstream round trip (and so the error remains attributable to
	// the caller instead of looking like an intermittent provider failure).
	maxToolsPerRequest = 128
)

var imageDetails = map[string]struct{}{
	"auto": {}, "low": {}, "high": {}, "original": {},
}

// textFormatTypes is the official text.format.type vocabulary.
var textFormatTypes = map[string]struct{}{
	"text": {}, "json_object": {}, "json_schema": {},
}

// toolNamePattern is the official constraint on function and custom tool names.
var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateResponsesRequest checks a Responses request against the parts of the
// official OpenAI contract the gateway can prove locally, so a request the
// official API would reject fails here with a clear non-retryable 400 instead
// of costing an upstream round trip and a retry across every other channel.
//
// It deliberately covers only enums, JSON types and cross-item relationships.
// Provider and model capability decisions stay upstream: whether a given
// provider implements code_interpreter is a routing concern, not a contract
// violation, and is handled by the capability-error classifier instead.
func ValidateResponsesRequest(r *dto.OpenAIResponsesRequest) error {
	if r == nil {
		return fmt.Errorf("request is required")
	}
	if err := validateInclude(r.Include); err != nil {
		return err
	}
	if err := validateToolChoice(r.ToolChoice, r.Tools); err != nil {
		return err
	}
	if err := validateReasoning(r.Model, r.Reasoning); err != nil {
		return err
	}
	if err := validateScalarParameters(r); err != nil {
		return err
	}
	if err := validateTools(r.Tools, "tools"); err != nil {
		return err
	}
	if err := validateText(r.Text); err != nil {
		return err
	}
	if err := validateMetadata(r.Metadata); err != nil {
		return err
	}
	if err := validatePromptCacheOptions(r.PromptCacheOptions); err != nil {
		return err
	}
	if err := validateInputState(r); err != nil {
		return err
	}
	return validateInput(r)
}

// ValidateReasoningEffort validates the reasoning_effort vocabulary shared by
// Chat Completions and Responses.
func ValidateReasoningEffort(effort string) error {
	if effort == "" {
		return nil
	}
	if _, ok := reasoningEfforts[effort]; !ok {
		return fmt.Errorf("invalid reasoning effort %q; supported values are none, minimal, low, medium, high, xhigh, and max", effort)
	}
	return nil
}

// ValidateChatRequest checks the Chat Completions contract violations observed
// in production. It intentionally stays limited to rules the gateway can prove
// locally and is only called by the OpenAI-GPT channel.
func ValidateChatRequest(r *dto.GeneralOpenAIRequest) error {
	if r == nil {
		return fmt.Errorf("request is required")
	}
	if err := ValidateReasoningEffort(r.ReasoningEffort); err != nil {
		return err
	}
	if IsGPT56Model(r.Model) && r.ReasoningEffort == "minimal" {
		return fmt.Errorf("reasoning effort %q is not supported by GPT-5.6; use none, low, medium, high, xhigh, or max", r.ReasoningEffort)
	}

	toolNames := make(map[string]struct{}, len(r.Tools))
	if len(r.Tools) > maxToolsPerRequest {
		return fmt.Errorf("tools must contain at most %d entries, got %d", maxToolsPerRequest, len(r.Tools))
	}
	for index, tool := range r.Tools {
		switch tool.Type {
		case "function":
			if err := validateToolName(tool.Function.Name, fmt.Sprintf("tools[%d].function.name", index)); err != nil {
				return err
			}
			toolNames[tool.Function.Name] = struct{}{}
		case "custom":
			var custom struct {
				Name string `json:"name"`
			}
			if len(tool.Custom) == 0 || common.GetJsonType(tool.Custom) == "null" {
				return fmt.Errorf("tools[%d] of type %q requires custom.name", index, tool.Type)
			}
			if err := common.Unmarshal(tool.Custom, &custom); err != nil {
				return fmt.Errorf("tools[%d].custom must be an object: %w", index, err)
			}
			if err := validateToolName(custom.Name, fmt.Sprintf("tools[%d].custom.name", index)); err != nil {
				return err
			}
			toolNames[custom.Name] = struct{}{}
		}
	}

	if r.ResponseFormat != nil {
		if err := validateChatResponseFormat(r.ResponseFormat.Type, r.ResponseFormat.JsonSchema); err != nil {
			return err
		}
	}
	if err := validateChatToolCallHistory(r.Messages); err != nil {
		return err
	}

	return validateChatToolChoice(r.ToolChoice, r.Tools, toolNames)
}

// ValidateChatToolSchemas checks strict function-tool schemas straight from the
// request bytes.
//
// dto.FunctionRequest does not model the per-tool strict flag, so a DTO-level
// check cannot tell a strict tool from a best-effort one. Reading the raw body
// keeps the flag and its schema together. A body the gateway cannot parse is
// left for the upstream to judge rather than rejected here, matching the rest
// of the Chat contract checks.
func ValidateChatToolSchemas(body []byte) error {
	var request struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := common.Unmarshal(body, &request); err != nil {
		return nil
	}
	for index, tool := range request.Tools {
		if toolType, _ := tool["type"].(string); toolType != "function" {
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		if err := validateStrictFunctionParameters(function, fmt.Sprintf("tools[%d].function", index)); err != nil {
			return err
		}
	}
	return nil
}

// validateChatToolCallHistory enforces the one-result-per-call rule on a
// replayed tool-calling conversation.
//
// A second result for the same tool_call_id is not a request the model can
// resolve: the two payloads contradict each other, and whichever one the model
// happens to consume becomes a silent, non-deterministic data-integrity
// failure. The same goes for a tool result naming a call that no preceding
// assistant message made. Both are rejected before the request costs a
// generation.
func validateChatToolCallHistory(messages []dto.Message) error {
	announced := make(map[string]struct{})
	answered := make(map[string]struct{})
	for index := range messages {
		message := &messages[index]
		switch message.Role {
		case "assistant":
			for _, call := range message.ParseToolCalls() {
				if call.ID != "" {
					announced[call.ID] = struct{}{}
				}
			}
		case "tool":
			callID := strings.TrimSpace(message.ToolCallId)
			if callID == "" {
				return fmt.Errorf("messages[%d] of role tool requires tool_call_id", index)
			}
			if _, ok := announced[callID]; !ok {
				return fmt.Errorf("messages[%d].tool_call_id %q does not match any preceding assistant tool_call", index, callID)
			}
			if _, ok := answered[callID]; ok {
				return fmt.Errorf("messages[%d] submits a second result for tool_call_id %q; each tool call accepts exactly one result", index, callID)
			}
			answered[callID] = struct{}{}
		}
	}
	missing := make([]string, 0)
	for callID := range announced {
		if _, ok := answered[callID]; !ok {
			missing = append(missing, callID)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("assistant tool_calls must be followed by tool messages responding to each tool_call_id; missing responses for: %s", strings.Join(missing, ", "))
	}
	return nil
}

// validateChatToolChoice mirrors the Responses-side tool_choice checks for the
// Chat shape, where a named choice nests the name under function.name or
// custom.name rather than carrying it directly.
func validateChatToolChoice(value any, tools []dto.ToolCallRequest, toolNames map[string]struct{}) error {
	switch choice := value.(type) {
	case string:
		switch choice {
		case "none", "auto", "required":
		default:
			return fmt.Errorf("invalid tool_choice %q; supported string values are none, auto, and required", choice)
		}
		if choice == "required" && len(tools) == 0 {
			return fmt.Errorf("tool_choice %q requires at least one tool", choice)
		}
	case map[string]any:
		choiceType, _ := choice["type"].(string)
		if choiceType != "function" && choiceType != "custom" {
			// Hosted tool choices name no function, so there is nothing to
			// cross-check against tools.
			return nil
		}
		nested, _ := choice[choiceType].(map[string]any)
		name, _ := nested["name"].(string)
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("tool_choice of type %q requires %s.name", choiceType, choiceType)
		}
		if _, ok := toolNames[name]; !ok {
			return fmt.Errorf("tool_choice %q is not present in tools", name)
		}
	}
	return nil
}

func validateToolName(name string, path string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s is required", path)
	}
	if !toolNamePattern.MatchString(name) {
		return fmt.Errorf("%s %q must match %s", path, name, toolNamePattern.String())
	}
	return nil
}

func validateScalarParameters(r *dto.OpenAIResponsesRequest) error {
	if r.MaxToolCalls != nil && *r.MaxToolCalls == 0 {
		return fmt.Errorf("max_tool_calls must be at least 1 when provided")
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 2) {
		return fmt.Errorf("temperature must be between 0 and 2")
	}
	if r.TopP != nil && (*r.TopP < 0 || *r.TopP > 1) {
		return fmt.Errorf("top_p must be between 0 and 1")
	}
	if r.TopLogProbs != nil && (*r.TopLogProbs < 0 || *r.TopLogProbs > 20) {
		return fmt.Errorf("top_logprobs must be between 0 and 20")
	}
	// parallel_tool_calls and store are carried as raw JSON so unknown provider
	// extensions pass through untouched. That also lets a client send the
	// string "true", which the official API rejects with an opaque error, so
	// the JSON type is checked explicitly here.
	if err := requireBooleanIfPresent(r.ParallelToolCalls, "parallel_tool_calls"); err != nil {
		return err
	}
	if err := requireBooleanIfPresent(r.Store, "store"); err != nil {
		return err
	}
	if len(r.Truncation) == 0 || common.GetJsonType(r.Truncation) == "null" {
		return nil
	}
	if common.GetJsonType(r.Truncation) != "string" {
		return fmt.Errorf("truncation must be a string")
	}
	var truncation string
	if err := common.Unmarshal(r.Truncation, &truncation); err != nil {
		return fmt.Errorf("invalid truncation: %w", err)
	}
	if truncation != "auto" && truncation != "disabled" {
		return fmt.Errorf("invalid truncation %q; supported values are auto and disabled", truncation)
	}
	return nil
}

func requireBooleanIfPresent(raw json.RawMessage, field string) error {
	if len(raw) == 0 {
		return nil
	}
	switch common.GetJsonType(raw) {
	case "null", "boolean":
		return nil
	default:
		return fmt.Errorf("%s must be a boolean, got %s", field, common.GetJsonType(raw))
	}
}

func validateTools(raw json.RawMessage, path string) error {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var tools any
	if err := common.Unmarshal(raw, &tools); err != nil {
		return fmt.Errorf("%s must be an array of objects: %w", path, err)
	}
	return validateToolList(tools, path, 0)
}

func validateToolList(value any, path string, depth int) error {
	if depth > maxToolNestingDepth {
		return fmt.Errorf("Responses tools exceed maximum nesting depth of %d", maxToolNestingDepth)
	}
	tools, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array of objects", path)
	}
	if len(tools) > maxToolsPerRequest {
		return fmt.Errorf("%s must contain at most %d entries, got %d", path, maxToolsPerRequest, len(tools))
	}
	for index, value := range tools {
		tool, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s[%d] must be an object", path, index)
		}
		toolPath := fmt.Sprintf("%s[%d]", path, index)
		toolType, _ := tool["type"].(string)
		switch toolType {
		case "function", "custom":
			name, _ := tool["name"].(string)
			if err := validateToolName(name, toolPath+".name"); err != nil {
				return err
			}
			if toolType == "function" {
				if err := validateStrictFunctionParameters(tool, toolPath); err != nil {
					return err
				}
			}
		case "web_search", "web_search_preview", "web_search_preview_2025_03_11":
			if allowedCallersContains(tool["allowed_callers"], "programmatic") {
				return fmt.Errorf("%s web_search does not support allowed_callers=programmatic; use a supported programmatic host tool", toolPath)
			}
		}
		if nested, exists := tool["tools"]; exists {
			if err := validateToolList(nested, toolPath+".tools", depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func allowedCallersContains(value any, expected string) bool {
	callers, ok := value.([]any)
	if !ok {
		return false
	}
	for _, caller := range callers {
		if caller == expected {
			return true
		}
	}
	return false
}

// validateText checks text.format, which the official API validates but most
// third-party providers report only as an opaque upstream failure.
func validateText(raw json.RawMessage) error {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var text struct {
		Format json.RawMessage `json:"format"`
	}
	if err := common.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("text must be an object: %w", err)
	}
	if len(text.Format) == 0 || common.GetJsonType(text.Format) == "null" {
		return nil
	}
	var format map[string]json.RawMessage
	if err := common.Unmarshal(text.Format, &format); err != nil {
		return fmt.Errorf("text.format must be an object: %w", err)
	}
	rawType, exists := format["type"]
	if !exists {
		return fmt.Errorf("text.format.type is required")
	}
	var formatType string
	if err := common.Unmarshal(rawType, &formatType); err != nil {
		return fmt.Errorf("text.format.type must be a string: %w", err)
	}
	if _, ok := textFormatTypes[formatType]; !ok {
		return fmt.Errorf("invalid text.format.type %q; supported values are text, json_object, and json_schema", formatType)
	}
	if formatType == "json_schema" {
		if _, ok := format["schema"]; !ok {
			return fmt.Errorf("text.format.schema is required when text.format.type is json_schema")
		}
	}
	return validateStrictTextFormat(format)
}

func validatePromptCacheOptions(raw json.RawMessage) error {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var options map[string]json.RawMessage
	if err := common.Unmarshal(raw, &options); err != nil {
		return fmt.Errorf("prompt_cache_options must be an object: %w", err)
	}
	if rawMode, exists := options["mode"]; exists {
		var mode string
		if err := common.Unmarshal(rawMode, &mode); err != nil {
			return fmt.Errorf("prompt_cache_options.mode must be a string: %w", err)
		}
		if mode != "implicit" && mode != "explicit" {
			return fmt.Errorf("invalid prompt_cache_options.mode %q; supported values are implicit and explicit", mode)
		}
	}
	// ttl is deliberately not validated against a fixed vocabulary. The only
	// documented value today is "30m", but that is a value OpenAI is expected to
	// extend, and a stale local allow-list would reject a request the upstream
	// would have accepted. Let the upstream own it.
	return nil
}

func validateInclude(raw json.RawMessage) error {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var values []string
	if err := common.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("include must be an array of strings: %w", err)
	}
	for _, value := range values {
		if _, ok := includeValues[value]; !ok {
			return fmt.Errorf("invalid include value %q", value)
		}
	}
	return nil
}

// validateToolChoice covers both forms: the string vocabulary, and the object
// form naming a specific function, which must actually be present in tools.
func validateToolChoice(raw json.RawMessage, tools json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	switch common.GetJsonType(raw) {
	case "string":
		var choice string
		if err := common.Unmarshal(raw, &choice); err != nil {
			return fmt.Errorf("invalid tool_choice: %w", err)
		}
		switch choice {
		case "none", "auto", "required":
			return nil
		default:
			return fmt.Errorf("invalid tool_choice %q; supported string values are none, auto, and required", choice)
		}
	case "object":
		var choice struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err := common.Unmarshal(raw, &choice); err != nil {
			return fmt.Errorf("invalid tool_choice: %w", err)
		}
		if choice.Type != "function" && choice.Type != "custom" {
			// Hosted tool choices (file_search, web_search, ...) name no
			// function, so there is nothing to cross-check.
			return nil
		}
		if strings.TrimSpace(choice.Name) == "" {
			return fmt.Errorf("tool_choice of type %q requires a name", choice.Type)
		}
		names, err := collectToolNames(tools)
		if err != nil {
			return err
		}
		if _, ok := names[choice.Name]; !ok {
			return fmt.Errorf("tool_choice %q is not present in tools", choice.Name)
		}
	}
	return nil
}

func collectToolNames(raw json.RawMessage) (map[string]struct{}, error) {
	names := make(map[string]struct{})
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return names, nil
	}
	var tools []map[string]any
	if err := common.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("tools must be an array of objects: %w", err)
	}
	for _, tool := range tools {
		if name, _ := tool["name"].(string); name != "" {
			names[name] = struct{}{}
		}
	}
	return names, nil
}

func validateReasoning(model string, reasoning *dto.Reasoning) error {
	if reasoning == nil {
		return nil
	}
	if reasoning.Effort != "" {
		if err := ValidateReasoningEffort(reasoning.Effort); err != nil {
			return err
		}
		if IsGPT56Model(model) && reasoning.Effort == "minimal" {
			return fmt.Errorf("reasoning effort %q is not supported by GPT-5.6; use none, low, medium, high, xhigh, or max", reasoning.Effort)
		}
	}
	if err := validateEnumMember(reasoning.Mode, "reasoning.mode", reasoningModes); err != nil {
		return err
	}
	if err := validateEnumMember(reasoning.Context, "reasoning.context", reasoningContexts); err != nil {
		return err
	}
	if err := validateEnumMember(reasoning.GenerateSummary, "reasoning.generate_summary", reasoningSummaries); err != nil {
		return err
	}
	if reasoning.Summary != "" {
		if _, ok := reasoningSummaries[reasoning.Summary]; !ok {
			return fmt.Errorf("invalid reasoning.summary %q; supported values are %s", reasoning.Summary, joinVocabulary(reasoningSummaries))
		}
	}
	return nil
}

// validateEnumMember checks a raw JSON value against a closed vocabulary,
// treating an absent or explicitly null value as unset.
func validateEnumMember(raw json.RawMessage, path string, vocabulary map[string]struct{}) error {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var value string
	if err := common.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%s must be a string: %w", path, err)
	}
	if _, ok := vocabulary[value]; !ok {
		return fmt.Errorf("invalid %s %q; supported values are %s", path, value, joinVocabulary(vocabulary))
	}
	return nil
}

func joinVocabulary(vocabulary map[string]struct{}) string {
	values := make([]string, 0, len(vocabulary))
	for value := range vocabulary {
		values = append(values, value)
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}

// validateMetadata enforces the documented size limits on the metadata map:
// at most 16 pairs, keys up to 64 characters and string values up to 512.
func validateMetadata(raw json.RawMessage) error {
	if len(raw) == 0 || common.GetJsonType(raw) == "null" {
		return nil
	}
	var metadata map[string]any
	if err := common.Unmarshal(raw, &metadata); err != nil {
		return fmt.Errorf("metadata must be an object of string key-value pairs: %w", err)
	}
	if len(metadata) > maxMetadataPairs {
		return fmt.Errorf("metadata accepts at most %d key-value pairs, got %d", maxMetadataPairs, len(metadata))
	}
	for _, key := range sortedKeys(metadata) {
		if len(key) > maxMetadataKeyLength {
			return fmt.Errorf("metadata key %q exceeds the maximum length of %d characters", key, maxMetadataKeyLength)
		}
		value, ok := metadata[key].(string)
		if !ok {
			return fmt.Errorf("metadata value for %q must be a string", key)
		}
		if len(value) > maxMetadataValueLength {
			return fmt.Errorf("metadata value for %q exceeds the maximum length of %d characters", key, maxMetadataValueLength)
		}
	}
	return nil
}

// validateInputState rejects message identifiers that violate the official
// contract. Easy input messages omit id; replayed output messages use msg_*
// identifiers rather than item_*.
func validateInputState(r *dto.OpenAIResponsesRequest) error {
	if len(r.Input) == 0 || common.GetJsonType(r.Input) != "array" {
		return nil
	}
	var input []any
	if err := common.Unmarshal(r.Input, &input); err != nil {
		return nil
	}
	for index, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		id, _ := item["id"].(string)
		if !strings.HasPrefix(strings.TrimSpace(id), "item_") {
			continue
		}
		if _, messageLike := item["role"]; messageLike {
			return fmt.Errorf("input[%d].id must use an official msg_* message identifier or be omitted; item_* is not valid", index)
		}
	}
	return nil
}

func validateInput(r *dto.OpenAIResponsesRequest) error {
	if len(r.Input) == 0 || common.GetJsonType(r.Input) == "string" {
		return nil
	}

	var input any
	if err := common.Unmarshal(r.Input, &input); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}

	functionCalls := make(map[string]struct{})
	itemReferences := make(map[string]struct{})
	functionOutputs := make([]string, 0)
	err := walkJSON(input, func(item map[string]any) error {
		if breakpoint, exists := item["prompt_cache_breakpoint"]; exists {
			breakpointObject, ok := breakpoint.(map[string]any)
			if !ok {
				return fmt.Errorf("prompt_cache_breakpoint must be an object")
			}
			mode, _ := breakpointObject["mode"].(string)
			if mode != "explicit" {
				return fmt.Errorf("invalid prompt_cache_breakpoint mode %q; only explicit is supported", mode)
			}
		}

		typeName, _ := item["type"].(string)
		if typeName == "additional_tools" {
			if tools, exists := item["tools"]; exists {
				if err := validateToolList(tools, "input.additional_tools", 0); err != nil {
					return err
				}
			}
		}
		switch typeName {
		case "input_image":
			if detail, _ := item["detail"].(string); detail != "" {
				if _, ok := imageDetails[detail]; !ok {
					return fmt.Errorf("invalid input_image detail %q; supported values are auto, low, high, and original", detail)
				}
			}
			if imageURL, _ := item["image_url"].(string); strings.HasPrefix(strings.ToLower(imageURL), "data:image/") {
				if err := ValidateDataImage(imageURL); err != nil {
					return err
				}
			}
		case "function_call":
			if callID, _ := item["call_id"].(string); callID != "" {
				functionCalls[callID] = struct{}{}
			}
		case "item_reference":
			if id, _ := item["id"].(string); id != "" {
				itemReferences[id] = struct{}{}
			}
		case "function_call_output":
			if callID, _ := item["call_id"].(string); callID != "" {
				functionOutputs = append(functionOutputs, callID)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// A conversation or previous response can carry the matching function call.
	// Without either, every output must reference a function_call (or an
	// item_reference) included in this request.
	if r.PreviousResponseID != "" || len(r.Conversation) > 0 {
		return nil
	}
	for _, callID := range functionOutputs {
		if _, ok := functionCalls[callID]; ok {
			continue
		}
		if _, ok := itemReferences[callID]; ok {
			continue
		}
		return fmt.Errorf("function_call_output call_id %q has no matching function_call context; include the call, conversation, or previous_response_id", callID)
	}
	return nil
}

func walkJSON(value any, visit func(map[string]any) error) error {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if err := walkJSON(item, visit); err != nil {
				return err
			}
		}
	case map[string]any:
		if err := visit(typed); err != nil {
			return err
		}
		for _, child := range typed {
			if err := walkJSON(child, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateDataImage reports whether an inline data: URL decodes to an image
// this gateway recognizes. The capability classifier reuses it to tell a
// genuinely corrupt image (reject locally) from one a particular provider
// happens to refuse (retry elsewhere).
func ValidateDataImage(dataURL string) error {
	header, payload, found := strings.Cut(dataURL, ",")
	if !found || !strings.Contains(strings.ToLower(header), ";base64") {
		return fmt.Errorf("input_image data URL must contain base64 image data")
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fmt.Errorf("input_image contains invalid base64 data: %w", err)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		if err == nil {
			err = fmt.Errorf("image dimensions must be positive")
		}
		return fmt.Errorf("input_image data does not represent a supported PNG, JPEG, WEBP, or GIF image: %w", err)
	}
	return nil
}

// IsGPT56Model reports whether a model name is in the GPT-5.6 family.
func IsGPT56Model(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "gpt-5.6" || strings.HasPrefix(model, "gpt-5.6-")
}
