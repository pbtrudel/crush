package workspace

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/stretchr/testify/require"
)

func TestDefaultAuthChoiceMatchesTheTUI(t *testing.T) {
	t.Parallel()
	withLogin := config.ProviderConfig{OAuthToken: &oauth.Token{AccessToken: "a"}}
	for _, tc := range []struct {
		id          string
		pc          config.ProviderConfig
		key, holder bool
		want        AuthChoice
	}{
		{"hyper", config.ProviderConfig{}, false, false, AuthChoiceOAuth},
		{"copilot", config.ProviderConfig{}, true, false, AuthChoiceOAuth},
		{"openai", config.ProviderConfig{}, false, true, AuthChoiceOAuth},
		{"openai", withLogin, true, false, AuthChoiceOAuth},
		{"openai", config.ProviderConfig{}, false, false, AuthChoicePick},
		{"openai", config.ProviderConfig{}, true, false, AuthChoiceAPIKey},
		{"xai", config.ProviderConfig{}, false, false, AuthChoicePick},
		{"anthropic", config.ProviderConfig{}, false, false, AuthChoiceAPIKey},
	} {
		require.Equal(t, tc.want, DefaultAuthChoice(tc.id, tc.pc, tc.key, tc.holder), tc.id)
	}
}

func TestSignInTargetsListsKnownProvidersWithMethods(t *testing.T) {
	t.Parallel()
	cfg := testModelConfig(t) // M3 helper in models_test.go: providers p1 and p2 (p2 disabled)
	known := []catwalk.Provider{
		{ID: "hyper", Name: "Hyper"},
		{ID: "copilot", Name: "GitHub Copilot"},
		{ID: "openai", Name: "OpenAI"},
		{ID: "anthropic", Name: "Anthropic"},
		{ID: "p2", Name: "Off"},
	}
	got := SignInTargets(cfg, known)
	ids := make([]string, len(got))
	for i, g := range got {
		ids[i] = g.ID
	}
	require.Equal(t, []string{"hyper", "copilot", "openai", "anthropic"}, ids, "a disabled provider is left out")
	require.Equal(t, []string{"oauth"}, got[0].Methods)
	require.Equal(t, "device", got[1].OAuthKind)
	require.Equal(t, []string{"oauth", "api_key"}, got[2].Methods)
	require.Equal(t, "browser", got[2].OAuthKind)
	require.Equal(t, []string{"api_key"}, got[3].Methods)
}

func TestSignInTargetsWithoutConfigListsEveryKnownProvider(t *testing.T) {
	t.Parallel()
	got := SignInTargets(nil, []catwalk.Provider{{ID: "hyper", Name: "Hyper"}, {ID: "anthropic", Name: "Anthropic"}})
	require.Len(t, got, 2, "a workspace with no config yet can still sign in")
	require.False(t, got[0].Configured)
}

func TestSignInTargetsWithoutProvidersListsEveryKnownProvider(t *testing.T) {
	t.Parallel()
	got := SignInTargets(&config.Config{}, []catwalk.Provider{{ID: "hyper", Name: "Hyper"}, {ID: "anthropic", Name: "Anthropic"}})
	require.Len(t, got, 2, "a config whose provider map is not loaded yet lists them unconfigured")
	require.False(t, got[0].Configured)
	require.False(t, got[1].Configured)
}
