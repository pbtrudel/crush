package agent

import (
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	openaisdk "github.com/charmbracelet/openai-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func usageFromCompletion(t *testing.T, body string) fantasy.Usage {
	t.Helper()
	var response openaisdk.ChatCompletion
	require.NoError(t, json.Unmarshal([]byte(body), &response))
	usage, _ := cacheAwareUsage(response)
	return usage
}

func usageFromChunk(t *testing.T, body string) fantasy.Usage {
	t.Helper()
	var chunk openaisdk.ChatCompletionChunk
	require.NoError(t, json.Unmarshal([]byte(body), &chunk))
	usage, _ := cacheAwareStreamUsage(chunk, nil, nil)
	return usage
}

// TestCacheAwareUsageDialects pins the cache-read extraction across the
// usage dialects OpenAI-compatible providers actually send. Without the
// fallbacks the DeepSeek and Kimi shapes stay folded into the input
// tokens, so cache hits on those providers are billed at the full input
// price and read as zero in the cached-share metric.
func TestCacheAwareUsageDialects(t *testing.T) {
	t.Run("standard field wins and input excludes cached", func(t *testing.T) {
		usage := usageFromCompletion(t, `{
			"id":"c1","object":"chat.completion","created":1,"model":"m",
			"choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,
				"prompt_tokens_details":{"cached_tokens":800}}
		}`)
		assert.Equal(t, int64(800), usage.CacheReadTokens)
		assert.Equal(t, int64(200), usage.InputTokens)
	})

	t.Run("deepseek prompt_cache_hit_tokens", func(t *testing.T) {
		usage := usageFromCompletion(t, `{
			"id":"c2","object":"chat.completion","created":1,"model":"deepseek-v4-flash",
			"choices":[],
			"usage":{"prompt_tokens":4000,"completion_tokens":20,"total_tokens":4020,
				"prompt_cache_hit_tokens":3000,"prompt_cache_miss_tokens":1000}
		}`)
		assert.Equal(t, int64(3000), usage.CacheReadTokens)
		assert.Equal(t, int64(1000), usage.InputTokens)
	})

	t.Run("kimi top-level cached_tokens", func(t *testing.T) {
		usage := usageFromCompletion(t, `{
			"id":"c3","object":"chat.completion","created":1,"model":"kimi-k3",
			"choices":[],
			"usage":{"prompt_tokens":2500,"completion_tokens":5,"total_tokens":2505,
				"cached_tokens":2000}
		}`)
		assert.Equal(t, int64(2000), usage.CacheReadTokens)
		assert.Equal(t, int64(500), usage.InputTokens)
	})

	t.Run("explicit zero standard field stops the chain", func(t *testing.T) {
		usage := usageFromCompletion(t, `{
			"id":"c4","object":"chat.completion","created":1,"model":"m",
			"choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":1,"total_tokens":1001,
				"prompt_tokens_details":{"cached_tokens":0},
				"prompt_cache_hit_tokens":700}
		}`)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(1000), usage.InputTokens)
	})

	t.Run("no cache fields leaves usage untouched", func(t *testing.T) {
		usage := usageFromCompletion(t, `{
			"id":"c5","object":"chat.completion","created":1,"model":"m",
			"choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010}
		}`)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(1000), usage.InputTokens)
	})

	t.Run("stream chunk deepseek shape", func(t *testing.T) {
		usage := usageFromChunk(t, `{
			"id":"c6","object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash",
			"choices":[],
			"usage":{"prompt_tokens":4000,"completion_tokens":20,"total_tokens":4020,
				"prompt_cache_hit_tokens":3000}
		}`)
		assert.Equal(t, int64(3000), usage.CacheReadTokens)
		assert.Equal(t, int64(1000), usage.InputTokens)
	})

	t.Run("stream chunk standard shape", func(t *testing.T) {
		usage := usageFromChunk(t, `{
			"id":"c7","object":"chat.completion.chunk","created":1,"model":"m",
			"choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,
				"prompt_tokens_details":{"cached_tokens":800}}
		}`)
		assert.Equal(t, int64(800), usage.CacheReadTokens)
		assert.Equal(t, int64(200), usage.InputTokens)
	})

	t.Run("chunk without usage stays zero", func(t *testing.T) {
		usage := usageFromChunk(t, `{
			"id":"c8","object":"chat.completion.chunk","created":1,"model":"m","choices":[]
		}`)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(0), usage.InputTokens)
	})

	t.Run("negative standard cached tokens is repaired", func(t *testing.T) {
		usage := usageFromCompletion(t, `{
			"id":"c9","object":"chat.completion","created":1,"model":"m",
			"choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,
				"prompt_tokens_details":{"cached_tokens":-40}}
		}`)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(1000), usage.InputTokens)
	})

	t.Run("negative fallback value is floored", func(t *testing.T) {
		usage := usageFromCompletion(t, `{
			"id":"c10","object":"chat.completion","created":1,"model":"deepseek-v4-flash",
			"choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":5,"total_tokens":1005,
				"prompt_cache_hit_tokens":-500}
		}`)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(1000), usage.InputTokens)
	})

	t.Run("fallback larger than prompt is capped", func(t *testing.T) {
		usage := usageFromCompletion(t, `{
			"id":"c11","object":"chat.completion","created":1,"model":"kimi-k3",
			"choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":5,"total_tokens":1005,
				"cached_tokens":3000}
		}`)
		assert.Equal(t, int64(1000), usage.CacheReadTokens)
		assert.Equal(t, int64(0), usage.InputTokens)
	})

	t.Run("malformed fallback value falls through", func(t *testing.T) {
		usage := usageFromCompletion(t, `{
			"id":"c12","object":"chat.completion","created":1,"model":"deepseek-v4-flash",
			"choices":[],
			"usage":{"prompt_tokens":1000,"completion_tokens":5,"total_tokens":1005,
				"prompt_cache_hit_tokens":"oops"}
		}`)
		assert.Equal(t, int64(0), usage.CacheReadTokens)
		assert.Equal(t, int64(1000), usage.InputTokens)
	})
}
