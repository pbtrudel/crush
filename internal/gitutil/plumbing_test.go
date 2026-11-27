package gitutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRepoPrefix(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "sub", "inner"), 0o755))

	prefix, err := RepoPrefix(context.Background(), repo)
	require.NoError(t, err)
	require.Empty(t, prefix)
	prefix, err = RepoPrefix(context.Background(), filepath.Join(repo, "sub", "inner"))
	require.NoError(t, err)
	require.Equal(t, "sub/inner/", filepath.ToSlash(prefix))
	_, err = RepoPrefix(context.Background(), t.TempDir())
	require.Error(t, err)
}

func TestCommitAll(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	ctx := context.Background()

	committed, err := CommitAll(ctx, repo, "Nothing", nil)
	require.NoError(t, err)
	require.False(t, committed, "an empty change set makes no commit")
	require.Equal(t, "initial", gitText(t, repo, "log", "-1", "--format=%s"))

	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o644))
	committed, err = CommitAll(ctx, repo, "Add things", nil)
	require.NoError(t, err)
	require.True(t, committed)
	require.Equal(t, "Add things", gitText(t, repo, "log", "-1", "--format=%s"))
	require.Equal(t, "Test", gitText(t, repo, "log", "-1", "--format=%an"), "the author comes from the repository config")
	require.Empty(t, gitText(t, repo, "status", "--porcelain"))
}

// TestCommitAllStagesOnlyThePathspecs pins that a commit with pathspecs
// leaves every change outside them in the work tree, that a pathspec is a
// literal path and not a glob, and that a pathspec matching nothing is not
// an error.
func TestCommitAllStagesOnlyThePathspecs(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	ctx := context.Background()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "a.md"), []byte("docs\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "star*.md"), []byte("literal\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "starX.md"), []byte("glob\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "stray.txt"), []byte("stray\n"), 0o644))

	committed, err := CommitAll(ctx, repo, "Docs only", []string{"docs/star*.md", "docs/a.md", "docs/missing.md"})
	require.NoError(t, err)
	require.True(t, committed)
	require.ElementsMatch(t, []string{"docs/star*.md", "docs/a.md"},
		strings.Fields(gitText(t, repo, "show", "--name-only", "--format=", "HEAD")),
		"a pathspec is literal, and one that matches nothing commits nothing")
	status := gitText(t, repo, "status", "--porcelain")
	require.Contains(t, status, "M README.md", "a change outside the pathspecs stays uncommitted")
	require.Contains(t, status, "?? stray.txt")
	require.Contains(t, status, "?? docs/starX.md")

	committed, err = CommitAll(ctx, repo, "Nothing named", []string{"docs/missing.md"})
	require.NoError(t, err)
	require.False(t, committed, "pathspecs that match nothing make no commit")

	// A tracked path the work tree no longer holds is still in the index,
	// so deleting it inside a scope is a change to commit.
	require.NoError(t, os.Remove(filepath.Join(repo, "docs", "a.md")))
	committed, err = CommitAll(ctx, repo, "Delete docs/a.md", []string{"docs/a.md"})
	require.NoError(t, err)
	require.True(t, committed)
	require.Equal(t, "docs/a.md", gitText(t, repo, "show", "--name-only", "--format=", "HEAD"))
	require.NotContains(t, gitText(t, repo, "status", "--porcelain"), "docs/a.md")
}

// branchWithCommit makes a worktree on a new branch, writes name there, and
// commits it.
func branchWithCommit(t *testing.T, repo, name, content string) string {
	t.Helper()
	path, branch, err := CreateWorktree(context.Background(), repo, "feature")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(path, name), []byte(content), 0o644))
	committed, err := CommitAll(context.Background(), path, "Feature work", nil)
	require.NoError(t, err)
	require.True(t, committed)
	return branch
}

func TestMergeNoFFMakesAMergeCommit(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "feature.txt", "feature\n")

	require.NoError(t, MergeNoFF(context.Background(), repo, branch, "Merge it"))
	require.FileExists(t, filepath.Join(repo, "feature.txt"))
	require.Equal(t, "Merge it", gitText(t, repo, "log", "-1", "--format=%s"))
	require.Len(t, strings.Fields(gitText(t, repo, "rev-list", "--parents", "-n", "1", "HEAD")), 3,
		"a fast-forward was possible, and --no-ff still made a merge commit")
}

func TestMergeNoFFAbortsAConflict(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "README.md", "branch\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("main\n"), 0o644))
	runGit(t, repo, "commit", "-am", "main change")

	err := MergeNoFF(context.Background(), repo, branch, "Merge it")
	var mergeErr *MergeError
	require.True(t, errors.As(err, &mergeErr), "%v", err)
	require.Contains(t, mergeErr.Reason, "conflict")
	require.Contains(t, mergeErr.Output, "README.md")
	require.False(t, mergeInProgressForTest(repo), "the merge was aborted")
	require.Empty(t, gitText(t, repo, "status", "--porcelain"))
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	require.NoError(t, err)
	require.Equal(t, "main\n", string(data))
}

func TestMergeNoFFRefusesADirtyOverlap(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "README.md", "branch\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("local\n"), 0o644))

	err := MergeNoFF(context.Background(), repo, branch, "Merge it")
	var mergeErr *MergeError
	require.True(t, errors.As(err, &mergeErr), "%v", err)
	require.False(t, mergeInProgressForTest(repo))
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	require.NoError(t, err)
	require.Equal(t, "local\n", string(data), "the Lead's uncommitted change is kept")
}

func TestMergeNoFFRefusesADetachedHead(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "feature.txt", "feature\n")
	runGit(t, repo, "checkout", "--detach")

	err := MergeNoFF(context.Background(), repo, branch, "Merge it")
	var mergeErr *MergeError
	require.True(t, errors.As(err, &mergeErr), "%v", err)
	require.Contains(t, mergeErr.Reason, "detached")
	require.NoFileExists(t, filepath.Join(repo, "feature.txt"))
}

// TestMergeNoFFLeavesAMergeInProgressAlone is controller ruling W1: a merge
// the Lead already has in progress is never merged over and never aborted.
func TestMergeNoFFLeavesAMergeInProgressAlone(t *testing.T) {
	t.Parallel()
	repo := initPlumbingRepo(t)
	conflicting := branchWithCommit(t, repo, "README.md", "branch\n")
	other := branchWithCommit(t, repo, "other.txt", "other\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("main\n"), 0o644))
	runGit(t, repo, "commit", "-am", "main change")

	// The Lead starts a merge that stops on a conflict and leaves it there.
	leadMerge := exec.CommandContext(context.Background(), "git", "-C", repo,
		"merge", "--no-ff", "-m", "Lead merge", "--end-of-options", conflicting)
	require.Error(t, leadMerge.Run(), "the Lead's merge conflicts")
	require.True(t, mergeInProgressForTest(repo), "the Lead's merge is in progress")

	mergeHeadBefore := gitText(t, repo, "rev-parse", "MERGE_HEAD")
	conflictedBefore, err := os.ReadFile(filepath.Join(repo, "README.md"))
	require.NoError(t, err)
	require.Contains(t, string(conflictedBefore), "<<<<<<<")

	err = MergeNoFF(context.Background(), repo, other, "Merge it")
	var mergeErr *MergeError
	require.True(t, errors.As(err, &mergeErr), "%v", err)
	require.Contains(t, mergeErr.Reason, "merge is already in progress")

	require.Equal(t, mergeHeadBefore, gitText(t, repo, "rev-parse", "MERGE_HEAD"),
		"MERGE_HEAD still points at the Lead's merge")
	conflictedAfter, err := os.ReadFile(filepath.Join(repo, "README.md"))
	require.NoError(t, err)
	require.Equal(t, string(conflictedBefore), string(conflictedAfter),
		"the conflicted file is unchanged")
	require.NoFileExists(t, filepath.Join(repo, "other.txt"))
}

// blockingMergeGit puts a git wrapper on PATH that runs the real merge, then
// holds the process until the test lets it go, so the test can cancel the
// caller's context while the merge has already left its conflict behind.
type blockingMergeGit struct {
	startedPath string
	releasePath string
}

func newBlockingMergeGit(t *testing.T) blockingMergeGit {
	t.Helper()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	dir := t.TempDir()
	b := blockingMergeGit{
		startedPath: filepath.Join(dir, "started"),
		releasePath: filepath.Join(dir, "release"),
	}
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
	if [ "$arg" = "--abort" ]; then
		exec %q "$@"
	fi
done
for arg in "$@"; do
	if [ "$arg" = "merge" ]; then
		%q "$@"
		status=$?
		: > %q
		i=0
		while [ ! -f %q ] && [ $i -lt 500 ]; do sleep 0.01; i=$((i+1)); done
		exit $status
	fi
done
exec %q "$@"
`, realGit, realGit, b.startedPath, b.releasePath, realGit)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(b.release)
	return b
}

func (b blockingMergeGit) wait(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Stat(b.startedPath)
		return err == nil
	}, 10*time.Second, 5*time.Millisecond, "git must reach the merge")
}

func (b blockingMergeGit) release() {
	_ = os.WriteFile(b.releasePath, nil, 0o644)
}

// TestMergeNoFFCleansUpAfterACancelledCaller pins the carried review item: a
// caller that gives up during the merge never leaves the Lead's tree
// half-merged. The test changes PATH, so it cannot run in parallel.
func TestMergeNoFFCleansUpAfterACancelledCaller(t *testing.T) {
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "README.md", "branch\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("main\n"), 0o644))
	runGit(t, repo, "commit", "-am", "main change")
	held := newBlockingMergeGit(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- MergeNoFF(ctx, repo, branch, "Merge it") }()
	held.wait(t)
	cancel()
	held.release()
	err := <-done
	var mergeErr *MergeError
	require.True(t, errors.As(err, &mergeErr), "%v", err)
	require.False(t, mergeInProgressForTest(repo), "the cancelled caller's merge was aborted")
	require.Empty(t, gitText(t, repo, "status", "--porcelain"))
	data, err := os.ReadFile(filepath.Join(repo, "README.md"))
	require.NoError(t, err)
	require.Equal(t, "main\n", string(data))
}

// failingAbortGit puts a git wrapper on PATH that refuses to abort a merge.
func failingAbortGit(t *testing.T) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
	if [ "$arg" = "--abort" ]; then
		echo "abort refused" >&2
		exit 1
	fi
done
exec %q "$@"
`, realGit)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestMergeNoFFReportsAFailedAbort pins that a merge git could not abort is
// reported, so no notice claims the tree is clean. The test changes PATH, so
// it cannot run in parallel.
func TestMergeNoFFReportsAFailedAbort(t *testing.T) {
	repo := initPlumbingRepo(t)
	branch := branchWithCommit(t, repo, "README.md", "branch\n")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("main\n"), 0o644))
	runGit(t, repo, "commit", "-am", "main change")
	failingAbortGit(t)

	err := MergeNoFF(context.Background(), repo, branch, "Merge it")
	var mergeErr *MergeError
	require.True(t, errors.As(err, &mergeErr), "%v", err)
	require.True(t, mergeErr.AbortFailed)
	require.Contains(t, mergeErr.Output, "abort refused")
	require.Contains(t, mergeErr.Reason, "conflict")
	require.Contains(t, mergeErr.Reason, "could not be aborted")
	require.NotContains(t, mergeErr.Reason, "was aborted", "a reason never says the merge was aborted when it was not")
	require.True(t, mergeInProgressForTest(repo), "the merge is still in progress")
}
