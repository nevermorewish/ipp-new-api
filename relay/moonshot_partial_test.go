package relay

import (
	"fmt"
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
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Without the partial flag the upstream treats the prefix as a finished turn
// and answers again from scratch, so every Chat path must keep it on the wire.
func TestMoonshotPartialModeWire(t *testing.T) {
	service.InitHttpClient()
	global := model_setting.GetGlobalSettings()
	previous := global.PassThroughRequestEnabled
	global.PassThroughRequestEnabled = false
	t.Cleanup(func() { global.PassThroughRequestEnabled = previous })
	for _, tc := range []struct {
		name                     string
		kvv, passthrough, stream bool
	}{
		{name: "default"},
		{name: "KVV", kvv: true},
		{name: "KVV stream", kvv: true, stream: true},
		{name: "passthrough", passthrough: true},
	} {
		for _, model := range []string{"kimi-k3", "kimi-k3.1-pro"} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				var wire []byte
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					wire, _ = io.ReadAll(r.Body)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadGateway)
					_, _ = io.WriteString(w, `{"error":{"message":"fixture upstream failure","type":"upstream_error"}}`)
				}))
				defer upstream.Close()
				body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"完整答案必须是 MODEL READY，不要添加其他内容。"},{"role":"assistant","content":"MODEL ","partial":true}],"reasoning_effort":"low","stream":%t}`, model, tc.stream)
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.UnmarshalJsonStr(body, &request))
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
				common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMoonshot)
				common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{MoonshotKVVEnabled: tc.kvv, PassThroughBodyEnabled: tc.passthrough})
				info := &relaycommon.RelayInfo{Request: &request, OriginModelName: model, RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI, IsStream: tc.stream, RequestURLPath: c.Request.URL.Path}
				apiErr := TextHelper(c, info)
				require.NotNil(t, apiErr)
				require.Equal(t, http.StatusBadGateway, apiErr.StatusCode, apiErr.Error())
				require.Equal(t, "true", gjson.GetBytes(wire, "messages.1.partial").Raw, string(wire))
				require.Equal(t, "MODEL ", gjson.GetBytes(wire, "messages.1.content").String())
			})
		}
	}
}
