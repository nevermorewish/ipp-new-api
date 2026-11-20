package openai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/samber/hot"
)

const moonshotTTLBindingNamespace = "new-api:moonshot_cache_ttl:v1:"

type moonshotTTLUsage struct {
	Read, Write, Five, Hour int64
	Source                  string
}

// This is a relay observation of an exact prompt, never a provider cache ID.
// Only hashes and counters are retained; credentials and prompt text are not.
type moonshotTTLBinding struct {
	TTL       string `json:"ttl"`
	Tokens    int64  `json:"tokens"`
	ExpiresAt int64  `json:"expires_at_unix_ms"`
	Source    string `json:"source"`
}

type moonshotTTLAssociation struct {
	CacheKey  string `json:"cache_key"`
	Status    string `json:"status"`
	Estimated bool   `json:"estimated"`
	// The association is inferred from identical prompt inputs and reported
	// usage. It does not assert access to the provider's physical cache.
	Basis string `json:"basis"`
	*moonshotTTLBinding
}

var moonshotTTLMemory = struct {
	sync.Mutex
	cache *hot.HotCache[string, moonshotTTLBinding]
}{cache: hot.NewHotCache[string, moonshotTTLBinding](hot.LRU, 10000).Build()}

// moonshotTTLPromptKey conservatively associates identical final prompt inputs.
// It deliberately does not guess the token boundary of a partial-prefix hit.
// TTL and output transport/sampling options do not change the prompt identity.
func moonshotTTLPromptKey(info *relaycommon.RelayInfo, request map[string]any) string {
	messages, ok := request["messages"].([]any)
	if !ok || len(messages) == 0 {
		return ""
	}
	for _, key := range []string{"stream", "stream_options", "prompt_cache_options", "max_tokens", "max_completion_tokens", "temperature", "top_p", "n", "seed", "stop", "presence_penalty", "frequency_penalty", "logprobs", "top_logprobs"} {
		delete(request, key)
	}
	request["model"] = info.UpstreamModelName
	// Including the exact selected upstream key prevents a multi-key pool or
	// key rotation from attributing another account's cache lifetime to a hit.
	encoded, err := common.Marshal([]any{info.UserId, info.ChannelId, info.ChannelBaseUrl, info.ApiKey, info.Organization, request})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func moonshotTTLDuration(ttl string) time.Duration {
	switch ttl {
	case "5m":
		return 5 * time.Minute
	case "1h":
		return time.Hour
	default:
		return 0
	}
}

// transitionMoonshotTTL locks an observed cache's lifetime and renews it only
// after a real cache hit. Missing history never turns a requested TTL into
// evidence about an already-cached prefix.
func transitionMoonshotTTL(old moonshotTTLBinding, usage moonshotTTLUsage, now time.Time) (moonshotTTLBinding, moonshotTTLUsage, string) {
	matched := moonshotTTLDuration(old.TTL) > 0 && old.ExpiresAt > now.UnixMilli() && usage.Read > 0 && usage.Read <= old.Tokens
	if usage.Source == "request-inferred" {
		switch {
		case usage.Write == 0:
			usage.Source = "upstream-counts"
		case matched:
			usage.Five, usage.Hour = 0, 0
			if old.TTL == "1h" {
				usage.Hour = usage.Write
			} else {
				usage.Five = usage.Write
			}
			usage.Source = "cache-associated"
		case usage.Read > 0:
			// A partial hit with unknown lifetime may lock the new writes too.
			usage.Source = ""
		}
	}
	if usage.Write > 0 && usage.Source != "" {
		ttl := ""
		if usage.Five == usage.Write && usage.Hour == 0 {
			ttl = "5m"
		} else if usage.Hour == usage.Write && usage.Five == 0 {
			ttl = "1h"
		}
		if ttl == "" {
			// A mixed report cannot be represented by one locked cache entry.
			return moonshotTTLBinding{}, usage, "untracked"
		}
		source := usage.Source
		if matched && ttl == old.TTL && source == "cache-associated" {
			source = old.Source
		}
		return moonshotTTLBinding{TTL: ttl, Tokens: usage.Read + usage.Write, ExpiresAt: now.Add(moonshotTTLDuration(ttl)).UnixMilli(), Source: source}, usage, "write"
	}
	if usage.Write == 0 && matched {
		old.ExpiresAt = now.Add(moonshotTTLDuration(old.TTL)).UnixMilli()
		return old, usage, "hit"
	}
	return moonshotTTLBinding{}, usage, "untracked"
}

// associateMoonshotTTL uses an atomic Redis transaction across replicas. Without
// Redis, a bounded local cache supplies the same transition semantics. A storage
// error leaves the association unknown instead of inventing a binding.
func associateMoonshotTTL(c *gin.Context, usage moonshotTTLUsage) (moonshotTTLUsage, *moonshotTTLAssociation) {
	key := c.GetString(moonshotTTLKeyContext)
	if key == "" || usage.Read+usage.Write == 0 {
		_, resolved, _ := transitionMoonshotTTL(moonshotTTLBinding{}, usage, time.Now())
		return resolved, nil
	}
	var next moonshotTTLBinding
	var resolved moonshotTTLUsage
	var status string
	var err error
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
		defer cancel()
		fullKey := moonshotTTLBindingNamespace + key
		for range 3 {
			err = common.RDB.Watch(ctx, func(tx *redis.Tx) error {
				var old moonshotTTLBinding
				raw, getErr := tx.Get(ctx, fullKey).Result()
				if getErr != nil && !errors.Is(getErr, redis.Nil) {
					return getErr
				}
				if getErr == nil && common.UnmarshalJsonStr(raw, &old) != nil {
					return errors.New("invalid Moonshot cache TTL binding")
				}
				now := time.Now()
				next, resolved, status = transitionMoonshotTTL(old, usage, now)
				_, setErr := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					if status == "untracked" {
						pipe.Del(ctx, fullKey)
						return nil
					}
					encoded, marshalErr := common.Marshal(next)
					if marshalErr != nil {
						return marshalErr
					}
					pipe.Set(ctx, fullKey, encoded, time.Duration(next.ExpiresAt-now.UnixMilli())*time.Millisecond)
					return nil
				})
				return setErr
			}, fullKey)
			if !errors.Is(err, redis.TxFailedErr) {
				break
			}
		}
	} else {
		moonshotTTLMemory.Lock()
		old, _, _ := moonshotTTLMemory.cache.Get(key)
		now := time.Now()
		next, resolved, status = transitionMoonshotTTL(old, usage, now)
		if status != "untracked" {
			moonshotTTLMemory.cache.SetWithTTL(key, next, time.Duration(next.ExpiresAt-now.UnixMilli())*time.Millisecond)
		} else {
			moonshotTTLMemory.cache.Delete(key)
		}
		moonshotTTLMemory.Unlock()
	}
	association := &moonshotTTLAssociation{CacheKey: "relay-cache-" + key, Status: status, Estimated: true, Basis: "exact-prompt-observation"}
	if err != nil {
		_, resolved, _ = transitionMoonshotTTL(moonshotTTLBinding{}, usage, time.Now())
		association.Status = "untracked"
	} else if status != "untracked" {
		association.moonshotTTLBinding = &next
	}
	return resolved, association
}
