package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	ginKeyPromptCacheAnchor    = "prompt_cache_anchor_digest"
	ginKeyPromptCacheTriedKeys = "prompt_cache_tried_keys"
)

// PromptCacheAffinityKey selects the key of a multi-key AnthropicOfficial
// channel for this attempt.
//
// Anthropic keeps prompt caches per workspace, and the keys of one pool usually
// belong to different workspaces or organizations. A random or polling pick
// makes every key write the same prefix again before it can read it. Requests
// that share a prompt head instead always go to the key that ranks first for
// that head, so a cached prefix is read back by the key that wrote it. A retry
// skips keys this request already tried. When all of them failed, or the
// request has no prompt head, it reports ok=false and the caller keeps its
// ordinary pick.
func PromptCacheAffinityKey(c *gin.Context, channel *model.Channel, modelName string) (string, int, bool) {
	if c == nil {
		return "", 0, false
	}
	// A retry may land on another channel; never log this attempt with the
	// anchor of an earlier one.
	common.SetContextKey(c, constant.ContextKeyPromptCacheAnchor, "")
	if channel == nil || channel.Type != constant.ChannelTypeAnthropicOfficial || !channel.ChannelInfo.IsMultiKey {
		return "", 0, false
	}

	var anchor []byte
	if cached, exists := c.Get(ginKeyPromptCacheAnchor); exists {
		anchor, _ = cached.([]byte)
	} else {
		if storage, err := common.GetBodyStorage(c); err == nil {
			if body, err := storage.Bytes(); err == nil {
				anchor, _ = promptCacheAnchor(body, modelName)
			}
		}
		c.Set(ginKeyPromptCacheAnchor, anchor)
	}
	if len(anchor) == 0 {
		return "", 0, false
	}

	var tried map[string]struct{}
	if value, exists := c.Get(ginKeyPromptCacheTriedKeys); exists {
		tried, _ = value.(map[string]struct{})
	}
	if tried == nil {
		tried = make(map[string]struct{})
		c.Set(ginKeyPromptCacheTriedKeys, tried)
	}
	key, index, ok := channel.GetEnabledKeyByRendezvous(anchor, tried)
	if !ok {
		return "", 0, false
	}
	tried[model.ChannelKeyFingerprint(key)] = struct{}{}
	common.SetContextKey(c, constant.ContextKeyPromptCacheAnchor, hex.EncodeToString(anchor[:4]))
	return key, index, true
}

// promptCacheAnchor identifies the prompt head that requests sharing a cached
// prefix have in common: the model, the tool definitions and the system prompt.
// The system prompt counts only up to its last cache breakpoint, because a
// block after it may change per request (a timestamp, say) without touching the
// cached prefix. Without a system prompt, and unless a tool definition carries
// the breakpoint, the first conversation item stands in for it: every turn of
// one conversation keeps its key while unrelated conversations spread over the
// pool. It reads the Claude Messages, OpenAI Chat, Responses and Gemini request
// shapes, and reports ok=false for a request that has none of these parts.
func promptCacheAnchor(body []byte, modelName string) ([]byte, bool) {
	tools := gjson.GetBytes(body, "tools")
	_, toolsCached := rawThroughCacheBreakpoint(tools, false)

	var system gjson.Result
	for _, path := range []string{"system", "instructions", "systemInstruction", "system_instruction"} {
		if system = gjson.GetBytes(body, path); system.Exists() {
			break
		}
	}
	if !system.Exists() {
		// OpenAI Chat carries the system prompt as its leading message.
		leading := gjson.GetBytes(body, "messages.0")
		if role := leading.Get("role").String(); role == "system" || role == "developer" {
			system = leading.Get("content")
		}
	}
	head := ""
	if system.Type != gjson.Null && system.String() != "" && system.Raw != "[]" {
		head = system.Raw
		if cached, found := rawThroughCacheBreakpoint(system, true); found {
			head = cached
		}
	}

	first := ""
	if head == "" && !toolsCached {
		for _, path := range []string{"messages.0", "input.0", "contents.0"} {
			item := gjson.GetBytes(body, path)
			if !item.Exists() {
				continue
			}
			first = item.Raw
			if cached, found := rawThroughCacheBreakpoint(item.Get("content"), false); found {
				first = item.Get("role").Raw + cached
			}
			break
		}
		if first == "" {
			if input := gjson.GetBytes(body, "input"); input.Type == gjson.String {
				first = input.Raw
			}
		}
	}

	if !tools.Exists() && head == "" && first == "" {
		return nil, false
	}
	hasher := sha256.New()
	for _, part := range []string{modelName, tools.Raw, head, first} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		hasher.Write(size[:])
		hasher.Write([]byte(part))
	}
	return hasher.Sum(nil), true
}

// rawThroughCacheBreakpoint joins the raw JSON of the blocks of an array up to
// the first block carrying cache_control, or with last set up to the final one.
// found is false when blocks is not an array or no block carries one.
func rawThroughCacheBreakpoint(blocks gjson.Result, last bool) (raw string, found bool) {
	if !blocks.IsArray() {
		return "", false
	}
	items := blocks.Array()
	end := -1
	for i, item := range items {
		if !item.Get("cache_control").Exists() {
			continue
		}
		end = i
		if !last {
			break
		}
	}
	if end < 0 {
		return "", false
	}
	var builder strings.Builder
	for i, item := range items[:end+1] {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(item.Raw)
	}
	return builder.String(), true
}
