package moonshot

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestVideoBodyFormats(t *testing.T) {
	for _, tc := range []struct {
		name, field, block, imagePath, imageKind string
		format                                   types.RelayFormat
	}{
		{"chat URL", "messages", `{"type":"video_url","video_url":{"url":"https://example.com/a.mp4"}}`, "image_url.url", "image_url", types.RelayFormatOpenAI},
		{"chat string URL", "messages", `{"type":"video_url","video_url":"https://example.com/a.mp4"}`, "image_url.url", "image_url", types.RelayFormatOpenAI},
		{"responses", "input", `{"type":"input_video","video_url":"https://example.com/a.mp4"}`, "image_url", "input_image", types.RelayFormatOpenAIResponses},
		{"claude upload", "messages", `{"type":"video","source":{"type":"base64","media_type":"video/mp4","data":"YWJj"}}`, "source.data", "image", types.RelayFormatClaude},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"seed":9007199254740993,"%s":[{"role":"user","content":[{"type":"text","text":"original"},%s,{"type":"image_url","image_url":{"url":"original.jpg"}}]}]}`, tc.field, tc.block))
			out, err := convertMediaBody(context.Background(), body, tc.format, func(ctx context.Context, ref string) ([]videoFrame, error) {
				require.NotEmpty(t, ref)
				return []videoFrame{{Seconds: 0, JPEG: []byte("first")}, {Seconds: 5, JPEG: []byte("last")}}, nil
			})
			require.NoError(t, err)
			assert.Equal(t, "9007199254740993", gjson.GetBytes(out, "seed").Raw)
			parts := gjson.GetBytes(out, tc.field+".0.content").Array()
			require.Len(t, parts, 7)
			assert.Equal(t, "original", parts[0].Get("text").String())
			assert.Equal(t, tc.imageKind, parts[3].Get("type").String())
			assert.Contains(t, parts[3].Get(tc.imagePath).String(), base64.StdEncoding.EncodeToString([]byte("first")))
			assert.Contains(t, parts[4].Get("text").String(), "5.00 seconds")
			assert.Equal(t, "original.jpg", parts[6].Get("image_url.url").String())
		})
	}
}

func TestVideoBodyPreservationAndErrors(t *testing.T) {
	untouched := []byte(`{ "messages": [{"role":"user","content":"https://example.com/video.mp4"}], "tools": [] }`)
	out, err := convertMediaBody(context.Background(), untouched, types.RelayFormatOpenAI, nil)
	require.NoError(t, err)
	assert.Equal(t, untouched, out)
	_, err = convertMediaBody(context.Background(), []byte(`{"messages":[{"content":[{"type":"video_url"}]}]}`), types.RelayFormatOpenAI, nil)
	assert.ErrorContains(t, err, "requires")
	_, err = convertMediaBody(context.Background(), []byte(`{"messages":[{"content":[{"type":"video_url","video_url":"x"}]}]}`), types.RelayFormatOpenAI, func(context.Context, string) ([]videoFrame, error) { return nil, fmt.Errorf("decode failed") })
	assert.ErrorContains(t, err, "decode failed")
	for _, ref := range []string{"file:///etc/passwd", "ftp://example.com/video", "data:text/plain;base64,YQ==", "data:video/mp4;base64,!!!"} {
		_, err := loadVideo(context.Background(), ref)
		assert.Error(t, err)
	}
}

// Only a Chat image_url string changes; the Responses input_image string form is standard.
func TestMediaCompatibilityImageURLForms(t *testing.T) {
	service.InitHttpClient()
	for _, tc := range []struct {
		name, body, path, want string
		format                 types.RelayFormat
	}{
		{"chat string", `{"model":"kimi-k3.1-pro","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"Read it."},{"type":"image_url","image_url":"data:image/png;base64,AAAA"}]}]}`, "messages.0.content.1.image_url", `{"url":"data:image/png;base64,AAAA"}`, types.RelayFormatOpenAI},
		{"chat object", `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png","detail":"low"}}]}]}`, "messages.0.content.0.image_url", `{"url":"https://example.com/a.png","detail":"low"}`, types.RelayFormatOpenAI},
		{"responses input_image", `{"model":"kimi-k3","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/a.png"}]}]}`, "input.0.content.0.image_url", `"https://example.com/a.png"`, types.RelayFormatOpenAIResponses},
	} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.name, enabled), func(t *testing.T) {
				var received []byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					received, _ = io.ReadAll(r.Body)
					_, _ = w.Write([]byte(`{}`))
				}))
				defer server.Close()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
				info := &relaycommon.RelayInfo{RelayFormat: tc.format, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ChannelSetting: dto.ChannelSettings{MoonshotVideoToImages: enabled}}}
				response, err := (&Adaptor{}).DoRequest(c, info, bytes.NewReader([]byte(tc.body)))
				require.NoError(t, err)
				require.NoError(t, response.(*http.Response).Body.Close())
				if !enabled {
					assert.JSONEq(t, tc.body, string(received))
					return
				}
				assert.JSONEq(t, tc.want, gjson.GetBytes(received, tc.path).Raw)
			})
		}
	}
}

// Media URLs reach the upstream as base64 through the final-body hook in both
// protocols, and every other byte, data URLs and file IDs included, stays as
// sent. A URL used twice is downloaded once per request.
func TestMediaURLsSentAsBase64(t *testing.T) {
	previousLimit := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	originalFetch := *system_setting.GetFetchSetting()
	system_setting.GetFetchSetting().AllowPrivateIp = true
	system_setting.GetFetchSetting().AllowedPorts = []string{"1-65535"}
	t.Cleanup(func() {
		constant.MaxFileDownloadMB = previousLimit
		*system_setting.GetFetchSetting() = originalFetch
	})
	service.InitHttpClient()

	files := map[string]struct{ contentType, body string }{
		"/a.png": {"image/png", "\x89PNG\r\n\x1a\nfake png"},
		// Object storage serves files uploaded without a type like this.
		"/photo":    {"binary/octet-stream", "\xff\xd8\xff\xe0fake jpeg"},
		"/clip.mp4": {"video/mp4", "\x00\x00\x00\x18ftypmp42fake mp4"},
		"/page":     {"text/html", "<html>sign in</html>"},
	}
	var mu sync.Mutex
	downloads := map[string]int{}
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		downloads[r.URL.Path]++
		mu.Unlock()
		file, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", file.contentType)
		_, _ = w.Write([]byte(file.body))
	}))
	defer media.Close()
	fill := strings.NewReplacer(
		"{media}", media.URL,
		"{png}", base64.StdEncoding.EncodeToString([]byte(files["/a.png"].body)),
		"{jpeg}", base64.StdEncoding.EncodeToString([]byte(files["/photo"].body)),
		"{mp4}", base64.StdEncoding.EncodeToString([]byte(files["/clip.mp4"].body)),
	)

	for _, tc := range []struct {
		name, body, want, err string
		format                types.RelayFormat
		blocked, enabled      bool
		downloads             map[string]int
	}{
		{
			name:      "chat",
			format:    types.RelayFormatOpenAI,
			enabled:   true,
			body:      `{"model":"kimi-k3.1-pro","seed":9007199254740993,"messages":[{"role":"system","content":"Describe media."},{"role":"user","content":[{"type":"image_url","image_url":{"url":"{media}/a.png","detail":"low"}},{"type":"image_url","image_url":"{media}/a.png"},{"type":"video_url","video_url":{"url":"{media}/clip.mp4"}},{"type":"video_url","video_url":"{media}/clip.mp4"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},{"type":"video_url","video_url":{"url":"ms://file-1"}}]}]}`,
			want:      `{"model":"kimi-k3.1-pro","seed":9007199254740993,"messages":[{"role":"system","content":"Describe media."},{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,{png}","detail":"low"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,{png}"}},{"type":"video_url","video_url":{"url":"data:video/mp4;base64,{mp4}"}},{"type":"video_url","video_url":{"url":"data:video/mp4;base64,{mp4}"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},{"type":"video_url","video_url":{"url":"ms://file-1"}}]}]}`,
			downloads: map[string]int{"/a.png": 1, "/clip.mp4": 1},
		},
		{
			name:      "messages",
			format:    types.RelayFormatClaude,
			enabled:   true,
			body:      `{"model":"kimi-k3.1-pro","max_tokens":64,"messages":[{"role":"user","content":"Take a screenshot."},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"snap","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"shot"},{"type":"image","source":{"type":"url","url":"{media}/a.png"}}]},{"type":"image","source":{"type":"url","url":"{media}/photo"}},{"type":"video","source":{"type":"url","url":"{media}/clip.mp4"}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}},{"type":"image","source":{"type":"url","url":"ms://file-1"}}]}]}`,
			want:      `{"model":"kimi-k3.1-pro","max_tokens":64,"messages":[{"role":"user","content":"Take a screenshot."},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"snap","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"shot"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"{png}"}}]},{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"{jpeg}"}},{"type":"video","source":{"type":"base64","media_type":"video/mp4","data":"{mp4}"}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}},{"type":"image","source":{"type":"url","url":"ms://file-1"}}]}]}`,
			downloads: map[string]int{"/a.png": 1, "/photo": 1, "/clip.mp4": 1},
		},
		{
			name:   "switch off",
			format: types.RelayFormatClaude,
			body:   `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"{media}/a.png"}}]}]}`,
			want:   `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"{media}/a.png"}}]}]}`,
		},
		{
			name:    "missing file",
			format:  types.RelayFormatOpenAI,
			enabled: true,
			body:    `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"text","text":"Read it."},{"type":"image_url","image_url":{"url":"{media}/gone.png"}}]}]}`,
			err:     "messages.0.content.1: cannot download image URL: failed to download file, status code: 404",
		},
		{
			name:    "page instead of image",
			format:  types.RelayFormatClaude,
			enabled: true,
			body:    `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"{media}/page"}}]}]}`,
			err:     "messages.0.content.0: image URL returned text/html; charset=utf-8 instead of image data",
		},
		{
			name:    "private address under default SSRF protection",
			format:  types.RelayFormatOpenAI,
			enabled: true,
			blocked: true,
			body:    `{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":[{"type":"video_url","video_url":{"url":"{media}/clip.mp4"}}]}]}`,
			err:     "messages.0.content.0: cannot download video URL: failed to download file from",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			system_setting.GetFetchSetting().AllowPrivateIp = !tc.blocked
			mu.Lock()
			clear(downloads)
			mu.Unlock()
			var received []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer upstream.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			info := &relaycommon.RelayInfo{RelayFormat: tc.format, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ChannelSetting: dto.ChannelSettings{MoonshotMediaURLToBase64: tc.enabled}}}
			response, err := (&Adaptor{}).DoRequest(c, info, strings.NewReader(fill.Replace(tc.body)))
			if tc.err != "" {
				var apiErr *types.NewAPIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				assert.Contains(t, err.Error(), tc.err)
				assert.Nil(t, received, "the upstream must not be called")
				return
			}
			require.NoError(t, err)
			require.NoError(t, response.(*http.Response).Body.Close())
			assert.Equal(t, fill.Replace(tc.want), string(received))
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, len(tc.downloads), len(downloads))
			for path, count := range tc.downloads {
				assert.Equal(t, count, downloads[path], path)
			}
		})
	}
}

// Exercise the actual final-body hook and ffmpeg, including a disabled channel.
func TestVideoRelayIntegration(t *testing.T) {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skip(binary + " is required for video integration")
		}
	}
	file := filepath.Join(t.TempDir(), "clip.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=96x64:r=2:d=2", "-c:v", "mpeg4", file)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	ref := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(data)
	frames, err := extractVideoFrames(context.Background(), ref)
	require.NoError(t, err)
	require.NotEmpty(t, frames)
	img, err := jpeg.Decode(bytes.NewReader(frames[0].JPEG))
	require.NoError(t, err)
	r, g, b, _ := img.At(10, 10).RGBA()
	assert.Greater(t, r, g*2)
	assert.Greater(t, r, b*2)
	service.InitHttpClient()
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			var received []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			body, err := common.Marshal(map[string]any{"model": "kimi-k3", "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": ref}}}}}})
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: server.URL, ChannelSetting: dto.ChannelSettings{MoonshotVideoToImages: enabled}}, RelayFormat: types.RelayFormatOpenAI}
			response, err := (&Adaptor{}).DoRequest(c, info, bytes.NewReader(body))
			require.NoError(t, err)
			defer response.(*http.Response).Body.Close()
			if enabled {
				assert.NotContains(t, string(received), `"video_url"`)
				assert.Contains(t, string(received), "data:image/jpeg;base64,")
			} else {
				assert.JSONEq(t, string(body), string(received))
			}
		})
	}
}
