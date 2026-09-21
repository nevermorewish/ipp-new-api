package openaigpt

import (
	"testing"

	appconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

// relayInfoForGPT56 builds the minimum RelayInfo the shared OpenAI conversion
// reads: the channel type it branches on and the upstream model name that
// triggers the gpt-5 parameter stripping.
func relayInfoForGPT56() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.6-sol",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       appconstant.ChannelTypeOpenAIGPT,
			UpstreamModelName: "gpt-5.6-sol",
		},
	}
}

// TestConvertOpenAIRequestPreservesSamplingParameters covers feedback item 8,
// and the Chat half of items 2 and 3.
//
// openai.Adaptor deletes temperature, top_p and logprobs for every gpt-5*
// model. Delegating to it unchanged meant a client that asked for logprobs got
// HTTP 200 with logprobs:null and no indication the parameter never left the
// gateway. This channel forwards them and lets the upstream answer.
func TestConvertOpenAIRequestPreservesSamplingParameters(t *testing.T) {
	adaptor := &Adaptor{}
	info := relayInfoForGPT56()
	adaptor.Init(info)

	request := &dto.GeneralOpenAIRequest{
		Model:       "gpt-5.6-sol",
		Temperature: lo.ToPtr(0.2),
		TopP:        lo.ToPtr(0.9),
		LogProbs:    lo.ToPtr(true),
		TopLogProbs: lo.ToPtr(2),
	}

	converted, err := adaptor.ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)

	result, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, result.Temperature)
	require.InDelta(t, 0.2, *result.Temperature, 1e-9)
	require.NotNil(t, result.TopP)
	require.InDelta(t, 0.9, *result.TopP, 1e-9)
	require.NotNil(t, result.LogProbs)
	require.True(t, *result.LogProbs)
	require.NotNil(t, result.TopLogProbs)
	require.Equal(t, 2, *result.TopLogProbs)
}

// A request that never set them must not gain them.
func TestConvertOpenAIRequestLeavesUnsetSamplingParametersUnset(t *testing.T) {
	adaptor := &Adaptor{}
	info := relayInfoForGPT56()
	adaptor.Init(info)

	converted, err := adaptor.ConvertOpenAIRequest(nil, info, &dto.GeneralOpenAIRequest{Model: "gpt-5.6-sol"})
	require.NoError(t, err)

	result, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Nil(t, result.Temperature)
	require.Nil(t, result.TopP)
	require.Nil(t, result.LogProbs)
}
