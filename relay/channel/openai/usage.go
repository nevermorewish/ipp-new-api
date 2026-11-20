package openai

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// Moonshot reports prompt_tokens as a total that already contains the cache
// writes and reads, and repeats the write count in
// Msh-Usage-Cache-Write-Tokens-5m / -1h. Those two statements must agree:
// a write can never exceed the prompt it belongs to, and a client comparing the
// header with usage.prompt_tokens_details.cache_write_tokens must see the same
// number. Upstreams break both when their accounting drifts, so the relay
// reconciles them here instead of forwarding contradictory numbers.
const (
	moonshotCacheWriteHeader5m = "Msh-Usage-Cache-Write-Tokens-5m"
	moonshotCacheWriteHeader1h = "Msh-Usage-Cache-Write-Tokens-1h"
)

// moonshotCacheWrite is the cache-write report one Moonshot response states. The
// two lifetime counts do not overlap and sum to Total.
type moonshotCacheWrite struct {
	Total    int
	Tokens5m int
	Tokens1h int
}

func (w moonshotCacheWrite) isZero() bool { return w.Total == 0 }

// resolveMoonshotCacheWrite picks the one write report a response forwards, and
// bounds it by the prompt the writes belong to.
//
// A response that states its write count in both places forwards the header
// count: it is the upstream's explicit statement of these tokens, and it may be
// the only one, since a body that omits cache_write_tokens would otherwise erase
// a write the upstream already charged for. Only a response with no write header
// falls back to the usage body.
//
// stream changes what can still be corrected. A streaming response sends its
// headers before its body, so the client has already read the header counts by
// the time the final usage arrives; there the usage is aligned to those counts
// and cannot be re-bounded, because shrinking the usage alone would create the
// very mismatch this reconciliation removes. A non-stream response still holds
// both, so the bound is applied and the headers follow it.
//
// Moonshot's prompt_tokens is a total that already contains the cached reads and
// writes, so writes larger than the remaining prompt are an upstream accounting
// error. A report that has to shrink keeps its lifetime mix, scaled
// proportionally, rather than collapsing to the 5m rate.
func resolveMoonshotCacheWrite(header http.Header, usage *dto.Usage, stream bool) moonshotCacheWrite {
	if fromHeader, ok := moonshotCacheWriteFromHeader(header); ok {
		if stream {
			return fromHeader
		}
		return boundMoonshotCacheWrite(fromHeader, usage)
	}
	return boundMoonshotCacheWrite(moonshotCacheWriteFromUsage(usage), usage)
}

// boundMoonshotCacheWrite limits a write report to the prompt the writes belong
// to, so cached reads plus writes never exceed prompt_tokens. A report that has
// to shrink keeps its lifetime mix, scaled proportionally, rather than
// collapsing to the 5m rate.
func boundMoonshotCacheWrite(write moonshotCacheWrite, usage *dto.Usage) moonshotCacheWrite {
	if usage == nil || usage.PromptTokens == 0 || write.Total <= 0 {
		return write
	}
	limit := max(usage.PromptTokens-usage.PromptTokensDetails.CachedTokens, 0)
	if write.Total <= limit {
		return write
	}
	write.Total = limit
	if write.Tokens5m+write.Tokens1h > 0 {
		scale := float64(limit) / float64(write.Tokens5m+write.Tokens1h)
		write.Tokens5m = int(float64(write.Tokens5m) * scale)
	}
	write.Tokens1h = limit - write.Tokens5m
	if write.Tokens1h < 0 {
		write.Tokens1h = 0
	}
	return write
}

// moonshotCacheWriteFromUsage reads the write report from an upstream usage
// object. Scalar and split counters describe the same tokens, so the larger is
// used and they are never added.
func moonshotCacheWriteFromUsage(usage *dto.Usage) moonshotCacheWrite {
	if usage == nil {
		return moonshotCacheWrite{}
	}
	details := usage.PromptTokensDetails
	write := moonshotCacheWrite{Total: max(details.CacheWriteTokens, details.CacheCreationTokensTotal())}
	if split := details.CacheWriteTokensDetails; split != nil {
		write.Tokens5m, write.Tokens1h = split.TTL5mTokens, split.TTL1hTokens
	}
	if write.Tokens5m < 0 {
		write.Tokens5m = 0
	}
	if write.Tokens1h < 0 {
		write.Tokens1h = 0
	}
	if split := write.Tokens5m + write.Tokens1h; split > 0 {
		write.Total = max(write.Total, split)
	} else if write.Total > 0 {
		// A total without a split is an unqualified write, billed as the default
		// short-lived lifetime.
		write.Tokens5m = write.Total
	}
	return write
}

// moonshotCacheWriteFromHeader reads the write report Moonshot repeats in its
// response headers. ok is false when the response sent neither header, and also
// when both report zero: an upstream that reported no write must not erase a
// write its usage body states.
func moonshotCacheWriteFromHeader(header http.Header) (moonshotCacheWrite, bool) {
	tokens5m, has5m := moonshotCacheWriteHeader(header, moonshotCacheWriteHeader5m)
	tokens1h, has1h := moonshotCacheWriteHeader(header, moonshotCacheWriteHeader1h)
	if !has5m && !has1h {
		return moonshotCacheWrite{}, false
	}
	write := moonshotCacheWrite{Tokens5m: tokens5m, Tokens1h: tokens1h, Total: tokens5m + tokens1h}
	return write, !write.isZero()
}

// setMoonshotCacheWrite stores a report on the usage the relay bills, so the
// typed usage and a forwarded usage object break the tokens down the same way.
func setMoonshotCacheWrite(usage *dto.Usage, write moonshotCacheWrite) {
	if usage == nil {
		return
	}
	details := &usage.PromptTokensDetails
	details.CacheWriteTokens = write.Total
	if write.isZero() {
		details.CacheWriteTokensDetails = nil
		return
	}
	details.CacheWriteTokensDetails = &dto.CacheWriteTokenDetails{
		TTL5mTokens: write.Tokens5m,
		TTL1hTokens: write.Tokens1h,
	}
}

// setMoonshotCacheWriteHeaders rewrites the write headers a response carries, so
// a non-stream client reads the same counts its body states. A response that
// sent neither header does not gain one.
func setMoonshotCacheWriteHeaders(header http.Header, write moonshotCacheWrite) {
	if header == nil {
		return
	}
	if header.Get(moonshotCacheWriteHeader5m) == "" && header.Get(moonshotCacheWriteHeader1h) == "" {
		return
	}
	header.Set(moonshotCacheWriteHeader5m, strconv.Itoa(write.Tokens5m))
	header.Set(moonshotCacheWriteHeader1h, strconv.Itoa(write.Tokens1h))
}

// rewriteMoonshotCacheWriteUsageJSON replaces the cache-write members of one
// upstream usage object with the resolved report. The object is forwarded
// verbatim otherwise, so fields this DTO does not model survive untouched.
func rewriteMoonshotCacheWriteUsageJSON(usageJSON string, write moonshotCacheWrite) string {
	if usageJSON == "" {
		return usageJSON
	}
	payload := []byte(usageJSON)
	if !ojson.ValidBytes(payload) {
		return usageJSON
	}
	var err error
	if payload, err = ojson.SetBytes(payload, "cache_write_tokens", write.Total); err != nil {
		return usageJSON
	}
	if write.isZero() {
		if payload, err = ojson.DeleteBytes(payload, "cache_write_tokens_details"); err != nil {
			return usageJSON
		}
		return string(payload)
	}
	details, err := common.Marshal(dto.CacheWriteTokenDetails{
		TTL5mTokens: write.Tokens5m,
		TTL1hTokens: write.Tokens1h,
	})
	if err != nil {
		return usageJSON
	}
	if payload, err = ojson.SetRawBytes(payload, "cache_write_tokens_details", details); err != nil {
		return usageJSON
	}
	return string(payload)
}

func moonshotCacheWriteHeader(header http.Header, name string) (int, bool) {
	if header == nil {
		return 0, false
	}
	value, ok := header[http.CanonicalHeaderKey(name)]
	if !ok || len(value) == 0 {
		return 0, false
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value[0]))
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func applyUsagePostProcessing(info *relaycommon.RelayInfo, usage *dto.Usage, responseBody []byte) {
	if info == nil || usage == nil {
		return
	}

	switch info.ChannelType {
	case constant.ChannelTypeDeepSeek:
		if usage.PromptTokensDetails.CachedTokens == 0 && usage.PromptCacheHitTokens != 0 {
			usage.PromptTokensDetails.CachedTokens = usage.PromptCacheHitTokens
		}
	case constant.ChannelTypeZhipu_v4:
		// 智普的cached_tokens在标准位置: usage.prompt_tokens_details.cached_tokens
		if usage.PromptTokensDetails.CachedTokens == 0 {
			if usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > 0 {
				usage.PromptTokensDetails.CachedTokens = usage.InputTokensDetails.CachedTokens
			} else if cachedTokens, ok := extractCachedTokensFromBody(responseBody); ok {
				usage.PromptTokensDetails.CachedTokens = cachedTokens
			} else if usage.PromptCacheHitTokens > 0 {
				usage.PromptTokensDetails.CachedTokens = usage.PromptCacheHitTokens
			}
		}
	case constant.ChannelTypeMoonshot:
		// Moonshot的cached_tokens在非标准位置: choices[].usage.cached_tokens
		if usage.PromptTokensDetails.CachedTokens == 0 {
			if usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > 0 {
				usage.PromptTokensDetails.CachedTokens = usage.InputTokensDetails.CachedTokens
			} else if cachedTokens, ok := extractMoonshotCachedTokensFromBody(responseBody); ok {
				usage.PromptTokensDetails.CachedTokens = cachedTokens
			} else if cachedTokens, ok := extractCachedTokensFromBody(responseBody); ok {
				usage.PromptTokensDetails.CachedTokens = cachedTokens
			} else if usage.PromptCacheHitTokens > 0 {
				usage.PromptTokensDetails.CachedTokens = usage.PromptCacheHitTokens
			}
		}
	case constant.ChannelTypeOpenAI:
		if usage.PromptTokensDetails.CachedTokens == 0 {
			if cachedTokens, ok := extractLlamaCachedTokensFromBody(responseBody); ok {
				usage.PromptTokensDetails.CachedTokens = cachedTokens
			}
		}
	}
}

func extractCachedTokensFromBody(body []byte) (int, bool) {
	if len(body) == 0 {
		return 0, false
	}

	var payload struct {
		Usage struct {
			PromptTokensDetails struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CachedTokens         *int `json:"cached_tokens"`
			PromptCacheHitTokens *int `json:"prompt_cache_hit_tokens"`
		} `json:"usage"`
	}

	if err := common.Unmarshal(body, &payload); err != nil {
		return 0, false
	}

	if payload.Usage.PromptTokensDetails.CachedTokens != nil {
		return *payload.Usage.PromptTokensDetails.CachedTokens, true
	}
	if payload.Usage.CachedTokens != nil {
		return *payload.Usage.CachedTokens, true
	}
	if payload.Usage.PromptCacheHitTokens != nil {
		return *payload.Usage.PromptCacheHitTokens, true
	}
	return 0, false
}

// extractMoonshotCachedTokensFromBody 从Moonshot的非标准位置提取cached_tokens
// Moonshot的流式响应格式: {"choices":[{"usage":{"cached_tokens":111}}]}
func extractMoonshotCachedTokensFromBody(body []byte) (int, bool) {
	if len(body) == 0 {
		return 0, false
	}

	var payload struct {
		Choices []struct {
			Usage struct {
				CachedTokens *int `json:"cached_tokens"`
			} `json:"usage"`
		} `json:"choices"`
	}

	if err := common.Unmarshal(body, &payload); err != nil {
		return 0, false
	}

	// 遍历choices查找cached_tokens
	for _, choice := range payload.Choices {
		if choice.Usage.CachedTokens != nil && *choice.Usage.CachedTokens > 0 {
			return *choice.Usage.CachedTokens, true
		}
	}

	return 0, false
}

// extractLlamaCachedTokensFromBody 从llama.cpp的非标准位置提取cache_n
func extractLlamaCachedTokensFromBody(body []byte) (int, bool) {
	if len(body) == 0 {
		return 0, false
	}

	var payload struct {
		Timings struct {
			CachedTokens *int `json:"cache_n"`
		} `json:"timings"`
	}

	if err := common.Unmarshal(body, &payload); err != nil {
		return 0, false
	}

	if payload.Timings.CachedTokens == nil {
		return 0, false
	}
	return *payload.Timings.CachedTokens, true
}
