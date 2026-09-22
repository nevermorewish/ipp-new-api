package openaigpt

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPTTemperatureSettingDefaultsOff(t *testing.T) {
	for _, raw := range []string{`{}`, `{"remove_gpt_temperature":false}`, `{"remove_gpt_temperature":true}`} {
		settings := dto.ChannelOtherSettings{}
		require.NoError(t, common.UnmarshalJsonStr(raw, &settings))
		wantRemoved := raw == `{"remove_gpt_temperature":true}`
		for _, model := range []string{"gpt-5.6-terra", "gpt-6-astra"} {
			body := []byte(fmt.Sprintf(`{"model":%q,"input":"hello","temperature":0}`, model))
			prepared, request, changed, err := PrepareResponsesBody(body, "", settings)
			require.NoError(t, err)
			require.Equal(t, wantRemoved, changed)
			require.Equal(t, !wantRemoved, gjson.GetBytes(prepared, "temperature").Exists())
			if wantRemoved {
				require.Nil(t, request.Temperature)
			} else {
				require.Equal(t, body, prepared)
				require.NotNil(t, request.Temperature)
				require.Zero(t, *request.Temperature)
			}
		}
	}
}

func TestNormalizeUnsupportedTemperaturePreservesOtherFields(t *testing.T) {
	for _, model := range []string{"gpt-5.6-terra", "gpt-5-mini", "gpt-6", "gpt-6-astra", "gpt-6.1"} {
		t.Run(model, func(t *testing.T) {
			// Raw messages prevent unrelated large integers from being rounded
			// while cleaning a pass-through request or parameter override.
			body := []byte(`{"model":"alias","temperature":0,"top_p":0,"logprobs":false,"provider_extension":{"id":9007199254740993}}`)
			prepared, changed, err := NormalizeGPTTemperatureInBody(body, model, true)
			require.NoError(t, err)
			require.True(t, changed)
			require.False(t, gjson.GetBytes(prepared, "temperature").Exists())
			require.Equal(t, "alias", gjson.GetBytes(prepared, "model").String())
			require.Equal(t, "0", gjson.GetBytes(prepared, "top_p").Raw)
			require.Equal(t, "false", gjson.GetBytes(prepared, "logprobs").Raw)
			require.Equal(t, "9007199254740993", gjson.GetBytes(prepared, "provider_extension.id").Raw)
			again, changed, err := NormalizeGPTTemperatureInBody(prepared, model, true)
			require.NoError(t, err)
			require.False(t, changed)
			require.Equal(t, prepared, again)
		})
	}
}

func TestNormalizeUnsupportedTemperatureUsesEffectiveModel(t *testing.T) {
	for _, model := range []string{"gpt-4o", "openrouter-model", "o3custom", "gpt-50", "gpt-60", "o3", "o4-mini"} {
		body := []byte(`{"model":"gpt-5.6-terra","temperature":0}`)
		prepared, changed, err := NormalizeGPTTemperatureInBody(body, model, true)
		require.NoError(t, err)
		require.False(t, changed, model)
		require.Equal(t, body, prepared)
	}
}

func TestPrepareResponsesBodyNormalizesMappedTemperature(t *testing.T) {
	body := []byte(`{"model":"alias","input":"hello","temperature":0}`)
	prepared, request, changed, err := PrepareResponsesBody(body, "gpt-5.6-terra", dto.ChannelOtherSettings{RemoveGPTTemperature: true})
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, request.Temperature)
	require.Equal(t, "gpt-5.6-terra", request.Model)
	require.Equal(t, "alias", gjson.GetBytes(prepared, "model").String())
	require.False(t, gjson.GetBytes(prepared, "temperature").Exists())
}

func TestNormalizeAzureGPTSamplingParametersRemovesReasoningOnlyFields(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","top_p":0.2,"logprobs":true,"top_logprobs":2,"provider_extension":{"keep":true}}`)
	got, changed, err := NormalizeAzureGPTSamplingParametersInBody(body, "", true)
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(got, "top_p").Exists())
	require.False(t, gjson.GetBytes(got, "logprobs").Exists())
	require.False(t, gjson.GetBytes(got, "top_logprobs").Exists())
	require.True(t, gjson.GetBytes(got, "provider_extension.keep").Bool())

	untouched, changed, err := NormalizeAzureGPTSamplingParametersInBody(body, "", false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, untouched)
}

func TestNormalizeAzureGPTSamplingParametersLeavesGPT4ModelsUntouched(t *testing.T) {
	body := []byte(`{"model":"gpt-4.1","top_p":0.2,"logprobs":true,"top_logprobs":2}`)
	got, changed, err := NormalizeAzureGPTSamplingParametersInBody(body, "", true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, got)
}
