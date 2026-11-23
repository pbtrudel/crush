package webtest

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/stretchr/testify/require"
)

func TestSeedM3(t *testing.T) {
	t.Parallel()
	f := NewFake()
	clock := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	require.NoError(t, SeedM3(f, clock))

	cfg := f.Config()
	require.NotNil(t, cfg)
	require.Equal(t, config.SelectedModel{Provider: "p1", Model: "m1"}, cfg.Models[config.SelectedModelTypeMain])
	model := f.AgentModel()
	require.Equal(t, "m1", model.ModelCfg.Model)
	require.Equal(t, "Model One", model.CatwalkCfg.Name)

	sessions, err := f.ListSessions(t.Context())
	require.NoError(t, err)
	titles := make(map[string]string, len(sessions))
	for _, s := range sessions {
		titles[s.ID] = s.Title
	}
	require.Equal(t, map[string]string{
		"s1":  "Holodeck test",
		"s-2": "Tokenizer port",
		"s-3": "Diagnostics sweep",
		"s-4": "Release notes",
	}, titles)
	for _, id := range []string{"s-2", "s-3", "s-4"} {
		s, err := f.GetSession(t.Context(), id)
		require.NoError(t, err)
		require.Equal(t, clock.Unix(), s.CreatedAt)
	}

	cmds := M3Commands()
	require.Len(t, cmds, 2)
	require.Equal(t, "user:fix", cmds[0].ID)
	require.Equal(t, []string{"FILE"}, argIDs(cmds[0].Arguments))
	require.True(t, cmds[0].Arguments[0].Required)
	require.Equal(t, "project:hello", cmds[1].ID)
	require.Empty(t, cmds[1].Arguments)

	entries, err := f.ListSkills(t.Context())
	require.NoError(t, err)
	require.Equal(t, []skills.CatalogEntry{{
		ID:            "go-tests",
		Name:          "go-tests",
		Description:   "Run the Go tests and report the failures",
		Label:         "user:go-tests",
		Source:        skills.SourceProject,
		UserInvocable: true,
	}}, entries)

	prompts, err := f.ListMCPPrompts(t.Context())
	require.NoError(t, err)
	require.Len(t, prompts, 2)
	require.Equal(t, "github:triage", prompts[0].ID)
	require.Equal(t, "github", prompts[0].ClientID)
	require.Equal(t, "triage", prompts[0].PromptID)
	require.Len(t, prompts[0].Arguments, 1)
	require.Equal(t, "topic", prompts[0].Arguments[0].ID)
	require.True(t, prompts[0].Arguments[0].Required)
	require.Equal(t, "notes:summary", prompts[1].ID)
	require.Empty(t, prompts[1].Arguments)

	mcpStates := f.MCPGetStates()
	require.Equal(t, mcp.StateConnected, mcpStates["github"].State)
	require.Equal(t, 12, mcpStates["github"].Counts.Tools)
	require.Equal(t, mcp.StateNeedsAuth, mcpStates["notes"].State)
	require.EqualError(t, mcpStates["notes"].Error, "connection refused")
	require.Equal(t, []mcp.PendingAuthServer{{Name: "notes", URL: "https://auth.example/notes"}}, f.MCPPendingAuth())
	require.Equal(t, "https://auth.example/notes", f.MCPAuthURL("notes"))

	lspStates := f.LSPGetStates()
	require.Equal(t, lsp.StateReady, lspStates["gopls"].State)
	require.Equal(t, lsp.StateStopped, lspStates["tsserver"].State)
	require.Equal(t, lsp.DiagnosticCounts{Error: 2, Warning: 1}, f.LSPGetDiagnosticCounts("gopls"))
	require.Equal(t, lsp.DiagnosticCounts{}, f.LSPGetDiagnosticCounts("tsserver"))

	answer, err := f.AgentAsk(t.Context(), "s1", "How does the parser work?")
	require.NoError(t, err)
	require.Equal(t, "The parser uses a recursive descent.", answer)

	var chunks []string
	start := time.Now()
	resp, err := f.AgentRunShellCommand(t.Context(), "s1", "go test ./...", 120, func(chunk string) {
		chunks = append(chunks, chunk)
	}, false)
	require.NoError(t, err)
	require.Equal(t, proto.ShellCommandResponse{Output: strings.Join(m3ShellLines, ""), ExitCode: 0}, resp)
	require.Equal(t, m3ShellLines, chunks)
	require.GreaterOrEqual(t, time.Since(start), 250*time.Millisecond)
}

func argIDs(args []commands.Argument) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, arg.ID)
	}
	return out
}
