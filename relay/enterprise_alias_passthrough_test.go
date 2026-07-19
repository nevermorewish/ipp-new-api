package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEnterpriseAliasPassThroughSendsCanonicalModel(t *testing.T) {
	service.InitHttpClient()
	testCases := []struct {
		name          string
		path          string
		relayMode     int
		relayFormat   types.RelayFormat
		request       func() dto.Request
		invokeHandler func(*gin.Context, *relaycommon.RelayInfo) *types.NewAPIError
	}{
		{
			name:        "compatible chat",
			path:        "/v1/chat/completions",
			relayMode:   relayconstant.RelayModeChatCompletions,
			relayFormat: types.RelayFormatOpenAI,
			request: func() dto.Request {
				return &dto.GeneralOpenAIRequest{Model: "corp-chat"}
			},
			invokeHandler: TextHelper,
		},
		{
			name:        "responses",
			path:        "/v1/responses",
			relayMode:   relayconstant.RelayModeResponses,
			relayFormat: types.RelayFormatOpenAIResponses,
			request: func() dto.Request {
				return &dto.OpenAIResponsesRequest{Model: "corp-chat"}
			},
			invokeHandler: ResponsesHelper,
		},
		{
			name:        "claude messages",
			path:        "/v1/messages",
			relayMode:   relayconstant.RelayModeChatCompletions,
			relayFormat: types.RelayFormatClaude,
			request: func() dto.Request {
				return &dto.ClaudeRequest{Model: "corp-chat"}
			},
			invokeHandler: ClaudeHelper,
		},
		{
			name:        "rerank",
			path:        "/v1/rerank",
			relayMode:   relayconstant.RelayModeRerank,
			relayFormat: types.RelayFormatRerank,
			request: func() dto.Request {
				return &dto.RerankRequest{Model: "corp-chat"}
			},
			invokeHandler: RerankHelper,
		},
		{
			name:        "image generation",
			path:        "/v1/images/generations",
			relayMode:   relayconstant.RelayModeImagesGenerations,
			relayFormat: types.RelayFormatOpenAIImage,
			request: func() dto.Request {
				return &dto.ImageRequest{Model: "corp-chat", Prompt: "test"}
			},
			invokeHandler: ImageHelper,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			capturedBody := make(chan []byte, 1)
			upstream := newPassThroughCaptureServer(t, capturedBody)
			defer upstream.Close()

			body := `{"model":"corp-chat","sentinel":9007199254740993}`
			ctx := newPassThroughRelayContext(t, testCase.path, body, upstream.URL, true)
			info := &relaycommon.RelayInfo{
				Request:            testCase.request(),
				OriginModelName:    "real-model",
				RequestedModelName: "corp-chat",
				RelayMode:          testCase.relayMode,
				RelayFormat:        testCase.relayFormat,
				RequestURLPath:     testCase.path,
			}

			require.NotNil(t, testCase.invokeHandler(ctx, info))
			outbound := <-capturedBody
			var payload struct {
				Model string `json:"model"`
			}
			require.NoError(t, json.Unmarshal(outbound, &payload))
			require.Equal(t, "real-model", payload.Model)
			require.Contains(t, string(outbound), `"sentinel":9007199254740993`)
		})
	}
}

func TestNonEnterprisePassThroughPreservesOriginalBody(t *testing.T) {
	service.InitHttpClient()
	capturedBody := make(chan []byte, 1)
	upstream := newPassThroughCaptureServer(t, capturedBody)
	defer upstream.Close()

	body := "{ \n  \"model\" : \"ordinary-model\", \"unknown\" : 9007199254740993\n}"
	ctx := newPassThroughRelayContext(t, "/v1/chat/completions", body, upstream.URL, true)
	info := &relaycommon.RelayInfo{
		Request:         &dto.GeneralOpenAIRequest{Model: "ordinary-model"},
		OriginModelName: "ordinary-model",
		RelayMode:       relayconstant.RelayModeChatCompletions,
		RelayFormat:     types.RelayFormatOpenAI,
		RequestURLPath:  "/v1/chat/completions",
	}

	require.NotNil(t, TextHelper(ctx, info))
	require.Equal(t, body, string(<-capturedBody))
}

func TestNonEnterprisePassThroughPreservesOriginalBodyForNonJSONContentType(t *testing.T) {
	body := "opaque request body\x00with bytes"
	ctx := newPassThroughRelayContext(t, "/v1/custom", body, "http://unused.example", true)
	ctx.Request.Header.Del("Content-Type")
	info := &relaycommon.RelayInfo{OriginModelName: "ordinary-model"}

	reader, size, closer, err := newPassThroughRequestBody(ctx, info)
	require.NoError(t, err)
	require.Nil(t, closer)
	outbound, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.EqualValues(t, len(outbound), size)
	require.Equal(t, []byte(body), outbound)
}

func TestEnterpriseAliasPassThroughRejectsDuplicateModelFields(t *testing.T) {
	body := `{"model":"corp-chat","MODEL":"corp-chat"}`
	ctx := newPassThroughRelayContext(t, "/v1/chat/completions", body, "http://unused.example", true)
	info := &relaycommon.RelayInfo{
		OriginModelName:    "real-model",
		RequestedModelName: "corp-chat",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "real-model",
		},
	}

	reader, size, closer, err := newPassThroughRequestBody(ctx, info)
	require.ErrorContains(t, err, "exactly one model field")
	require.Nil(t, reader)
	require.Zero(t, size)
	require.Nil(t, closer)
}

func TestEnterpriseAliasGeminiPassThroughUsesCanonicalModelInURL(t *testing.T) {
	service.InitHttpClient()
	type capturedRequest struct {
		path string
		body []byte
	}
	captured := make(chan capturedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		captured <- capturedRequest{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, `{"error":{"message":"stop after capture","type":"test_error","code":"capture"}}`)
	}))
	defer upstream.Close()

	body := `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
	ctx := newPassThroughRelayContext(t, "/v1beta/models/corp-chat:generateContent", body, upstream.URL, true)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
	info := &relaycommon.RelayInfo{
		Request:            &dto.GeminiChatRequest{},
		OriginModelName:    "real-model",
		RequestedModelName: "corp-chat",
		RelayMode:          relayconstant.RelayModeGemini,
		RelayFormat:        types.RelayFormatGemini,
		RequestURLPath:     "/v1beta/models/corp-chat:generateContent",
	}

	require.NotNil(t, GeminiHelper(ctx, info))
	outbound := <-captured
	require.Contains(t, outbound.path, "/models/real-model:generateContent")
	require.NotContains(t, outbound.path, "corp-chat")
	require.Equal(t, body, string(outbound.body))
}

func TestEnterpriseAliasPassThroughCanonicalizesCaseInsensitiveModelField(t *testing.T) {
	service.InitHttpClient()
	capturedBody := make(chan []byte, 1)
	upstream := newPassThroughCaptureServer(t, capturedBody)
	defer upstream.Close()

	body := `{"Model":"corp-chat","sentinel":true}`
	ctx := newPassThroughRelayContext(t, "/v1/chat/completions", body, upstream.URL, true)
	info := &relaycommon.RelayInfo{
		Request:            &dto.GeneralOpenAIRequest{Model: "corp-chat"},
		OriginModelName:    "real-model",
		RequestedModelName: "corp-chat",
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        types.RelayFormatOpenAI,
		RequestURLPath:     "/v1/chat/completions",
	}

	require.NotNil(t, TextHelper(ctx, info))
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(<-capturedBody, &payload))
	require.Equal(t, "real-model", payload["Model"])
	require.NotContains(t, payload, "model")
}

func TestEnterpriseAliasPassThroughSupportsStructuredJSONContentType(t *testing.T) {
	body := `{"model":"corp-chat","sentinel":true}`
	ctx := newPassThroughRelayContext(t, "/v1/chat/completions", body, "http://unused.example", true)
	ctx.Request.Header.Set("Content-Type", "application/vnd.workbuddy.request+json; charset=utf-8")
	info := &relaycommon.RelayInfo{
		OriginModelName:    "real-model",
		RequestedModelName: "corp-chat",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "real-model",
		},
	}

	reader, size, closer, err := newPassThroughRequestBody(ctx, info)
	require.NoError(t, err)
	require.NotNil(t, closer)
	defer closer.Close()
	outbound, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.EqualValues(t, len(outbound), size)
	require.JSONEq(t, `{"model":"real-model","sentinel":true}`, string(outbound))
}

func TestEnterpriseAliasPassThroughRejectsUnsupportedContentType(t *testing.T) {
	testCases := []struct {
		name        string
		contentType string
	}{
		{name: "missing"},
		{name: "non JSON", contentType: "text/plain"},
		{name: "malformed", contentType: "application/json; charset"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := newPassThroughRelayContext(t, "/v1/chat/completions", `{"model":"corp-chat"}`, "http://unused.example", true)
			if testCase.contentType == "" {
				ctx.Request.Header.Del("Content-Type")
			} else {
				ctx.Request.Header.Set("Content-Type", testCase.contentType)
			}
			info := &relaycommon.RelayInfo{
				OriginModelName:    "real-model",
				RequestedModelName: "corp-chat",
				ChannelMeta: &relaycommon.ChannelMeta{
					UpstreamModelName: "real-model",
				},
			}

			reader, size, closer, err := newPassThroughRequestBody(ctx, info)
			require.ErrorContains(t, err, "content type")
			require.Nil(t, reader)
			require.Zero(t, size)
			require.Nil(t, closer)
		})
	}
}

func TestEnterpriseAliasMultipartPassThroughSendsCanonicalModel(t *testing.T) {
	service.InitHttpClient()
	capturedModel := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse upstream multipart form: %v", err)
		} else {
			capturedModel <- r.FormValue("model")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, `{"error":{"message":"stop after capture","type":"test_error","code":"capture"}}`)
	}))
	defer upstream.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "corp-chat"))
	require.NoError(t, writer.WriteField("prompt", "edit this"))
	filePart, err := writer.CreateFormFile("image", "image.png")
	require.NoError(t, err)
	_, err = filePart.Write([]byte("test-image"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	ctx := newPassThroughRelayContext(t, "/v1/images/edits", body.String(), upstream.URL, true)
	ctx.Request.Header.Set("Content-Type", writer.FormDataContentType())
	info := &relaycommon.RelayInfo{
		Request:            &dto.ImageRequest{Model: "corp-chat", Prompt: "edit this"},
		OriginModelName:    "real-model",
		RequestedModelName: "corp-chat",
		RelayMode:          relayconstant.RelayModeImagesEdits,
		RelayFormat:        types.RelayFormatOpenAIImage,
		RequestURLPath:     "/v1/images/edits",
	}

	require.NotNil(t, ImageHelper(ctx, info))
	require.Equal(t, "real-model", <-capturedModel)
}

func newPassThroughCaptureServer(t *testing.T, capturedBody chan<- []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		capturedBody <- body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, `{"error":{"message":"stop after capture","type":"test_error","code":"capture"}}`)
	}))
}

func newPassThroughRelayContext(t *testing.T, path string, body string, upstreamURL string, passThrough bool) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "real-model")
	common.SetContextKey(ctx, constant.ContextKeyRequestedModel, "corp-chat")
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, upstreamURL)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "test-key")
	common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: passThrough})
	return ctx
}
