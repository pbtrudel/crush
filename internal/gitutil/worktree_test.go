package gitutil

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func initTestRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644))
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

func TestCreateWorktree(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	path, branch, err := CreateWorktree(context.Background(), repo, "My Feature")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })
	require.Equal(t, "crush/my-feature", branch)
	require.DirExists(t, path)
	require.FileExists(t, filepath.Join(path, "README.md"))
	require.Equal(t, branch, CurrentBranch(path))
}

func TestCreateWorktreeUniqueNames(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	firstPath, firstBranch, err := CreateWorktree(context.Background(), repo, "session")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(firstPath)) })
	secondPath, secondBranch, err := CreateWorktree(context.Background(), repo, "session")
	require.NoError(t, err)

	require.NotEqual(t, firstPath, secondPath)
	require.NotEqual(t, firstBranch, secondBranch)
	require.Equal(t, "crush/session", firstBranch)
	require.Equal(t, "crush/session-2", secondBranch)
}

func TestCreateWorktreeNotARepository(t *testing.T) {
	t.Parallel()

	_, _, err := CreateWorktree(context.Background(), t.TempDir(), "session")
	require.Error(t, err)
}

func TestWorktreeSlug(t *testing.T) {
	t.Parallel()

	require.Equal(t, "my-feature", WorktreeSlug(" My Feature "))
	require.Equal(t, "fix-issue-42", WorktreeSlug("fix/issue #42"))
	require.Equal(t, "session", WorktreeSlug(""))
}

func TestListWorktrees(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	path, branch, err := CreateWorktree(context.Background(), repo, "feature")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })

	worktrees, err := ListWorktrees(context.Background(), repo)
	require.NoError(t, err)
	require.Len(t, worktrees, 2)
	require.True(t, worktrees[0].Current)
	require.Equal(t, "main", worktrees[0].Branch)
	require.Equal(t, path, worktrees[1].Path)
	require.Equal(t, branch, worktrees[1].Branch)
	require.False(t, worktrees[1].Current)
}

func TestCurrentWorktree(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	path, branch, err := CreateWorktree(context.Background(), repo, "feature")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })

	current, linked, err := CurrentWorktree(context.Background(), repo)
	require.NoError(t, err)
	require.False(t, linked)
	require.Equal(t, "main", current.Branch)

	current, linked, err = CurrentWorktree(context.Background(), path)
	require.NoError(t, err)
	require.True(t, linked)
	require.Equal(t, branch, current.Branch)
}

func TestRestoreWorktree(t *testing.T) {
	t.Parallel()

	t.Run("existing branch", func(t *testing.T) {
		t.Parallel()
		repo := initPlumbingRepo(t)
		path, branch, err := CreateWorktree(context.Background(), repo, "team-task-1")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(path, "work.txt"), []byte("keep\n"), 0o644))
		runGit(t, path, "add", ".")
		runGit(t, path, "commit", "-m", "work")
		require.NoError(t, os.RemoveAll(path))

		restored, err := RestoreWorktree(context.Background(), repo, path, branch, "HEAD")
		require.NoError(t, err)
		require.Equal(t, path, restored)
		require.FileExists(t, filepath.Join(restored, "work.txt"))
		require.Equal(t, branch, CurrentBranch(restored))
	})

	t.Run("missing branch", func(t *testing.T) {
		t.Parallel()
		repo := initPlumbingRepo(t)
		path := filepath.Join(filepath.Dir(repo), ".worktrees", "team-task-2")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

		restored, err := RestoreWorktree(context.Background(), repo, path, "crush/team-task-2", "HEAD")
		require.NoError(t, err)
		require.Equal(t, path, restored)
		require.FileExists(t, filepath.Join(restored, "README.md"))
		require.Equal(t, "crush/team-task-2", CurrentBranch(restored))
	})

	t.Run("occupied path", func(t *testing.T) {
		t.Parallel()
		repo := initPlumbingRepo(t)
		path := filepath.Join(filepath.Dir(repo), ".worktrees", "team-task-3")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("not a directory"), 0o644))

		restored, err := RestoreWorktree(context.Background(), repo, path, "crush/team-task-3", "HEAD")
		require.NoError(t, err)
		require.Equal(t, path+"-2", restored)
		require.FileExists(t, filepath.Join(restored, "README.md"))
	})
}

func TestRemoveWorktree(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	path, branch, err := CreateWorktree(context.Background(), repo, "feature")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })

	require.NoError(t, os.WriteFile(filepath.Join(path, "dirty.txt"), []byte("x"), 0o644))
	err = RemoveWorktree(context.Background(), repo, path, false)
	require.ErrorContains(t, err, "uncommitted changes")

	require.NoError(t, RemoveWorktree(context.Background(), repo, path, true))
	require.NoDirExists(t, path)

	require.NoError(t, DeleteBranch(context.Background(), repo, branch, true))
	worktrees, err := ListWorktrees(context.Background(), repo)
	require.NoError(t, err)
	require.Len(t, worktrees, 1)
}

func TestRemoveWorktreeRefusesCurrent(t *testing.T) {
	t.Parallel()

	repo := initTestRepo(t)
	err := RemoveWorktree(context.Background(), repo, repo, true)
	require.ErrorContains(t, err, "refusing to remove")
}
