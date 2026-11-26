package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/home"
)

func fixedScopes(scopes ...string) func(context.Context) []string {
	return func(context.Context) []string { return scopes }
}

func scopedTool(t *testing.T, name, workingDir string, scopes ...string) (fantasy.AgentTool, *fakeTool) {
	t.Helper()
	inner := &fakeTool{name: name, resp: fantasy.NewTextResponse("ok")}
	return gateToolsForScopes([]fantasy.AgentTool{inner}, fixedScopes(scopes...), workingDir)[0], inner
}

func runScoped(t *testing.T, tool fantasy.AgentTool, input string) fantasy.ToolResponse {
	t.Helper()
	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "call-1", Name: tool.Info().Name, Input: input})
	require.NoError(t, err)
	return resp
}

func filePathInput(path string) string {
	data, _ := json.Marshal(map[string]string{"file_path": path})
	return string(data)
}

func TestScopeGateWrapsOnlyWriteTools(t *testing.T) {
	t.Parallel()
	list := []fantasy.AgentTool{&fakeTool{name: tools.ViewToolName}, &fakeTool{name: tools.EditToolName}, &roleMCPTool{name: "mcp_x_y", server: "x"}}
	gated := gateToolsForScopes(list, fixedScopes("docs"), t.TempDir())
	require.Same(t, list[0], gated[0], "a read tool is not wrapped")
	require.Same(t, list[2], gated[2])
	wrapped, ok := gated[1].(*scopeGatedTool)
	require.True(t, ok)
	require.Equal(t, list[1].Info(), wrapped.Info(), "the gate must not change what the provider sees")
	require.Empty(t, wrapped.MCP())
}

// TestScopeGateForwardsProviderOptions pins the cache contract: the gate is
// transparent to provider options, so a cache breakpoint set through it lands
// on the real tool and reads back unchanged, and MCP identity passes through.
func TestScopeGateForwardsProviderOptions(t *testing.T) {
	t.Parallel()
	recorder := &recordingTool{name: tools.EditToolName}
	tool := gateToolsForScopes([]fantasy.AgentTool{recorder}, fixedScopes("internal/agent"), t.TempDir())[0]
	opts := fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
	}
	tool.SetProviderOptions(opts)
	require.Equal(t, opts, recorder.ProviderOptions(), "the options must land on the real tool")
	require.Equal(t, opts, tool.ProviderOptions(), "the wrapper must read the real tool's options")

	mcp := &scopeGatedTool{inner: &roleMCPTool{name: "mcp_x_y", server: "x"}}
	require.Equal(t, "x", mcp.MCP(), "MCP identity must pass through the gate")
}

func TestScopeGatePaths(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	cases := []struct {
		path string
		ok   bool
	}{
		{"internal/agent/x.go", true},
		{"./internal/agent/x.go", true},
		{filepath.Join(wd, "internal/agent/x.go"), true},
		{"internal/agent/sub/../x.go", true},
		{"docs/agent-teams.md", true},
		{"internal/agentteam/x.go", false},
		{"internal/agent/../ui/x.go", false},
		{"internal/ui/x.go", false},
		{"../outside.go", false},
		{"/etc/passwd", false},
	}
	for _, name := range []string{tools.EditToolName, tools.MultiEditToolName, tools.WriteToolName, tools.DownloadToolName, tools.ReplaceSymbolToolName} {
		for _, tc := range cases {
			tool, inner := scopedTool(t, name, wd, "internal/agent", "docs/agent-teams.md")
			resp := runScoped(t, tool, filePathInput(tc.path))
			require.Equal(t, tc.ok, inner.called, "%s %s", name, tc.path)
			require.Equal(t, !tc.ok, resp.IsError, "%s %s", name, tc.path)
		}
	}

	tool, _ := scopedTool(t, tools.EditToolName, wd, "internal/agent", "docs/agent-teams.md")
	resp := runScoped(t, tool, filePathInput("internal/ui/x.go"))
	require.Equal(t, `path "internal/ui/x.go" is outside the write scopes of your claimed task: internal/agent, docs/agent-teams.md`, resp.Content)
}

// TestScopeGateRefusesDownloadOutsideScopes pins that download writes only
// inside a teammate's claimed write scopes: it saves the file_path the call
// names, exactly as write does.
func TestScopeGateRefusesDownloadOutsideScopes(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	tool, inner := scopedTool(t, tools.DownloadToolName, wd, "internal/agent")
	require.Equal(t, inner.Info(), tool.Info(), "the gate must not change what the provider sees")
	download := func(path string) string {
		data, _ := json.Marshal(map[string]string{
			"url":       "https://example.com/report.txt",
			"file_path": path,
		})
		return string(data)
	}
	resp := runScoped(t, tool, download("internal/ui/report.txt"))
	require.True(t, resp.IsError)
	require.False(t, inner.called, "a refused download must not reach the real tool")
	require.Contains(t, resp.Content, `path "internal/ui/report.txt"`)

	require.False(t, runScoped(t, tool, download("internal/agent/report.txt")).IsError)
	require.True(t, inner.called)
}

func TestScopeGateApplyPatchChecksEveryFile(t *testing.T) {
	t.Parallel()
	patch := func(paths ...string) string {
		body := "*** Begin Patch\n"
		for _, p := range paths {
			body += "*** Add File: " + p + "\n+x\n"
		}
		body += "*** End Patch"
		data, _ := json.Marshal(tools.ApplyPatchParams{Patch: body})
		return string(data)
	}
	wd := t.TempDir()
	tool, inner := scopedTool(t, tools.ApplyPatchToolName, wd, "internal/agent")
	require.False(t, runScoped(t, tool, patch("internal/agent/a.go", "internal/agent/b.go")).IsError)
	require.True(t, inner.called)

	tool, inner = scopedTool(t, tools.ApplyPatchToolName, wd, "internal/agent")
	resp := runScoped(t, tool, patch("internal/agent/a.go", "internal/ui/b.go"))
	require.True(t, resp.IsError)
	require.False(t, inner.called, "one file outside the scopes refuses the whole patch")
	require.Contains(t, resp.Content, `path "internal/ui/b.go"`)
}

// TestScopeGateApplyPatchChecksDirectives covers the section headers a patch
// writes through besides Add File: an update, a delete, and a move whose
// destination is outside the scopes while its source is inside.
func TestScopeGateApplyPatchChecksDirectives(t *testing.T) {
	t.Parallel()
	patchInput := func(body string) string {
		data, _ := json.Marshal(tools.ApplyPatchParams{Patch: body})
		return string(data)
	}
	cases := []struct {
		name  string
		patch string
		path  string
	}{
		{
			name:  "update file",
			patch: "*** Begin Patch\n*** Update File: internal/ui/a.go\n@@\n-old\n+new\n*** End Patch",
			path:  "internal/ui/a.go",
		},
		{
			name:  "delete file",
			patch: "*** Begin Patch\n*** Delete File: internal/ui/a.go\n*** End Patch",
			path:  "internal/ui/a.go",
		},
		{
			name:  "move destination",
			patch: "*** Begin Patch\n*** Update File: internal/agent/a.go\n*** Move to: internal/ui/a.go\n@@\n-old\n+new\n*** End Patch",
			path:  "internal/ui/a.go",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool, inner := scopedTool(t, tools.ApplyPatchToolName, t.TempDir(), "internal/agent")
			resp := runScoped(t, tool, patchInput(tc.patch))
			require.True(t, resp.IsError)
			require.False(t, inner.called, "a refused patch must not reach the real tool")
			require.Contains(t, resp.Content, `path "`+tc.path+`"`)
		})
	}
}

// TestScopeGateHomePathOutsideWorkspace pins that a home-relative path is
// expanded the way the edit tools expand it, so a teammate cannot escape its
// scopes by writing through ~.
func TestScopeGateHomePathOutsideWorkspace(t *testing.T) {
	// The home package already uses a scratch directory under go test, so
	// HOME is pinned only to make the intent explicit: either way the home
	// sits outside the workspace below.
	t.Setenv("HOME", t.TempDir())
	wd := t.TempDir()
	require.False(t,
		strings.HasPrefix(filepath.Clean(home.Long("~/outside.go")), filepath.Clean(wd)+string(filepath.Separator)),
		"the test home must not sit inside the workspace",
	)

	tool, inner := scopedTool(t, tools.EditToolName, wd, "internal/agent")
	resp := runScoped(t, tool, filePathInput("~/outside.go"))
	require.True(t, resp.IsError)
	require.False(t, inner.called)
	require.Contains(t, resp.Content, `path "~/outside.go"`)
}

func TestScopeGateRefusesRenameUnderScopes(t *testing.T) {
	t.Parallel()
	tool, inner := scopedTool(t, tools.RenameToolName, t.TempDir(), "internal/agent")
	resp := runScoped(t, tool, `{"symbol":"A","new_name":"B"}`)
	require.True(t, resp.IsError)
	require.False(t, inner.called)
	require.Contains(t, resp.Content, "Use edit")
}

func TestScopeGateWithoutScopesPasses(t *testing.T) {
	t.Parallel()
	for _, name := range scopedWriteTools {
		tool, inner := scopedTool(t, name, t.TempDir())
		runScoped(t, tool, `{"file_path":"/anywhere/x.go"}`)
		require.True(t, inner.called, "%s with no claimed scopes is not restricted", name)
	}
}

func TestScopeGateRefusalRunsNoHook(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	runner := newRunner(t, "touch '"+marker+"'")
	inner := &fakeTool{name: tools.EditToolName, resp: fantasy.NewTextResponse("ok")}
	tool := gateToolsForScopes(wrapToolsWithHooks([]fantasy.AgentTool{inner}, runner, false), fixedScopes("internal/agent"), t.TempDir())[0]

	require.True(t, runScoped(t, tool, filePathInput("internal/ui/x.go")).IsError)
	_, err := os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist, "a refused call must not run a hook")

	require.False(t, runScoped(t, tool, filePathInput("internal/agent/x.go")).IsError)
	_, err = os.Stat(marker)
	require.NoError(t, err, "an allowed call still runs its hook")
}

// TestClaimedScopesReadTheCallerTasks pins the coordinator half: the scopes
// come from the tasks the calling member owns in progress, and the Lead has
// none.
func TestClaimedScopesReadTheCallerTasks(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	team := coord.teamService()
	_, err := team.SpawnTeammate(t.Context(), "lead", agentteam.SpawnRequest{Name: "impl", Description: "d", Prompt: "p", ParentID: teamTestSessionID})
	require.NoError(t, err)
	task, err := team.CreateTask("lead", teamTestSessionID, agentteam.CreateTaskRequest{Subject: "A", Description: "d", WriteScopes: []string{"internal/agent"}})
	require.NoError(t, err)
	_, err = team.UpdateTask("impl", agentteam.UpdateTaskRequest{TaskID: task.ID, ExpectedRevision: task.Revision, Action: "claim"})
	require.NoError(t, err)

	require.Equal(t, []string{"internal/agent"}, coord.claimedScopes(agentteam.WithMember(t.Context(), "impl")))
	require.Empty(t, coord.claimedScopes(t.Context()), "the Lead is never scope-gated")
}
