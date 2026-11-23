package agent

// request_compression opts any OpenAI-compatible provider into compressed
// request bodies, in the order the user prefers them.

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// compressingCoordinator is a test coordinator whose providerID asks for
// the given encodings.
func compressingCoordinator(t *testing.T, providerID string, encodings ...string) *coordinator {
	t.Helper()
	c := testCoordinator(t)
	c.cfg.Config().Providers.Set(providerID, config.ProviderConfig{
		ID:                 providerID,
		Type:               catwalk.TypeCompletions,
		RequestCompression: encodings,
	})
	return c
}

func TestRequestCompressionOption(t *testing.T) {
	t.Parallel()

	t.Run("a provider that opts in uploads its first encoding", func(t *testing.T) {
		t.Parallel()
		rec := newEncodingRecorder(t, false)

		coord := compressingCoordinator(t, "compressing", "gzip", "br")
		require.NoError(t, generateAgainst(t, coord, rec.server.URL+"/v1", "compressing", longPrompt()))

		encodings, bodies := rec.received()
		require.Equal(t, []string{"gzip"}, encodings)
		require.Contains(t, bodies[0], `"model":"test-model"`)
	})

	t.Run("a refused first encoding falls back to the next", func(t *testing.T) {
		t.Parallel()
		rec := newEncodingRecorder(t, true)

		coord := compressingCoordinator(t, "compressing", "br", "gzip")
		require.NoError(t, generateAgainst(t, coord, rec.server.URL+"/v1", "compressing", longPrompt()))

		encodings, _ := rec.received()
		require.Equal(t, []string{"br", "gzip"}, encodings)
	})

	t.Run("hyper follows the option over its default", func(t *testing.T) {
		t.Parallel()
		rec := newEncodingRecorder(t, false)

		coord := compressingCoordinator(t, hyper.Name, "gzip")
		require.NoError(t, generateAgainst(t, coord, rec.server.URL+"/v1", hyper.Name, longPrompt()))

		encodings, _ := rec.received()
		require.Equal(t, []string{"gzip"}, encodings)
	})

	t.Run("a configured provider without the option uploads plain JSON", func(t *testing.T) {
		t.Parallel()
		rec := newEncodingRecorder(t, false)

		coord := compressingCoordinator(t, "plain")
		require.NoError(t, generateAgainst(t, coord, rec.server.URL+"/v1", "plain", longPrompt()))

		encodings, _ := rec.received()
		require.Equal(t, []string{""}, encodings)
	})
}

func TestRequestEncodings(t *testing.T) {
	t.Parallel()

	coord := compressingCoordinator(t, "compressing", "br", "gzip")
	require.Equal(t,
		[]openaicompat.RequestEncoding{openaicompat.EncodingBrotli, openaicompat.EncodingGzip},
		coord.requestEncodings("compressing", "https://example.com/v1"))
	require.Equal(t,
		[]openaicompat.RequestEncoding{openaicompat.EncodingBrotli, openaicompat.EncodingGzip},
		coord.requestEncodings(hyper.Name, "https://hyper.example/v1"), "hyper compresses by default")
	require.Empty(t, coord.requestEncodings("unknown", "https://example.com/v1"))

	// Mistral signs its requests through its own transport; compression
	// stays off there until it is tested with that signing.
	mistral := compressingCoordinator(t, "mistral", "gzip")
	require.Empty(t, mistral.requestEncodings("mistral", "https://api.mistral.ai/v1"))
}

// The prompt_cache_key retry reads and rewrites the body under the option
// the same way it does under Hyper's default.
func TestPromptCacheKeyRetryUnderRequestCompressionOption(t *testing.T) {
	t.Parallel()
	server := newKeyRejectingServer(t, false)

	coord := compressingCoordinator(t, "compressing", "gzip")
	provider, err := coord.buildOpenaiCompatProvider(server.server.URL+"/v1", "test-key", nil, nil, "compressing", false, catwalk.TypeCompletions)
	require.NoError(t, err)
	lm, err := provider.LanguageModel(t.Context(), "test-model")
	require.NoError(t, err)
	_, err = lm.Generate(t.Context(), cacheKeyCall())
	require.NoError(t, err)

	encodings, bodies := server.received()
	require.Equal(t, []string{"gzip", "gzip"}, encodings)
	require.Contains(t, bodies[0], promptCacheKeyField)
	require.NotContains(t, bodies[1], promptCacheKeyField, "the retry drops the key")
	require.Contains(t, bodies[1], `"model":"test-model"`)
}
