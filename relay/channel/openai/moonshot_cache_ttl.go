package openai

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

const (
	moonshotTTLContext        = "moonshot_cache_ttl_compat_ttl"
	moonshotTTLKeyContext     = "moonshot_cache_ttl_compat_key"
	moonshotTTLHeadersContext = "moonshot_cache_ttl_compat_headers"
)

func moonshotTTLCompatEnabled(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil &&
		info.ChannelType == constant.ChannelTypeMoonshot && info.ChannelSetting.MoonshotCacheTTLCompat &&
		info.RelayFormat == types.RelayFormatOpenAI && info.RelayMode == relayconstant.RelayModeChatCompletions &&
		dto.IsKimiK3FamilyModel(info.UpstreamModelName)
}

// RememberMoonshotCacheTTL reads the final request, including parameter overrides.
// Invalid values stay unknown instead of silently becoming a default lifetime.
func RememberMoonshotCacheTTL(c *gin.Context, info *relaycommon.RelayInfo, body []byte) {
	if c == nil || !moonshotTTLCompatEnabled(info) {
		return
	}
	c.Set(moonshotTTLContext, "")
	c.Set(moonshotTTLKeyContext, "")
	var req struct {
		Options *struct {
			TTL *string `json:"ttl"`
		} `json:"prompt_cache_options"`
	}
	if common.Unmarshal(body, &req) != nil {
		return
	}
	ttl := "5m"
	if req.Options != nil && req.Options.TTL != nil {
		ttl = *req.Options.TTL
	}
	if ttl == "5m" || ttl == "1h" {
		c.Set(moonshotTTLContext, ttl)
	}
	var request map[string]any
	if common.UnmarshalUseNumber(body, &request) == nil {
		c.Set(moonshotTTLKeyContext, moonshotTTLPromptKey(info, request))
	}
}

// CaptureMoonshotCacheTTLHeaders snapshots the upstream headers before the relay
// rewrites them, so the write report can still be read when the usage arrives.
// It adds no header of its own: the client reads exactly the upstream's headers.
func CaptureMoonshotCacheTTLHeaders(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) {
	if c == nil || !moonshotTTLCompatEnabled(info) || resp == nil || resp.StatusCode != http.StatusOK {
		return
	}
	c.Set(moonshotTTLHeadersContext, resp.Header.Clone())
}

// enrichMoonshotCacheTTL changes only the client-facing representation, after
// billing usage has been selected. It never invents cache reads/writes, changes
// real splits, or treats the requested lifetime as evidence of actual retention.
// upstreamUsage is the upstream usage object when body is not the upstream body
// itself; an empty string means the body already is the upstream report.
func enrichMoonshotCacheTTL(c *gin.Context, info *relaycommon.RelayInfo, body []byte, upstreamUsage string) []byte {
	if c == nil || !moonshotTTLCompatEnabled(info) {
		return body
	}
	var envelope map[string]json.RawMessage
	if common.Unmarshal(body, &envelope) != nil {
		return body
	}
	var usage map[string]json.RawMessage
	if common.Unmarshal(envelope["usage"], &usage) != nil || usage == nil {
		return body
	}
	outputUsage := usage
	if upstreamUsage != "" {
		usage = nil
		if common.UnmarshalJsonStr(upstreamUsage, &usage) != nil || usage == nil {
			return body
		}
	}
	var details map[string]json.RawMessage
	if common.Unmarshal(usage["prompt_tokens_details"], &details) != nil {
		return body
	}
	total, ok := moonshotTTLInteger(details["cache_write_tokens"])
	if !ok {
		return body
	}
	prompt, ok := moonshotTTLInteger(usage["prompt_tokens"])
	if !ok || total > prompt {
		return body
	}
	var read int64
	if cached, present := details["cached_tokens"]; present {
		var valid bool
		read, valid = moonshotTTLInteger(cached)
		if !valid || read > prompt-total {
			return body
		}
	}
	var tokens5m, tokens1h int64
	source := "request-inferred"
	// Preserve all upstream representations. Conflicting/invalid reports never
	// establish a binding and are never repaired to satisfy a detector.
	preserveCreation := false
	for _, report := range []struct {
		raw        json.RawMessage
		five, hour string
	}{
		{usage["cache_creation"], "ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"},
		{details["cache_creation"], "ephemeral_5m_input_tokens", "ephemeral_1h_input_tokens"},
		{details["cache_write_tokens_details"], "ttl_5m_tokens", "ttl_1h_tokens"},
	} {
		if len(report.raw) == 0 {
			continue
		}
		var split map[string]json.RawMessage
		if common.Unmarshal(report.raw, &split) != nil {
			return body
		}
		// DTO serialization omits zero members, so a reported single bucket is
		// complete only when its count equals the independently reported total.
		five, hour, valid := moonshotTTLBodySplit(split, total, report.five, report.hour)
		if !valid || (source == "upstream-usage" && (tokens5m != five || tokens1h != hour)) {
			return body
		}
		tokens5m, tokens1h = five, hour
		source = "upstream-usage"
		preserveCreation = preserveCreation || report.five == "ephemeral_5m_input_tokens"
	}
	if value, exists := c.Get(moonshotTTLHeadersContext); exists && source == "request-inferred" {
		headers, _ := value.(http.Header)
		if len(headers.Values(moonshotCacheWriteHeader5m)) != 0 || len(headers.Values(moonshotCacheWriteHeader1h)) != 0 {
			var valid5m, valid1h bool
			tokens5m, valid5m = moonshotTTLHeader(headers, moonshotCacheWriteHeader5m)
			tokens1h, valid1h = moonshotTTLHeader(headers, moonshotCacheWriteHeader1h)
			if !valid5m || !valid1h || tokens5m+tokens1h != total {
				return body
			}
			source = "upstream-headers"
		}
	}
	if source == "request-inferred" {
		switch c.GetString(moonshotTTLContext) {
		case "5m":
			tokens5m = total
		case "1h":
			tokens1h = total
		default:
			if total > 0 {
				return body
			}
		}
	}
	resolved, association := associateMoonshotTTL(c, moonshotTTLUsage{Read: read, Write: total, Five: tokens5m, Hour: tokens1h, Source: source})
	if resolved.Source != "" && !preserveCreation {
		creation, _ := common.Marshal(map[string]int64{"ephemeral_5m_input_tokens": resolved.Five, "ephemeral_1h_input_tokens": resolved.Hour})
		outputUsage["cache_creation"] = creation
		outputUsage["cache_ttl_source"], _ = common.Marshal(resolved.Source)
	}
	if association != nil {
		// This extension describes relay observations; standard usage counters
		// and upstream cache_creation remain unchanged.
		outputUsage["cache_ttl"], _ = common.Marshal(association)
	}
	encoded, err := common.Marshal(outputUsage)
	if err != nil {
		return body
	}
	result, err := ojson.SetRawBytes(body, "usage", encoded)
	if err != nil {
		return body
	}
	return result
}

func moonshotTTLInteger(raw json.RawMessage) (int64, bool) {
	n, err := strconv.ParseInt(string(raw), 10, 64)
	return n, err == nil && n >= 0 && n <= 9007199254740991
}

func moonshotTTLHeader(header http.Header, key string) (int64, bool) {
	values := header.Values(key)
	if len(values) != 1 {
		return 0, false
	}
	return moonshotTTLInteger(json.RawMessage(strings.TrimSpace(values[0])))
}

func moonshotTTLBodySplit(split map[string]json.RawMessage, total int64, fiveKey, hourKey string) (int64, int64, bool) {
	var a, b int64
	var ok bool
	if len(split) == 0 {
		return 0, 0, false
	}
	if raw, exists := split[fiveKey]; exists {
		a, ok = moonshotTTLInteger(raw)
		if !ok {
			return 0, 0, false
		}
	}
	if raw, exists := split[hourKey]; exists {
		b, ok = moonshotTTLInteger(raw)
		if !ok {
			return 0, 0, false
		}
	}
	if _, five := split[fiveKey]; !five {
		if _, hour := split[hourKey]; !hour {
			return 0, 0, false
		}
	}
	return a, b, a+b == total
}
