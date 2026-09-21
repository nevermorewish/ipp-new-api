package relay

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	openaichannel "github.com/QuantumNous/new-api/relay/channel/openai"
	openaigptchannel "github.com/QuantumNous/new-api/relay/channel/openaigpt"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func applySystemPromptIfNeeded(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) {
	if info == nil || request == nil {
		return
	}
	if info.ChannelSetting.SystemPrompt == "" {
		return
	}

	systemRole := request.GetSystemRoleName()

	containSystemPrompt := false
	for _, message := range request.Messages {
		if message.Role == systemRole {
			containSystemPrompt = true
			break
		}
	}
	if !containSystemPrompt {
		systemMessage := dto.Message{
			Role:    systemRole,
			Content: info.ChannelSetting.SystemPrompt,
		}
		request.Messages = append([]dto.Message{systemMessage}, request.Messages...)
		return
	}

	if !info.ChannelSetting.SystemPromptOverride {
		return
	}

	common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
	for i, message := range request.Messages {
		if message.Role != systemRole {
			continue
		}
		if message.IsStringContent() {
			request.Messages[i].SetStringContent(info.ChannelSetting.SystemPrompt + "\n" + message.StringContent())
			return
		}
		contents := message.ParseContent()
		contents = append([]dto.MediaContent{
			{
				Type: dto.ContentTypeText,
				Text: info.ChannelSetting.SystemPrompt,
			},
		}, contents...)
		request.Messages[i].Content = contents
		return
	}
}

func textRequestViaResponses(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request any) (*dto.Usage, *types.NewAPIError) {
	paramOverrideApplied := false
	if chatRequest, ok := request.(*dto.GeneralOpenAIRequest); ok {
		chatJSON, err := common.Marshal(chatRequest)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		chatJSON, err = relaycommon.RemoveDisabledFields(chatJSON, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		if len(info.ParamOverride) > 0 {
			chatJSON, err = relaycommon.ApplyParamOverrideWithRelayInfo(chatJSON, info)
			if err != nil {
				return nil, newAPIErrorFromParamOverride(err)
			}
			paramOverrideApplied = true
		}

		if info.ChannelType == constant.ChannelTypeOpenAIGPT {
			chatJSON, _, err = openaigptchannel.NormalizeAzureGPTChatEncryption(chatJSON, info.ChannelOtherSettings.RemoveAzureGPTEncryption)
			if err != nil {
				return nil, newOpenAIGPTContractError(err)
			}
			var changed bool
			chatJSON, changed, err = openaigptchannel.NormalizeChatImageDataURLs(chatJSON)
			if err != nil {
				return nil, newOpenAIGPTContractError(err)
			}
			if imageErr := openaigptchannel.ValidateChatImageURLs(chatJSON); imageErr != nil {
				return nil, newOpenAIGPTContractError(imageErr)
			}
			if changed {
				logger.LogInfo(c, "normalized bare base64 Chat image URLs to data URLs for OpenAI-GPT compatibility")
			}
		}

		var overriddenChatReq dto.GeneralOpenAIRequest
		if err := common.Unmarshal(chatJSON, &overriddenChatReq); err != nil {
			return nil, types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid, types.ErrOptionWithSkipRetry())
		}
		if info.ChannelType == constant.ChannelTypeOpenAIGPT {
			// This path returns before the Chat contract gate in TextHelper, so the
			// same checks run here. Unknown fields and the per-tool strict flag are
			// read from the client's original body, which chatJSON no longer
			// carries after the DTO round trip above.
			if inboundBody, storageErr := common.GetBodyStorage(c); storageErr == nil {
				if inboundBytes, readErr := inboundBody.Bytes(); readErr == nil {
					if err := openaigptchannel.ValidateInboundChatBody(inboundBytes); err != nil {
						return nil, newOpenAIGPTContractError(err)
					}
					if err := openaigptchannel.ValidateChatToolSchemas(inboundBytes); err != nil {
						return nil, newOpenAIGPTContractError(err)
					}
					openaigptchannel.MarkLegacyFunctionCall(c.Set, inboundBytes)
				}
			}
			if err := openaigptchannel.ValidateChatRequest(&overriddenChatReq); err != nil {
				return nil, newOpenAIGPTContractError(err)
			}
		}
		request = &overriddenChatReq
	}

	result, err := service.ConvertRequest(c, info, types.RelayFormatOpenAIResponses, request)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	responsesReq, ok := result.Value.(*dto.OpenAIResponsesRequest)
	if !ok {
		return nil, types.NewError(fmt.Errorf("expected OpenAI responses request, got %T", result.Value), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if info.ChannelType == constant.ChannelTypeOpenAIGPT {
		if chat, ok := request.(*dto.GeneralOpenAIRequest); ok {
			openaigptchannel.NormalizeConvertedChatRequest(chat, responsesReq)
		}
	}
	return relayResponsesRequest(c, info, adaptor, responsesReq, paramOverrideApplied)
}

func relayResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, responsesReq *dto.OpenAIResponsesRequest, paramOverrideApplied bool) (*dto.Usage, *types.NewAPIError) {
	savedRelayMode := info.RelayMode
	savedRequestURLPath := info.RequestURLPath
	defer func() {
		info.RelayMode = savedRelayMode
		info.RequestURLPath = savedRequestURLPath
	}()

	info.RelayMode = relayconstant.RelayModeResponses
	info.RequestURLPath = "/v1/responses"

	convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *responsesReq)
	if err != nil {
		return nil, newConvertRequestFailedError(c, info, err)
	}
	relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if !paramOverrideApplied && len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return nil, newAPIErrorFromParamOverride(err)
		}
	}

	validatedRequest := responsesReq
	requestWasLocallyValid := false
	if info.ChannelType == constant.ChannelTypeOpenAIGPT {
		preparedBody, finalRequest, changed, contractErr := openaigptchannel.PrepareResponsesBody(jsonData, "", info.ChannelOtherSettings)
		if contractErr != nil {
			return nil, newOpenAIGPTContractError(contractErr)
		}
		if changed {
			logger.LogInfo(c, "normalized OpenAI-GPT Responses parameters in converted Chat request")
		}
		jsonData = preparedBody
		validatedRequest = finalRequest
		requestWasLocallyValid = true
	}

	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()
	jsonData = nil
	var requestBody io.Reader = body

	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	if resp == nil {
		return nil, types.NewOpenAIError(nil, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	httpResp = resp.(*http.Response)
	clientStream := info.IsStream
	upstreamStream := isResponsesEventStreamContentType(httpResp.Header.Get("Content-Type"))
	info.IsStream = clientStream || upstreamStream
	if httpResp.StatusCode != http.StatusOK {
		newApiErr := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		if info.ChannelType == constant.ChannelTypeOpenAIGPT {
			if openaigptchannel.ClassifyResponsesError(newApiErr, requestWasLocallyValid) ||
				openaigptchannel.ClassifyResponsesNamespaceError(newApiErr, validatedRequest) ||
				openaigptchannel.ClassifyImageDataError(newApiErr, validatedRequest) {
				logger.LogWarn(c, "channel does not support a requested Responses capability; retrying another channel")
			}
		}
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return nil, newApiErr
	}

	if upstreamStream && clientStream {
		usage, newApiErr := openaichannel.OaiResponsesToChatStreamHandler(c, info, httpResp)
		if newApiErr != nil {
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return nil, newApiErr
		}
		return usage, nil
	}
	if upstreamStream {
		info.IsStream = false
		usage, newApiErr := openaichannel.OaiResponsesToChatBufferedStreamHandler(c, info, httpResp)
		if newApiErr != nil {
			service.ResetStatusCode(newApiErr, statusCodeMappingStr)
			return nil, newApiErr
		}
		return usage, nil
	}

	usage, newApiErr := openaichannel.OaiResponsesToChatHandler(c, info, httpResp)
	if newApiErr != nil {
		if info.ChannelType == constant.ChannelTypeOpenAIGPT {
			if openaigptchannel.ClassifyResponsesError(newApiErr, requestWasLocallyValid) ||
				openaigptchannel.ClassifyResponsesNamespaceError(newApiErr, validatedRequest) ||
				openaigptchannel.ClassifyImageDataError(newApiErr, validatedRequest) {
				logger.LogWarn(c, "channel does not support a requested Responses capability; retrying another channel")
			}
		}
		service.ResetStatusCode(newApiErr, statusCodeMappingStr)
		return nil, newApiErr
	}
	return usage, nil
}

func isResponsesEventStreamContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}
