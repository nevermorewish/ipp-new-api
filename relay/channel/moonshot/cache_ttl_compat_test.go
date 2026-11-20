package moonshot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The streaming scanner reads the configured idle timeout; tests do not run the
// env init that sets it, so supply one for this package's streaming fixtures.
func init() {
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 30
	}
}

func TestMoonshotCacheTTLCompatibility(t *testing.T) {
	for _, streamShape := range []string{"json", "inline", "standalone"} {
		for _, tc := range []struct {
			name, options, details, source string
			headers                        http.Header
			enabled, wantTTL               bool
			five, hour                     int64
		}{
			{name: "off leaves missing evidence absent", options: `{"ttl":"1h"}`, details: `{"cache_write_tokens":512}`, enabled: false},
			{name: "default lifetime is labelled as inferred", options: `{}`, details: `{"cache_write_tokens":512}`, enabled: true, wantTTL: true, five: 512, source: "request-inferred"},
			{name: "explicit hour is labelled as inferred", options: `{"ttl":"1h"}`, details: `{"cache_write_tokens":512}`, enabled: true, wantTTL: true, hour: 512, source: "request-inferred"},
			{name: "explicit zero writes retain both zero buckets", options: `{"ttl":"1h"}`, details: `{"cached_tokens":512,"cache_write_tokens":0}`, enabled: true, wantTTL: true, source: "upstream-counts"},
			{name: "reported split wins over requested lifetime", options: `{"ttl":"1h"}`, details: `{"cache_write_tokens":512,"cache_write_tokens_details":{"ttl_5m_tokens":512,"ttl_1h_tokens":0}}`, enabled: true, wantTTL: true, five: 512, source: "upstream-usage"},
			{name: "real headers win over requested lifetime", options: `{"ttl":"1h"}`, details: `{"cache_write_tokens":512}`, headers: http.Header{"Msh-Usage-Cache-Write-Tokens-5m": {"512"}, "Msh-Usage-Cache-Write-Tokens-1h": {"0"}, "Msh-Request-Id": {"official-id"}}, enabled: true, wantTTL: true, five: 512, source: "upstream-headers"},
			{name: "missing total is not fabricated", options: `{"ttl":"1h"}`, details: `{"cached_tokens":512}`, enabled: true},
			{name: "invalid total stays visible", options: `{"ttl":"1h"}`, details: `{"cache_write_tokens":-1}`, enabled: true},
			{name: "unknown lifetime remains unknown", options: `{"ttl":"2h"}`, details: `{"cache_write_tokens":512}`, enabled: true},
			{name: "partial headers are not evidence", options: `{"ttl":"1h"}`, details: `{"cache_write_tokens":512}`, headers: http.Header{"Msh-Usage-Cache-Write-Tokens-5m": {"512"}}, enabled: true},
		} {
			t.Run(streamShape+"/"+tc.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
					IsStream: streamShape != "json", ShouldIncludeUsage: true, DisablePing: true,
					ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: "kimi-k3.1-pro", ChannelSetting: dto.ChannelSettings{MoonshotCacheTTLCompat: tc.enabled, ForceFormat: streamShape == "standalone"}}}
				openai.RememberMoonshotCacheTTL(c, info, []byte(`{"prompt_cache_options":`+tc.options+`}`))
				usage := `{"prompt_tokens":1024,"completion_tokens":1,"total_tokens":1025,"provider_extension":"preserved","prompt_tokens_details":` + tc.details + `}`
				body := `{"id":"chatcmpl-kimi","model":"kimi-k3.1-pro","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":` + usage + `}`
				if streamShape != "json" {
					finish := `{"id":"chatcmpl-kimi","model":"kimi-k3.1-pro","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}]`
					if streamShape == "inline" {
						body = "data: " + finish + `,"usage":` + usage + "}\n\n"
					} else {
						body = "data: " + finish + "}\n\ndata: " + `{"id":"chatcmpl-kimi","model":"kimi-k3.1-pro","choices":[],"usage":` + usage + "}\n\n"
					}
					body += "data: [DONE]\n\n"
				}
				headers := tc.headers.Clone()
				if headers == nil {
					headers = make(http.Header)
				}
				resp := &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
				billed, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
				require.Nil(t, apiErr)
				result := recorder.Result()
				defer result.Body.Close()
				var final string
				if streamShape == "json" {
					final = recorder.Body.String()
				} else {
					count := 0
					for _, line := range strings.Split(recorder.Body.String(), "\n") {
						data := strings.TrimPrefix(line, "data: ")
						if json.Valid([]byte(data)) && ojson.Get(data, "usage").IsObject() {
							count++
							final = data
						}
					}
					require.Equal(t, 1, count, "one final usage frame")
					assert.Contains(t, recorder.Body.String(), "data: [DONE]")
				}
				assert.Equal(t, tc.wantTTL, ojson.Get(final, "usage.cache_creation").Exists(), final)
				if tc.wantTTL {
					assert.Equal(t, "preserved", ojson.Get(final, "usage.provider_extension").String())
					assert.True(t, ojson.Get(final, "usage.cache_creation.ephemeral_5m_input_tokens").Exists())
					assert.True(t, ojson.Get(final, "usage.cache_creation.ephemeral_1h_input_tokens").Exists())
					assert.Equal(t, tc.five, ojson.Get(final, "usage.cache_creation.ephemeral_5m_input_tokens").Int())
					assert.Equal(t, tc.hour, ojson.Get(final, "usage.cache_creation.ephemeral_1h_input_tokens").Int())
					assert.Equal(t, tc.source, ojson.Get(final, "usage.cache_ttl_source").String())
					assert.False(t, ojson.Get(final, "usage.cache_ttl_simulated").Exists())
					// Simulation changes no cache token totals used for billing.
					assert.Equal(t, int(tc.five+tc.hour), billed.(*dto.Usage).PromptTokensDetails.CacheWriteTokens)
				}
				// The relay adds no header of its own: the client reads exactly
				// what the upstream sent, including when there is none.
				if tc.headers.Get("Msh-Request-Id") != "" {
					assert.Equal(t, "official-id", result.Header.Get("Msh-Request-Id"))
				} else {
					assert.Empty(t, result.Header.Get("Msh-Request-Id"))
				}
				assert.Empty(t, result.Header.Get("X-Huanxing-Cache-TTL-Mode"))
				assert.Empty(t, result.Header.Get("X-Huanxing-Msh-Request-Id-Source"))
			})
		}
	}
}

func TestMoonshotCacheTTLCompatibilityDoesNotApplyToOtherModelsOrProtocols(t *testing.T) {
	for _, tc := range []struct {
		model string
		mode  int
	}{
		{"moonshot-v1-8k", relayconstant.RelayModeChatCompletions},
		{"kimi-k3.1-pro", relayconstant.RelayModeResponses},
	} {
		t.Run(fmt.Sprint(tc.model), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			info := &relaycommon.RelayInfo{RelayMode: tc.mode, RelayFormat: types.RelayFormatOpenAI,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeMoonshot, UpstreamModelName: tc.model, ChannelSetting: dto.ChannelSettings{MoonshotCacheTTLCompat: true}}}
			openai.RememberMoonshotCacheTTL(c, info, []byte(`{"messages":[{"role":"user","content":"other"}],"prompt_cache_options":{"ttl":"1h"}}`))
			resp := &http.Response{StatusCode: 200, Header: make(http.Header)}
			openai.CaptureMoonshotCacheTTLHeaders(c, info, resp)
			assert.Empty(t, resp.Header)
			if tc.mode != relayconstant.RelayModeChatCompletions {
				return
			}
			body := `{"id":"ttl-limit","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":16,"prompt_tokens_details":{"cache_write_tokens":8}}}`
			resp.Body = io.NopCloser(strings.NewReader(body))
			_, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
			assert.Nil(t, apiErr)
			assert.JSONEq(t, body, recorder.Body.String(), "the setting leaves other models untouched")
		})
	}
}

func ttlBindingResponse(t *testing.T, info *relaycommon.RelayInfo, request, details, stream string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info.IsStream = stream != "json"
	openai.RememberMoonshotCacheTTL(c, info, []byte(request))
	usage := `{"prompt_tokens":1024,"completion_tokens":1,"total_tokens":1025,"prompt_tokens_details":` + details + `}`
	body := `{"id":"ttl-binding","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":` + usage + `}`
	if stream == "inline" {
		body = "data: " + `{"id":"ttl-binding","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}],"usage":` + usage + "}\n\ndata: [DONE]\n\n"
	} else if stream == "standalone" {
		body = "data: " + `{"id":"ttl-binding","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}]}` + "\n\ndata: " + `{"id":"ttl-binding","choices":[],"usage":` + usage + "}\n\ndata: [DONE]\n\n"
	}
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
	billed, apiErr := (&Adaptor{}).DoResponse(c, resp, info)
	require.Nil(t, apiErr)
	assert.Equal(t, int(ojson.Get(details, "cache_write_tokens").Int()), billed.(*dto.Usage).PromptTokensDetails.CacheWriteTokens)
	assert.Equal(t, int(ojson.Get(details, "cached_tokens").Int()), billed.(*dto.Usage).PromptTokensDetails.CachedTokens)
	if stream == "json" {
		return recorder.Body.String()
	}
	var final string
	for line := range strings.SplitSeq(recorder.Body.String(), "\n") {
		data := strings.TrimPrefix(line, "data: ")
		if ojson.Get(data, "usage").IsObject() {
			require.Empty(t, final, "only one usage frame")
			final = data
		}
	}
	require.NotEmpty(t, final)
	return final
}

func TestMoonshotCacheTTLBinding(t *testing.T) {
	previousRedis, previousEnabled := common.RDB, common.RedisEnabled
	t.Cleanup(func() { common.RDB, common.RedisEnabled = previousRedis, previousEnabled })
	for _, storage := range []string{"redis", "memory"} {
		t.Run(storage, func(t *testing.T) {
			var server *miniredis.Miniredis
			if storage == "redis" {
				server = miniredis.RunT(t)
				common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
				t.Cleanup(func() { _ = common.RDB.Close() })
				common.RedisEnabled = true
			} else {
				common.RDB, common.RedisEnabled = nil, false
			}
			for _, stream := range []string{"json", "inline", "standalone"} {
				t.Run(stream, func(t *testing.T) {
					check := func(t *testing.T) {
						info := &relaycommon.RelayInfo{UserId: 901, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions, ShouldIncludeUsage: true, DisablePing: true,
							ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 145, ChannelType: constant.ChannelTypeMoonshot, ApiKey: "fixture-upstream-key", ChannelBaseUrl: "https://upstream.invalid", UpstreamModelName: "kimi-k3.1-pro", ChannelSetting: dto.ChannelSettings{MoonshotCacheTTLCompat: true}}}
						request := fmt.Sprintf(`{"messages":[{"role":"user","content":%q}],"prompt_cache_options":{"ttl":"5m"}}`, t.Name())
						first := ttlBindingResponse(t, info, request, `{"cache_write_tokens":512}`, stream)
						id := ojson.Get(first, "usage.cache_ttl.cache_key").String()
						require.NotEmpty(t, id)
						assert.Equal(t, "write", ojson.Get(first, "usage.cache_ttl.status").String())
						assert.Equal(t, "5m", ojson.Get(first, "usage.cache_ttl.ttl").String())
						assert.False(t, ojson.Get(first, "usage.cache_ttl.simulated").Exists())
						assert.Equal(t, "request-inferred", ojson.Get(first, "usage.cache_ttl.source").String())
						assert.False(t, ojson.Get(first, "usage.cache_ttl.estimated").Exists())
						advance := func(d time.Duration) {
							if server != nil {
								server.FastForward(d)
							} else {
								time.Sleep(d)
							}
						}
						advance(time.Minute)
						changedTTL := strings.Replace(request, `"5m"`, `"1h"`, 1)
						changedTTL = strings.Replace(changedTTL, `"messages":`, `"stream":true,"max_completion_tokens":512,"messages":`, 1)
						hit := ttlBindingResponse(t, info, changedTTL, `{"cached_tokens":512,"cache_write_tokens":0}`, stream)
						assert.Equal(t, id, ojson.Get(hit, "usage.cache_ttl.cache_key").String())
						assert.Equal(t, "hit", ojson.Get(hit, "usage.cache_ttl.status").String())
						assert.False(t, ojson.Get(hit, "usage.cache_ttl.simulated").Exists())
						assert.Equal(t, "5m", ojson.Get(hit, "usage.cache_ttl.ttl").String(), "request cannot change a live cache's TTL")
						assert.Zero(t, ojson.Get(hit, "usage.cache_creation.ephemeral_1h_input_tokens").Int())
						if server != nil {
							assert.Equal(t, 5*time.Minute, server.TTL("new-api:moonshot_cache_ttl:v1:"+strings.TrimPrefix(id, "relay-cache-")))
						} else {
							assert.Equal(t, int64(time.Minute/time.Millisecond), ojson.Get(hit, "usage.cache_ttl.expires_at_unix_ms").Int()-ojson.Get(first, "usage.cache_ttl.expires_at_unix_ms").Int())
						}
						partial := ttlBindingResponse(t, info, changedTTL, `{"cached_tokens":512,"cache_write_tokens":256}`, stream)
						assert.Equal(t, int64(256), ojson.Get(partial, "usage.cache_creation.ephemeral_5m_input_tokens").Int())
						assert.Zero(t, ojson.Get(partial, "usage.cache_creation.ephemeral_1h_input_tokens").Int())
						assert.Equal(t, "cache-associated", ojson.Get(partial, "usage.cache_ttl_source").String())
						advance(6 * time.Minute)
						expired := ttlBindingResponse(t, info, changedTTL, `{"cached_tokens":768,"cache_write_tokens":0}`, stream)
						assert.Equal(t, "untracked", ojson.Get(expired, "usage.cache_ttl.status").String())
						assert.False(t, ojson.Get(expired, "usage.cache_ttl.ttl").Exists(), "a hit with no history does not inherit the request TTL")
						rewritten := ttlBindingResponse(t, info, changedTTL, `{"cache_write_tokens":768}`, stream)
						assert.Equal(t, "1h", ojson.Get(rewritten, "usage.cache_ttl.ttl").String(), "new writes after expiration can establish a new TTL")
					}
					if storage == "memory" {
						synctest.Test(t, check)
					} else {
						check(t)
					}
				})
			}
		})
	}
}

func TestMoonshotCacheTTLBindingIsolationAndEvidence(t *testing.T) {
	previousRedis, previousEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = nil, false
	t.Cleanup(func() { common.RDB, common.RedisEnabled = previousRedis, previousEnabled })
	info := &relaycommon.RelayInfo{UserId: 902, RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions, ShouldIncludeUsage: true, DisablePing: true,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 145, ChannelType: constant.ChannelTypeMoonshot, ApiKey: "key-a", UpstreamModelName: "kimi-k3.1-pro", ChannelSetting: dto.ChannelSettings{MoonshotCacheTTLCompat: true}}}
	request := `{"messages":[{"role":"user","content":"binding-isolation"}],"prompt_cache_options":{"ttl":"5m"}}`
	first := ttlBindingResponse(t, info, request, `{"cache_write_tokens":512,"cache_creation":{"ephemeral_1h_input_tokens":512}}`, "json")
	assert.Equal(t, "1h", ojson.Get(first, "usage.cache_ttl.ttl").String(), "actual TTL wins")
	assert.False(t, ojson.Get(first, "usage.cache_ttl.simulated").Exists())
	assert.Equal(t, "upstream-usage", ojson.Get(first, "usage.cache_ttl.source").String())
	partial := ttlBindingResponse(t, info, request, `{"cached_tokens":512,"cache_write_tokens":256}`, "standalone")
	assert.Equal(t, int64(256), ojson.Get(partial, "usage.cache_creation.ephemeral_1h_input_tokens").Int())
	for _, changed := range []string{"user", "channel", "key", "endpoint", "model", "prompt", "tools"} {
		t.Run(changed, func(t *testing.T) {
			other := *info
			meta := *info.ChannelMeta
			other.ChannelMeta = &meta
			body := request
			switch changed {
			case "user":
				other.UserId++
			case "channel":
				other.ChannelId++
			case "key":
				other.ApiKey = "key-b"
			case "endpoint":
				other.ChannelBaseUrl = "https://other-upstream.invalid"
			case "model":
				other.UpstreamModelName = "kimi-k3"
			case "prompt":
				body = strings.Replace(body, "binding-isolation", "different-prompt", 1)
			case "tools":
				body = strings.Replace(body, `"messages":`, `"tools":[{"type":"function","function":{"name":"new_tool"}}],"messages":`, 1)
			}
			response := ttlBindingResponse(t, &other, body, `{"cached_tokens":512,"cache_write_tokens":256}`, "json")
			assert.Equal(t, "untracked", ojson.Get(response, "usage.cache_ttl.status").String())
			assert.False(t, ojson.Get(response, "usage.cache_ttl.ttl").Exists())
			assert.False(t, ojson.Get(response, "usage.cache_creation").Exists(), "unknown partial hit must not be assigned the new request TTL")
		})
	}
}
