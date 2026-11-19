package gitx_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/gitx"
	"github.com/stretchr/testify/require"
)

// TestEnvCarriesNothingFromTheParent checks that the environment of a git
// command is the runner's own: a secret of the parent process never reaches
// a hook or a fsmonitor script.
func TestEnvCarriesNothingFromTheParent(t *testing.T) {
	t.Setenv("CRUSH_EVAL_SECRET", "sk-eval-must-not-leak")
	t.Setenv("GH_TOKEN", "gh-parent-token")

	env := gitx.Env()
	require.NotEmpty(t, env)
	joined := strings.Join(env, "\n")
	require.NotContains(t, joined, "sk-eval-must-not-leak")
	require.NotContains(t, joined, "gh-parent-token")
	require.Contains(t, joined, "GIT_CONFIG_NOSYSTEM=1")
	require.Contains(t, joined, "GIT_CONFIG_GLOBAL="+os.DevNull)
	for _, kv := range env {
		key := strings.SplitN(kv, "=", 2)[0]
		require.Contains(t, []string{"PATH", "HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"}, key)
	}
}

// TestCommandsIgnoreRepositoryHooks runs git in a repository whose own
// config points core.fsmonitor at a script that writes a marker. The script
// must not run: a model can write that config, and it would see the
// runner's environment.
func TestCommandsIgnoreRepositoryHooks(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	script := filepath.Join(dir, "fsmonitor.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nenv >> "+marker+"\n"), 0o700))

	repo := filepath.Join(dir, "repo")
	plain(t, dir, "git", "init", "-q", "-b", "main", repo)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o600))
	plain(t, repo, "git", "add", "-A")
	plain(t, repo, "git", "commit", "-q", "-m", "base")
	plain(t, repo, "git", "config", "core.fsmonitor", script)

	// The control: git on its own runs the script.
	plain(t, repo, "git", "status", "--porcelain")
	require.FileExists(t, marker, "the control did not run the fsmonitor")
	require.NoError(t, os.Remove(marker))

	for _, args := range [][]string{
		{"status", "--porcelain"},
		{"add", "-N", "."},
		{"diff", "--numstat", "HEAD"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
		{"for-each-ref", "--format=%(refname)", "refs/heads"},
	} {
		_, err := gitx.Output(context.Background(), repo, args...)
		require.NoError(t, err, strings.Join(args, " "))
	}
	require.NoFileExists(t, marker, "a git command ran the repository's fsmonitor")

	out, err := gitx.Stdin(context.Background(), repo, []byte("a\n"), "hash-object", "--stdin")
	require.NoError(t, err)
	require.Len(t, out, 40)
	require.NoFileExists(t, marker)
}

// plain runs one command with the parent environment, as a user would.
func plain(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}
