package webtest

import (
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// The tests below set WEBFAKE_THEME, which t.Setenv forbids in a parallel
// test, so none of them run in parallel.

func TestSeedM4bKeepsTheM4Seams(t *testing.T) {
	t.Setenv("WEBFAKE_THEME", "")
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	clock := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	seams, err := SeedM4b(f, done, clock)
	require.NoError(t, err)

	cfg := f.Config()
	require.NotNil(t, cfg)
	known, err := seams.KnownProviders(cfg)
	require.NoError(t, err)
	ids := make([]string, 0, len(known))
	for _, p := range known {
		ids = append(ids, string(p.ID))
	}
	require.Equal(t, []string{"hyper", "copilot", "openai", "xai", "anthropic"}, ids)

	require.NoError(t, seams.TestAPIKey(t.Context(), catwalk.Provider{}, "sk-valid", config.IdentityResolver()))
	require.Error(t, seams.TestAPIKey(t.Context(), catwalk.Provider{}, "sk-wrong", config.IdentityResolver()))

	flow, err := seams.SignInFlow("copilot")
	require.NoError(t, err)
	copilot, ok := flow.(*FakeFlow)
	require.True(t, ok)
	require.Equal(t, "WDJB-MJHT", copilot.Challenge.UserCode)

	// The M3 seeds come along with the M4 data.
	sessions, err := f.ListSessions(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, sessions, "the M3 sessions are seeded")

	// The tab starts on the Holodeck theme.
	require.NotNil(t, cfg.Options)
	require.NotNil(t, cfg.Options.WebDisplay)
	require.Equal(t, "holodeck", cfg.Options.WebDisplay.Theme)
}

func TestSeedM4bHonoursTheTheme(t *testing.T) {
	t.Setenv("WEBFAKE_THEME", "solid")
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	clock := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	_, err := SeedM4b(f, done, clock)
	require.NoError(t, err)
	cfg := f.Config()
	require.NotNil(t, cfg.Options)
	require.NotNil(t, cfg.Options.WebDisplay)
	require.Equal(t, "solid", cfg.Options.WebDisplay.Theme)
}

func TestSeedM4bRefusesAnUnknownTheme(t *testing.T) {
	t.Setenv("WEBFAKE_THEME", "neon")
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	_, err := SeedM4b(f, done, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	require.ErrorContains(t, err, "WEBFAKE_THEME")
}
