package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestMoonshotMessagesCacheControlWire covers the wire for a Messages request
// that marks a content block for caching. The upstream answers a Chat
// Completions content part carrying cache_control with a 400, so the mark must
// leave the body: its lifetime moves to the request level and no block keeps
// the member.
func TestMoonshotMessagesCacheControlWire(t *testing.T) {
	service.InitHttpClient()
	for _, tc := range []struct {
		name, body, wantTTL string
		blockMarks          int
	}{
		{
			name:       "message block mark is lifted to the request level",
			body:       `{"model":"kimi-k3.1-pro","max_tokens":256,"messages":[{"role":"user","content":[{"type":"text","text":"只回复OK。","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`,
			wantTTL:    "5m",
			blockMarks: 0,
		},
		{
			name:       "system block mark is lifted too",
			body:       `{"model":"kimi-k3.1-pro","max_tokens":256,"system":[{"type":"text","text":"Be helpful","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"hi"}]}`,
			wantTTL:    "1h",
			blockMarks: 0,
		},
		{
			name:       "request level mark is kept and a block mark dropped",
			body:       `{"model":"kimi-k3.1-pro","max_tokens":256,"cache_control":{"type":"ephemeral","ttl":"1h"},"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`,
			wantTTL:    "1h",
			blockMarks: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wire []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture upstream failure","type":"upstream_error"}}`)
			}))
			defer upstream.Close()
			var request dto.ClaudeRequest
			require.NoError(t, common.UnmarshalJsonStr(tc.body, &request))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMoonshot)
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "kimi-k3.1-pro")
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{MoonshotKVVEnabled: true, MoonshotK31ParamCompat: true})
			info := &relaycommon.RelayInfo{Request: &request, OriginModelName: "kimi-k3.1-pro", RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatClaude, RequestURLPath: c.Request.URL.Path}
			apiErr := ClaudeHelper(c, info)
			require.NotNil(t, apiErr)
			require.Equal(t, 502, apiErr.StatusCode, apiErr.Error())
			assert.Equal(t, tc.wantTTL, gjson.GetBytes(wire, "cache_control.ttl").String())
			marks := 0
			gjson.GetBytes(wire, "messages").ForEach(func(_, message gjson.Result) bool {
				message.Get("content").ForEach(func(_, block gjson.Result) bool {
					if block.Get("cache_control").Exists() {
						marks++
					}
					return true
				})
				return true
			})
			gjson.GetBytes(wire, "system").ForEach(func(_, block gjson.Result) bool {
				if block.Get("cache_control").Exists() {
					marks++
				}
				return true
			})
			assert.Equal(t, tc.blockMarks, marks, "a content block must not keep cache_control")
		})
	}
}
