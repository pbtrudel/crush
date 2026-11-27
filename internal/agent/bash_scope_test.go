package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/gitutil"
	"github.com/charmbracelet/crush/internal/shell"
)

// shellTool stands in for bash: running it runs fn, the "command", and
// returns resp and err.
type shellTool struct {
	fn   func()
	resp fantasy.ToolResponse
	err  error
}

func (s *shellTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: tools.BashToolName} }

func (s *shellTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if s.fn != nil {
		s.fn()
	}
	return s.resp, s.err
}

func (s *shellTool) ProviderOptions() fantasy.ProviderOptions     { return nil }
func (s *shellTool) SetProviderOptions(_ fantasy.ProviderOptions) {}

func gatedShell(inner *shellTool, scopes ...string) fantasy.AgentTool {
	return gateToolsForScopes([]fantasy.AgentTool{inner}, fixedScopes(scopes...), nil, "")[0]
}

func runShell(t *testing.T, tool fantasy.AgentTool, ctx context.Context) fantasy.ToolResponse {
	t.Helper()
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "b1", Name: tools.BashToolName, Input: `{"command":"x"}`})
	require.NoError(t, err)
	return resp
}

func writeIn(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func readIn(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}

func inWorktree(t *testing.T, repo string) context.Context {
	return withWorkspace(t.Context(), agentteam.Workspace{Root: repo, Dir: repo})
}

func TestBashScopeCheckRestoresAnOutOfScopeEdit(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "README.md", "sed -i was here\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")

	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "outside the write scopes of your claimed task (docs)")
	require.Contains(t, resp.Content, "Restored: README.md.")
	require.True(t, strings.HasSuffix(resp.Content, "ran"), "the command's own output follows")
	require.Equal(t, "hello\n", readIn(t, repo, "README.md"))
	require.Empty(t, gitInRepo(t, repo, "status", "--porcelain"))
}

func TestBashScopeCheckRemovesANewOutOfScopeFile(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	writeIn(t, repo, ".gitignore", "build/\n")
	gitInRepo(t, repo, "add", ".")
	gitInRepo(t, repo, "commit", "-m", "ignore build")
	tool := gatedShell(&shellTool{fn: func() {
		writeIn(t, repo, "notes.txt", "stray\n")
		writeIn(t, repo, "build/out.bin", "ignored\n")
	}, resp: fantasy.NewTextResponse("ran")}, "docs")

	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Restored: notes.txt.")
	require.NotContains(t, resp.Content, "build/out.bin", "ignored paths are not checked")
	require.NoFileExists(t, filepath.Join(repo, "notes.txt"))
	require.FileExists(t, filepath.Join(repo, "build", "out.bin"))
}

func TestBashScopeCheckKeepsInScopeEdits(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "docs/a.md", "docs\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, inWorktree(t, repo))
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "ran", resp.Content)
	require.Equal(t, "docs\n", readIn(t, repo, "docs/a.md"))
}

// TestBashScopeCheckKeepsInScopeEditsOfAMixedCommand pins Review Focus 2.
func TestBashScopeCheckKeepsInScopeEditsOfAMixedCommand(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() {
		writeIn(t, repo, "docs/a.md", "docs\n")
		writeIn(t, repo, "README.md", "changed\n")
	}, resp: fantasy.NewTextResponse("ran")}, "docs")

	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Restored: README.md.")
	require.NotContains(t, resp.Content, "docs/a.md")
	require.Equal(t, "docs\n", readIn(t, repo, "docs/a.md"), "the in-scope edit stays")
	require.Equal(t, "hello\n", readIn(t, repo, "README.md"))
}

func TestBashScopeCheckRestoresADirtyFileToItsSavedContent(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	writeIn(t, repo, "README.md", "mine\n")
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "README.md", "theirs\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Equal(t, "mine\n", readIn(t, repo, "README.md"), "a file dirty before the call gets that content back, not HEAD's")
}

func TestBashScopeCheckReportsAFileOverTheCap(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	writeIn(t, repo, "big.bin", strings.Repeat("x", 2<<20))
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "big.bin", "small\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Not restored")
	require.Contains(t, resp.Content, "could not be restored safely")
	require.Contains(t, resp.Content, "big.bin")
	require.Equal(t, "small\n", readIn(t, repo, "big.bin"), "a path over the 1 MiB cap is reported, not restored")
}

func TestBashScopeCheckFollowsACommit(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() {
		writeIn(t, repo, "README.md", "committed\n")
		gitInRepo(t, repo, "commit", "-am", "sneaky")
	}, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, inWorktree(t, repo))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Restored: README.md.")
	require.Equal(t, "hello\n", readIn(t, repo, "README.md"))
}

func TestBashScopeCheckMapsTheLeadSubdirectory(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	ctx := withWorkspace(t.Context(), agentteam.Workspace{Root: repo, Dir: filepath.Join(repo, "sub")})
	tool := gatedShell(&shellTool{fn: func() {
		writeIn(t, repo, "sub/docs/a.md", "in scope\n")
		writeIn(t, repo, "docs/a.md", "outside the Lead's directory\n")
	}, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, ctx)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Restored: docs/a.md.")
	require.Equal(t, "in scope\n", readIn(t, repo, "sub/docs/a.md"))
}

func TestBashScopeCheckNeedsAWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "README.md", "changed\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")
	resp := runShell(t, tool, t.Context())
	require.False(t, resp.IsError, "a teammate without a worktree keeps today's ungated bash")
	require.Equal(t, "changed\n", readIn(t, repo, "README.md"))
}

func TestBashScopeCheckHoldsABackgroundJob(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	ctx := inWorktree(t, repo)
	var job *shell.BackgroundShell
	inner := &shellTool{}
	inner.fn = func() {
		var err error
		job, err = shell.GetBackgroundShellManager().Start(context.Background(), nil, repo, nil,
			"sleep 1 && echo leak > leak.txt && echo finished", "bg", 10)
		require.NoError(t, err)
		meta, err := json.Marshal(tools.BashResponseMetadata{Background: true, ShellID: job.ID})
		require.NoError(t, err)
		inner.resp = fantasy.ToolResponse{Type: "text", Content: "Background shell started with ID: " + job.ID, Metadata: string(meta)}
	}
	resp := runShell(t, gatedShell(inner, "docs"), ctx)
	require.False(t, resp.IsError, "the job is checked when it finishes, not now")

	select {
	case <-job.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the background job never finished")
	}
	require.NoFileExists(t, filepath.Join(repo, "leak.txt"))
	stdout, _, done, _ := job.GetOutput()
	require.True(t, done)
	require.True(t, strings.HasPrefix(stdout, "This command changed files outside the write scopes"), stdout)
	require.Contains(t, stdout, "Restored: leak.txt.")
	require.Contains(t, stdout, "finished")
}

// startedJob is a bash stand-in that starts command as a real background
// job and answers with its shell ID, the way bash does.
type startedJob struct {
	tool *shellTool
	job  *shell.BackgroundShell
}

func startJobTool(t *testing.T, dir, command string) *startedJob {
	t.Helper()
	started := &startedJob{tool: &shellTool{}}
	started.tool.fn = func() {
		var err error
		started.job, err = shell.GetBackgroundShellManager().Start(context.Background(), nil, dir, nil, command, "bg", 10)
		require.NoError(t, err)
		meta, err := json.Marshal(tools.BashResponseMetadata{Background: true, ShellID: started.job.ID})
		require.NoError(t, err)
		started.tool.resp = fantasy.ToolResponse{Type: "text", Content: "Background shell started with ID: " + started.job.ID, Metadata: string(meta)}
	}
	return started
}

// wait blocks until the job finishes and returns its stdout, note included.
func (s *startedJob) wait(t *testing.T) string {
	t.Helper()
	select {
	case <-s.job.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the background job never finished")
	}
	stdout, _, done, _ := s.job.GetOutput()
	require.True(t, done)
	return stdout
}

// liveScopes is a claimed-scope source a test can change between the bash
// call and the job's finish, when the finisher reads it again.
type liveScopes struct {
	mu     sync.Mutex
	scopes []string
}

func (l *liveScopes) get(context.Context) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.scopes
}

func (l *liveScopes) set(scopes ...string) {
	l.mu.Lock()
	l.scopes = scopes
	l.mu.Unlock()
}

// liveWorkspace is a member workspace a test can change between the bash
// call and the job's finish.
type liveWorkspace struct {
	mu sync.Mutex
	ws agentteam.Workspace
	ok bool
}

func (l *liveWorkspace) get(context.Context) (agentteam.Workspace, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ws, l.ok
}

func (l *liveWorkspace) set(ws agentteam.Workspace, ok bool) {
	l.mu.Lock()
	l.ws, l.ok = ws, ok
	l.mu.Unlock()
}

// TestBashScopeJobChecksAgainstTheScopesItFindsAtFinish pins the finisher
// reading the member's claimed scopes again when the job ends.
func TestBashScopeJobChecksAgainstTheScopesItFindsAtFinish(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	started := agentteam.Workspace{Root: repo, Dir: repo}
	scopes := &liveScopes{scopes: []string{"internal"}}
	workspace := &liveWorkspace{ws: started, ok: true}
	job := startJobTool(t, repo, "sleep 1 && mkdir -p docs && echo docs > docs/a.md && echo finished")
	tool := gateToolsForScopes([]fantasy.AgentTool{job.tool}, scopes.get, workspace.get, "")[0]

	require.False(t, runShell(t, tool, withWorkspace(t.Context(), started)).IsError)
	scopes.set("docs")

	stdout := job.wait(t)
	require.NotContains(t, stdout, "outside the write scopes", "the scopes the member holds at finish apply")
	require.Contains(t, stdout, "finished")
	require.Equal(t, "docs\n", readIn(t, repo, "docs/a.md"))
}

// TestBashScopeJobSkipsTheCheckOnceTheTaskIsReleased pins W7: a released
// task leaves nothing to hold the job to, so nothing is restored.
func TestBashScopeJobSkipsTheCheckOnceTheTaskIsReleased(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	started := agentteam.Workspace{Root: repo, Dir: repo}
	scopes := &liveScopes{scopes: []string{"docs"}}
	workspace := &liveWorkspace{ws: started, ok: true}
	job := startJobTool(t, repo, "sleep 1 && echo leak > leak.txt && echo finished")
	tool := gateToolsForScopes([]fantasy.AgentTool{job.tool}, scopes.get, workspace.get, "")[0]

	require.False(t, runShell(t, tool, withWorkspace(t.Context(), started)).IsError)
	scopes.set()

	stdout := job.wait(t)
	require.Contains(t, stdout, "was not checked against your write scopes")
	require.Contains(t, stdout, "no longer holds them")
	require.NotContains(t, stdout, "Restored:")
	require.Contains(t, stdout, "finished")
	require.FileExists(t, filepath.Join(repo, "leak.txt"), "nothing is restored without a claim")
}

// TestBashScopeJobSkipsTheCheckWhenTheWorktreeMoved pins W7: a job never
// restores into a directory that is not the current workspace root.
func TestBashScopeJobSkipsTheCheckWhenTheWorktreeMoved(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	other := initTeamGitRepo(t)
	started := agentteam.Workspace{Root: repo, Dir: repo}
	workspace := &liveWorkspace{ws: started, ok: true}
	job := startJobTool(t, repo, "sleep 1 && echo leak > leak.txt && echo finished")
	tool := gateToolsForScopes([]fantasy.AgentTool{job.tool}, fixedScopes("docs"), workspace.get, "")[0]

	require.False(t, runShell(t, tool, withWorkspace(t.Context(), started)).IsError)
	workspace.set(agentteam.Workspace{Root: other, Dir: other}, true)

	stdout := job.wait(t)
	require.Contains(t, stdout, "was not checked against your write scopes")
	require.Contains(t, stdout, "not the one the command ran in")
	require.NotContains(t, stdout, "Could not check")
	require.Contains(t, stdout, "finished")
	require.FileExists(t, filepath.Join(repo, "leak.txt"), "the worktree the command ran in is left alone")
	require.NoFileExists(t, filepath.Join(other, "leak.txt"), "nothing is restored into another worktree")
}

// TestBashScopeJobSkipsTheCheckWhenTheWorktreeIsGone pins W7: a removed
// worktree is not recreated or written to.
func TestBashScopeJobSkipsTheCheckWhenTheWorktreeIsGone(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	started := agentteam.Workspace{Root: repo, Dir: repo}
	workspace := &liveWorkspace{ws: started, ok: true}
	job := startJobTool(t, repo, "sleep 1 && echo leak > leak.txt && echo finished")
	tool := gateToolsForScopes([]fantasy.AgentTool{job.tool}, fixedScopes("docs"), workspace.get, "")[0]

	require.False(t, runShell(t, tool, withWorkspace(t.Context(), started)).IsError)
	workspace.set(agentteam.Workspace{}, false)

	stdout := job.wait(t)
	require.Contains(t, stdout, "was not checked against your write scopes")
	require.Contains(t, stdout, "task worktree is gone")
	require.Contains(t, stdout, "finished")
	require.FileExists(t, filepath.Join(repo, "leak.txt"), "nothing is restored without a worktree")
}

// TestBashScopeCheckRestoresInALinkedWorktree runs the check where a
// teammate works: a linked worktree of the Lead's repository.
func TestBashScopeCheckRestoresInALinkedWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	worktree := filepath.Join(t.TempDir(), "team-task")
	gitInRepo(t, repo, "worktree", "add", "-b", "crush/team-task", worktree)
	if resolved, err := filepath.EvalSymlinks(worktree); err == nil {
		worktree = resolved
	}
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, worktree, "README.md", "changed\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")

	resp := runShell(t, tool, withWorkspace(t.Context(), agentteam.Workspace{Root: worktree, Dir: worktree}))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Restored: README.md.")
	require.Equal(t, "hello\n", readIn(t, worktree, "README.md"))
	require.Empty(t, gitInRepo(t, worktree, "status", "--porcelain"))
	require.Empty(t, gitInRepo(t, repo, "status", "--porcelain"), "the Lead's tree is untouched")
}

// TestBashScopeCheckRefusesWithoutARecord pins the plan choice: a bash call
// whose before-snapshot fails is refused, because it cannot be held.
func TestBashScopeCheckRefusesWithoutARecord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	called := false
	tool := gatedShell(&shellTool{fn: func() { called = true }, resp: fantasy.NewTextResponse("ran")}, "docs")

	resp := runShell(t, tool, withWorkspace(t.Context(), agentteam.Workspace{Root: dir, Dir: dir}))
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "bash is refused")
	require.Contains(t, resp.Content, "could not record your task worktree")
	require.False(t, called, "a command that cannot be recorded does not run")
}

// TestBashScopeCheckReportsTheRestoreWithTheError pins M1: when the call
// itself fails, the restore still reaches the model, appended to the error.
func TestBashScopeCheckReportsTheRestoreWithTheError(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	inner := &shellTool{
		fn:  func() { writeIn(t, repo, "README.md", "changed\n") },
		err: context.Canceled,
	}
	resp, err := gatedShell(inner, "docs").Run(inWorktree(t, repo), fantasy.ToolCall{ID: "b1", Name: tools.BashToolName, Input: `{"command":"x"}`})
	require.ErrorIs(t, err, context.Canceled)
	require.Contains(t, err.Error(), "Restored: README.md.")
	require.False(t, resp.IsError)
	require.Equal(t, "hello\n", readIn(t, repo, "README.md"))
}

// TestBashScopeCheckWaitsForAWorktreeCommit pins the shared lock: while a
// commit holds the worktree, the check waits rather than running its git
// work beside it.
func TestBashScopeCheckWaitsForAWorktreeCommit(t *testing.T) {
	t.Parallel()
	repo := initTeamGitRepo(t)
	unlock := gitutil.LockWorktree(repo)
	tool := gatedShell(&shellTool{fn: func() { writeIn(t, repo, "README.md", "changed\n") }, resp: fantasy.NewTextResponse("ran")}, "docs")
	type result struct {
		resp fantasy.ToolResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := tool.Run(inWorktree(t, repo), fantasy.ToolCall{ID: "b1", Name: tools.BashToolName, Input: `{"command":"x"}`})
		done <- result{resp, err}
	}()
	select {
	case <-done:
		t.Fatal("the check ran while a commit held the worktree")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.True(t, got.resp.IsError)
		require.Equal(t, "hello\n", readIn(t, repo, "README.md"))
	case <-time.After(10 * time.Second):
		t.Fatal("the check never ran once the commit released the worktree")
	}
}

// TestSettleMemberJobsStopsTheMembersJobs pins the coordinator half of W17:
// the jobs of the member are stopped, their checks have run when it returns,
// and another member's job keeps running.
func TestSettleMemberJobsStopsTheMembersJobs(t *testing.T) {
	// Not parallel: the fake runner gives every member the same session, so
	// two of these tests would kill each other's jobs in the one manager.
	coord, _ := newWorktreeTestCoordinator(t)
	claimInWorktree(t, coord, "impl", "docs")
	team := coord.teamService()
	session := team.MemberSession("impl")
	require.Equal(t, "session-fake", session, "the spawned run records its session")

	checked := make(chan struct{})
	job, err := shell.GetBackgroundShellManager().StartWithSession(t.Context(), nil, t.TempDir(), nil, "sleep 30", "bg", session, 10)
	require.NoError(t, err)
	job.SetFinisher(func() string {
		time.Sleep(50 * time.Millisecond)
		close(checked)
		return "checked"
	})
	other, err := shell.GetBackgroundShellManager().StartWithSession(t.Context(), nil, t.TempDir(), nil, "sleep 30", "bg", "session-other-member", 10)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = shell.GetBackgroundShellManager().Kill(ctx, other.ID)
		_ = shell.GetBackgroundShellManager().Kill(ctx, job.ID)
	})

	coord.settleMemberJobs(context.Background(), "impl")
	select {
	case <-checked:
	default:
		t.Fatal("settleMemberJobs returned before the job's scope check ran")
	}
	require.True(t, job.IsDone())
	_, tracked := shell.GetBackgroundShellManager().Get(job.ID)
	require.False(t, tracked, "a stopped job leaves the manager")
	_, otherTracked := shell.GetBackgroundShellManager().Get(other.ID)
	require.True(t, otherTracked, "another member's job keeps running")
}

// TestSettleMemberJobsLeavesALoneMemberAlone pins the constraint: a member
// with no task worktree keeps today's background jobs.
func TestSettleMemberJobsLeavesALoneMemberAlone(t *testing.T) {
	// Not parallel: see TestSettleMemberJobsStopsTheMembersJobs.
	coord := newTeammateTestCoordinator(t)
	team := coord.teamService()
	_, err := team.SpawnTeammate(t.Context(), agentteam.LeadName, agentteam.SpawnRequest{Name: "impl", Description: "d", Prompt: "p", ParentID: teamTestSessionID})
	require.NoError(t, err)
	session := team.MemberSession("impl")
	require.Equal(t, "session-fake", session)

	job, err := shell.GetBackgroundShellManager().StartWithSession(t.Context(), nil, t.TempDir(), nil, "sleep 30", "bg", session, 10)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = shell.GetBackgroundShellManager().Kill(ctx, job.ID)
	})

	coord.settleMemberJobs(context.Background(), "impl")
	_, tracked := shell.GetBackgroundShellManager().Get(job.ID)
	require.True(t, tracked, "a member without a worktree keeps its jobs")
	require.False(t, job.IsDone())
}
