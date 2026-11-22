package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// `crush run` runs in a new Git worktree when --worktree is given: the flag
// is the root command's, and the directory the run uses is the worktree it
// created.
func TestRunTakesTheWorktreeFlag(t *testing.T) {
	flag := rootCmd.PersistentFlags().Lookup("worktree")
	require.NotNil(t, flag, "crush has a --worktree flag")
	require.Equal(t, "string", flag.Value.Type())
	require.Empty(t, flag.DefValue, "no worktree by default")

	repo := initRunTestRepo(t)
	cmd := &cobra.Command{}
	cmd.Flags().String("worktree", "", "Run in a new Git worktree with the given name")
	cmd.SetContext(t.Context())
	require.NoError(t, cmd.Flags().Set("worktree", "Run Flags"))

	dir, worktree, err := applyWorktreeFlag(cmd, repo)
	require.NoError(t, err)
	require.Equal(t, worktree, dir)
	require.NotEmpty(t, worktree)
	require.DirExists(t, worktree)
	require.FileExists(t, filepath.Join(worktree, "README.md"))

	// No flag leaves the directory the command was given alone.
	plain := &cobra.Command{}
	plain.Flags().String("worktree", "", "Run in a new Git worktree with the given name")
	plain.SetContext(t.Context())
	dir, worktree, err = applyWorktreeFlag(plain, repo)
	require.NoError(t, err)
	require.Equal(t, repo, dir)
	require.Empty(t, worktree)
}

// initRunTestRepo is a Git repository with one commit, so a worktree can be
// created from it.
func initRunTestRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644))
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "initial"}} {
		out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return dir
}
