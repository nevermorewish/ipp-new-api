package openaigpt

import (
	"bytes"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// Adaptor is the OpenAI-GPT channel.
//
// It speaks the same wire protocol as the OpenAI channel, so it embeds
// openai.Adaptor and delegates everything that has no channel-specific
// behaviour. What makes this channel distinct is that it enforces the official
// OpenAI request contract: requests that the official API would reject are
// rejected locally with a clear non-retryable 400 instead of costing an
// upstream round trip, and upstream rejections that indicate a provider
// capability gap are classified as retryable so another channel can serve them.
//
// That handling lives here rather than in shared code on purpose. It was
// previously applied to every OpenAI-compatible channel, which forced OpenAI's
// hardcoded vocabularies onto ~40 third-party providers that legitimately
// accept different values.
type Adaptor struct {
	openaiAdaptor openai.Adaptor
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.openaiAdaptor.Init(info)
}

// GetRequestURL delegates: openai.Adaptor's switch falls through to the default
// branch for this channel type, which is the plain base-URL join this channel
// wants, and the realtime ws:// upgrade ahead of the switch applies too.
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return a.openaiAdaptor.GetRequestURL(info)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	if err := a.openaiAdaptor.SetupRequestHeader(c, header, info); err != nil {
		return err
	}
	// openai.Adaptor only emits the organization header for channel type 1, so
	// this channel has to set it itself or org-scoped keys silently stop working.
	if info.Organization != "" {
		header.Set("OpenAI-Organization", info.Organization)
	}
	forwardCodexProtocolHeaders(c, header, info)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	// openai.Adaptor drops StreamOptions for every channel type except OpenAI
	// and Azure. This channel supports stream_options.include_usage, so keep the
	// client's value across the delegation.
	streamOptions := request.StreamOptions
	// Preserve caller parameters across the shared adaptor. The optional
	// OpenAI-GPT temperature filter runs on final bodies after overrides.
	temperature := request.Temperature
	topP := request.TopP
	logProbs := request.LogProbs
	converted, err := a.openaiAdaptor.ConvertOpenAIRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	if convertedRequest, ok := converted.(*dto.GeneralOpenAIRequest); ok {
		convertedRequest.StreamOptions = streamOptions
		convertedRequest.Temperature = temperature
		convertedRequest.TopP = topP
		convertedRequest.LogProbs = logProbs
	}
	return converted, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return a.openaiAdaptor.ConvertRerankRequest(c, relayMode, request)
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return a.openaiAdaptor.ConvertEmbeddingRequest(c, info, request)
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return a.openaiAdaptor.ConvertAudioRequest(c, info, request)
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return a.openaiAdaptor.ConvertImageRequest(c, info, request)
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	return a.openaiAdaptor.ConvertClaudeRequest(c, info, request)
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	return a.openaiAdaptor.ConvertGeminiRequest(c, info, request)
}

// ConvertOpenAIResponsesRequest delegates DTO conversion. OpenAI-GPT
// normalization and contract validation happen in the shared handler after
// pass-through handling and parameter overrides, so they inspect the exact
// bytes that will be sent upstream.
func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return a.openaiAdaptor.ConvertOpenAIResponsesRequest(c, info, request)
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info.RelayMode == relayconstant.RelayModeResponses || info.RelayMode == relayconstant.RelayModeResponsesCompact || info.RelayMode == relayconstant.RelayModeChatCompletions {
		// Delegating DoRequest to the embedded adaptor would pass that adaptor
		// to the transport and bypass this channel's SetupRequestHeader.
		return channel.DoApiRequest(a, c, info, requestBody)
	}
	return a.openaiAdaptor.DoRequest(c, info, requestBody)
}
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	forwardCodexStreamTurnState(c, resp, info)
	restoreLegacyFunctionCallBody(c, resp, info)
	return a.openaiAdaptor.DoResponse(c, resp, info)
}

// restoreLegacyFunctionCallBody rewrites a non-streaming Chat response back
// into the deprecated function_call shape when the client asked for it.
//
// It runs before delegation so the shared handler counts tokens and copies
// headers against the body the client will actually receive. Any problem --
// unreadable body, unparseable JSON, no tool call present -- leaves the
// original bytes in place; this is a compatibility narrowing, never a gate.
//
// Two conditions disable it, both because the shared handler re-marshals the
// response from dto.OpenAITextResponse in those cases. dto.Message models
// tool_calls but not function_call, so rewriting first would drop the tool call
// entirely rather than merely presenting it in the older shape:
//
//   - ForceFormat, which re-encodes even for OpenAI-format clients;
//   - a non-OpenAI client format, where the response is converted to the
//     Claude or Gemini shape and the legacy OpenAI contract does not apply.
func restoreLegacyFunctionCallBody(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) {
	if c == nil || resp == nil || info == nil || resp.Body == nil {
		return
	}
	if info.RelayMode != relayconstant.RelayModeChatCompletions || info.IsStream {
		return
	}
	if resp.StatusCode != http.StatusOK {
		return
	}
	if info.RelayFormat != types.RelayFormatOpenAI || info.ChannelSetting.ForceFormat {
		return
	}
	if legacy, exists := c.Get(LegacyFunctionCallContextKey()); !exists || legacy != true {
		return
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		// The body is already consumed and cannot be handed back, so surface an
		// empty reader and let the shared handler report the read failure.
		resp.Body = io.NopCloser(bytes.NewReader(nil))
		return
	}

	restored, changed := RestoreLegacyFunctionCall(body)
	if changed {
		logger.LogInfo(c, "restored deprecated function_call response shape for a legacy functions request")
		resp.ContentLength = int64(len(restored))
	}
	resp.Body = io.NopCloser(bytes.NewReader(restored))
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
