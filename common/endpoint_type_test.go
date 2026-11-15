package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedanceExposesOpenAIVideoEndpoint(t *testing.T) {
	endpointTypes := GetEndpointTypesByChannelType(constant.ChannelTypeSeedance, "doubao-seedance-2.0")

	assert.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIVideo}, endpointTypes)
}

// AnthropicOfficial must reach the Anthropic adaptor and both of its endpoints;
// an unmapped type would silently fall back to the OpenAI adaptor.
func TestAnthropicOfficialUsesTheAnthropicAdaptor(t *testing.T) {
	apiType, ok := ChannelType2APIType(constant.ChannelTypeAnthropicOfficial)
	require.True(t, ok)
	assert.Equal(t, constant.APITypeAnthropic, apiType)
	assert.Equal(t,
		[]constant.EndpointType{constant.EndpointTypeAnthropic, constant.EndpointTypeOpenAI},
		GetEndpointTypesByChannelType(constant.ChannelTypeAnthropicOfficial, "claude-haiku-5-5"))
	assert.Equal(t, "AnthropicOfficial", constant.GetChannelTypeName(constant.ChannelTypeAnthropicOfficial))
	assert.Equal(t, "https://api.anthropic.com", constant.GetChannelBaseURL(constant.ChannelTypeAnthropicOfficial))
}
