package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
)

// initTeamGitRepo is a Lead tree in a real repository: one commit, no
// signing, whatever the user's global git config says.
func initTeamGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the worktree tests use a POSIX repository layout")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		gitInRepo(t, dir, args...)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644))
	gitInRepo(t, dir, "add", ".")
	gitInRepo(t, dir, "commit", "-m", "initial")
	return dir
}

func gitInRepo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// newWorktreeTestCoordinator is newTeammateTestCoordinator with a Team whose
// Lead works in a git repository, so scoped claims get worktrees.
func newWorktreeTestCoordinator(t *testing.T) (*coordinator, string) {
	t.Helper()
	repo := initTeamGitRepo(t)
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	coord.hookRunner = hooks.NewRunner(hooks.Static(nil), t.TempDir(), t.TempDir())
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{
		WorkingDir: repo,
		Autostart:  func() bool { return false },
	}, newTeamFakeRunner())
	require.NoError(t, err)
	t.Cleanup(service.Shutdown)
	coord.setTeam(service)
	lead := coord.coderAgent()
	require.NotNil(t, lead)
	leadTools, err := coord.buildTools(t.Context(), coord.cfg.Config().Agents[config.AgentCoder], roleLead, func() Model { return coord.mainModels().Large })
	require.NoError(t, err)
	lead.SetTools(leadTools)
	return coord, repo
}

// claimInWorktree spawns teammate name and has it claim a task scoped to
// scope, which gives the task its worktree.
func claimInWorktree(t *testing.T, coord *coordinator, name, scope string) agentteam.Workspace {
	t.Helper()
	team := coord.teamService()
	_, err := team.SpawnTeammate(t.Context(), agentteam.LeadName, agentteam.SpawnRequest{Name: name, Description: "d", Prompt: "p", ParentID: teamTestSessionID})
	require.NoError(t, err)
	task, err := team.CreateTask(agentteam.LeadName, teamTestSessionID, agentteam.CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{scope}})
	require.NoError(t, err)
	claimed, err := team.UpdateTask(name, agentteam.UpdateTaskRequest{TaskID: task.ID, ExpectedRevision: task.Revision, Action: "claim", SessionID: teamTestSessionID})
	require.NoError(t, err)
	require.NotEmpty(t, claimed.Worktree)
	ws, ok := team.MemberWorkspace(name)
	require.True(t, ok)
	return ws
}

// resolvedTestPath resolves symlinks and normalizes slashes, so a path and
// the same path as a tool or a shell prints it compare equal everywhere.
func resolvedTestPath(t *testing.T, path string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.ToSlash(path)
}

func runIn(t *testing.T, ctx context.Context, tool fantasy.AgentTool, input string) fantasy.ToolResponse {
	t.Helper()
	require.NotNil(t, tool)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: tool.Info().Name, Input: input})
	require.NoError(t, err)
	return resp
}

func TestWorktreeToolForwardsIdentity(t *testing.T) {
	t.Parallel()
	recorder := &recordingTool{name: tools.EditToolName}
	none := func(context.Context) (agentteam.Workspace, bool) { return agentteam.Workspace{}, false }
	tool := placeToolsInWorktrees([]fantasy.AgentTool{recorder}, none)[0]
	require.Equal(t, recorder.Info(), tool.Info(), "the wrapper must not change what the provider sees")
	opts := fantasy.ProviderOptions{anthropic.Name: &anthropic.ProviderCacheControlOptions{CacheControl: anthropic.CacheControl{Type: "ephemeral"}}}
	tool.SetProviderOptions(opts)
	require.Equal(t, opts, recorder.ProviderOptions())
	require.Equal(t, opts, tool.ProviderOptions())
	mcp := &worktreeTool{inner: &roleMCPTool{name: "mcp_x_y", server: "x"}, workspace: none}
	require.Equal(t, "x", mcp.MCP())
}

func TestWorktreeToolPassesTheWorkspaceOn(t *testing.T) {
	t.Parallel()
	ws := agentteam.Workspace{Root: "/tree", Dir: "/tree/sub", Branch: "crush/team-task-1"}
	inside := func(context.Context) (agentteam.Workspace, bool) { return ws, true }
	inner := &fakeTool{name: tools.ViewToolName, resp: fantasy.NewTextResponse("ok")}
	runIn(t, t.Context(), placeToolsInWorktrees([]fantasy.AgentTool{inner}, inside)[0], `{}`)
	require.Equal(t, "/tree/sub", tools.WorkingDirFrom(inner.gotCtx, "/lead"))
	got, ok := workspaceFrom(inner.gotCtx)
	require.True(t, ok)
	require.Equal(t, ws, got)

	lsp := &fakeTool{name: tools.SymbolsToolName}
	resp := runIn(t, t.Context(), placeToolsInWorktrees([]fantasy.AgentTool{lsp}, inside)[0], `{}`)
	require.False(t, lsp.called)
	require.True(t, resp.IsError)
	require.Equal(t, lspWorktreeRefusal, resp.Content)

	outside := func(context.Context) (agentteam.Workspace, bool) { return agentteam.Workspace{}, false }
	plain := &fakeTool{name: tools.SymbolsToolName, resp: fantasy.NewTextResponse("ok")}
	runIn(t, t.Context(), placeToolsInWorktrees([]fantasy.AgentTool{plain}, outside)[0], `{}`)
	require.True(t, plain.called, "with no worktree the tool runs as before")
	require.Equal(t, "/lead", tools.WorkingDirFrom(plain.gotCtx, "/lead"))
}

// TestWorktreeToolRefusesBashOutsideTheRoot pins that a teammate in a task
// worktree cannot steer its shell out of it with working_dir: the gate that
// holds bash to the worktree has to know where the shell starts.
func TestWorktreeToolRefusesBashOutsideTheRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	inside := func(context.Context) (agentteam.Workspace, bool) {
		return agentteam.Workspace{Root: root, Dir: filepath.Join(root, "sub"), Branch: "crush/team-task-1"}, true
	}

	for _, tc := range []struct {
		name       string
		workingDir string
		refused    bool
	}{
		{name: "no working_dir", workingDir: "", refused: false},
		{name: "inside the worktree", workingDir: "pkg", refused: false},
		{name: "the worktree root", workingDir: root, refused: false},
		{name: "a relative escape", workingDir: "../../..", refused: true},
		{name: "an absolute escape", workingDir: filepath.Dir(root), refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := &fakeTool{name: tools.BashToolName, resp: fantasy.NewTextResponse("ok")}
			input, err := json.Marshal(tools.BashParams{Command: "pwd", Description: "where", WorkingDir: tc.workingDir})
			require.NoError(t, err)
			resp := runIn(t, t.Context(), placeToolsInWorktrees([]fantasy.AgentTool{inner}, inside)[0], string(input))
			if !tc.refused {
				require.True(t, inner.called, resp.Content)
				require.Equal(t, filepath.Join(root, "sub"), tools.WorkingDirFrom(inner.gotCtx, ""),
					"the wrapper hands the shell the worktree as its working directory")
				return
			}
			require.False(t, inner.called, "a shell outside the worktree must not start")
			require.True(t, resp.IsError, resp.Content)
			require.Contains(t, resp.Content, "outside the task worktree")
		})
	}
}

func TestTeammateToolsRunInTheTaskWorktree(t *testing.T) {
	coord, repo := newWorktreeTestCoordinator(t)
	ws := claimInWorktree(t, coord, "impl", "docs")
	teammate := buildTeammateAgent(t, coord, agentteam.Member{Name: "impl", Role: agentteam.RoleTeammate})
	list := teammate.(*sessionAgent).tools.Copy()
	sess, err := coord.sessions.Create(t.Context(), "Teammate impl")
	require.NoError(t, err)
	ctx := context.WithValue(agentteam.WithMember(t.Context(), "impl"), tools.SessionIDContextKey, sess.ID)

	resp := runIn(t, ctx, toolByName(list, tools.WriteToolName), `{"file_path":"docs/a.md","content":"in the worktree\n"}`)
	require.False(t, resp.IsError, resp.Content)
	require.FileExists(t, filepath.Join(ws.Root, "docs", "a.md"))
	require.NoFileExists(t, filepath.Join(repo, "docs", "a.md"), "the Lead's tree is untouched")

	resp = runIn(t, ctx, toolByName(list, tools.ViewToolName), `{"file_path":"docs/a.md"}`)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "in the worktree")

	resp = runIn(t, ctx, toolByName(list, tools.BashToolName), `{"command":"pwd","description":"where"}`)
	require.Contains(t, resp.Content, resolvedTestPath(t, ws.Root))

	resp = runIn(t, ctx, toolByName(list, tools.SymbolsToolName), `{"file_path":"README.md"}`)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "LSP tools are not available in a task worktree")
}

// TestTeammateInWorktreeKeepsLeadPrefix extends the cache tests: a teammate
// in a task worktree sends the Lead's tools block byte for byte and the
// Lead's system prompt plus its identity, and no tool definition names a
// working directory.
func TestTeammateInWorktreeKeepsLeadPrefix(t *testing.T) {
	coord, repo := newWorktreeTestCoordinator(t)
	ws := claimInWorktree(t, coord, "impl", "docs")
	lead := coord.coderAgent()
	require.NotNil(t, lead)
	member := agentteam.Member{Name: "impl", Role: agentteam.RoleTeammate}
	teammate := buildTeammateAgent(t, coord, member)

	leadTools := lead.(*sessionAgent).tools.Copy()
	teammateTools := teammate.(*sessionAgent).tools.Copy()
	require.Equal(t, toolNames(leadTools), toolNames(teammateTools))
	for i, tool := range teammateTools {
		want, err := json.Marshal(leadTools[i].Info())
		require.NoError(t, err)
		have, err := json.Marshal(tool.Info())
		require.NoError(t, err)
		require.Equal(t, string(want), string(have), "tool %q", tool.Info().Name)
		for _, dir := range []string{coord.cfg.WorkingDir(), repo, ws.Root, ws.Dir} {
			for _, spelling := range []string{dir, resolvedTestPath(t, dir)} {
				require.NotContains(t, string(want), spelling, "Lead tool %q names a working directory", tool.Info().Name)
				require.NotContains(t, string(have), spelling, "teammate tool %q names a working directory", tool.Info().Name)
			}
		}
	}
	require.Equal(t, lead.SystemPrompt()+teamIdentity(member), teammate.SystemPrompt())
	require.NotContains(t, teammate.SystemPrompt(), ws.Root)
}

// TestTeammateInWorktreeRefusesDelegation pins that a teammate holding a task
// worktree cannot hand work to a subagent: the agent tool refuses inside a
// teammate, so no subagent runs behind the scope gate and writes outside the
// worktree. A headless teammate keeps the tool in its palette for cache
// parity, which is why the refusal, not a missing tool, is what closes the
// door. The tool is built directly because an interactive coordinator drops
// it from the palette in favour of the dispatch tool.
func TestTeammateInWorktreeRefusesDelegation(t *testing.T) {
	coord, _ := newWorktreeTestCoordinator(t)
	_ = claimInWorktree(t, coord, "impl", "docs")
	sess, err := coord.sessions.Create(t.Context(), "Teammate impl")
	require.NoError(t, err)
	ctx := context.WithValue(agentteam.WithMember(t.Context(), "impl"), tools.SessionIDContextKey, sess.ID)

	tool, err := coord.agentTool(ctx, config.AgentCoder)
	require.NoError(t, err)
	resp := runIn(t, ctx, tool, `{"prompt":"write the docs"}`)
	require.True(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "nested delegation is not supported inside a teammate")
}

// TestTeammateInWorktreeSubAgentToolsSeeTheWorktree pins the propagation a
// subagent would rely on: the worktree wrapper stamps the workspace on the
// tool call's context, a subagent run starts on that same context, and the
// tools it calls from inside the run resolve their paths in the worktree. So
// a subagent of a teammate cannot write in the Lead's tree.
func TestTeammateInWorktreeSubAgentToolsSeeTheWorktree(t *testing.T) {
	coord, repo := newWorktreeTestCoordinator(t)
	ws := claimInWorktree(t, coord, "impl", "docs")
	parent, err := coord.sessions.Create(t.Context(), "Teammate impl")
	require.NoError(t, err)
	ctx := context.WithValue(agentteam.WithMember(t.Context(), "impl"), tools.SessionIDContextKey, parent.ID)

	// The context one of the teammate's tool calls runs with, taken from the
	// worktree wrapper itself.
	stamp := &fakeTool{name: tools.ViewToolName, resp: fantasy.NewTextResponse("ok")}
	runIn(t, ctx, placeToolsInWorktrees([]fantasy.AgentTool{stamp}, coord.memberWorkspace)[0], `{}`)
	subCtx := stamp.gotCtx
	require.Equal(t, ws.Dir, tools.WorkingDirFrom(subCtx, ""))

	// A subagent's own tools are the task agent's, not the teammate's
	// palette: an unwrapped write tool built for the Lead's working
	// directory, run on the context the subagent's run receives.
	write := tools.NewWriteTool(nil, coord.permissions, coord.history, coord.filetracker, coord.cfg.WorkingDir())
	sub := newMockAgent(coord.mainModels().Large.ModelCfg.Provider, 4096, func(runCtx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		resp, err := write.Run(runCtx, fantasy.ToolCall{ID: "w1", Name: tools.WriteToolName, Input: `{"file_path":"docs/b.md","content":"from the subagent\n"}`})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		return agentResultWithText("done"), nil
	})
	_, err = coord.runSubAgent(subCtx, subAgentParams{
		Agent:          sub,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "write docs/b.md",
		SessionTitle:   "sub",
	})
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(ws.Root, "docs", "b.md"), "the subagent's tool writes in the worktree")
	require.NoFileExists(t, filepath.Join(repo, "docs", "b.md"))
	require.NoFileExists(t, filepath.Join(coord.cfg.WorkingDir(), "docs", "b.md"), "the Lead's tree is untouched")
}

// TestTeammateInWorktreeScopeGateResolvesInTheWorktree pins that the write
// scope gate checks a teammate's paths against its worktree: an absolute
// path in the worktree is inside the scope, where the Lead's directory would
// have made it look like a path outside the workspace.
func TestTeammateInWorktreeScopeGateResolvesInTheWorktree(t *testing.T) {
	coord, _ := newWorktreeTestCoordinator(t)
	ws := claimInWorktree(t, coord, "impl", "docs")
	teammate := buildTeammateAgent(t, coord, agentteam.Member{Name: "impl", Role: agentteam.RoleTeammate})
	list := teammate.(*sessionAgent).tools.Copy()
	sess, err := coord.sessions.Create(t.Context(), "Teammate impl")
	require.NoError(t, err)
	ctx := context.WithValue(agentteam.WithMember(t.Context(), "impl"), tools.SessionIDContextKey, sess.ID)

	inside := filepath.Join(ws.Root, "docs", "scoped.md")
	input, err := json.Marshal(tools.WriteParams{FilePath: inside, Content: "scoped\n"})
	require.NoError(t, err)
	resp := runIn(t, ctx, toolByName(list, tools.WriteToolName), string(input))
	require.False(t, resp.IsError, resp.Content)
	require.FileExists(t, inside)

	outside := filepath.Join(ws.Root, "notes.md")
	input, err = json.Marshal(tools.WriteParams{FilePath: outside, Content: "nope\n"})
	require.NoError(t, err)
	resp = runIn(t, ctx, toolByName(list, tools.WriteToolName), string(input))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "outside the write scopes of your claimed task")
}
