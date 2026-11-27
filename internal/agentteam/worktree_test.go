package agentteam

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/gitutil"
)

// initTeamRepo is the Lead's tree for the worktree tests: a repository with
// one commit whose commits never ask for a signature.
func initTeamRepo(t *testing.T) string {
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
		gitIn(t, dir, args...)
	}
	writeTreeFile(t, dir, "README.md", "hello\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-m", "initial")
	return dir
}

// gitIn runs git in dir and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func writeTreeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func branchExists(repo, branch string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// worktreesDir is where gitutil.CreateWorktreeFrom puts the worktrees of
// repo.
func worktreesDir(repo string) string {
	return filepath.Join(filepath.Dir(repo), "."+filepath.Base(repo)+"-worktrees")
}

// resolvedPath is path with symlinks resolved and slashes normalized, so two
// spellings of one directory compare equal on every platform.
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.ToSlash(path)
}

// requireSamePath compares two paths to the same directory.
func requireSamePath(t *testing.T, want, got string) {
	t.Helper()
	require.Equal(t, resolvedPath(t, want), resolvedPath(t, got))
}

func newWorktreeService(t *testing.T, workingDir string, autostart bool) (*Service, *fakeRunner) {
	t.Helper()
	cfg := Config{WorkingDir: workingDir}
	if !autostart {
		cfg.Autostart = func() bool { return false }
	}
	return newPipelineService(t, cfg)
}

func spawnMember(t *testing.T, s *Service, name string) {
	t.Helper()
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{Name: name, Description: "d", Prompt: "p", ParentID: pipelineSession})
	require.NoError(t, err)
}

func TestDispatchGivesAScopedTaskItsWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, true)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "Write docs.", Role: "implementer", WriteScopes: []string{"docs"}})

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, "crush/team-"+task.ID, got.Branch)
	requireSamePath(t, filepath.Join(worktreesDir(repo), "team-"+task.ID), got.Worktree)
	require.DirExists(t, got.Worktree)
	require.Equal(t, got.Branch, gitutil.CurrentBranch(got.Worktree))
	require.Equal(t, gitIn(t, repo, "rev-parse", "HEAD"), gitIn(t, got.Worktree, "rev-parse", "HEAD"), "the worktree starts at the Lead's HEAD")
	require.Equal(t, 2, got.Revision, "recording the worktree is bookkeeping, not a new revision")
	require.Contains(t, runner.lastPrompt(), fmt.Sprintf("Worktree: %s (branch %s).", got.Worktree, got.Branch))
	require.Contains(t, runner.lastPrompt(), "relative paths resolve inside it")
	require.Contains(t, runner.lastPrompt(), "expected_revision 2", "the brief's revision is still the one to complete with")
}

func TestTasksWithoutScopesOrRepoGetNoWorktree(t *testing.T) {
	t.Parallel()
	t.Run("no scopes", func(t *testing.T) {
		t.Parallel()
		s, runner := newWorktreeService(t, initTeamRepo(t), true)
		task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
		require.Empty(t, taskByID(t, s, task.ID).Worktree)
		require.NotContains(t, runner.lastPrompt(), "Worktree:")
	})
	t.Run("not a repository", func(t *testing.T) {
		t.Parallel()
		s, runner := newWorktreeService(t, t.TempDir(), true)
		task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})
		got := taskByID(t, s, task.ID)
		require.Equal(t, TaskInProgress, got.Status, "the task starts in the shared tree, as before")
		require.Empty(t, got.Worktree)
		require.Equal(t, []string{"implementer-1"}, runner.memberNames())
	})
}

// TestWorktreeCreationFailureReleasesWithoutAnAttempt pins Review Focus 4.
func TestWorktreeCreationFailureReleasesWithoutAnAttempt(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	// A file where the worktrees directory goes makes git worktree add fail.
	require.NoError(t, os.WriteFile(worktreesDir(repo), []byte("not a directory"), 0o644))
	s, runner := newWorktreeService(t, repo, true)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Empty(t, got.OwnerID)
	require.Zero(t, got.Attempts, "the work never started, so nothing is charged")
	require.False(t, got.Escalated)
	require.Empty(t, got.Worktree)
	require.Empty(t, runner.memberNames(), "nothing starts without its worktree")
	require.Equal(t, StatusInactive, memberStatus(s, "implementer-1"), "the member made for the claim is free, not stuck provisioning")
	notices := leadNotices(t, s, pipelineSession)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0], "could not get its git worktree")

	// Another write runs a dispatch pass; the refused task is left alone.
	createTask(t, s, CreateTaskRequest{Subject: "Other", Description: "d"})
	require.Len(t, leadNotices(t, s, pipelineSession), 1, "a refused task waits for the Lead instead of looping")

	// The Lead edits it, and the dispatcher tries again.
	act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Description = "Write the docs." })
	require.Len(t, leadNotices(t, s, pipelineSession), 2)
	got = taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Zero(t, got.Attempts)
	require.Empty(t, runner.memberNames())
}

func TestManualClaimPlacesTheWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "rev")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}, Verify: new(true)})

	view := act(t, s, "impl", task.ID, "claim", nil)
	require.Equal(t, "crush/team-"+task.ID, view.Branch)
	require.DirExists(t, view.Worktree)
	ws, ok := s.MemberWorkspace("impl")
	require.True(t, ok)
	requireSamePath(t, view.Worktree, ws.Root)
	requireSamePath(t, view.Worktree, ws.Dir)
	require.Equal(t, view.Branch, ws.Branch)
	_, ok = s.MemberWorkspace("rev")
	require.False(t, ok, "a member with no scoped task works in the Lead tree")
	_, ok = s.MemberWorkspace(leadName)
	require.False(t, ok, "the Lead never leaves its tree")

	act(t, s, "impl", task.ID, "complete", nil)
	// The task is in review now, and the implementer keeps its worktree
	// until the verdict, so a running teammate never moves mid-run.
	ws, ok = s.MemberWorkspace("impl")
	require.True(t, ok, "work in review keeps the worktree of its implementer")
	requireSamePath(t, view.Worktree, ws.Root)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, "rev", check.ID, "claim", nil)
	ws, ok = s.MemberWorkspace("rev")
	require.True(t, ok, "the verifier runs in the worktree of the work it checks")
	requireSamePath(t, view.Worktree, ws.Root)
}

func TestLeadAssignPlacesTheWorktree(t *testing.T) {
	t.Parallel()
	s, _ := newWorktreeService(t, initTeamRepo(t), false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, leadName, task.ID, "reassign", func(r *UpdateTaskRequest) { r.Owner = "impl" })
	require.DirExists(t, view.Worktree)

	own := createTask(t, s, CreateTaskRequest{Subject: "Mine", Description: "d", WriteScopes: []string{"internal"}})
	require.Empty(t, act(t, s, leadName, own.ID, "claim", nil).Worktree, "the Lead's own claim stays in its tree")
}

func TestMemberWorkspaceKeepsTheLeadSubdirectory(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	writeTreeFile(t, repo, "sub/keep.txt", "keep\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "sub")
	s, _ := newWorktreeService(t, filepath.Join(repo, "sub"), false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)

	ws, ok := s.MemberWorkspace("impl")
	require.True(t, ok)
	requireSamePath(t, view.Worktree, ws.Root)
	requireSamePath(t, filepath.Join(view.Worktree, "sub"), ws.Dir)
	require.Equal(t, filepath.Join(resolvedPath(t, view.Worktree), "sub"), resolvedPath(t, ws.Dir), "the teammate starts where the Lead works")
	require.FileExists(t, filepath.Join(ws.Dir, "keep.txt"))
}

func TestReleasedTaskKeepsItsWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "impl2")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	first := act(t, s, "impl", task.ID, "claim", nil)
	act(t, s, "impl", task.ID, "release", nil)
	require.Equal(t, first.Worktree, taskByID(t, s, task.ID).Worktree)

	second := act(t, s, "impl2", task.ID, "claim", nil)
	require.Equal(t, first.Worktree, second.Worktree, "nothing is recreated")
	worktrees, err := gitutil.ListWorktrees(context.Background(), repo)
	require.NoError(t, err)
	require.Len(t, worktrees, 2, "the Lead's tree and the one task worktree")
}

func TestReopenedTaskKeepsItsWorktree(t *testing.T) {
	t.Parallel()
	s, _ := newWorktreeService(t, initTeamRepo(t), false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}, Verify: new(true)})
	view := act(t, s, "impl", task.ID, "claim", nil)
	act(t, s, "impl", task.ID, "complete", nil)
	act(t, s, leadName, task.ID, "reopen", nil)
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Equal(t, view.Worktree, got.Worktree)
	require.DirExists(t, view.Worktree)
	require.Equal(t, view.Worktree, act(t, s, "impl", task.ID, "claim", nil).Worktree)
}

func TestDeleteRemovesTheWorktreeAndBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)
	writeTreeFile(t, view.Worktree, "docs/draft.md", "unfinished\n")

	act(t, s, leadName, task.ID, "delete", nil)
	require.NoDirExists(t, view.Worktree)
	require.False(t, branchExists(repo, view.Branch), "an unmerged branch goes with its deleted task")
	require.Empty(t, leadNotices(t, s, pipelineSession))
}

func TestDeleteRemovalFailureNoticesTheLead(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)
	// A locked worktree refuses git worktree remove --force.
	gitIn(t, repo, "worktree", "lock", view.Worktree)

	act(t, s, leadName, task.ID, "delete", nil)
	require.Equal(t, TaskDeleted, taskByID(t, s, task.ID).Status, "the state change stands")
	notices := leadNotices(t, s, pipelineSession)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0], "Could not remove the git worktree of task "+task.ID)
	require.Contains(t, notices[0], view.Branch)
}

func TestOneScopedWorktreePerMember(t *testing.T) {
	t.Parallel()
	s, _ := newWorktreeService(t, initTeamRepo(t), false)
	spawnMember(t, s, "impl")
	a := createTask(t, s, CreateTaskRequest{Subject: "A", Description: "d", WriteScopes: []string{"docs"}})
	b := createTask(t, s, CreateTaskRequest{Subject: "B", Description: "d", WriteScopes: []string{"internal"}})
	c := createTask(t, s, CreateTaskRequest{Subject: "C", Description: "d"})
	act(t, s, "impl", a.ID, "claim", nil)

	err := actErr(s, "impl", b.ID, "claim", nil)
	require.Equal(t, CodeTaskInvalidTransition, ErrorCode(err))
	require.Contains(t, err.Error(), "already hold")
	require.Empty(t, taskByID(t, s, b.ID).Worktree)
	act(t, s, "impl", c.ID, "claim", nil)
}

func TestPlaceStartDiscardsAWorktreeForAMovedClaim(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})

	// The planned claim is stale: the task is not owned by impl.
	start := dispatchStart{member: memberByID(t, s, "impl"), taskID: task.ID, sessionID: pipelineSession, revision: task.Revision, task: taskByID(t, s, task.ID)}
	_, ok := s.placeStart(start)
	require.False(t, ok, "a claim the board no longer holds does not start")
	require.NoDirExists(t, filepath.Join(worktreesDir(repo), "team-"+task.ID))
	require.False(t, branchExists(repo, "crush/team-"+task.ID))
	require.Empty(t, taskByID(t, s, task.ID).Worktree)
}

// TestWorktreeCreationRunsOutsideTheLock pins that the git worktree of a
// claim is created with no Team lock held, like Runner.Start. The
// repository's post-checkout hook holds git inside git worktree add until
// the test has probed the locks.
func TestWorktreeCreationRunsOutsideTheLock(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	wait := filepath.Join(t.TempDir(), "hook")
	started, release := wait+".started", wait+".go"
	hooks := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\n: > %q\ni=0\nwhile [ ! -f %q ] && [ $i -lt 1000 ]; do sleep 0.01; i=$((i+1)); done\n",
		started, release)
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte(script), 0o755))
	gitIn(t, repo, "config", "core.hooksPath", hooks)
	t.Cleanup(func() {
		require.NoError(t, os.WriteFile(release, nil, 0o644))
	})

	var autostart atomic.Bool
	path := filepath.Join(t.TempDir(), "team.json")
	runner := newFakeRunner()
	s, err := New(path, Config{WorkingDir: repo, Autostart: autostart.Load}, runner)
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)
	key, err := filepath.Abs(path)
	require.NoError(t, err)

	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})
	autostart.Store(true)
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		s.dispatch(context.Background(), pipelineSession)
	}()

	require.Eventually(t, func() bool {
		_, err := os.Stat(started)
		return err == nil
	}, 10*time.Second, 5*time.Millisecond, "git must reach the worktree checkout")

	require.True(t, s.mu.TryLock(), "the worktree must be created with no Team lock held")
	s.mu.Unlock()
	value, held := stateLocks.Load(key)
	require.True(t, held)
	slot := value.(chan struct{})
	select {
	case slot <- struct{}{}:
		<-slot
	default:
		t.Fatal("the worktree must be created with no write slot held")
	}

	require.NoError(t, os.WriteFile(release, nil, 0o644))
	select {
	case <-dispatched:
	case <-time.After(10 * time.Second):
		t.Fatal("the dispatch pass did not finish")
	}
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.DirExists(t, got.Worktree)
	require.Equal(t, []string{"implementer-1"}, runner.memberNames())
}

func TestWorktreeFieldsAreOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	for _, value := range []any{Task{ID: "task-1"}, TaskView{ID: "task-1"}} {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		require.NotContains(t, string(data), "worktree")
		require.NotContains(t, string(data), "branch")
	}
}
