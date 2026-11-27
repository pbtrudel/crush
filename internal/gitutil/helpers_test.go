package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gitAvailable reports whether git is on PATH.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// runGit runs git in dir and fails the test when it fails.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitArgs := append([]string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)
	cmd := exec.CommandContext(context.Background(), "git", gitArgs...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(output))
}

// skipWithoutGit skips a test that needs to create a repository when the
// platform or the PATH cannot run git.
func skipWithoutGit(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("git plumbing tests do not run on Windows")
	}
	if !gitAvailable() {
		t.Skip("git is not available")
	}
}

// initPlumbingRepo is a repository whose commits never ask for a signature,
// whatever the user's global git config says.
func initPlumbingRepo(t *testing.T) string {
	t.Helper()
	skipWithoutGit(t)
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644))
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
}

// gitText runs git in dir and returns its trimmed output.
func gitText(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// mergeInProgressForTest reports whether a merge stopped half-way at dir.
func mergeInProgressForTest(dir string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "-C", dir, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return cmd.Run() == nil
}
