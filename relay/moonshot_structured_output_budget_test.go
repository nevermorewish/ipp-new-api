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

// Exercise the final HTTP body, including the second KVV conversion after
// channel parameter overrides. A client budget must not be silently enlarged.
func TestMoonshotKVVStructuredOutputBudgetWire(t *testing.T) {
	service.InitHttpClient()
	global := model_setting.GetGlobalSettings()
	previous := global.PassThroughRequestEnabled
	global.PassThroughRequestEnabled = false
	t.Cleanup(func() { global.PassThroughRequestEnabled = previous })
	for _, tc := range []struct {
		name, limits, format string
		override             bool
	}{
		{name: "reported 200 token budget", limits: `"max_tokens":200`, format: `{"type":"json_object"}`},
		{name: "completion budget", limits: `"max_completion_tokens":4096`, format: `{"type":"json_object"}`},
		{name: "explicit zero", limits: `"max_completion_tokens":0`, format: `{"type":"json_object"}`},
		{name: "both budgets", limits: `"max_tokens":200,"max_completion_tokens":4096`, format: `{"type":"json_object"}`},
		{name: "schema", limits: `"max_completion_tokens":4096`, format: `{"type":"json_schema","json_schema":{"name":"person","strict":true,"schema":{"type":"object","properties":{"name":{"type":"string"},"age":{"type":"integer"}},"required":["name","age"],"additionalProperties":false}}}`},
		{name: "explicit channel override", limits: `"max_tokens":200`, format: `{"type":"json_object"}`, override: true},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				received := make(chan []byte, 1)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					wire, err := io.ReadAll(r.Body)
					if err == nil {
						received <- wire
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadGateway)
					_, _ = io.WriteString(w, `{"error":{"message":"fixture upstream failure","type":"upstream_error"}}`)
				}))
				defer upstream.Close()
				body := fmt.Sprintf(`{"model":"kimi-k3.1-pro","messages":[{"role":"user","content":"Return JSON with name and age."}],"response_format":%s,"stream":%t,%s}`, tc.format, stream, tc.limits)
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.UnmarshalJsonStr(body, &request))
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
				common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMoonshot)
				common.SetContextKey(c, constant.ContextKeyOriginalModel, "kimi-k3.1-pro")
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
				common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{MoonshotKVVEnabled: true})
				if tc.override {
					common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"max_tokens": 4096})
				}
				info := &relaycommon.RelayInfo{Request: &request, OriginModelName: "kimi-k3.1-pro", RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI, IsStream: stream, RequestURLPath: c.Request.URL.Path}
				apiErr := TextHelper(c, info)
				require.NotNil(t, apiErr)
				require.Equal(t, http.StatusBadGateway, apiErr.StatusCode, apiErr.Error())
				require.Len(t, received, 1)
				wire := <-received
				require.JSONEq(t, tc.format, gjson.GetBytes(wire, "response_format").Raw)
				for _, field := range []string{"max_tokens", "max_completion_tokens"} {
					expected := gjson.Get(body, field).Raw
					if tc.override && field == "max_tokens" {
						expected = "4096"
					}
					require.Equal(t, expected, gjson.GetBytes(wire, field).Raw, field)
				}
				require.Equal(t, stream, gjson.GetBytes(wire, "stream").Bool())
				require.Equal(t, "enabled", gjson.GetBytes(wire, "thinking.type").String())
				require.Equal(t, "max", gjson.GetBytes(wire, "reasoning_effort").String())
			})
		}
	}
}
