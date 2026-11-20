package route

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/stretchr/testify/require"
)

const githubToken = "github-refresh-token"

// TestPrepareTokenRefreshesAndWrites checks the host-side refresh of a
// jailed Copilot run: the stored refresh token is exchanged, and the run's
// own config gets the fresh access token with its expiry, so the child never
// has to reach api.github.com.
func TestPrepareTokenRefreshesAndWrites(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	writeDataConfig(t, h, map[string]any{
		"providers": map[string]any{
			ProviderCopilot: map[string]any{
				"api_key": "stale",
				"oauth": map[string]any{
					"access_token":  "stale",
					"refresh_token": githubToken,
					"expires_at":    time.Now().Add(-time.Minute).Unix(),
				},
			},
		},
	})

	fresh := &oauth.Token{
		AccessToken:  "fresh",
		RefreshToken: githubToken,
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	}
	var gotProvider, gotRefresh string
	refresh := func(_ context.Context, provider, refreshToken string) (*oauth.Token, error) {
		gotProvider, gotRefresh = provider, refreshToken
		return fresh, nil
	}

	runData := filepath.Join(t.TempDir(), "data")
	r := &Route{Name: "gpt", Provider: ProviderCopilot}
	require.NoError(t, r.PrepareToken(context.Background(), h, runData, 10*time.Minute, refresh))
	require.Equal(t, ProviderCopilot, gotProvider)
	require.Equal(t, githubToken, gotRefresh)

	copilot := runProvider(t, runData, ProviderCopilot)
	require.Equal(t, "fresh", copilot["api_key"])
	token, ok := copilot["oauth"].(map[string]any)
	require.True(t, ok, "the run config holds the token object")
	require.Equal(t, "fresh", token["access_token"])
	require.Equal(t, float64(fresh.ExpiresAt), token["expires_at"])
}

// TestPrepareTokenFailsOnNearExpiry checks that a token Crush would still
// accept, but which dies before the run's timeout plus the margin, fails the
// run instead of starting it, and is not exchanged first.
func TestPrepareTokenFailsOnNearExpiry(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	writeDataConfig(t, h, map[string]any{
		"providers": map[string]any{
			ProviderCopilot: map[string]any{
				"oauth": map[string]any{
					"access_token":  "short",
					"refresh_token": githubToken,
					"expires_at":    time.Now().Add(12 * time.Minute).Unix(),
				},
			},
		},
	})

	refresh := func(context.Context, string, string) (*oauth.Token, error) {
		t.Error("a token Crush still accepts must not be exchanged")
		return nil, nil
	}

	runData := filepath.Join(t.TempDir(), "data")
	r := &Route{Name: "gpt", Provider: ProviderCopilot}
	err := r.PrepareToken(context.Background(), h, runData, 10*time.Minute, refresh)
	require.ErrorContains(t, err, "expires")
	require.ErrorContains(t, err, "crush-eval login gpt")
	require.NoFileExists(t, filepath.Join(runData, "crush.json"))
}

// TestPrepareTokenKeepsAValidToken checks the other side of the same rule: a
// token that outlives the timeout plus the margin is written as it is, with
// no exchange, so a run does not rotate a perfectly good credential.
func TestPrepareTokenKeepsAValidToken(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	stored := &oauth.Token{
		AccessToken:  "long-lived",
		RefreshToken: githubToken,
		ExpiresAt:    time.Now().Add(2 * time.Hour).Unix(),
	}
	writeDataConfig(t, h, map[string]any{
		"providers": map[string]any{ProviderCopilot: map[string]any{"oauth": stored}},
	})

	refresh := func(context.Context, string, string) (*oauth.Token, error) {
		t.Error("a token that outlives the run must not be exchanged")
		return nil, nil
	}

	runData := filepath.Join(t.TempDir(), "data")
	r := &Route{Name: "gpt", Provider: ProviderCopilot}
	require.NoError(t, r.PrepareToken(context.Background(), h, runData, 10*time.Minute, refresh))

	token := runProvider(t, runData, ProviderCopilot)["oauth"].(map[string]any)
	require.Equal(t, "long-lived", token["access_token"])
	require.Equal(t, float64(stored.ExpiresAt), token["expires_at"])
}

// TestPrepareTokenIgnoresProvidersThatRefreshAtTheAPIHost checks that Hyper
// needs no host-side help: it renews at hyper.charm.land, the host the jail
// already allows, so the run config is left alone.
func TestPrepareTokenIgnoresProvidersThatRefreshAtTheAPIHost(t *testing.T) {
	t.Parallel()

	refresh := func(context.Context, string, string) (*oauth.Token, error) {
		t.Error("hyper refreshes at its API host, not on the runner")
		return nil, nil
	}

	runData := filepath.Join(t.TempDir(), "data")
	r := &Route{Name: "glm", Provider: "hyper"}
	require.NoError(t, r.PrepareToken(context.Background(), home.At(t.TempDir()), runData, 10*time.Minute, refresh))
	require.NoFileExists(t, filepath.Join(runData, "crush.json"))
}

// TestPrepareTokenWithoutStoredToken checks that a route signed out on the
// runner says how to sign it back in.
func TestPrepareTokenWithoutStoredToken(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	runData := filepath.Join(t.TempDir(), "data")
	r := &Route{Name: "gpt", Provider: ProviderCopilot}
	err := r.PrepareToken(context.Background(), h, runData, 10*time.Minute, DefaultRefreshToken)
	require.ErrorContains(t, err, "crush-eval login gpt")
}

// TestDefaultRefreshTokenRejectsOtherProviders checks that the runner does
// not invent an exchange for a provider Crush refreshes some other way.
func TestDefaultRefreshTokenRejectsOtherProviders(t *testing.T) {
	t.Parallel()

	_, err := DefaultRefreshToken(context.Background(), "commandcode", "token")
	require.ErrorContains(t, err, "commandcode")
}

// writeDataConfig writes the eval home's data config, the file `crush-eval
// login` fills with the route's token.
func writeDataConfig(t *testing.T, h *home.Home, doc map[string]any) {
	t.Helper()
	writeJSON(t, filepath.Join(h.DataDir(), "crush.json"), doc)
}

// runProvider returns providers.<provider> of a run's config.
func runProvider(t *testing.T, runData, provider string) map[string]any {
	t.Helper()
	var doc struct {
		Providers map[string]map[string]any `json:"providers"`
	}
	data, err := os.ReadFile(filepath.Join(runData, "crush.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &doc))
	providerDoc, ok := doc.Providers[provider]
	require.True(t, ok, "the run config has no %s provider", provider)
	return providerDoc
}

// writeJSON writes one JSON document.
func writeJSON(t *testing.T, path string, doc map[string]any) {
	t.Helper()
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}
