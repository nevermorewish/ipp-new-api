package relay

import (
	"bytes"
	"fmt"
	"github.com/QuantumNous/new-api/relay/channel/openaigpt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

func TextHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	textReq, ok := info.Request.(*dto.GeneralOpenAIRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.GeneralOpenAIRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(textReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to GeneralOpenAIRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if request.WebSearchOptions != nil {
		c.Set("chat_completion_web_search_context_size", request.WebSearchOptions.SearchContextSize)
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	if err = helper.ApplyReasoningModelSuffix(info, request); err != nil {
		return newConvertRequestFailedError(c, info, err)
	}

	includeUsage := true
	// 判断用户是否需要返回使用情况
	if request.StreamOptions != nil {
		includeUsage = request.StreamOptions.IncludeUsage
	}

	// 如果不支持StreamOptions，将StreamOptions设置为nil
	if !info.SupportStreamOptions || !lo.FromPtrOr(request.Stream, false) {
		request.StreamOptions = nil
	} else {
		// 如果支持StreamOptions，且请求中没有设置StreamOptions，根据配置文件设置StreamOptions
		if constant.ForceStreamOption {
			request.StreamOptions = &dto.StreamOptions{
				IncludeUsage: true,
			}
		}
	}

	info.ShouldIncludeUsage = includeUsage

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	passThroughGlobal := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	if info.RelayMode == relayconstant.RelayModeChatCompletions &&
		!passThroughGlobal &&
		!info.ChannelSetting.PassThroughBodyEnabled &&
		service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName) {
		applySystemPromptIfNeeded(c, info, request)
		usage, newApiErr := textRequestViaResponses(c, info, adaptor, request)
		if newApiErr != nil {
			return newApiErr
		}

		var containAudioTokens = usage.CompletionTokenDetails.AudioTokens > 0 || usage.PromptTokensDetails.AudioTokens > 0
		var containsAudioRatios = ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)

		if containAudioTokens && containsAudioRatios {
			service.PostAudioConsumeQuota(c, info, usage, "")
		} else {
			service.PostTextConsumeQuota(c, info, usage, nil)
		}
		return nil
	}

	var requestBody io.Reader

	if passThroughGlobal || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if common.DebugEnabled {
			if debugBytes, bErr := storage.Bytes(); bErr == nil {
				logger.LogDebug(c, "requestBody: %s", debugBytes)
			}
		}
		requestBody = common.NewReplayableBodyReader(storage)
	} else {
		convertedRequest, err := adaptor.ConvertOpenAIRequest(c, info, request)
		if err != nil {
			return newConvertRequestFailedError(c, info, err)
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

		if info.ChannelSetting.SystemPrompt != "" {
			// 如果有系统提示，则将其添加到请求中
			request, ok := convertedRequest.(*dto.GeneralOpenAIRequest)
			if ok {
				containSystemPrompt := false
				for _, message := range request.Messages {
					if message.Role == request.GetSystemRoleName() {
						containSystemPrompt = true
						break
					}
				}
				if !containSystemPrompt {
					// 如果没有系统提示，则添加系统提示
					systemMessage := dto.Message{
						Role:    request.GetSystemRoleName(),
						Content: info.ChannelSetting.SystemPrompt,
					}
					request.Messages = append([]dto.Message{systemMessage}, request.Messages...)
				} else if info.ChannelSetting.SystemPromptOverride {
					common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
					// 如果有系统提示，且允许覆盖，则拼接到前面
					for i, message := range request.Messages {
						if message.Role == request.GetSystemRoleName() {
							if message.IsStringContent() {
								request.Messages[i].SetStringContent(info.ChannelSetting.SystemPrompt + "\n" + message.StringContent())
							} else {
								contents := message.ParseContent()
								contents = append([]dto.MediaContent{
									{
										Type: dto.ContentTypeText,
										Text: info.ChannelSetting.SystemPrompt,
									},
								}, contents...)
								request.Messages[i].Content = contents
							}
							break
						}
					}
				}
			}
		}

		jsonData, err := common.Marshal(convertedRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeJsonMarshalFailed, types.ErrOptionWithSkipRetry())
		}

		// remove disabled fields for OpenAI API
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// apply param override
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
			if err != nil {
				return newAPIErrorFromParamOverride(err)
			}
		}

		logger.LogDebug(c, "text request body: %s", jsonData)

		body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		defer closer.Close()
		jsonData = nil
		requestBody = body
	}

	chatRequestWasLocallyValid := false
	if info.ChannelType == constant.ChannelTypeOpenAIGPT && info.RelayMode == relayconstant.RelayModeChatCompletions {
		// Unknown-field and strict-tool-schema checks read the client's
		// original body: the outbound body is re-marshaled from the DTO on the
		// non-pass-through path, which drops unmodeled fields and the per-tool
		// strict flag before they can be inspected.
		if inboundBody, storageErr := common.GetBodyStorage(c); storageErr == nil {
			if inboundBytes, readErr := inboundBody.Bytes(); readErr == nil {
				if contractErr := openaigpt.ValidateInboundChatBody(inboundBytes); contractErr != nil {
					return newOpenAIGPTContractError(contractErr)
				}
				if contractErr := openaigpt.ValidateChatToolSchemas(inboundBytes); contractErr != nil {
					return newOpenAIGPTContractError(contractErr)
				}
				// A client on the deprecated functions contract expects
				// message.function_call back. Record that here so the response
				// stage can restore the shape the request asked for.
				openaigpt.MarkLegacyFunctionCall(c.Set, inboundBytes)
			}
		}
		bodyBytes, readErr := io.ReadAll(requestBody)
		if readErr != nil {
			return types.NewErrorWithStatusCode(readErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		bodyBytes, imageURLsChanged, normalizeErr := openaigpt.NormalizeChatImageDataURLs(bodyBytes)
		if normalizeErr != nil {
			return newOpenAIGPTContractError(normalizeErr)
		}
		if imageErr := openaigpt.ValidateChatImageURLs(bodyBytes); imageErr != nil {
			return newOpenAIGPTContractError(imageErr)
		}
		if imageURLsChanged {
			logger.LogInfo(c, "normalized bare base64 Chat image URLs to data URLs for OpenAI-GPT compatibility")
		}
		normalizedBody, schemaChanged, normalizeErr := openaigpt.NormalizeNonStrictChatResponseSchema(bodyBytes)
		if normalizeErr != nil {
			return newOpenAIGPTContractError(normalizeErr)
		}
		if schemaChanged {
			logger.LogInfo(c, "closed object schemas in non-strict Chat response_format for upstream compatibility")
			bodyBytes = normalizedBody
		}
		bodyBytes, tokenLimitsChanged, normalizeErr := openaigpt.NormalizeZeroChatTokenLimitsInBody(bodyBytes)
		if normalizeErr != nil {
			return newOpenAIGPTContractError(normalizeErr)
		}
		if tokenLimitsChanged {
			logger.LogInfo(c, "normalized Chat token limits below 1 to max_completion_tokens=1 for OpenAI-GPT compatibility")
		}
		effectiveModel := ""
		if passThroughGlobal || info.ChannelSetting.PassThroughBodyEnabled {
			// Pass-through preserves the caller's raw model while routing uses the
			// mapped model held by the parsed request.
			effectiveModel = request.Model
		}
		bodyBytes, temperatureChanged, normalizeErr := openaigpt.NormalizeGPTTemperatureInBody(bodyBytes, effectiveModel, info.ChannelOtherSettings.RemoveGPTTemperature || info.ChannelOtherSettings.RemoveAzureGPTEncryption)
		if normalizeErr != nil {
			return newOpenAIGPTContractError(normalizeErr)
		}
		if temperatureChanged {
			logger.LogInfo(c, "removed GPT temperature from final Chat body by channel setting")
		}
		bodyBytes, encryptionChanged, normalizeErr := openaigpt.NormalizeAzureGPTChatEncryption(bodyBytes, info.ChannelOtherSettings.RemoveAzureGPTEncryption)
		if normalizeErr != nil {
			return newOpenAIGPTContractError(normalizeErr)
		}
		if encryptionChanged {
			logger.LogInfo(c, "applied Azure GPT compatibility to final Chat body by channel setting")
		}
		// A body the DTO cannot parse is passed through untouched and reports
		// validated=false, so an opaque upstream 400 for it is not mistaken for
		// a provider capability gap.
		_, validated, contractErr := openaigpt.ValidateChatBody(bodyBytes, effectiveModel)
		if contractErr != nil {
			return newOpenAIGPTContractError(contractErr)
		}
		chatRequestWasLocallyValid = validated
		requestBody = bytes.NewReader(bodyBytes)
	}

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	if resp != nil {
		httpResp = resp.(*http.Response)
		info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
		if httpResp.StatusCode != http.StatusOK {
			newApiErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			if info.ChannelType == constant.ChannelTypeOpenAIGPT &&
				info.RelayMode == relayconstant.RelayModeChatCompletions &&
				openaigpt.ClassifyChatError(newApiErr, chatRequestWasLocallyValid) {
				logger.LogWarn(c, "channel does not support a requested Chat Completions capability; retrying another channel")
			}
			// reset status code 重置状态码
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return newApiErr
		}
	}

	usage, newApiErr := adaptor.DoResponse(c, httpResp, info)
	if newApiErr != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return newApiErr
	}

	var containAudioTokens = usage.(*dto.Usage).CompletionTokenDetails.AudioTokens > 0 || usage.(*dto.Usage).PromptTokensDetails.AudioTokens > 0
	var containsAudioRatios = ratio_setting.ContainsAudioRatio(info.OriginModelName) || ratio_setting.ContainsAudioCompletionRatio(info.OriginModelName)

	if containAudioTokens && containsAudioRatios {
		service.PostAudioConsumeQuota(c, info, usage.(*dto.Usage), "")
	} else {
		service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), nil)
	}
	return nil
}
