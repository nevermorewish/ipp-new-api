package claude

import (
	"testing"

	"github.com/MoeForge/moego/ojson"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPatchClaudeMessageDeltaUsageDataPreserveUnknownFields(t *testing.T) {
	originalData := `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":53},"vendor_meta":{"trace_id":"trace_001"}}`
	usage := &dto.ClaudeUsage{
		InputTokens:              100,
		CacheReadInputTokens:     30,
		CacheCreationInputTokens: 50,
	}

	patchedData := patchClaudeMessageDeltaUsageData(originalData, usage)

	require.Equal(t, "message_delta", ojson.Get(patchedData, "type").String())
	require.Equal(t, "end_turn", ojson.Get(patchedData, "delta.stop_reason").String())
	require.Equal(t, "trace_001", ojson.Get(patchedData, "vendor_meta.trace_id").String())
	require.EqualValues(t, 53, ojson.Get(patchedData, "usage.output_tokens").Int())
	require.EqualValues(t, 100, ojson.Get(patchedData, "usage.input_tokens").Int())
	require.EqualValues(t, 30, ojson.Get(patchedData, "usage.cache_read_input_tokens").Int())
	require.EqualValues(t, 50, ojson.Get(patchedData, "usage.cache_creation_input_tokens").Int())
}

func TestPatchClaudeMessageDeltaUsageDataZeroValueChecks(t *testing.T) {
	originalData := `{"type":"message_delta","usage":{"output_tokens":53,"input_tokens":9,"cache_read_input_tokens":0}}`
	usage := &dto.ClaudeUsage{
		InputTokens:              100,
		CacheReadInputTokens:     30,
		CacheCreationInputTokens: 0,
	}

	patchedData := patchClaudeMessageDeltaUsageData(originalData, usage)

	require.EqualValues(t, 9, ojson.Get(patchedData, "usage.input_tokens").Int())
	require.EqualValues(t, 30, ojson.Get(patchedData, "usage.cache_read_input_tokens").Int())
	assert.False(t, ojson.Get(patchedData, "usage.cache_creation_input_tokens").Exists())
}

func TestPatchClaudeMessageDeltaUsageDataKeepsTTLSplitPair(t *testing.T) {
	// A stream that reports the 5m lifetime must also report the 1h lifetime,
	// including an explicit zero: non-stream responses carry both keys, and a
	// client diffing the two shapes must not see the key disappear.
	originalData := `{"type":"message_delta","usage":{"output_tokens":80,"cache_creation":{"ephemeral_5m_input_tokens":5632}}}`
	usage := &dto.ClaudeUsage{
		OutputTokens:             80,
		CacheCreationInputTokens: 5632,
		CacheCreation:            &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 5632},
	}

	patchedData := patchClaudeMessageDeltaUsageData(originalData, usage)

	require.EqualValues(t, 5632, ojson.Get(patchedData, "usage.cache_creation.ephemeral_5m_input_tokens").Int())
	require.True(t, ojson.Get(patchedData, "usage.cache_creation.ephemeral_1h_input_tokens").Exists())
	require.EqualValues(t, 0, ojson.Get(patchedData, "usage.cache_creation.ephemeral_1h_input_tokens").Int())
}

func TestPatchClaudeMessageDeltaUsageDataLeavesAbsentCacheCreationAlone(t *testing.T) {
	originalData := `{"type":"message_delta","usage":{"output_tokens":53}}`
	usage := &dto.ClaudeUsage{OutputTokens: 53}

	patchedData := patchClaudeMessageDeltaUsageData(originalData, usage)

	assert.False(t, ojson.Get(patchedData, "usage.cache_creation").Exists())
}

func TestShouldSkipClaudeMessageDeltaUsagePatch(t *testing.T) {
	originGlobalPassThrough := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	t.Cleanup(func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = originGlobalPassThrough
	})

	model_setting.GetGlobalSettings().PassThroughRequestEnabled = true
	assert.True(t, shouldSkipClaudeMessageDeltaUsagePatch(&relaycommon.RelayInfo{}))

	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	assert.True(t, shouldSkipClaudeMessageDeltaUsagePatch(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: true}},
	}))
	assert.False(t, shouldSkipClaudeMessageDeltaUsagePatch(&relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{PassThroughBodyEnabled: false}},
	}))
}

func TestBuildMessageDeltaPatchUsage(t *testing.T) {
	t.Run("merge missing fields from claudeInfo", func(t *testing.T) {
		claudeResponse := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{OutputTokens: 53}}
		claudeInfo := &ClaudeResponseInfo{
			Usage: &dto.Usage{
				PromptTokens: 100,
				PromptTokensDetails: dto.InputTokenDetails{
					CachedTokens:         30,
					CachedCreationTokens: 50,
				},
				ClaudeCacheCreation5mTokens: 10,
				ClaudeCacheCreation1hTokens: 20,
			},
		}

		usage := buildMessageDeltaPatchUsage(claudeResponse, claudeInfo)
		require.NotNil(t, usage)
		require.EqualValues(t, 100, usage.InputTokens)
		require.EqualValues(t, 30, usage.CacheReadInputTokens)
		require.EqualValues(t, 50, usage.CacheCreationInputTokens)
		require.EqualValues(t, 53, usage.OutputTokens)
		require.NotNil(t, usage.CacheCreation)
		require.EqualValues(t, 30, usage.CacheCreation.Ephemeral5mInputTokens)
		require.EqualValues(t, 20, usage.CacheCreation.Ephemeral1hInputTokens)
	})

	t.Run("keep upstream non-zero values", func(t *testing.T) {
		claudeResponse := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{
			InputTokens:              9,
			CacheReadInputTokens:     7,
			CacheCreationInputTokens: 6,
		}}
		claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{
			PromptTokens: 100,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:         30,
				CachedCreationTokens: 50,
			},
		}}

		usage := buildMessageDeltaPatchUsage(claudeResponse, claudeInfo)
		require.EqualValues(t, 9, usage.InputTokens)
		require.EqualValues(t, 7, usage.CacheReadInputTokens)
		require.EqualValues(t, 6, usage.CacheCreationInputTokens)
	})

	t.Run("default aggregate cache creation to 5m when split missing", func(t *testing.T) {
		claudeResponse := &dto.ClaudeResponse{Usage: &dto.ClaudeUsage{
			OutputTokens:             53,
			CacheCreationInputTokens: 50,
		}}
		claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{
			PromptTokensDetails: dto.InputTokenDetails{
				CachedCreationTokens: 50,
			},
		}}

		usage := buildMessageDeltaPatchUsage(claudeResponse, claudeInfo)
		require.NotNil(t, usage)
		require.NotNil(t, usage.CacheCreation)
		require.EqualValues(t, 50, usage.CacheCreation.Ephemeral5mInputTokens)
		require.EqualValues(t, 0, usage.CacheCreation.Ephemeral1hInputTokens)
	})
}
