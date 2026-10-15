package agent

import (
	"encoding/json"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openai"
	openaisdk "github.com/charmbracelet/openai-go"
	"github.com/charmbracelet/openai-go/packages/respjson"
)

// cacheAwareUsage extracts prompt-cache reads from OpenAI-compatible usage,
// covering the dialects that deviate from the OpenAI shape. Providers on
// this path report cache hits as prompt_tokens_details.cached_tokens
// (OpenAI and gateways that normalize to it), as prompt_cache_hit_tokens
// (DeepSeek), or as a top-level cached_tokens (Kimi/Moonshot). fantasy's
// default extraction only understands the first, so on the others the
// cache reads stay folded into the input tokens: cost accounting bills
// them at the full input price and the cached-share metric reads zero.
//
// Precedence follows the nullish chain other clients use: the standard
// field wins whenever it is present, even as an explicit 0, and only its
// absence falls through to the alternate dialects.
func cacheAwareUsage(response openaisdk.ChatCompletion) (fantasy.Usage, fantasy.ProviderOptionsData) {
	usage, metadata := openai.DefaultUsageFunc(response)
	usage = adoptFallbackCacheRead(usage, response.Usage.PromptTokensDetails.JSON.CachedTokens.Valid(), response.Usage.JSON.ExtraFields)
	return usage, metadata
}

// cacheAwareStreamUsage is the streaming counterpart of cacheAwareUsage.
func cacheAwareStreamUsage(chunk openaisdk.ChatCompletionChunk, ctx map[string]any, metadata fantasy.ProviderMetadata) (fantasy.Usage, fantasy.ProviderMetadata) {
	usage, metadata := openai.DefaultStreamUsageFunc(chunk, ctx, metadata)
	usage = adoptFallbackCacheRead(usage, chunk.Usage.PromptTokensDetails.JSON.CachedTokens.Valid(), chunk.Usage.JSON.ExtraFields)
	return usage, metadata
}

// adoptFallbackCacheRead folds a non-standard cache-read field into the
// usage when the standard field is absent. The provider reports
// prompt_tokens including cached tokens, and the default extraction only
// subtracts the standard field, so the adopted count must come out of the
// uncached input as well.
//
// Counts outside [0, prompt tokens] are repaired rather than trusted: a
// negative count would both inflate the input tokens and be billed as a
// negative cache read, and a count larger than the prompt would inflate
// the cached-share metric.
func adoptFallbackCacheRead(usage fantasy.Usage, standardPresent bool, extraFields map[string]respjson.Field) fantasy.Usage {
	if standardPresent {
		if usage.CacheReadTokens < 0 {
			usage.InputTokens = max(usage.InputTokens+usage.CacheReadTokens, 0)
			usage.CacheReadTokens = 0
		}
		return usage
	}
	read, ok := fallbackCacheRead(extraFields)
	if !ok {
		return usage
	}
	read = min(max(read, 0), max(usage.InputTokens, 0))
	usage.CacheReadTokens = read
	usage.InputTokens = max(usage.InputTokens-read, 0)
	return usage
}

// fallbackCacheRead reads the cache-read token count out of the
// non-standard usage fields providers attach, in dialect order.
func fallbackCacheRead(extraFields map[string]respjson.Field) (int64, bool) {
	fields := openai.ExtractExtraFields(extraFields)
	for _, key := range []string{"prompt_cache_hit_tokens", "cached_tokens"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var read int64
		if err := json.Unmarshal(raw, &read); err != nil {
			continue
		}
		return read, true
	}
	return 0, false
}

// clampUsageCounters floors provider-reported counters at zero before
// they are priced and accumulated. A negative cache read would
// otherwise reduce session totals and produce a negative cost credit.
func clampUsageCounters(u fantasy.Usage) fantasy.Usage {
	u.InputTokens = max(u.InputTokens, 0)
	u.CacheReadTokens = max(u.CacheReadTokens, 0)
	u.CacheCreationTokens = max(u.CacheCreationTokens, 0)
	return u
}
