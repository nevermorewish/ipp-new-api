package moonshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	channelconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// doRequestWithDynamicToolFallback preserves native dynamic declarations unless
// an upstream explicitly rejects required because it only counts global tools.
// Every Moonshot chat channel is covered, not only KVV ones: the upstream error
// is the trigger, so a channel whose upstream accepts dynamic tools never pays
// for the retry path.
func (a *Adaptor) doRequestWithDynamicToolFallback(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (*http.Response, error) {
	if info == nil || info.ChannelMeta == nil || info.ChannelType != channelconstant.ChannelTypeMoonshot ||
		info.RelayMode != relayconstant.RelayModeChatCompletions ||
		info.RelayFormat != types.RelayFormatOpenAI || body == nil {
		return channel.DoApiRequest(a, c, info, body)
	}
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read Moonshot dynamic tools: %w", err)
	}
	fallback, eligible := promoteRequiredDynamicTools(payload)
	resp, err := channel.DoApiRequest(a, c, info, bytes.NewReader(payload))
	if err != nil || !eligible || resp == nil || resp.StatusCode != http.StatusBadRequest || resp.Body == nil {
		return resp, err
	}
	// Bound inspection and restore every byte when this is not the known error.
	const limit = 64 * 1024
	rejection, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	original := resp.Body
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(rejection), original), original}
	if readErr != nil || len(rejection) > limit {
		return resp, nil
	}
	message := gjson.GetBytes(rejection, "error.message").String()
	if message != "tool_choice 'required' requires at least one tool" && message != `tool_choice "required" requires at least one tool` {
		return resp, nil
	}
	if c.Request.Context().Err() != nil {
		return resp, nil
	}
	_ = resp.Body.Close()
	return channel.DoApiRequest(a, c, info, bytes.NewReader(fallback))
}

// promoteRequiredDynamicTools keeps raw tool definitions and required semantics.
// Declarations that the gateway can prove are invalid, or that would change the
// meaning of the conversation, must retain the original upstream error. A
// declaration on a non-system message, or one that shares its message with
// instructions, is still a valid tool declaration for an upstream that only
// counts global tools, so it is promoted and its text is preserved.
func promoteRequiredDynamicTools(payload []byte) ([]byte, bool) {
	if gjson.GetBytes(payload, "tool_choice").Type != gjson.String || gjson.GetBytes(payload, "tool_choice").String() != "required" {
		return nil, false
	}
	var request map[string]json.RawMessage
	if common.Unmarshal(payload, &request) != nil {
		return nil, false
	}
	var tools []json.RawMessage
	if raw := request["tools"]; len(raw) > 0 && common.Unmarshal(raw, &tools) != nil {
		return nil, false
	}
	names := make(map[string]bool)
	for _, tool := range tools {
		name := gjson.GetBytes(tool, "function.name").String()
		if name == "" || names[name] {
			return nil, false
		}
		names[name] = true
	}
	var messages []map[string]json.RawMessage
	if common.Unmarshal(request["messages"], &messages) != nil {
		return nil, false
	}
	kept := make([]map[string]json.RawMessage, 0, len(messages))
	moved := false
	for _, message := range messages {
		raw, exists := message["tools"]
		if !exists {
			kept = append(kept, message)
			continue
		}
		var dynamic []json.RawMessage
		if common.Unmarshal(raw, &dynamic) != nil || dynamic == nil {
			return nil, false
		}
		for _, tool := range dynamic {
			name := gjson.GetBytes(tool, "function.name").String()
			if gjson.GetBytes(tool, "type").String() != "function" || !kvvToolName.MatchString(name) || names[name] {
				return nil, false
			}
			names[name] = true
		}
		if len(dynamic) == 0 {
			kept = append(kept, message)
			continue
		}
		tools = append(tools, dynamic...)
		moved = true
		delete(message, "tools")
		// A declaration message usually carries no text, so drop the empty
		// content it leaves behind. Instructions in the same message survive.
		content := bytes.TrimSpace(message["content"])
		if len(content) == 0 || string(content) == `""` || string(content) == "null" {
			delete(message, "content")
		}
		// Drop only an empty declaration; retain any metadata on that message.
		if len(message) > 1 {
			kept = append(kept, message)
		}
	}
	if !moved || len(kept) == 0 {
		return nil, false
	}
	var err error
	request["tools"], err = common.Marshal(tools)
	if err != nil {
		return nil, false
	}
	request["messages"], err = common.Marshal(kept)
	if err != nil {
		return nil, false
	}
	result, err := common.Marshal(request)
	return result, err == nil
}
