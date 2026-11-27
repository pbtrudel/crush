package webtest

import (
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// The tests below set WEBFAKE_* variables, which t.Setenv forbids in a
// parallel test, so they do not run in parallel.

func TestSeedM4bKeepsTheM4Seams(t *testing.T) {
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
}

func TestSeedM4bAddsTheM4bSurfaces(t *testing.T) {
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
	const name = "An extremely long model name that will not fit in the HUD at all"
	t.Setenv("WEBFAKE_MODEL_NAME", name)
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	_, err := SeedM4b(f, done, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, name, f.AgentModel().CatwalkCfg.Name)
}

func TestSeedM4bHonoursTheHudServers(t *testing.T) {
	t.Setenv("WEBFAKE_HUD_SERVERS", "full")
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	_, err := SeedM4b(f, done, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, f.MCP, 9)
	require.Len(t, f.LSP, 4)
	require.Contains(t, f.MCP, "mcp-9")
	require.Contains(t, f.LSP, "lsp-4")
}

func TestSeedM4bRefusesAnUnknownHudServers(t *testing.T) {
	t.Setenv("WEBFAKE_HUD_SERVERS", "some")
	f := NewFake()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	_, err := SeedM4b(f, done, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	require.ErrorContains(t, err, "WEBFAKE_HUD_SERVERS")
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

// toolCallIDs lists the tool call each history version records.
func toolCallIDs(metas []history.FileMeta) []string {
	ids := make([]string, 0, len(metas))
	for _, m := range metas {
		if m.ToolCallID != "" {
			ids = append(ids, m.ToolCallID)
		}
	}
	return ids
}

// TestSeedM4bLinksEveryToolCall pins the drawer's links: each seeded history
// version that records a tool call names one that exists in s1's messages.
func TestSeedM4bLinksEveryToolCall(t *testing.T) {
	t.Parallel()
	f := NewFake()
	clock := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	_, err := SeedM4b(f, done, clock)
	require.NoError(t, err)
	msgs, err := f.ListMessages(t.Context(), "s1")
	require.NoError(t, err)
	calls := map[string]bool{}
	for _, m := range msgs {
		for _, p := range m.Parts {
			if tc, ok := p.(message.ToolCall); ok {
				calls[tc.ID] = true
			}
		}
	}
	for _, id := range []string{"tc-edit-1", "tc-edit-2"} {
		require.True(t, calls[id], "the seeded history id %s has a tool call", id)
	}
	metas, err := f.ListSessionHistoryMeta(t.Context(), "s1")
	require.NoError(t, err)
	require.Contains(t, toolCallIDs(metas), "tc-edit-1")
	require.Contains(t, toolCallIDs(metas), "tc-edit-2")
}
