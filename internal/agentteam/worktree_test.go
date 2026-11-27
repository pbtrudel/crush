package agentteam

import (
	"context"
	"encoding/json"
	"errors"
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

// blockingGit holds git inside git worktree add until the test releases it,
// so a test can act on the board while a worktree is being created.
type blockingGit struct {
	started string
	release string
}

// blockWorktreeCreation installs a post-checkout hook that holds git inside
// git worktree add until the test releases it.
func blockWorktreeCreation(t *testing.T, repo string) *blockingGit {
	t.Helper()
	return newBlockingGit(t, repo, false)
}

// blockWorktreeCreationUntilFailure is blockWorktreeCreation for a git
// worktree add that fails once the test releases it.
func blockWorktreeCreationUntilFailure(t *testing.T, repo string) *blockingGit {
	t.Helper()
	return newBlockingGit(t, repo, true)
}

func newBlockingGit(t *testing.T, repo string, fail bool) *blockingGit {
	t.Helper()
	dir := t.TempDir()
	b := &blockingGit{started: filepath.Join(dir, "started"), release: filepath.Join(dir, "go")}
	hooks := filepath.Join(dir, "hooks")
	require.NoError(t, os.MkdirAll(hooks, 0o755))
	tail := ""
	if fail {
		tail = "exit 1\n"
	}
	script := fmt.Sprintf("#!/bin/sh\n: > %q\ni=0\nwhile [ ! -f %q ] && [ $i -lt 1000 ]; do sleep 0.01; i=$((i+1)); done\n%s",
		b.started, b.release, tail)
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte(script), 0o755))
	gitIn(t, repo, "config", "core.hooksPath", hooks)
	t.Cleanup(func() { b.releaseGit() })
	return b
}

// wait blocks until git reached the worktree checkout.
func (b *blockingGit) wait(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Stat(b.started)
		return err == nil
	}, 10*time.Second, 5*time.Millisecond, "git must reach the worktree checkout")
}

// releaseGit lets the held git worktree add finish.
func (b *blockingGit) releaseGit() {
	_ = os.WriteFile(b.release, nil, 0o644)
}

// waitDone waits for a goroutine's work to finish.
func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the git work did not finish")
	}
}

// taskStatus returns a task's status without failing the test.
func taskStatus(s *Service, id string) TaskStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.state.Tasks {
		if task.ID == id {
			return task.Status
		}
	}
	return ""
}

// newGatedWorktreeService opens a worktree service whose dispatch passes run
// only once the returned flag is set, so a test creates a task and starts
// the pass itself.
func newGatedWorktreeService(t *testing.T, workingDir string) (*Service, *fakeRunner, *atomic.Bool, string) {
	t.Helper()
	var autostart atomic.Bool
	path := filepath.Join(t.TempDir(), "team.json")
	runner := newFakeRunner()
	s, err := New(path, Config{WorkingDir: workingDir, Autostart: autostart.Load}, runner)
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)
	key, err := filepath.Abs(path)
	require.NoError(t, err)
	return s, runner, &autostart, key
}

// TestWorktreeCreationRunsOutsideTheLock pins that the git worktree of a
// claim is created with no Team lock held, like Runner.Start. The
// repository's post-checkout hook holds git inside git worktree add until
// the test has probed the locks.
func TestWorktreeCreationRunsOutsideTheLock(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	held := blockWorktreeCreation(t, repo)
	s, runner, autostart, key := newGatedWorktreeService(t, repo)

	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})
	autostart.Store(true)
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		s.dispatch(context.Background(), pipelineSession)
	}()

	held.wait(t)
	require.True(t, s.mu.TryLock(), "the worktree must be created with no Team lock held")
	s.mu.Unlock()
	value, ok := stateLocks.Load(key)
	require.True(t, ok)
	slot := value.(chan struct{})
	select {
	case slot <- struct{}{}:
		<-slot
	default:
		t.Fatal("the worktree must be created with no write slot held")
	}

	held.releaseGit()
	waitDone(t, dispatched)
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.DirExists(t, got.Worktree)
	require.Equal(t, []string{"implementer-1"}, runner.memberNames())
}

// TestDispatcherStartSurvivesALeadEditDuringGit pins that an edit landing
// while git worktree add runs does not rob the started member of its
// worktree: the claim is recorded and the member starts in it.
func TestDispatcherStartSurvivesALeadEditDuringGit(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	held := blockWorktreeCreation(t, repo)
	s, runner, autostart, _ := newGatedWorktreeService(t, repo)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})

	autostart.Store(true)
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		s.dispatch(context.Background(), pipelineSession)
	}()
	held.wait(t)
	act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Description = "Write the docs now." })
	held.releaseGit()
	waitDone(t, dispatched)

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, 3, got.Revision)
	requireSamePath(t, filepath.Join(worktreesDir(repo), "team-"+task.ID), got.Worktree)
	require.DirExists(t, got.Worktree)
	require.Equal(t, []string{"implementer-1"}, runner.memberNames(), "the claim still starts")
	require.Contains(t, runner.lastPrompt(), fmt.Sprintf("Worktree: %s (branch %s).", got.Worktree, got.Branch))
	require.Contains(t, runner.lastPrompt(), "expected_revision 3", "the brief carries the revision the edit left")
	ws, ok := s.MemberWorkspace("implementer-1")
	require.True(t, ok)
	requireSamePath(t, got.Worktree, ws.Root)
}

// TestManualClaimSurvivesALeadEditDuringGit is the same pin for a claim a
// teammate made itself: the claim returns with its worktree.
func TestManualClaimSurvivesALeadEditDuringGit(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	held := blockWorktreeCreation(t, repo)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})

	type claimResult struct {
		view TaskView
		err  error
	}
	result := make(chan claimResult, 1)
	go func() {
		view, err := s.UpdateTask("impl", updateRequest(s, task.ID, "claim", nil))
		result <- claimResult{view: view, err: err}
	}()
	held.wait(t)
	act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Description = "Write the docs now." })
	held.releaseGit()
	var claimed claimResult
	select {
	case claimed = <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("the claim did not finish")
	}
	require.NoError(t, claimed.err)

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, 3, got.Revision)
	requireSamePath(t, got.Worktree, claimed.view.Worktree)
	require.DirExists(t, claimed.view.Worktree)
	require.Equal(t, got.Revision, claimed.view.Revision, "the claim reports the task as the edit left it")
}

// TestReassignDuringCreationLeavesNoClaim pins that a Lead assignment that
// lands while git worktree add runs leaves the task with its new owner, the
// worktree discarded, and no claim behind.
func TestReassignDuringCreationLeavesNoClaim(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	held := blockWorktreeCreation(t, repo)
	s, runner, autostart, _ := newGatedWorktreeService(t, repo)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})

	autostart.Store(true)
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		s.dispatch(context.Background(), pipelineSession)
	}()
	held.wait(t)
	act(t, s, leadName, task.ID, "reassign", func(r *UpdateTaskRequest) { r.Owner = leadName })
	held.releaseGit()
	waitDone(t, dispatched)

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status, "the Lead's assignment stands")
	require.Equal(t, leadName, got.OwnerID)
	require.Empty(t, got.Worktree, "the worktree belongs to nobody")
	require.NoDirExists(t, filepath.Join(worktreesDir(repo), "team-"+task.ID))
	require.False(t, branchExists(repo, "crush/team-"+task.ID))
	require.Equal(t, StatusInactive, memberStatus(s, "implementer-1"), "the member made for the claim is free")
	require.Empty(t, runner.memberNames(), "nothing starts for a claim that moved on")
	s.mu.Lock()
	claims, starting := len(s.claims), len(s.starting)
	s.mu.Unlock()
	require.Zero(t, claims, "no claim is left behind")
	require.Zero(t, starting)
}

// TestParallelScopedClaimsGiveOneWorktree pins that a claim whose worktree
// is still being created already holds the member's one worktree, so a
// second scoped claim is refused.
func TestParallelScopedClaimsGiveOneWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	held := blockWorktreeCreation(t, repo)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	a := createTask(t, s, CreateTaskRequest{Subject: "A", Description: "d", WriteScopes: []string{"docs"}})
	b := createTask(t, s, CreateTaskRequest{Subject: "B", Description: "d", WriteScopes: []string{"internal"}})

	done := make(chan error, 1)
	go func() {
		_, err := s.UpdateTask("impl", updateRequest(s, a.ID, "claim", nil))
		done <- err
	}()
	held.wait(t)
	err := actErr(s, "impl", b.ID, "claim", nil)
	require.Equal(t, CodeTaskInvalidTransition, ErrorCode(err))
	require.Contains(t, err.Error(), "finish or release")
	require.Empty(t, taskByID(t, s, b.ID).Worktree)

	held.releaseGit()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the first claim did not finish")
	}
	require.DirExists(t, taskByID(t, s, a.ID).Worktree)
	require.Empty(t, taskByID(t, s, b.ID).Worktree)
}

// TestClaimRestoresAMissingWorktree pins that a task whose worktree
// directory went away gets it back, commits intact, before its member works
// there again.
func TestClaimRestoresAMissingWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)
	writeTreeFile(t, view.Worktree, "docs/draft.md", "committed\n")
	gitIn(t, view.Worktree, "add", ".")
	gitIn(t, view.Worktree, "commit", "-m", "draft")
	require.NoError(t, os.RemoveAll(view.Worktree))

	ws, ok := s.MemberWorkspace("impl")
	require.True(t, ok, "the recorded worktree is still the member's")
	require.True(t, ws.Missing, "a member is never placed in a path that is gone")
	require.Empty(t, ws.Dir)

	act(t, s, leadName, task.ID, "release", nil)
	again := act(t, s, "impl", task.ID, "claim", nil)
	requireSamePath(t, view.Worktree, again.Worktree)
	require.DirExists(t, again.Worktree)
	require.FileExists(t, filepath.Join(again.Worktree, "docs", "draft.md"), "the branch keeps its commits")
	ws, ok = s.MemberWorkspace("impl")
	require.True(t, ok)
	require.False(t, ws.Missing)
	requireSamePath(t, again.Worktree, ws.Root)
}

// TestDispatchRestoresAMissingWorktree is the same pin for a task the
// dispatcher starts again.
func TestDispatchRestoresAMissingWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, true)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})
	first := taskByID(t, s, task.ID)
	require.DirExists(t, first.Worktree)
	writeTreeFile(t, first.Worktree, "docs/draft.md", "committed\n")
	gitIn(t, first.Worktree, "add", ".")
	gitIn(t, first.Worktree, "commit", "-m", "draft")
	require.NoError(t, os.RemoveAll(first.Worktree))

	// The Lead hands the task back to the board; the next pass restores it.
	act(t, s, leadName, task.ID, "reassign", func(r *UpdateTaskRequest) { r.Owner = "" })
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	requireSamePath(t, first.Worktree, got.Worktree)
	require.DirExists(t, got.Worktree)
	require.FileExists(t, filepath.Join(got.Worktree, "docs", "draft.md"), "the branch keeps its commits")
	require.Equal(t, []string{"implementer-1", "implementer-2"}, runner.memberNames())
	require.Contains(t, runner.lastPrompt(), "Worktree: "+got.Worktree)
}

// TestReassignToTheSameMemberKeepsOneWorktree pins P7: a Lead assignment to
// the member the dispatcher already claimed for adopts the worktree that
// claim placed, instead of releasing the task the member runs on.
func TestReassignToTheSameMemberKeepsOneWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	held := blockWorktreeCreation(t, repo)
	s, runner, autostart, _ := newGatedWorktreeService(t, repo)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})

	autostart.Store(true)
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		s.dispatch(context.Background(), pipelineSession)
	}()
	held.wait(t)
	type assignResult struct {
		view TaskView
		err  error
	}
	assigned := make(chan assignResult, 1)
	go func() {
		view, err := s.UpdateTask(leadName, updateRequest(s, task.ID, "reassign", func(r *UpdateTaskRequest) { r.Owner = "implementer-1" }))
		assigned <- assignResult{view: view, err: err}
	}()
	held.releaseGit()
	waitDone(t, dispatched)
	var result assignResult
	select {
	case result = <-assigned:
	case <-time.After(10 * time.Second):
		t.Fatal("the assignment did not finish")
	}
	require.NoError(t, result.err)

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status, "the running task keeps its owner")
	require.Equal(t, memberID(t, s, "implementer-1"), got.OwnerID)
	require.NotEmpty(t, got.Worktree)
	requireSamePath(t, got.Worktree, result.view.Worktree)
	worktrees, err := gitutil.ListWorktrees(context.Background(), repo)
	require.NoError(t, err)
	require.Len(t, worktrees, 2, "the Lead's tree and the one task worktree")
	require.Equal(t, []string{"implementer-1"}, runner.memberNames())
	require.Contains(t, runner.lastPrompt(), "Worktree: "+got.Worktree)
	require.Empty(t, leadNotices(t, s, pipelineSession))
}

// TestFailedCreationReleasesAfterALeadEdit pins N1 on the dispatcher path: a
// worktree that fails while the Lead edits the task still hands the claim
// back, pending and uncharged.
func TestFailedCreationReleasesAfterALeadEdit(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	held := blockWorktreeCreationUntilFailure(t, repo)
	s, runner, autostart, _ := newGatedWorktreeService(t, repo)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})

	autostart.Store(true)
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		s.dispatch(context.Background(), pipelineSession)
	}()
	held.wait(t)
	act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Description = "Write the docs." })
	held.releaseGit()
	waitDone(t, dispatched)

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Empty(t, got.OwnerID)
	require.Zero(t, got.Attempts, "the work never started, so nothing is charged")
	require.Empty(t, got.Worktree)
	require.Empty(t, runner.memberNames(), "nothing starts without its worktree")
	require.Equal(t, StatusInactive, memberStatus(s, "implementer-1"))
	notices := leadNotices(t, s, pipelineSession)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0], "could not get its git worktree")
}

// TestFailedClaimReleasesAfterALeadEdit is the same pin for a claim a
// teammate made itself, and its error says the claim was released.
func TestFailedClaimReleasesAfterALeadEdit(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	held := blockWorktreeCreationUntilFailure(t, repo)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})

	type claimResult struct {
		view TaskView
		err  error
	}
	result := make(chan claimResult, 1)
	go func() {
		view, err := s.UpdateTask("impl", updateRequest(s, task.ID, "claim", nil))
		result <- claimResult{view: view, err: err}
	}()
	held.wait(t)
	act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Description = "Write the docs." })
	held.releaseGit()
	var claimed claimResult
	select {
	case claimed = <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("the claim did not finish")
	}
	require.Equal(t, CodeTaskGit, ErrorCode(claimed.err))
	require.Contains(t, claimed.err.Error(), "claim was released")

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Empty(t, got.OwnerID)
	require.Zero(t, got.Attempts, "the work never started, so nothing is charged")
	require.Empty(t, got.Worktree)
	notices := leadNotices(t, s, pipelineSession)
	require.Len(t, notices, 1)
	require.Contains(t, notices[0], "could not get its git worktree")
}

// TestDeleteDuringRestoreDoesNotStartTheMember pins P8: a restore whose
// claim moves on is not launched.
func TestDeleteDuringRestoreDoesNotStartTheMember(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, true)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})
	first := taskByID(t, s, task.ID)
	require.DirExists(t, first.Worktree)
	require.NoError(t, os.RemoveAll(first.Worktree))

	held := blockWorktreeCreation(t, repo)
	handedBack := make(chan struct{})
	go func() {
		defer close(handedBack)
		_, _ = s.UpdateTask(leadName, updateRequest(s, task.ID, "reassign", func(r *UpdateTaskRequest) { r.Owner = "" }))
	}()
	held.wait(t)
	deleted := make(chan struct{})
	go func() {
		defer close(deleted)
		_, _ = s.UpdateTask(leadName, updateRequest(s, task.ID, "delete", nil))
	}()
	require.Eventually(t, func() bool { return taskStatus(s, task.ID) == TaskDeleted },
		10*time.Second, 5*time.Millisecond, "the delete must land before git finishes")
	held.releaseGit()
	waitDone(t, handedBack)
	waitDone(t, deleted)

	require.Equal(t, TaskDeleted, taskByID(t, s, task.ID).Status)
	require.Equal(t, []string{"implementer-1"}, runner.memberNames(), "no member starts for a claim that moved on")
	s.mu.Lock()
	claims := len(s.claims)
	s.mu.Unlock()
	require.Zero(t, claims)
}

// TestRestoreFinishingAfterADeleteLeavesNoOrphan pins W20.5: a delete clears
// the task's worktree record, and a restore that finishes after it discards
// the directory and the branch it recreated, so a deleted task leaves nothing
// behind.
func TestRestoreFinishingAfterADeleteLeavesNoOrphan(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
	gitIn(t, view.Worktree, "add", ".")
	gitIn(t, view.Worktree, "commit", "-m", "draft")
	claimed := taskByID(t, s, task.ID)

	act(t, s, "impl", task.ID, "delete", nil)
	deleted := taskByID(t, s, task.ID)
	require.Equal(t, TaskDeleted, deleted.Status)
	require.Empty(t, deleted.Worktree, "a deleted task keeps no worktree record")
	require.Empty(t, deleted.Branch, "a deleted task keeps no branch record")
	require.NoDirExists(t, view.Worktree)
	require.False(t, branchExists(repo, view.Branch))

	// A restore that was under way when the delete landed recreates the
	// worktree from a branch that no longer exists, and its claim is gone.
	ctx, cancel := gitContext()
	defer cancel()
	placed, err := s.placeWorktree(ctx, claimed)
	require.NoError(t, err)
	require.Equal(t, view.Worktree, placed.path)
	_, outcome := s.recordPlacedWorktree(claimed, placed.path, placed.branch)
	require.Equal(t, placedLost, outcome, "the deleted task holds no claim")
	require.DirExists(t, placed.path)

	s.discardUnnamedWorktree(ctx, claimed, placed)
	require.NoDirExists(t, placed.path, "a deleted task leaves no worktree")
	require.False(t, branchExists(repo, placed.branch), "a deleted task leaves no branch")
}

// TestLostClaimKeepsTheWorktreeItDidNotMake pins P11: a claim whose record
// misses must never remove a worktree the task still records, or the work
// another member has not committed goes with it.
func TestLostClaimKeepsTheWorktreeItDidNotMake(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "impl2")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	stale := act(t, s, "impl", task.ID, "claim", nil)
	writeTreeFile(t, stale.Worktree, "docs/draft.md", "unfinished\n")

	// The Lead hands the task to another member while impl still holds the
	// claim it made, so impl's placement of that claim runs late.
	assigned := act(t, s, leadName, task.ID, "reassign", func(r *UpdateTaskRequest) { r.Owner = "impl2" })
	requireSamePath(t, stale.Worktree, assigned.Worktree)
	_, err := s.placeClaim(stale)
	require.Equal(t, CodeTaskGit, ErrorCode(err))

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, memberID(t, s, "impl2"), got.OwnerID)
	requireSamePath(t, stale.Worktree, got.Worktree)
	require.DirExists(t, stale.Worktree, "the worktree the task records is still there")
	require.FileExists(t, filepath.Join(stale.Worktree, "docs", "draft.md"), "uncommitted work survives a lost claim")
	worktrees, err := gitutil.ListWorktrees(context.Background(), repo)
	require.NoError(t, err)
	require.Len(t, worktrees, 2, "the Lead's tree and the one task worktree")
	require.Empty(t, leadNotices(t, s, pipelineSession))
}

// TestDiscardKeepsAWorktreeThisCallDidNotMake pins the two guards behind
// P11: a placement removes only what it made, and only when the board does
// not record that path.
func TestDiscardKeepsAWorktreeThisCallDidNotMake(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)
	claimed := Task{ID: view.ID, SessionID: view.SessionID, OwnerID: view.OwnerID, Worktree: view.Worktree, Branch: view.Branch}

	// A reused worktree was not made here, so it stays whoever owns the
	// task.
	s.discardUnnamedWorktree(context.Background(), claimed, placedWorktree{path: view.Worktree, branch: view.Branch})
	require.DirExists(t, view.Worktree)

	// A restored one is this call's, but the task still records its path,
	// so the member that holds the task now keeps it.
	s.discardUnnamedWorktree(context.Background(), claimed, placedWorktree{path: view.Worktree, branch: view.Branch, made: true})
	require.DirExists(t, view.Worktree)
}

// TestReleaseSkipsATaskThatHasItsWorktree pins that a stale worktree failure
// does not hand back a task whose member already works in its worktree.
func TestReleaseSkipsATaskThatHasItsWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", WriteScopes: []string{"docs"}})
	view := act(t, s, "impl", task.ID, "claim", nil)

	released := true
	_ = s.write(mustApply, func() (bool, error) {
		released = s.releaseForWorktreeLocked(view.ID, view.SessionID, view.OwnerID, errors.New("stale failure"))
		return false, nil
	})
	require.False(t, released)
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, view.OwnerID, got.OwnerID)
	requireSamePath(t, view.Worktree, got.Worktree)
	require.Empty(t, leadNotices(t, s, pipelineSession))
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
