package moonshot

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	channelconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
	// namedToolCall is set when a named tool_choice went upstream as structured
	// output, so the response must be turned back into that function call.
	namedToolCall *kvvNamedToolCall
}

func (a *Adaptor) ConvertGeminiRequest(*gin.Context, *relaycommon.RelayInfo, *dto.GeminiChatRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.ClaudeRequest) (any, error) {
	if err := adaptKVVMessagesCacheControl(info, req); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	adaptor := claude.Adaptor{}
	return adaptor.ConvertClaudeRequest(c, info, req)
}

// adaptKVVMessagesCacheControl aligns a Claude Messages request with the
// official KVV cache contract while the compatibility switch is on: an
// unsupported cache_control member becomes the 400 the official endpoint would
// return, and a body-level lifetime is lifted to the request level because the
// official endpoint reads only the top-level field and would otherwise fall
// back to the default 5m lifetime.
//
// The upstream rejects cache_control inside a message content part as a Chat
// Completions field ("cache_control is not supported on Chat Completions; use
// prompt_cache_options"), so the mark must not survive the lift. Every block
// mark is removed once its lifetime has moved to the request level; the
// request-level mark itself is left exactly as the client wrote it.
func adaptKVVMessagesCacheControl(info *relaycommon.RelayInfo, request *dto.ClaudeRequest) error {
	if !kvvMessagesCacheCompat(info, request) {
		return nil
	}
	if err := validateKVVMessagesCacheControl(request.CacheControl, "cache_control"); err != nil {
		return err
	}
	marked := false
	for blockIndex, control := range claudeContentCacheControls(request.System) {
		if err := validateKVVMessagesCacheControl(control, fmt.Sprintf("system[%d].cache_control", blockIndex)); err != nil {
			return err
		}
		marked = true
	}
	for messageIndex := range request.Messages {
		for blockIndex, control := range claudeContentCacheControls(request.Messages[messageIndex].Content) {
			path := fmt.Sprintf("messages[%d].content[%d].cache_control", messageIndex, blockIndex)
			if err := validateKVVMessagesCacheControl(control, path); err != nil {
				return err
			}
			marked = true
		}
	}
	if !marked {
		return nil
	}
	if ttl, ok := kimiMessagesCacheTTL(request); ok {
		request.CacheControl, _ = common.Marshal(map[string]string{"type": "ephemeral", "ttl": ttl})
	}
	stripClaudeContentCacheControls(request)
	return nil
}

// kvvMessagesCacheCompat reports whether the KVV cache contract applies to this
// Messages request. The switch is shared with the Chat contract, and the model
// check keeps non-K3 Moonshot models on their existing behavior.
func kvvMessagesCacheCompat(info *relaycommon.RelayInfo, request *dto.ClaudeRequest) bool {
	if info == nil || info.ChannelMeta == nil || request == nil ||
		info.ChannelType != channelconstant.ChannelTypeMoonshot ||
		!info.ChannelSetting.MoonshotKVVEnabled {
		return false
	}
	if !isKimiK3FamilyModel(info.UpstreamModelName) {
		return false
	}
	return info.ChannelSetting.MoonshotKVVParamCompat ||
		(isKimiK31Model(info.UpstreamModelName) && info.ChannelSetting.MoonshotK31ParamCompat)
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//TODO implement me
	return nil, errors.New("not supported")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	adaptor := openai.Adaptor{}
	return adaptor.ConvertImageRequest(c, info, request)
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	baseURL := info.ChannelBaseUrl
	if specialPlan, ok := channelconstant.ChannelSpecialBases[baseURL]; ok {
		if info.RelayFormat == types.RelayFormatClaude {
			return fmt.Sprintf("%s/v1/messages", specialPlan.ClaudeBaseURL), nil
		}
		if info.RelayFormat == types.RelayFormatOpenAI {
			return fmt.Sprintf("%s/chat/completions", specialPlan.OpenAIBaseURL), nil
		}
		if info.RelayFormat == types.RelayFormatOpenAIResponses {
			// This gateway forwards native Responses bodies, so keep their
			// endpoint while resolving the special-plan alias to a real base URL.
			return fmt.Sprintf("%s/responses", specialPlan.OpenAIBaseURL), nil
		}
	}

	switch info.RelayFormat {
	case types.RelayFormatClaude:
		return fmt.Sprintf("%s/v1/messages", strings.TrimRight(baseURL, "/")), nil
	default:
		if info.RelayMode == constant.RelayModeRerank {
			return fmt.Sprintf("%s/v1/rerank", info.ChannelBaseUrl), nil
		} else if info.RelayMode == constant.RelayModeEmbeddings {
			return fmt.Sprintf("%s/v1/embeddings", info.ChannelBaseUrl), nil
		} else if info.RelayMode == constant.RelayModeChatCompletions {
			return fmt.Sprintf("%s/v1/chat/completions", info.ChannelBaseUrl), nil
		} else if info.RelayMode == constant.RelayModeCompletions {
			return fmt.Sprintf("%s/v1/completions", info.ChannelBaseUrl), nil
		} else if info.RelayMode == constant.RelayModeResponses {
			return fmt.Sprintf("%s/v1/responses", info.ChannelBaseUrl), nil
		}
		return fmt.Sprintf("%s/v1/chat/completions", info.ChannelBaseUrl), nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	req.Set("Authorization", fmt.Sprintf("Bearer %s", info.ApiKey))
	if info.RelayFormat == types.RelayFormatClaude {
		version := c.Request.Header.Get("anthropic-version")
		if version == "" {
			version = "2023-06-01"
		}
		req.Set("anthropic-version", version)
		claude.CommonClaudeHeadersOperation(c, req, info)
	}
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, nil
	}
	if kvvMediaStringShorthand(info) {
		rewriteKimiMediaStringShorthand(request)
	}
	// Thinking stays on unless the client turns it off; the switch only lets
	// the client do so.
	allowDisableThinking := info != nil && info.ChannelMeta != nil &&
		info.ChannelSetting.MoonshotDisableThinking && info.RelayMode == constant.RelayModeChatCompletions
	emulateNamedToolChoice := false
	if KVVEnabled(info) {
		if err := adaptKVVRequest(request, info); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if request.ReasoningEffort == "none" {
			switch {
			case kvvOfficialCompat(info) || allowDisableThinking:
				// The official API accepts disabled thinking, which a named
				// tool_choice requires. thinking.type alone turns it off; none is
				// not one of the official reasoning_effort levels.
				request.ReasoningEffort = ""
				emulateNamedToolChoice = true
			case strings.EqualFold(getUpstreamModelName(info, request.Model), "kimi-k3.1-pro"):
				return nil, types.NewOpenAIError(fmt.Errorf("thinking must be enabled"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
		}
	} else if allowDisableThinking && isKimiK3FamilyModel(getUpstreamModelName(info, request.Model)) &&
		ojson.GetBytes(request.THINKING, "type").String() == "disabled" {
		emulateNamedToolChoice = true
	}
	// TextHelper converts a KVV request a second time, on the body this pass
	// already rewrote. That pass finds no named tool_choice, so it must keep
	// the call recorded here for the response.
	if emulateNamedToolChoice {
		if call := emulateKVVNamedToolChoice(request); call != nil {
			a.namedToolCall = call
		}
	}
	if request.Temperature != nil && isTemperatureOneOnlyModel(getUpstreamModelName(info, request.Model)) && *request.Temperature != 1.0 {
		request.Temperature = common.GetPointer[float64](1.0)
	}
	// Moonshot only reports prompt cache usage for streaming requests when the
	// upstream request opts into the terminal usage chunk. Enable it on the wire
	// even when the client omitted stream_options (or set include_usage to
	// false); info.ShouldIncludeUsage still controls whether that chunk is
	// exposed back to the client.
	ensureKimiStreamUsage(request)
	return request, nil
}

func ensureKimiStreamUsage(request *dto.GeneralOpenAIRequest) {
	if request == nil || request.Stream == nil || !*request.Stream {
		return
	}
	if request.StreamOptions == nil {
		request.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
		return
	}
	request.StreamOptions.IncludeUsage = true
}

func hasMeaningfulKimiMessageContent(content any) bool {
	switch value := content.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(value) != ""
	case []any:
		for _, item := range value {
			if hasMeaningfulKimiMessageContentItem(item) {
				return true
			}
		}
		return false
	case []dto.MediaContent:
		for _, item := range value {
			if item.Type != dto.ContentTypeText || strings.TrimSpace(item.Text) != "" {
				return true
			}
		}
		return false
	default:
		// Preserve malformed or provider-specific content so this compatibility
		// filter does not silently turn an unrelated request error into data loss.
		return true
	}
}

func hasMeaningfulKimiMessageContentItem(item any) bool {
	content, ok := item.(map[string]any)
	if !ok {
		return item != nil
	}
	contentType, _ := content["type"].(string)
	if contentType != dto.ContentTypeText {
		return len(content) > 0
	}
	text, _ := content["text"].(string)
	return strings.TrimSpace(text) != ""
}

func hasKimiAssistantToolCalls(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var toolCalls []dto.ToolCallRequest
	if err := common.Unmarshal(raw, &toolCalls); err != nil {
		// Keep malformed tool calls and let normal validation report them rather
		// than misclassifying the entire assistant message as empty.
		return true
	}
	return len(toolCalls) > 0
}

// rewriteKimiMediaStringShorthand turns the official string shorthand for
// image_url and video_url into the object form. The official API accepts both
// shapes, but Kimi upstreams answer the string form with a generic 400, so the
// rewrite is a compatibility behavior and only runs while it is enabled.
func rewriteKimiMediaStringShorthand(request *dto.GeneralOpenAIRequest) {
	for messageIndex := range request.Messages {
		content, ok := request.Messages[messageIndex].Content.([]any)
		if !ok {
			continue
		}
		changed := false
		rewritten := make([]any, 0, len(content))
		for _, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if !ok {
				rewritten = append(rewritten, rawPart)
				continue
			}
			partType := strings.ToLower(strings.TrimSpace(kimiMediaStringValue(part["type"])))
			if partType != dto.ContentTypeImageURL && partType != "video_url" {
				rewritten = append(rewritten, rawPart)
				continue
			}
			url, ok := part[partType].(string)
			if !ok || url == "" {
				rewritten = append(rewritten, rawPart)
				continue
			}
			objectPart := map[string]any{
				"type":   partType,
				partType: map[string]any{"url": url},
			}
			for key, value := range part {
				if key != partType && key != "type" {
					objectPart[key] = value
				}
			}
			rewritten = append(rewritten, objectPart)
			changed = true
		}
		if changed {
			request.Messages[messageIndex].Content = rewritten
		}
	}
}

func kimiMediaStringValue(value any) string {
	text, _ := value.(string)
	return text
}

func getUpstreamModelName(info *relaycommon.RelayInfo, fallback string) string {
	if info != nil && info.ChannelMeta != nil && info.UpstreamModelName != "" {
		return info.UpstreamModelName
	}
	return fallback
}

func isTemperatureOneOnlyModel(model string) bool {
	return strings.EqualFold(model, "kimi-k2.6")
}

// ConvertOpenAIResponsesRequest forwards Responses requests natively. Kimi's
// /v1/responses currently serves kimi-k3 only; other models are rejected by
// the upstream, not here.
func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	// Normalize the final body so passthrough, protocol conversion and channel
	// parameter overrides all use the same independent tool-schema documents.
	if requestBody != nil {
		payload, err := io.ReadAll(requestBody)
		if err != nil {
			return nil, fmt.Errorf("read tool schemas: %w", err)
		}
		if info != nil && info.ChannelMeta != nil && info.ChannelSetting.MoonshotVideoToImages {
			payload, err = convertMediaBody(c.Request.Context(), payload, info.RelayFormat, extractVideoFrames)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
		}
		// After frame conversion, so a video sent as frames is not downloaded again.
		if info != nil && info.ChannelMeta != nil && info.ChannelSetting.MoonshotMediaURLToBase64 {
			payload, err = inlineKimiMediaURLs(c, payload, info.RelayFormat)
			if err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
		}
		normalized, _, err := normalizeKimiToolSchemaRefsBody(payload)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		// A recursive schema with no finite instance is the official 400. It stays
		// behind the K3.1 switch because the baseline forwards such a schema.
		if kvvOfficialCompat(info) {
			if err := validateKimiToolSchemaRecursion(payload); err != nil {
				return nil, types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
		}
		if err := validateKimiToolResultPairing(normalized, info); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		// Split here rather than in ConvertOpenAIRequest: a KVV request is
		// converted twice, the split is not idempotent, and the dynamic tool
		// fallback below must carry the same parts.
		if info != nil && info.ChannelMeta != nil && info.ChannelSetting.MoonshotSplitCacheText &&
			info.RelayFormat == types.RelayFormatOpenAI && info.RelayMode == constant.RelayModeChatCompletions &&
			isKimiK3FamilyModel(info.UpstreamModelName) {
			normalized = splitKimiCacheTextBody(normalized)
		}
		requestBody = bytes.NewReader(normalized)
	}
	resp, err := a.doRequestWithDynamicToolFallback(c, info, requestBody)
	if err == nil && info.RelayFormat == types.RelayFormatClaude && resp != nil && resp.StatusCode == http.StatusBadRequest && resp.Body != nil {
		return a.retryUnsupportedMessagesCacheControl(c, info, resp)
	}
	return resp, err
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	openai.CaptureMoonshotCacheTTLHeaders(c, info, resp)
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		adaptor := claude.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	default:
		if a.namedToolCall != nil {
			if info.IsStream {
				resp.Body = newKVVNamedToolCallStream(resp.Body, *a.namedToolCall)
			} else {
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil {
					return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
				}
				resp.Body = io.NopCloser(bytes.NewReader(a.namedToolCall.rewriteResponse(body)))
			}
		}
		adaptor := openai.Adaptor{}
		return adaptor.DoResponse(c, resp, info)
	}
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
