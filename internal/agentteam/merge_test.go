package agentteam

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/gitutil"
)

// scopedTask creates a docs-scoped task and has member claim it, which gives
// it its worktree.
func scopedTask(t *testing.T, s *Service, member, subject string, verify bool) TaskView {
	t.Helper()
	task := createTask(t, s, CreateTaskRequest{Subject: subject, Description: "d", WriteScopes: []string{"docs"}, Verify: new(verify)})
	view := act(t, s, member, task.ID, "claim", nil)
	require.NotEmpty(t, view.Worktree)
	return view
}

// passCheck has reviewer claim the check of taskID and pass it.
func passCheck(t *testing.T, s *Service, reviewer, taskID string) {
	t.Helper()
	check := verificationTaskFor(t, s, taskID)
	act(t, s, reviewer, check.ID, "claim", nil)
	act(t, s, reviewer, check.ID, "pass", nil)
}

func readTreeFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(data)
}

func lastLeadNotice(t *testing.T, s *Service) string {
	t.Helper()
	notices := leadNotices(t, s, pipelineSession)
	require.NotEmpty(t, notices)
	return notices[len(notices)-1]
}

// failingPreCommit makes every commit in repo, worktrees included, fail.
func failingPreCommit(t *testing.T, repo string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the test hook is a shell script")
	}
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	require.NoError(t, os.MkdirAll(filepath.Dir(hook), 0o755))
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\necho refused by hook >&2\nexit 1\n"), 0o755))
}

// runSettled reports whether name's fake run was cancelled.
func runSettled(r *fakeRunner, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[name]
	if !ok {
		return false
	}
	select {
	case <-run.Done():
		return true
	default:
		return false
	}
}

func TestCompleteCommitsAndPassMergesTheBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "rev")
	view := scopedTask(t, s, "impl", "Write the docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	act(t, s, "impl", view.ID, "complete", nil)
	require.Equal(t, TaskInReview, taskByID(t, s, view.ID).Status)
	require.Equal(t, "Write the docs", gitIn(t, view.Worktree, "log", "-1", "--format=%s"), "complete commits with the subject")
	require.Empty(t, gitIn(t, view.Worktree, "status", "--porcelain"))
	require.Contains(t, verificationTaskFor(t, s, view.ID).Description, "committed on branch "+view.Branch)

	passCheck(t, s, "rev", view.ID)
	got := taskByID(t, s, view.ID)
	require.Equal(t, TaskCompleted, got.Status)
	require.Empty(t, got.Worktree, "a merged task forgets its removed worktree")
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
	require.Equal(t, "Merge team task "+view.ID+": Write the docs", gitIn(t, repo, "log", "-1", "--format=%s"))
	require.Len(t, strings.Fields(gitIn(t, repo, "rev-list", "--parents", "-n", "1", "HEAD")), 3, "--no-ff makes a merge commit")
	require.NoDirExists(t, view.Worktree)
	require.False(t, branchExists(repo, view.Branch))
}

func TestEmptyChangeSetStillMovesToReview(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Nothing to do", true)
	act(t, s, "impl", view.ID, "complete", nil)
	require.Equal(t, TaskInReview, taskByID(t, s, view.ID).Status)
	require.Equal(t, gitIn(t, repo, "rev-parse", "HEAD"), gitIn(t, view.Worktree, "rev-parse", "HEAD"), "no empty commit")
}

func TestCommitFailureRefusesComplete(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	failingPreCommit(t, repo)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	err := actErr(s, "impl", view.ID, "complete", nil)
	require.Equal(t, CodeTaskGit, ErrorCode(err))
	require.Contains(t, err.Error(), "refused by hook")
	got := taskByID(t, s, view.ID)
	require.Equal(t, TaskInProgress, got.Status, "the task is unchanged")
	require.Equal(t, view.Revision, got.Revision)
}

func TestSettleCommitFailureReleasesTheTask(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	failingPreCommit(t, repo)
	s, runner := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	runner.finish("impl")
	require.Eventually(t, func() bool { return taskByID(t, s, view.ID).Status == TaskPending }, 5*time.Second, 20*time.Millisecond,
		"work that is not on its branch is released, not merged")
	require.Equal(t, view.Worktree, taskByID(t, s, view.ID).Worktree)
	require.Equal(t, "docs\n", readTreeFile(t, view.Worktree, "docs/a.md"), "the work stays for the next turn")
	require.NoFileExists(t, filepath.Join(repo, "docs", "a.md"))
}

func TestFailKeepsTheWorktree(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "rev")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
	act(t, s, "impl", view.ID, "complete", nil)
	check := verificationTaskFor(t, s, view.ID)
	act(t, s, "rev", check.ID, "claim", nil)
	act(t, s, "rev", check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "Missing a section" })

	require.Equal(t, TaskInProgress, taskByID(t, s, view.ID).Status)
	require.DirExists(t, view.Worktree)
	require.True(t, branchExists(repo, view.Branch))
	ws, ok := s.MemberWorkspace("impl")
	require.True(t, ok)
	require.Equal(t, view.Worktree, ws.Root, "the implementer is back in its worktree")
	require.NoFileExists(t, filepath.Join(repo, "docs", "a.md"))
}

// TestReopenedTaskShowsPriorWorkToTheNextImplementer pins Review Focus 3.
func TestReopenedTaskShowsPriorWorkToTheNextImplementer(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, true)
	task := createTask(t, s, CreateTaskRequest{Subject: "Docs", Description: "d", Role: "implementer", WriteScopes: []string{"docs"}})
	first := taskByID(t, s, task.ID)
	require.NotEmpty(t, first.Worktree)
	writeTreeFile(t, first.Worktree, "docs/a.md", "first try\n")
	act(t, s, "implementer-1", task.ID, "complete", nil)
	act(t, s, leadName, task.ID, "reopen", nil)

	// implementer-1's turn is still running, so a second member takes it.
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, memberID(t, s, "implementer-2"), got.OwnerID)
	require.Equal(t, first.Worktree, got.Worktree, "the reopened task keeps its worktree")
	require.Contains(t, runner.lastPrompt(), "Worktree: "+first.Worktree)
	require.Equal(t, "first try\n", readTreeFile(t, got.Worktree, "docs/a.md"))
	require.Equal(t, "Docs", gitIn(t, got.Worktree, "log", "-1", "--format=%s"), "the prior work is committed on the branch")
	ws, ok := s.MemberWorkspace("implementer-2")
	require.True(t, ok)
	require.Equal(t, first.Worktree, ws.Root)
}

func TestLeadAcceptMergesTheBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
	act(t, s, "impl", view.ID, "complete", nil)
	check := verificationTaskFor(t, s, view.ID)

	act(t, s, leadName, view.ID, "complete", nil)
	require.Equal(t, TaskCompleted, taskByID(t, s, view.ID).Status)
	require.Equal(t, TaskDeleted, taskByID(t, s, check.ID).Status, "accepted work keeps no check")
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
	require.NoDirExists(t, view.Worktree)
	require.False(t, branchExists(repo, view.Branch))
}

func TestUnverifiedCompleteMergesTheBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	done := act(t, s, "impl", view.ID, "complete", nil)
	require.Equal(t, TaskCompleted, done.Status, "the view shows the merged outcome")
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
	require.NoDirExists(t, view.Worktree)
}

func TestSettledTurnCommitsAndMerges(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	runner.finish("impl")
	require.Eventually(t, func() bool { return taskByID(t, s, view.ID).Status == TaskCompleted }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
	// The removal follows the write that completes the task, so wait for it.
	require.Eventually(t, func() bool { return !dirExists(view.Worktree) }, 5*time.Second, 20*time.Millisecond,
		"the merged worktree goes")
}

func TestMergeConflictEscalatesAndKeepsTheBranch(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "rev")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "README.md", "branch\n")
	act(t, s, "impl", view.ID, "complete", nil)
	// The Lead's tree moves on underneath the branch.
	writeTreeFile(t, repo, "README.md", "lead\n")
	gitIn(t, repo, "commit", "-am", "lead change")

	passCheck(t, s, "rev", view.ID)
	got := taskByID(t, s, view.ID)
	require.Equal(t, TaskInReview, got.Status, "the original completes only after a merge")
	require.True(t, got.Escalated)
	require.DirExists(t, view.Worktree)
	require.True(t, branchExists(repo, view.Branch))
	require.Equal(t, "lead\n", readTreeFile(t, repo, "README.md"))
	require.Empty(t, gitIn(t, repo, "status", "--porcelain"), "the aborted merge leaves the Lead's tree clean")
	notice := lastLeadNotice(t, s)
	require.Contains(t, notice, view.Branch)
	require.Contains(t, notice, "conflict")
	require.NotContains(t, notice, "Nothing was changed", "a refused merge never claims the tree is untouched")
	require.Contains(t, notice, "as git left it")
}

// TestMergeRefusedOnADirtyOrDetachedLeadTree pins Review Focus 1.
func TestMergeRefusedOnADirtyOrDetachedLeadTree(t *testing.T) {
	t.Parallel()
	t.Run("dirty", func(t *testing.T) {
		t.Parallel()
		repo := initTeamRepo(t)
		s, _ := newWorktreeService(t, repo, false)
		spawnMember(t, s, "impl")
		spawnMember(t, s, "rev")
		view := scopedTask(t, s, "impl", "Docs", true)
		writeTreeFile(t, view.Worktree, "README.md", "branch\n")
		act(t, s, "impl", view.ID, "complete", nil)
		writeTreeFile(t, repo, "README.md", "local\n")

		passCheck(t, s, "rev", view.ID)
		got := taskByID(t, s, view.ID)
		require.Equal(t, TaskInReview, got.Status)
		require.True(t, got.Escalated)
		require.Equal(t, "local\n", readTreeFile(t, repo, "README.md"), "the Lead's uncommitted change is untouched")
		require.DirExists(t, view.Worktree, "a refused merge keeps the worktree")
		require.True(t, branchExists(repo, view.Branch))
		notice := lastLeadNotice(t, s)
		require.Contains(t, notice, view.Branch)
		require.Contains(t, notice, "as git left it")
		require.NotContains(t, notice, "Nothing was changed")
	})
	t.Run("detached", func(t *testing.T) {
		t.Parallel()
		repo := initTeamRepo(t)
		s, _ := newWorktreeService(t, repo, false)
		spawnMember(t, s, "impl")
		spawnMember(t, s, "rev")
		view := scopedTask(t, s, "impl", "Docs", true)
		writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
		act(t, s, "impl", view.ID, "complete", nil)
		gitIn(t, repo, "checkout", "--detach")

		passCheck(t, s, "rev", view.ID)
		got := taskByID(t, s, view.ID)
		require.Equal(t, TaskInReview, got.Status)
		require.True(t, got.Escalated)
		require.NoFileExists(t, filepath.Join(repo, "docs", "a.md"))
		notice := lastLeadNotice(t, s)
		require.Contains(t, notice, "detached")
		require.Contains(t, notice, view.Branch)

		// The Lead fixes its tree and accepts the work: the merge runs now.
		gitIn(t, repo, "checkout", "main")
		act(t, s, leadName, view.ID, "complete", nil)
		require.Equal(t, TaskCompleted, taskByID(t, s, view.ID).Status)
		require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
		require.NoDirExists(t, view.Worktree)
	})
}

func TestLeadAcceptRefusedMergeReturnsTheError(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
	act(t, s, "impl", view.ID, "complete", nil)
	gitIn(t, repo, "checkout", "--detach")

	err := actErr(s, leadName, view.ID, "complete", nil)
	require.Equal(t, CodeTaskGit, ErrorCode(err))
	require.Contains(t, err.Error(), view.Branch)
	require.NotContains(t, err.Error(), "Nothing was changed")
	got := taskByID(t, s, view.ID)
	require.Equal(t, TaskInReview, got.Status)
	require.True(t, got.Escalated)
}

func TestMergeAfterTheBoardMovedKeepsTheMerge(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", true)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")
	act(t, s, "impl", view.ID, "complete", nil)

	stale := mergeRequestFor(taskByID(t, s, view.ID))
	stale.revision--
	s.mergeTask(context.Background(), stale, leadName)
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"), "the merge happened")
	require.Equal(t, TaskInReview, taskByID(t, s, view.ID).Status, "the write did not apply to a moved task")
	require.DirExists(t, view.Worktree)
	require.Contains(t, lastLeadNotice(t, s), "The merge stays in the Lead's tree")
}

// TestLeadAcceptClearsTheLastFailure pins the carried Task 2 review item: a
// Lead accepting in-review work ends the failure the task still records.
func TestLeadAcceptClearsTheLastFailure(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Verify: new(true)})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, check.ID, "claim", nil)
	act(t, s, leadName, check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "Missing tests" })
	require.Equal(t, "Missing tests", taskByID(t, s, task.ID).LastFailure)

	act(t, s, leadName, task.ID, "complete", nil)
	require.Equal(t, "Missing tests", taskByID(t, s, task.ID).LastFailure, "the notes stay for the next brief")
	act(t, s, leadName, task.ID, "complete", nil)

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskCompleted, got.Status)
	require.Empty(t, got.LastFailure, "accepting the work clears the failure notes")
}

// TestCompleteNeedsTheOwnerOrTheLead pins controller ruling W6: a teammate
// that does not own the work never commits it.
func TestCompleteNeedsTheOwnerOrTheLead(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, _ := newWorktreeService(t, repo, false)
	spawnMember(t, s, "impl")
	spawnMember(t, s, "impl2")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	err := actErr(s, "impl2", view.ID, "complete", nil)
	require.Equal(t, CodeTaskUnauthorized, ErrorCode(err))
	require.Equal(t, TaskInProgress, taskByID(t, s, view.ID).Status)
	require.Equal(t, gitIn(t, repo, "rev-parse", "HEAD"), gitIn(t, view.Worktree, "rev-parse", "HEAD"),
		"a complete by another member commits nothing")
	require.Equal(t, "docs\n", readTreeFile(t, view.Worktree, "docs/a.md"))
}

// TestMergeRefusedTextReportsAFailedAbort pins that a notice never claims the
// Lead's tree was left clean when the abort itself failed.
func TestMergeRefusedTextReportsAFailedAbort(t *testing.T) {
	t.Parallel()
	m := mergeRequest{taskID: "task-1", subject: "Docs", branch: "crush/team-task-1", worktree: "/tmp/wt"}

	failed := mergeRefusedText(m, &gitutil.MergeError{Reason: "the merge has conflicts and could not be aborted", AbortFailed: true})
	require.Contains(t, failed, "could not be aborted")
	require.Contains(t, failed, m.branch)
	require.NotContains(t, failed, "was aborted")
	require.NotContains(t, failed, "as git left it")

	refused := mergeRefusedText(m, &gitutil.MergeError{Reason: "git refused the merge"})
	require.Contains(t, refused, "as git left it")
	require.NotContains(t, refused, "could not be aborted")
}

// TestMergeStaysTextKeepsItsClaimAboutTheWorktree pins that the notice says
// the worktree is kept only when the board still holds it.
func TestMergeStaysTextKeepsItsClaimAboutTheWorktree(t *testing.T) {
	t.Parallel()
	m := mergeRequest{taskID: "task-1", subject: "Docs", branch: "crush/team-task-1", worktree: "/tmp/wt"}

	kept := mergeStaysText(m, "the task update was refused", true)
	require.Contains(t, kept, "The merge stays in the Lead's tree")
	require.Contains(t, kept, "worktree at /tmp/wt is kept")

	gone := mergeStaysText(m, "the task update was refused", false)
	require.Contains(t, gone, "The merge stays in the Lead's tree")
	require.NotContains(t, gone, "is kept")
	require.Contains(t, gone, "/tmp/wt")
}

// actPrompt applies action at the task's current revision and fails when the
// call takes longer than a teammate tool call should, which is what happens
// when the service waits for the caller's own run to settle.
func actPrompt(t *testing.T, s *Service, caller, taskID, action string, edit func(*UpdateTaskRequest)) TaskView {
	t.Helper()
	req := updateRequest(s, taskID, action, edit)
	type result struct {
		view TaskView
		err  error
	}
	done := make(chan result, 1)
	go func() {
		view, err := s.UpdateTask(caller, req)
		done <- result{view: view, err: err}
	}()
	select {
	case got := <-done:
		require.NoError(t, got.err)
		return got.view
	case <-time.After(2 * time.Second):
		t.Fatal("the call waited for the caller's own run to settle")
	}
	return TaskView{}
}

// TestOwnUnverifiedCompleteDoesNotStopItsOwnRun pins ruling W14: a member's
// own complete never interrupts its own turn, which cannot settle until the
// tool call returns.
func TestOwnUnverifiedCompleteDoesNotStopItsOwnRun(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, false)
	// The turn is inside the tool call, so it cannot settle until the call
	// returns.
	release := make(chan struct{})
	runner.setTurn(func(ctx context.Context) { <-release })
	t.Cleanup(func() { close(release) })
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/a.md", "docs\n")

	actPrompt(t, s, "impl", view.ID, "complete", nil)

	require.Equal(t, TaskCompleted, taskByID(t, s, view.ID).Status)
	require.Equal(t, "docs\n", readTreeFile(t, repo, "docs/a.md"))
	require.NoDirExists(t, view.Worktree, "the merged worktree goes")
	require.False(t, runner.turnCancelled("impl"), "a member never interrupts its own run")
	require.False(t, runSettled(runner, "impl"), "the turn is still running")
}

// TestOwnDeleteDoesNotStopItsOwnRun is the same pin for a delete.
func TestOwnDeleteDoesNotStopItsOwnRun(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, false)
	release := make(chan struct{})
	runner.setTurn(func(ctx context.Context) { <-release })
	t.Cleanup(func() { close(release) })
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/wip.md", "wip\n")

	actPrompt(t, s, "impl", view.ID, "delete", nil)

	require.Equal(t, TaskDeleted, taskByID(t, s, view.ID).Status)
	require.False(t, runner.turnCancelled("impl"), "a member never interrupts its own run")
	require.NoDirExists(t, view.Worktree)
	require.False(t, branchExists(repo, view.Branch))
	require.NoFileExists(t, filepath.Join(repo, "docs", "wip.md"))
}

// TestDeleteWakesTheStoppedMemberAfterRemoval pins rulings W15 and W16: the
// settle a delete waits for never resumes the member, and the delete wakes it
// once the worktree is gone, so its queued mail starts a turn that no removal
// cancels.
func TestDeleteWakesTheStoppedMemberAfterRemoval(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, false)
	runner.setTurn(func(ctx context.Context) { <-ctx.Done() })
	spawnMember(t, s, "impl")
	scoped := scopedTask(t, s, "impl", "Docs", false)
	other := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d"})
	act(t, s, "impl", other.ID, "claim", nil)
	require.NoError(t, s.SendMessage(t.Context(), leadName, pipelineSession,
		SendRequest{Target: "impl", Message: "Take another look."}))

	act(t, s, leadName, scoped.ID, "delete", nil)

	require.Equal(t, TaskDeleted, taskByID(t, s, scoped.ID).Status)
	// The wake comes after the removal, so the turn it starts runs with the
	// worktree already gone.
	require.NoDirExists(t, scoped.Worktree)
	require.Eventually(t, func() bool { return runner.promptCount() == 2 }, 5*time.Second, 5*time.Millisecond,
		"the stopped member's queued mail starts its next turn")
	require.Contains(t, runner.lastPrompt(), "Take another look.")
	require.False(t, runner.turnCancelled("impl"), "the woken turn is never cancelled by the removal")
	got := taskByID(t, s, other.ID)
	require.Equal(t, TaskPending, got.Status, "work the stopped turn left goes back while nothing runs for it")
	require.Empty(t, got.OwnerID)
	require.Zero(t, got.Attempts)
}

// TestDeleteDoesNotStrandTheMembersOtherTasks pins the other half of W14: the
// delete holds the member's tasks only for the settle that commits the work,
// and the member's other in-progress tasks go back to the board.
func TestDeleteDoesNotStrandTheMembersOtherTasks(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, false)
	runner.setTurn(func(ctx context.Context) { <-ctx.Done() })
	spawnMember(t, s, "impl")
	scoped := scopedTask(t, s, "impl", "Docs", false)
	other := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d"})
	act(t, s, "impl", other.ID, "claim", nil)

	act(t, s, leadName, scoped.ID, "delete", nil)

	require.Equal(t, TaskDeleted, taskByID(t, s, scoped.ID).Status)
	got := taskByID(t, s, other.ID)
	require.Equal(t, TaskPending, got.Status, "the stopped member's other task never stays in progress")
	require.Empty(t, got.OwnerID)
	require.Zero(t, got.Attempts, "the interruption is not the work's failure")
}

// TestDeleteInterruptsTheRunningOwner pins controller ruling W3: a delete
// that ends a worktree its owner still runs in stops that run first, so no
// tool call of the turn reaches the Lead's tree once the worktree is gone.
func TestDeleteInterruptsTheRunningOwner(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, false)
	// A turn that stops when its context is cancelled, like a real one
	// between tool calls.
	runner.setTurn(func(ctx context.Context) { <-ctx.Done() })
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/wip.md", "wip\n")

	act(t, s, leadName, view.ID, "delete", nil)

	require.Equal(t, TaskDeleted, taskByID(t, s, view.ID).Status)
	require.True(t, runSettled(runner, "impl"), "the run in the worktree was interrupted")
	require.Equal(t, StatusInactive, memberStatus(s, "impl"))
	require.NoDirExists(t, view.Worktree)
	require.False(t, branchExists(repo, view.Branch))
	require.NoFileExists(t, filepath.Join(repo, "docs", "wip.md"), "no tool call of the run landed in the Lead tree")
	require.Empty(t, gitIn(t, repo, "status", "--porcelain"))
}

// TestDeleteCommitsTheHeldWorkFirst pins the other half of W3: the
// interrupted run's settle commits what the turn left in the worktree before
// anything removes it. A locked worktree refuses the removal, so the branch
// survives to show the commit.
func TestDeleteCommitsTheHeldWorkFirst(t *testing.T) {
	t.Parallel()
	repo := initTeamRepo(t)
	s, runner := newWorktreeService(t, repo, false)
	runner.setTurn(func(ctx context.Context) { <-ctx.Done() })
	spawnMember(t, s, "impl")
	view := scopedTask(t, s, "impl", "Docs", false)
	writeTreeFile(t, view.Worktree, "docs/wip.md", "wip\n")
	gitIn(t, repo, "worktree", "lock", view.Worktree)

	act(t, s, leadName, view.ID, "delete", nil)

	require.Equal(t, TaskDeleted, taskByID(t, s, view.ID).Status)
	require.True(t, runSettled(runner, "impl"))
	require.Equal(t, "Docs", gitIn(t, repo, "log", "-1", "--format=%s", view.Branch), "the settle committed the held work")
	require.Empty(t, gitIn(t, view.Worktree, "status", "--porcelain"))
	require.Contains(t, lastLeadNotice(t, s), "Could not remove the git worktree")
}
