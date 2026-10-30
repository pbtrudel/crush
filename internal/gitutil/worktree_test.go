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
