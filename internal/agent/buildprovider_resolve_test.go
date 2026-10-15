package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestBuildProviderSurfacesResolveErrors pins that a failing variable
// resolution in a provider's credentials fails the provider build with
// the provider named, instead of silently constructing a provider with
// empty values that later surfaces as an opaque authentication error.
func TestBuildProviderSurfacesResolveErrors(t *testing.T) {
	t.Parallel()

	const providerID = "resolve-error-test"

	t.Run("api key", func(t *testing.T) {
		t.Parallel()

		env := testEnv(t)
		providerCfg := config.ProviderConfig{
			ID:      providerID,
			BaseURL: "https://example.com/v1",
			Type:    catwalk.TypeOpenAICompat,
			APIKey:  "${CRUSH_TEST_UNSET_STRICT_KEY:?API key is required}",
		}
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		_, err := coord.buildProvider(providerCfg, config.SelectedModel{
			Model:    "test-model",
			Provider: providerID,
		}, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), providerID)
		// The error is safe to log: it must not carry the API key
		// template or the resolution failure's inner message.
		require.NotContains(t, err.Error(), "CRUSH_TEST_UNSET_STRICT_KEY")
	})

	t.Run("base url", func(t *testing.T) {
		t.Parallel()

		env := testEnv(t)
		providerCfg := config.ProviderConfig{
			ID:      providerID,
			BaseURL: "${CRUSH_TEST_UNSET_STRICT_URL:?base URL is required}",
			Type:    catwalk.TypeOpenAICompat,
			APIKey:  "sk-test",
		}
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		_, err := coord.buildProvider(providerCfg, config.SelectedModel{
			Model:    "test-model",
			Provider: providerID,
		}, false)
		require.Error(t, err)
		require.Contains(t, err.Error(), providerID)
	})

	t.Run("plain unset variable still builds", func(t *testing.T) {
		t.Parallel()

		// Bash semantics: a plain unset variable expands to the empty
		// string, so providers with no configured credential (local
		// endpoints) must keep building.
		env := testEnv(t)
		providerCfg := config.ProviderConfig{
			ID:      providerID,
			BaseURL: "https://example.com/v1",
			Type:    catwalk.TypeOpenAICompat,
			APIKey:  "$CRUSH_TEST_UNSET_PLAIN_KEY",
		}
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		provider, err := coord.buildProvider(providerCfg, config.SelectedModel{
			Model:    "test-model",
			Provider: providerID,
		}, false)
		require.NoError(t, err)
		require.NotNil(t, provider)
	})
}
