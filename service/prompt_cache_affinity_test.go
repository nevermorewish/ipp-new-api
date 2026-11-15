package service

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func anthropicOfficialPool(id int, keys ...string) *model.Channel {
	return &model.Channel{
		Id:   id,
		Type: constant.ChannelTypeAnthropicOfficial,
		Key:  strings.Join(keys, "\n"),
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: len(keys),
			MultiKeyMode: constant.MultiKeyModeRandom,
		},
	}
}

func promptCacheRequest(t *testing.T, body map[string]any) *gin.Context {
	t.Helper()
	data, err := common.Marshal(body)
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(data))
	c.Set(common.KeyRequestBody, data)
	return c
}

// cacheProbeRequest mirrors the customer cache probe: one cached system block
// that never changes, followed by a user turn that changes on every request.
func cacheProbeRequest(system string, trial int) map[string]any {
	return map[string]any{
		"model":      "claude-haiku-5-5",
		"max_tokens": 32,
		"stream":     true,
		"system": []any{map[string]any{
			"type":          "text",
			"text":          system,
			"cache_control": map[string]any{"type": "ephemeral", "ttl": "5m"},
		}},
		"messages": []any{map[string]any{
			"role":    "user",
			"content": fmt.Sprintf("New trial %03d. Reply only OK.", trial),
		}},
	}
}

func TestPromptCacheAffinityKeepsCacheProbeOnOneKey(t *testing.T) {
	channel := anthropicOfficialPool(910001, "sk-ant-a", "sk-ant-b", "sk-ant-c", "sk-ant-d", "sk-ant-e", "sk-ant-f")
	system := strings.Repeat("Treat the reference below as static data. Answer the user with only OK.\n", 700)

	// One warm-up request and 50 measured requests, each a separate HTTP request.
	firstKey, firstIndex, firstAnchor := "", -1, ""
	for trial := range 51 {
		c := promptCacheRequest(t, cacheProbeRequest(system, trial))
		key, index, ok := PromptCacheAffinityKey(c, channel, "claude-haiku-5-5")
		require.True(t, ok, "trial %d", trial)
		anchor := common.GetContextKeyString(c, constant.ContextKeyPromptCacheAnchor)
		require.NotEmpty(t, anchor)
		if trial == 0 {
			firstKey, firstIndex, firstAnchor = key, index, anchor
			continue
		}
		assert.Equal(t, firstKey, key, "trial %d left the key that holds the cached system prompt", trial)
		assert.Equal(t, firstIndex, index, "trial %d", trial)
		assert.Equal(t, firstAnchor, anchor, "trial %d", trial)
	}
	assert.Equal(t, channel.GetKeys()[firstIndex], firstKey)
}

func TestPromptCacheAnchorGroupsRequestsThatShareACachedPrefix(t *testing.T) {
	text := func(value string) map[string]any { return map[string]any{"type": "text", "text": value} }
	cached := func(value string) map[string]any {
		block := text(value)
		block["cache_control"] = map[string]any{"type": "ephemeral"}
		return block
	}
	user := func(content any) map[string]any { return map[string]any{"role": "user", "content": content} }
	assistant := func(content any) map[string]any { return map[string]any{"role": "assistant", "content": content} }
	tool := func(name string) map[string]any {
		return map[string]any{"name": name, "input_schema": map[string]any{"type": "object"}}
	}
	cachedTool := func(name string) map[string]any {
		definition := tool(name)
		definition["cache_control"] = map[string]any{"type": "ephemeral"}
		return definition
	}

	tests := []struct {
		name  string
		left  map[string]any
		right map[string]any
		same  bool
	}{
		{
			name:  "a new user turn after the cached system prompt keeps the anchor",
			left:  cacheProbeRequest("static reference", 1),
			right: cacheProbeRequest("static reference", 2),
			same:  true,
		},
		{
			name: "a system block after the last breakpoint may change per request",
			left: map[string]any{"system": []any{cached("static reference"), text("Current time: 10:00")},
				"messages": []any{user("hi")}},
			right: map[string]any{"system": []any{cached("static reference"), text("Current time: 10:05")},
				"messages": []any{user("hi")}},
			same: true,
		},
		{
			name:  "a different system prompt moves the anchor",
			left:  cacheProbeRequest("static reference", 1),
			right: cacheProbeRequest("other reference", 1),
		},
		{
			name:  "different tool definitions move the anchor",
			left:  map[string]any{"tools": []any{tool("search")}, "system": "agent", "messages": []any{user("hi")}},
			right: map[string]any{"tools": []any{tool("fetch")}, "system": "agent", "messages": []any{user("hi")}},
		},
		{
			name:  "later turns of a conversation without a system prompt keep the anchor",
			left:  map[string]any{"messages": []any{user("plan the trip")}},
			right: map[string]any{"messages": []any{user("plan the trip"), assistant("Where to?"), user("Kyoto")}},
			same:  true,
		},
		{
			name:  "unrelated conversations without a system prompt get their own anchor",
			left:  map[string]any{"messages": []any{user("plan the trip")}},
			right: map[string]any{"messages": []any{user("fix the build")}},
		},
		{
			name:  "a question after a cached document in the first turn keeps the anchor",
			left:  map[string]any{"messages": []any{user([]any{cached("long document"), text("question 1")})}},
			right: map[string]any{"messages": []any{user([]any{cached("long document"), text("question 2")})}},
			same:  true,
		},
		{
			name:  "cached tool definitions anchor single-turn requests without a system prompt",
			left:  map[string]any{"tools": []any{tool("search"), cachedTool("fetch")}, "messages": []any{user("question 1")}},
			right: map[string]any{"tools": []any{tool("search"), cachedTool("fetch")}, "messages": []any{user("question 2")}},
			same:  true,
		},
		{
			name: "an OpenAI Chat request keeps the anchor of its leading system message",
			left: map[string]any{"messages": []any{map[string]any{"role": "system", "content": "assistant rules"},
				user("question 1")}},
			right: map[string]any{"messages": []any{map[string]any{"role": "system", "content": "assistant rules"},
				user("question 2")}},
			same: true,
		},
		{
			name:  "a Responses request keeps the anchor of its instructions",
			left:  map[string]any{"instructions": "assistant rules", "input": "question 1"},
			right: map[string]any{"instructions": "assistant rules", "input": "question 2"},
			same:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			left, err := common.Marshal(tc.left)
			require.NoError(t, err)
			right, err := common.Marshal(tc.right)
			require.NoError(t, err)
			leftAnchor, ok := promptCacheAnchor(left, "claude-haiku-5-5")
			require.True(t, ok)
			rightAnchor, ok := promptCacheAnchor(right, "claude-haiku-5-5")
			require.True(t, ok)
			if tc.same {
				assert.Equal(t, leftAnchor, rightAnchor)
			} else {
				assert.NotEqual(t, leftAnchor, rightAnchor)
			}
		})
	}

	body, err := common.Marshal(cacheProbeRequest("static reference", 1))
	require.NoError(t, err)
	haiku, _ := promptCacheAnchor(body, "claude-haiku-5-5")
	sonnet, _ := promptCacheAnchor(body, "claude-sonnet-5-5")
	assert.NotEqual(t, haiku, sonnet, "each model keeps its own prompt cache")
}

func TestPromptCacheAnchorRejectsRequestWithoutPromptHead(t *testing.T) {
	for _, body := range []string{`{"model":"claude-haiku-5-5"}`, `{"system":"","messages":[]}`, `not json`} {
		_, ok := promptCacheAnchor([]byte(body), "claude-haiku-5-5")
		assert.False(t, ok, body)
	}
}

func TestPromptCacheAffinityKeyRetrySkipsKeysAlreadyTried(t *testing.T) {
	channel := anthropicOfficialPool(910002, "sk-ant-a", "sk-ant-b", "sk-ant-c")
	c := promptCacheRequest(t, cacheProbeRequest("static reference", 1))

	seen := make(map[int]struct{})
	for attempt := range 3 {
		_, index, ok := PromptCacheAffinityKey(c, channel, "claude-haiku-5-5")
		require.True(t, ok, "attempt %d", attempt)
		_, repeated := seen[index]
		assert.False(t, repeated, "attempt %d reused key %d that already failed", attempt, index)
		seen[index] = struct{}{}
	}

	_, _, ok := PromptCacheAffinityKey(c, channel, "claude-haiku-5-5")
	assert.False(t, ok, "every key failed, so the ordinary key selection takes over")
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyPromptCacheAnchor))
}

func TestPromptCacheAffinityKeyMovesOnlyPromptsOfDisabledKey(t *testing.T) {
	channel := anthropicOfficialPool(910003, "sk-ant-a", "sk-ant-b", "sk-ant-c", "sk-ant-d", "sk-ant-e")
	pick := func(system string) int {
		c := promptCacheRequest(t, cacheProbeRequest(system, 1))
		_, index, ok := PromptCacheAffinityKey(c, channel, "claude-haiku-5-5")
		require.True(t, ok)
		return index
	}

	before := make(map[string]int)
	used := make(map[int]struct{})
	for i := range 40 {
		system := fmt.Sprintf("tenant %d reference", i)
		before[system] = pick(system)
		used[before[system]] = struct{}{}
	}
	assert.GreaterOrEqual(t, len(used), 3, "distinct prompts spread over the pool")

	disabled := before["tenant 0 reference"]
	channel.ChannelInfo.MultiKeyStatusList = map[int]int{disabled: common.ChannelStatusAutoDisabled}
	for system, index := range before {
		after := pick(system)
		if index == disabled {
			assert.NotEqual(t, disabled, after, system)
			continue
		}
		assert.Equal(t, index, after, "%s moved although its key stayed enabled", system)
	}
}

func TestPromptCacheAffinityKeyLeavesOtherChannelsToOrdinarySelection(t *testing.T) {
	c := promptCacheRequest(t, cacheProbeRequest("static reference", 1))
	_, _, ok := PromptCacheAffinityKey(c, anthropicOfficialPool(910004, "sk-ant-a", "sk-ant-b"), "claude-haiku-5-5")
	require.True(t, ok)
	require.NotEmpty(t, common.GetContextKeyString(c, constant.ContextKeyPromptCacheAnchor))

	anthropic := anthropicOfficialPool(910005, "sk-ant-a", "sk-ant-b")
	anthropic.Type = constant.ChannelTypeAnthropic
	single := &model.Channel{Id: 910006, Type: constant.ChannelTypeAnthropicOfficial, Key: "sk-ant-a"}
	for _, channel := range []*model.Channel{anthropic, single} {
		_, _, ok := PromptCacheAffinityKey(c, channel, "claude-haiku-5-5")
		assert.False(t, ok, "channel type %d", channel.Type)
		assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyPromptCacheAnchor),
			"a retry on another channel must not keep the earlier anchor")
	}
}
