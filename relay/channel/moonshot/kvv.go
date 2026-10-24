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
		(strings.EqualFold(info.UpstreamModelName, "kimi-k3") ||
			strings.EqualFold(info.UpstreamModelName, "kimi-k3.1-pro"))
}

var kvvToolName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,255}$`)

func adaptKVVRequest(request *dto.GeneralOpenAIRequest) error {
	if v := request.Temperature; v != nil && (math.IsNaN(*v) || *v < 0 || *v > 1) {
		return fmt.Errorf("temperature must be between 0 and 1 for kimi-k3")
	}
	if v := request.TopP; v != nil && *v != 0.95 {
		return fmt.Errorf("top_p must be 0.95 for kimi-k3")
	}
	if v := request.PresencePenalty; v != nil && *v != 0 {
		return fmt.Errorf("presence_penalty must be 0 for kimi-k3")
	}
	if v := request.FrequencyPenalty; v != nil && *v != 0 {
		return fmt.Errorf("frequency_penalty must be 0 for kimi-k3")
	}
	if v := request.N; v != nil && *v != 1 {
		return fmt.Errorf("n must be 1 for kimi-k3")
	}

	names := make(map[string]bool)
	for _, tool := range request.Tools {
		names[tool.Function.Name] = true
	}
	for i := range request.Messages {
		message := &request.Messages[i]
		if len(message.Tools) == 0 {
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
		// The DTO omits content for tool declarations when Content is nil.
		// Keep the original raw tools so strict and schema extensions survive.
		message.Content = nil
	}
	if err := adaptKVVThinking(request); err != nil {
		return err
	}
	// The KVV contract accepts [0,1], but Moonshot's thinking backend fixes
	// sampling at 1. Validate first so invalid input is never silently accepted.
	if request.Temperature != nil && request.ReasoningEffort != "none" {
		request.Temperature = common.GetPointer(1.0)
	}
	return nil
}

func adaptKVVThinking(request *dto.GeneralOpenAIRequest) error {
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
			if err := common.Unmarshal(raw, &effort); err != nil {
				return fmt.Errorf("thinking.effort must be low, high or max")
			}
		}
		switch effort {
		case "low", "high", "max":
		default:
			return fmt.Errorf("reasoning effort must be low, high or max")
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
