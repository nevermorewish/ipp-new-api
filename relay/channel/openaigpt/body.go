package openaigpt

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// PrepareResponsesBody normalizes and validates the final JSON body that will
// be sent upstream. Working on bytes keeps pass-through fields intact and also
// covers changes made by model mapping and parameter overrides.
func PrepareResponsesBody(body []byte, effectiveModel string, settings dto.ChannelOtherSettings) ([]byte, *dto.OpenAIResponsesRequest, bool, error) {
	var request dto.OpenAIResponsesRequest
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, nil, false, fmt.Errorf("invalid Responses request body: %w", err)
	}
	model := request.Model
	if effectiveModel != "" {
		model = effectiveModel
	}

	// Legacy field aliases and stale replay identifiers are resolved first so
	// the tool/schema/token normalizers below and ValidateResponsesRequest see
	// the canonical shape a client's still-valid intent should have taken.
	normalized, changed, err := NormalizeLegacyResponsesAliasesInBody(body)
	if err != nil {
		return nil, nil, false, err
	}
	normalized, idChanged, err := NormalizeInvalidMessageItemIDsInBody(normalized)
	if err != nil {
		return nil, nil, false, err
	}
	changed = changed || idChanged
	normalized, compatibilityChanged, err := NormalizeResponsesCompatibility(normalized, settings.RemoveAzureGPTEncryption)
	if err != nil {
		return nil, nil, false, err
	}
	changed = changed || compatibilityChanged
	normalized, samplingChanged, err := NormalizeGPTTemperatureInBody(normalized, model, settings.RemoveGPTTemperature || settings.RemoveAzureGPTEncryption)
	if err != nil {
		return nil, nil, false, err
	}
	changed = changed || samplingChanged
	normalized, azureSamplingChanged, err := NormalizeAzureGPTSamplingParametersInBody(normalized, model, settings.RemoveAzureGPTEncryption)
	if err != nil {
		return nil, nil, false, err
	}
	changed = changed || azureSamplingChanged
	normalized, toolsChanged, err := NormalizeGPT56ToolsInBody(normalized, model)
	if err != nil {
		return nil, nil, false, err
	}
	changed = changed || toolsChanged
	normalized, schemaChanged, err := NormalizeNonStrictResponsesTextSchema(normalized)
	if err != nil {
		return nil, nil, false, err
	}
	changed = changed || schemaChanged
	normalized, tokenChanged, err := NormalizeSmallMaxOutputTokensInBody(normalized)
	if err != nil {
		return nil, nil, false, err
	}
	changed = changed || tokenChanged
	if changed {
		// Unmarshal into a fresh value. encoding/json leaves fields untouched
		// when a key is absent, so unmarshaling into the original request would
		// retain parameters removed by a normalizer and make validation inspect
		// a different request than the bytes sent upstream.
		var normalizedRequest dto.OpenAIResponsesRequest
		if err := common.Unmarshal(normalized, &normalizedRequest); err != nil {
			return nil, nil, false, fmt.Errorf("invalid normalized Responses request body: %w", err)
		}
		request = normalizedRequest
	}
	request.Model = model
	if err := ValidateResponsesRequest(&request); err != nil {
		return nil, nil, false, err
	}
	return normalized, &request, changed, nil
}

// ValidateChatBody validates the final Chat Completions JSON while leaving the
// original bytes untouched. This is important for pass-through channels, where
// the DTO intentionally does not own every possible request field.
//
// A body the DTO cannot parse is deliberately NOT an error. Pass-through exists
// precisely so requests the gateway does not fully model still reach upstream:
// dto.GeneralOpenAIRequest types max_tokens as uint and stream as bool, so a
// perfectly serviceable `"max_tokens": 100.0` or `"stream": "true"` fails to
// unmarshal here while many providers accept it. Rejecting those locally would
// break traffic that used to succeed. Only requests the gateway can actually
// read are held to the contract; anything else is upstream's call.
//
// The returned request is nil when the body could not be parsed. ok reports
// whether contract validation actually ran, which callers use as the
// requestWasLocallyValid input to the capability-error classifier.
func ValidateChatBody(body []byte, effectiveModel string) (*dto.GeneralOpenAIRequest, bool, error) {
	var request dto.GeneralOpenAIRequest
	if err := common.Unmarshal(body, &request); err != nil {
		return nil, false, nil
	}
	if effectiveModel != "" {
		request.Model = effectiveModel
	}
	if err := ValidateChatRequest(&request); err != nil {
		return nil, false, err
	}
	return &request, true, nil
}
