package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
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

// A run in a new worktree keeps the original checkout's data directory, so
// its sessions land in the same database as every other run of the
// project. An explicit data directory still wins.
func TestWorktreeSharesTheCheckoutDataDir(t *testing.T) {
	newCmd := func(t *testing.T, dataDir string) *cobra.Command {
		t.Helper()
		cmd := &cobra.Command{}
		cmd.Flags().String("worktree", "", "")
		cmd.Flags().String("data-dir", "", "")
		cmd.Flags().Bool("debug", false, "")
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.Flags().Set("worktree", "shared-data"))
		if dataDir != "" {
			require.NoError(t, cmd.Flags().Set("data-dir", dataDir))
		}
		return cmd
	}
	isolate := func(t *testing.T) {
		t.Helper()
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Setenv("XDG_DATA_HOME", t.TempDir())
		t.Setenv("CRUSH_GLOBAL_CONFIG", t.TempDir())
		t.Setenv("CRUSH_GLOBAL_DATA", t.TempDir())
		t.Setenv("CRUSH_DATA_DIR", "")
	}

	t.Run("nothing set uses the checkout's data dir", func(t *testing.T) {
		isolate(t)
		repo := initRunTestRepo(t)
		store, dir, worktree, err := initLocalConfig(newCmd(t, ""), repo, config.WithoutProviderUpdate())
		require.NoError(t, err)
		require.Equal(t, worktree, dir)
		require.Equal(t, filepath.Join(repo, ".crush"), store.Config().Options.DataDirectory)
	})

	t.Run("--data-dir wins", func(t *testing.T) {
		isolate(t)
		repo := initRunTestRepo(t)
		explicit := t.TempDir()
		store, _, _, err := initLocalConfig(newCmd(t, explicit), repo, config.WithoutProviderUpdate())
		require.NoError(t, err)
		require.Equal(t, explicit, store.Config().Options.DataDirectory)
	})

	t.Run("CRUSH_DATA_DIR wins", func(t *testing.T) {
		isolate(t)
		repo := initRunTestRepo(t)
		explicit := t.TempDir()
		t.Setenv("CRUSH_DATA_DIR", explicit)
		store, _, _, err := initLocalConfig(newCmd(t, ""), repo, config.WithoutProviderUpdate())
		require.NoError(t, err)
		require.Equal(t, explicit, store.Config().Options.DataDirectory)
	})

	t.Run("a configured data_directory wins", func(t *testing.T) {
		isolate(t)
		repo := initRunTestRepo(t)
		explicit := t.TempDir()
		cfg := []byte(`{"options":{"data_directory":` + strconv.Quote(explicit) + `}}`)
		require.NoError(t, os.WriteFile(filepath.Join(os.Getenv("CRUSH_GLOBAL_CONFIG"), "crush.json"), cfg, 0o644))
		store, _, _, err := initLocalConfig(newCmd(t, ""), repo, config.WithoutProviderUpdate())
		require.NoError(t, err)
		require.Equal(t, explicit, store.Config().Options.DataDirectory)
	})
}
