package moonshot

import (
	"fmt"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/tidwall/gjson"
)

// validateKimiToolResultPairing rejects a Chat body whose tool result names a
// call that no preceding assistant message announced.
//
// Kimi answers such a request instead of failing it, so the client never learns
// that its tool history is inconsistent: the model reads an unexplained result,
// and every later turn replays the same broken context. The check reads the raw
// outbound body so pass-through requests are covered too, and it runs after the
// schema normalization so a rewritten body is what gets judged.
//
// A result repeated for the same call is rejected for the same reason the
// model cannot resolve it. Announced calls with no result are deliberately left
// alone: truncated histories are common and the upstream reports them itself.
func validateKimiToolResultPairing(body []byte, info *relaycommon.RelayInfo) error {
	if info == nil || info.RelayMode != relayconstant.RelayModeChatCompletions ||
		info.RelayFormat != types.RelayFormatOpenAI {
		return nil
	}
	if !isKimiK3Model(gjson.GetBytes(body, "model").String(), info.UpstreamModelName) {
		return nil
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return nil
	}

	announced := make(map[string]struct{})
	answered := make(map[string]struct{})
	for index, message := range messages.Array() {
		switch message.Get("role").String() {
		case "assistant":
			for _, call := range message.Get("tool_calls").Array() {
				if id := strings.TrimSpace(call.Get("id").String()); id != "" {
					announced[id] = struct{}{}
				}
			}
		case "tool":
			id := strings.TrimSpace(message.Get("tool_call_id").String())
			if id == "" {
				return fmt.Errorf("messages[%d].tool_call_id is required for role tool", index)
			}
			if _, ok := announced[id]; !ok {
				return fmt.Errorf("messages[%d].tool_call_id %q does not match any preceding assistant tool_calls", index, id)
			}
			if _, ok := answered[id]; ok {
				return fmt.Errorf("messages[%d] submits a second result for tool_call_id %q", index, id)
			}
			answered[id] = struct{}{}
		}
	}
	return nil
}

// isKimiK3Model scopes the pairing check to the model family whose upstream
// answers orphan tool results instead of rejecting them. Older Kimi models keep
// their existing pass-through behavior.
func isKimiK3Model(bodyModel string, upstreamModel string) bool {
	for _, model := range []string{bodyModel, upstreamModel} {
		switch {
		case strings.EqualFold(strings.TrimSpace(model), "kimi-k3"),
			strings.EqualFold(strings.TrimSpace(model), "kimi-k3.1-pro"):
			return true
		}
	}
	return false
}
