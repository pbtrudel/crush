package config

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/stretchr/testify/require"
)

// TestDefaultSmallModel pins how the small slot is chosen when the user has
// not configured one: the preferred cheap model when the provider serves it,
// the provider's declared small model next, and the main model last.
func TestDefaultSmallModel(t *testing.T) {
	t.Parallel()

	main := SelectedModel{Provider: "neuralwatt", Model: "glm-5.3", MaxTokens: 128000}

	t.Run("prefers deepseek v4.1 flash", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Providers: csync.NewMapFrom(map[string]ProviderConfig{
			"neuralwatt": {Models: []catwalk.Model{
				{ID: "glm-5.3", DefaultMaxTokens: 128000},
				{ID: "glm-5.3-flash", DefaultMaxTokens: 32000},
				{ID: "deepseek-v4.1-flash", DefaultMaxTokens: 64000},
			}},
		})}
		known := []catwalk.Provider{{
			ID:                  "neuralwatt",
			DefaultSmallModelID: "glm-5.3-flash",
		}}

		got := cfg.DefaultSmallModel("neuralwatt", main, known)
		require.Equal(t, "deepseek-v4.1-flash", got.Model)
		require.Equal(t, "neuralwatt", got.Provider)
		require.Equal(t, int64(64000), got.MaxTokens)
	})

	t.Run("matches a prefixed catalog id", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Providers: csync.NewMapFrom(map[string]ProviderConfig{
			"commandcode": {Models: []catwalk.Model{
				{ID: "deepseek/deepseek-v4.1-flash", DefaultMaxTokens: 32000},
			}},
		})}

		got := cfg.DefaultSmallModel("commandcode", SelectedModel{Provider: "commandcode", Model: "glm-5.3"}, nil)
		require.Equal(t, "deepseek/deepseek-v4.1-flash", got.Model)
	})

	t.Run("falls back to the provider small model", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Providers: csync.NewMapFrom(map[string]ProviderConfig{
			"openai": {Models: []catwalk.Model{
				{ID: "gpt-5.6", DefaultMaxTokens: 128000},
				{ID: "gpt-5.6-mini", DefaultMaxTokens: 32000},
			}},
		})}
		known := []catwalk.Provider{{
			ID:                  "openai",
			DefaultSmallModelID: "gpt-5.6-mini",
		}}

		got := cfg.DefaultSmallModel("openai", SelectedModel{Provider: "openai", Model: "gpt-5.6"}, known)
		require.Equal(t, "gpt-5.6-mini", got.Model)
		require.Equal(t, int64(32000), got.MaxTokens)
	})

	t.Run("falls back to the main model", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Providers: csync.NewMapFrom(map[string]ProviderConfig{
			"local": {Models: []catwalk.Model{{ID: "llama"}}},
		})}

		got := cfg.DefaultSmallModel("local", main, nil)
		require.Equal(t, main, got)
	})
}
