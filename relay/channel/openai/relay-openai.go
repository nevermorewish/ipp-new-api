package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openrouter"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/tokenkit"

	"github.com/MoeForge/moego/ojson"
	"github.com/gin-gonic/gin"
)

func sendStreamData(c *gin.Context, info *relaycommon.RelayInfo, data string, forceFormat bool, thinkToContent bool) error {
	if data == "" {
		return nil
	}
	// Only a frame that reports usage carries anything the TTL setting rewrites,
	// and the substring check keeps ordinary deltas from being parsed twice. A
	// standalone usage frame has no delta to format, so its raw counters, explicit
	// zeros and TTL evidence are forwarded unchanged even in force mode.
	if moonshotTTLCompatEnabled(info) && strings.Contains(data, `"usage"`) &&
		ojson.Get(data, "usage").IsObject() && len(ojson.Get(data, "choices").Array()) == 0 {
		return helper.StringData(c, string(enrichMoonshotCacheTTL(c, info, []byte(data), "")))
	}

	if !forceFormat && !thinkToContent {
		return helper.StringData(c, data)
	}

	var lastStreamResponse dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(data, &lastStreamResponse); err != nil {
		return err
	}

	if !thinkToContent {
		return sendFormattedStreamChunk(c, info, lastStreamResponse)
	}

	hasThinkingContent := false
	hasContent := false
	var thinkingContent strings.Builder
	for _, choice := range lastStreamResponse.Choices {
		if len(choice.Delta.GetReasoningContent()) > 0 {
			hasThinkingContent = true
			thinkingContent.WriteString(choice.Delta.GetReasoningContent())
		}
		if len(choice.Delta.GetContentString()) > 0 {
			hasContent = true
		}
	}

	// Handle think to content conversion
	if info.ThinkingContentInfo.IsFirstThinkingContent {
		if hasThinkingContent {
			response := lastStreamResponse.Copy()
			for i := range response.Choices {
				// send `think` tag with thinking content
				response.Choices[i].Delta.SetContentString("<think>\n" + thinkingContent.String())
				response.Choices[i].Delta.ReasoningContent = nil
				response.Choices[i].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.IsFirstThinkingContent = false
			info.ThinkingContentInfo.HasSentThinkingContent = true
			return sendFormattedStreamChunk(c, info, response)
		}
	}

	if lastStreamResponse.Choices == nil || len(lastStreamResponse.Choices) == 0 {
		return sendFormattedStreamChunk(c, info, lastStreamResponse)
	}

	// Process each choice
	for i, choice := range lastStreamResponse.Choices {
		// Handle transition from thinking to content
		// only send `</think>` tag when previous thinking content has been sent
		if hasContent && !info.ThinkingContentInfo.SendLastThinkingContent && info.ThinkingContentInfo.HasSentThinkingContent {
			response := lastStreamResponse.Copy()
			for j := range response.Choices {
				response.Choices[j].Delta.SetContentString("\n</think>\n")
				response.Choices[j].Delta.ReasoningContent = nil
				response.Choices[j].Delta.Reasoning = nil
			}
			info.ThinkingContentInfo.SendLastThinkingContent = true
			_ = sendFormattedStreamChunk(c, info, response)
		}

		// Convert reasoning content to regular content if any
		if len(choice.Delta.GetReasoningContent()) > 0 {
			lastStreamResponse.Choices[i].Delta.SetContentString(choice.Delta.GetReasoningContent())
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		} else if !hasThinkingContent && !hasContent {
			// flush thinking content
			lastStreamResponse.Choices[i].Delta.ReasoningContent = nil
			lastStreamResponse.Choices[i].Delta.Reasoning = nil
		}
	}

	return sendFormattedStreamChunk(c, info, lastStreamResponse)
}

// sendFormattedStreamChunk emits a chunk rebuilt from its typed form. The typed
// struct cannot carry the TTL evidence, so it is applied to the marshalled bytes
// rather than to the parsed chunk.
func sendFormattedStreamChunk(c *gin.Context, info *relaycommon.RelayInfo, chunk any) error {
	if !moonshotTTLCompatEnabled(info) {
		return helper.ObjectData(c, chunk)
	}
	encoded, err := common.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("error marshalling object: %w", err)
	}
	return helper.StringData(c, string(enrichMoonshotCacheTTL(c, info, encoded, "")))
}

func OaiStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, common.LogText("invalid response or response body"))
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	defer service.CloseResponseBodyGracefully(resp)

	model := info.UpstreamModelName
	var responseId string
	var createAt int64 = 0
	var systemFingerprint string
	var containStreamUsage bool
	var responseTextBuilder strings.Builder
	var toolCount int
	var usage = &dto.Usage{}
	var lastStreamData string
	var secondLastStreamData string // 保留倒数第二个stream data；部分兼容网关把完整usage放在倒数第二个事件
	seenStreamToolCalls := make(map[string]struct{})
	var streamFunctionCallNames []string
	isMoonshot := info.ChannelType == constant.ChannelTypeMoonshot

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		if lastStreamData != "" {
			if err := HandleStreamFormat(c, info, lastStreamData, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent); err != nil {
				common.SysLog(common.LogText("error handling stream format: %s", err.Error()))
				sr.Error(err)
			}
		}
		if len(data) > 0 {
			if lastStreamData != "" {
				secondLastStreamData = lastStreamData
			}

			lastStreamData = data
			observeStreamChoices(info, data, seenStreamToolCalls, &streamFunctionCallNames)
			if err := processTokenData(info, data, &responseTextBuilder, &toolCount); err != nil {
				logger.LogError(c, common.LogText("error processing stream token data: %s", err.Error()))
				sr.Error(err)
			}
		}
	})

	info.StreamStatus.RequireTerminal()

	// 处理最后的响应
	shouldSendLastResp := true
	if err := handleLastResponse(lastStreamData, &responseId, &createAt, &systemFingerprint, &model, &usage,
		&containStreamUsage, info, &shouldSendLastResp); err != nil {
		logger.LogError(c, common.LogText("error handling last response: %s, lastStreamData: [%s]", err.Error(), lastStreamData))
	}

	// 部分兼容网关把完整的累计usage附在倒数第二个事件上，随后发送一个空的最后事件。
	// 仅当最后一个事件没有有效usage时，回退到倒数第二个事件的完整快照。
	usageFrame := lastStreamData
	if !containStreamUsage && secondLastStreamData != "" {
		var streamResp struct {
			Usage *dto.Usage `json:"usage"`
		}
		err := common.Unmarshal([]byte(secondLastStreamData), &streamResp)
		if err == nil && streamResp.Usage != nil &&
			streamResp.Usage.PromptTokens > 0 &&
			(streamResp.Usage.CompletionTokens > 0 || streamResp.Usage.TotalTokens > 0) {
			usage = dto.MergeUsageNonZero(usage, streamResp.Usage)
			containStreamUsage = true
			usageFrame = secondLastStreamData

			if common.DebugEnabled {
				logger.LogDebug(c, "usage extracted from second last SSE: PromptTokens=%d, CompletionTokens=%d, TotalTokens=%d, InputTokens=%d, OutputTokens=%d",
					usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens,
					usage.InputTokens, usage.OutputTokens)
			}
		}
	}

	// Moonshot reports the terminal usage on the chunk that also carries the
	// last choice and finish_reason. OpenAI-compatible clients, including the
	// Kimi cache-usage contract, expect that usage in its own choices=[] chunk
	// after the finish chunk.
	forwardLastData := lastStreamData
	splitInlineUsage := ""
	if isMoonshot && containStreamUsage && usageFrame == lastStreamData {
		if stripped, usageJSON, ok := stripInlineStreamUsage(lastStreamData); ok {
			forwardLastData = stripped
			splitInlineUsage = usageJSON
			shouldSendLastResp = true
		}
	}

	if info.RelayFormat == types.RelayFormatOpenAI {
		if shouldSendLastResp {
			_ = sendStreamData(c, info, forwardLastData, info.ChannelSetting.ForceFormat, info.ChannelSetting.ThinkingToContent)
		}
	}

	if !containStreamUsage {
		usage = service.ResponseText2Usage(c, responseTextBuilder.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		usage.CompletionTokens += toolCount * 7
	}

	applyUsagePostProcessing(info, usage, common.StringToByteSlice(usageFrame))
	originalSplitInlineUsage := splitInlineUsage

	if isMoonshot {
		// The write headers left with the response before this usage arrived, so
		// they are what the client already read; align the usage to them and
		// rewrite the forwarded usage object the same way.
		write := resolveMoonshotCacheWrite(resp.Header, usage, true)
		setMoonshotCacheWrite(usage, write)
		if splitInlineUsage != "" {
			splitInlineUsage = rewriteMoonshotCacheWriteUsageJSON(splitInlineUsage, write)
		}
	}

	if splitInlineUsage != "" && info.ShouldIncludeUsage && info.RelayFormat == types.RelayFormatOpenAI {
		// Forward the upstream usage object verbatim: re-marshalling the typed
		// DTO would drop explicit zero counts (cache_write_tokens: 0) and
		// rename Moonshot's TTL split, which the cache contract compares
		// field by field.
		chunk := struct {
			Id                string          `json:"id"`
			Object            string          `json:"object"`
			Created           int64           `json:"created"`
			Model             string          `json:"model"`
			SystemFingerprint string          `json:"system_fingerprint"`
			Choices           []any           `json:"choices"`
			Usage             json.RawMessage `json:"usage"`
		}{
			Id: responseId, Object: "chat.completion.chunk", Created: createAt, Model: model,
			SystemFingerprint: systemFingerprint, Choices: []any{}, Usage: json.RawMessage(splitInlineUsage),
		}
		encoded, marshalErr := common.Marshal(chunk)
		if marshalErr == nil {
			_ = helper.StringData(c, string(enrichMoonshotCacheTTL(c, info, encoded, originalSplitInlineUsage)))
		}
	}

	for _, name := range streamFunctionCallNames {
		info.CountBillableToolCall(dto.BuildInCallFunctionCall, name)
	}

	HandleFinalResponse(c, info, lastStreamData, responseId, createAt, model, systemFingerprint, usage, containStreamUsage)

	return usage, nil
}

// stripInlineStreamUsage removes usage members from a chunk that also carries
// choices and returns the removed top-level usage as raw JSON, so the
// standalone usage chunk can forward exactly what the upstream reported. A
// choices=[] usage chunk is already the standard shape and is left untouched,
// which yields ok=false.
func stripInlineStreamUsage(data string) (string, string, bool) {
	var chunk struct {
		Usage   json.RawMessage `json:"usage"`
		Choices []struct {
			Usage json.RawMessage `json:"usage"`
		} `json:"choices"`
	}
	if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
		return data, "", false
	}
	if len(chunk.Choices) == 0 {
		return data, "", false
	}
	hasUsage := len(chunk.Usage) > 0 && string(chunk.Usage) != "null"
	for i := range chunk.Choices {
		if len(chunk.Choices[i].Usage) > 0 && string(chunk.Choices[i].Usage) != "null" {
			hasUsage = true
		}
	}
	if !hasUsage {
		return data, "", false
	}
	usageJSON := ""
	if len(chunk.Usage) > 0 && string(chunk.Usage) != "null" {
		usageJSON = string(chunk.Usage)
	}
	stripped, err := ojson.Delete(data, "usage")
	if err != nil {
		return data, "", false
	}
	for i := range chunk.Choices {
		if len(chunk.Choices[i].Usage) > 0 && string(chunk.Choices[i].Usage) != "null" {
			path := fmt.Sprintf("choices.%d.usage", i)
			stripped, err = ojson.Delete(stripped, path)
			if err != nil {
				return data, "", false
			}
		}
	}
	return stripped, usageJSON, usageJSON != ""
}

// observeStreamChoices collects billable function call names and records the
// finish reason facts used by health sampling from one parsed chunk.
func observeStreamChoices(info *relaycommon.RelayInfo, data string, seen map[string]struct{}, names *[]string) {
	var streamResponse dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
		return
	}
	for _, choice := range streamResponse.Choices {
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			if *choice.FinishReason == constant.FinishReasonContentFilter {
				info.PerformanceBusinessRejection = true
			}
			info.StreamStatus.MarkCompleted()
		}
		for i, tc := range choice.Delta.ToolCalls {
			name := strings.TrimSpace(tc.Function.Name)
			if name == "" {
				continue
			}
			toolIdx := i
			if tc.Index != nil {
				toolIdx = *tc.Index
			}
			fallbackKey := fmt.Sprintf("index\x00%d\x00%d\x00%s", choice.Index, toolIdx, name)
			activeKey := fmt.Sprintf("active\x00%d\x00%d\x00%s", choice.Index, toolIdx, name)
			callID := strings.TrimSpace(tc.ID)
			if callID != "" {
				idKey := fmt.Sprintf("id\x00%d\x00%s", choice.Index, callID)
				if _, ok := seen[idKey]; ok {
					continue
				}
				seen[idKey] = struct{}{}
				seen[activeKey] = struct{}{}
				if _, delayedID := seen[fallbackKey]; delayedID {
					delete(seen, fallbackKey)
					continue
				}
			} else {
				if _, ok := seen[fallbackKey]; ok {
					continue
				}
				if _, ok := seen[activeKey]; ok {
					continue
				}
				seen[fallbackKey] = struct{}{}
				seen[activeKey] = struct{}{}
			}
			*names = append(*names, name)
		}
	}
}

func OpenaiHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	var simpleResponse dto.OpenAITextResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	logger.LogDebug(c, "upstream response body: %s", responseBody)
	// Unmarshal to simpleResponse
	if info.ChannelType == constant.ChannelTypeOpenRouter && info.ChannelOtherSettings.IsOpenRouterEnterprise() {
		// 尝试解析为 openrouter enterprise
		var enterpriseResponse openrouter.OpenRouterEnterpriseResponse
		err = common.Unmarshal(responseBody, &enterpriseResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if enterpriseResponse.Success {
			responseBody = enterpriseResponse.Data
		} else {
			logger.LogError(c, common.LogText("openrouter enterprise response success=false, data: %s", enterpriseResponse.Data))
			return nil, types.NewOpenAIError(fmt.Errorf("openrouter response success=false"), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}

	err = common.Unmarshal(responseBody, &simpleResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	if oaiError := simpleResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	info.ObserveResponseModel(simpleResponse.Model)
	for _, choice := range simpleResponse.Choices {
		if choice.FinishReason == constant.FinishReasonContentFilter {
			info.PerformanceBusinessRejection = true
			common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "openai_finish_reason=content_filter")
			break
		}
	}

	for _, choice := range simpleResponse.Choices {
		for _, tc := range choice.Message.ParseToolCalls() {
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, tc.Function.Name)
		}
	}
	info.ApplyVendorToolUsage(responseBody)

	forceFormat := false
	if info.ChannelSetting.ForceFormat {
		forceFormat = true
	}

	usageModified := false
	if simpleResponse.Usage.PromptTokens == 0 {
		completionTokens := simpleResponse.Usage.CompletionTokens
		if completionTokens == 0 {
			for _, choice := range simpleResponse.Choices {
				ctkm := tokenkit.Count(info.UpstreamModelName, choice.Message.StringContent()+choice.Message.GetReasoningContent())
				completionTokens += ctkm
			}
		}
		fallbackUsage := &dto.Usage{
			PromptTokens:     info.GetEstimatePromptTokens(),
			CompletionTokens: completionTokens,
			TotalTokens:      info.GetEstimatePromptTokens() + completionTokens,
		}
		simpleResponse.Usage = *fallbackUsage
		usageModified = true
	}

	applyUsagePostProcessing(info, &simpleResponse.Usage, responseBody)

	if info.ChannelType == constant.ChannelTypeMoonshot {
		// The body has not been written yet, so both statements can still be
		// reconciled. Only a body that disagrees is rewritten: re-marshalling
		// an already-correct usage would drop members this DTO does not model.
		original := moonshotCacheWriteFromUsage(&simpleResponse.Usage)
		write := resolveMoonshotCacheWrite(resp.Header, &simpleResponse.Usage, false)
		setMoonshotCacheWrite(&simpleResponse.Usage, write)
		setMoonshotCacheWriteHeaders(resp.Header, write)
		if write != original {
			usageModified = true
		}
	}

	originalBody := responseBody

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		if usageModified {
			usageJSON, err := common.Marshal(simpleResponse.Usage)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
			responseBody, err = ojson.SetRawBytes(responseBody, "usage", usageJSON)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			}
		}
		if forceFormat {
			responseBody, err = common.Marshal(simpleResponse)
			if err != nil {
				return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
			}
		} else {
			break
		}
	case types.RelayFormatClaude:
		convertResult, err := service.ConvertResponse(c, info, types.RelayFormatClaude, &simpleResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		claudeRespStr, err := common.Marshal(convertResult.Value)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		// The converted body carries the gateway's own accounting fields on
		// usage; strip them so the client sees the official Messages shape.
		if scrubbed, didScrub := dto.ScrubClaudeInternalUsageInBody(claudeRespStr); didScrub {
			claudeRespStr = scrubbed
		}
		responseBody = claudeRespStr
	case types.RelayFormatGemini:
		convertResult, err := service.ConvertResponse(c, info, types.RelayFormatGemini, &simpleResponse)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		geminiRespStr, err := common.Marshal(convertResult.Value)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responseBody = geminiRespStr
	}

	responseBody = enrichMoonshotCacheTTL(c, info, responseBody, ojson.GetBytes(originalBody, "usage").Raw)
	service.IOCopyBytesGracefully(c, resp, responseBody)

	return &simpleResponse.Usage, nil
}
