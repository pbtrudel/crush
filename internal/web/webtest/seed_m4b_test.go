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

func TestSeedM4bAddsTheM4bSurfaces(t *testing.T) {
	t.Setenv("WEBFAKE_THEME", "")
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	clock := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	seams, err := SeedM4b(f, done, clock)
	require.NoError(t, err)

	// The m4b scenario carries a channel-capable MCP server.
	discord, ok := f.MCP["discord"]
	require.True(t, ok, "the discord server is seeded")
	require.True(t, discord.ChannelCapable)

	// Docker MCP can be enabled in the fake.
	require.NotNil(t, seams.DockerMCPAvailable)
	require.True(t, seams.DockerMCPAvailable())

	// The globally disabled skill is discovered but absent from the active
	// catalog, the way a config-disabled skill behaves.
	entries, err := f.ListAllSkills(t.Context())
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	require.Contains(t, names, "crush-config")
	active, err := f.ListSkills(t.Context())
	require.NoError(t, err)
	activeNames := make([]string, 0, len(active))
	for _, entry := range active {
		activeNames = append(activeNames, entry.Name)
	}
	require.NotContains(t, activeNames, "crush-config")
	require.NotNil(t, f.Cfg.Options)
	require.Contains(t, f.Cfg.Options.DisabledSkills, "crush-config")

	// The fake's terminal answers the two automation commands.
	require.NotNil(t, seams.Automation)
	reply, err := seams.Automation(t.Context(), "s1", "goal", "Ship the parser")
	require.NoError(t, err)
	require.Equal(t, "Goal set: Ship the parser", reply)
	reply, err = seams.Automation(t.Context(), "s1", "loop", "watch the tests")
	require.NoError(t, err)
	require.Equal(t, "Loops: 2 active", reply)
}

func TestSeedM4bHonoursTheModelName(t *testing.T) {
	t.Setenv("WEBFAKE_THEME", "")
	const name = "An extremely long model name that will not fit in the HUD at all"
	t.Setenv("WEBFAKE_MODEL_NAME", name)
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	_, err := SeedM4b(f, done, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, name, f.AgentModel().CatwalkCfg.Name)
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

func TestSeedM4bGivesTheBrowsedSessionAMessage(t *testing.T) {
	t.Parallel()
	f := NewFake()
	clock := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	_, err := SeedM4b(f, done, clock)
	require.NoError(t, err)
	msgs, err := f.ListMessages(t.Context(), "s-2")
	require.NoError(t, err)
	require.NotEmpty(t, msgs, "the browsed session has a message to show")
}

func TestSeedM4bRefusesAnUnknownTheme(t *testing.T) {
	t.Setenv("WEBFAKE_THEME", "neon")
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	_, err := SeedM4b(f, done, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	require.ErrorContains(t, err, "WEBFAKE_THEME")
}
