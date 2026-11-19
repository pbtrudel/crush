package checkout_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/stretchr/testify/require"
)

const crushJSON = `{"options":{}}` + "\n"

// repo is a throwaway repository with a base commit and one commit after it.
type repo struct {
	dir   string
	base  string
	later string
}

// newRepo builds a repository whose base holds a tracked file that .gitignore
// hides, an executable and a symlink.
func newRepo(t *testing.T) repo {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "crush.json"), crushJSON)
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	write(t, filepath.Join(dir, "sub", "b.txt"), "b\n")
	write(t, filepath.Join(dir, ".gitignore"), "hidden.txt\n")
	write(t, filepath.Join(dir, "hidden.txt"), "ignored but tracked\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.Symlink("a.txt", filepath.Join(dir, "link.txt")))

	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", "-A", "-f")
	git(t, dir, "commit", "-q", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, filepath.Join(dir, "later.txt"), "later\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "later")
	return repo{dir: dir, base: base, later: git(t, dir, "rev-parse", "HEAD")}
}

func TestPrepare(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	dest := t.TempDir()

	blob, err := checkout.Prepare(context.Background(), r.dir, r.base, dest)
	require.NoError(t, err)
	src := filepath.Join(dest, "src")

	require.Equal(t, git(t, src, "hash-object", "crush.json"), blob)
	require.Equal(t, "1", git(t, src, "rev-list", "--count", "HEAD"))
	require.Equal(t, "task base", git(t, src, "log", "-1", "--format=%s"))
	require.Equal(t, "crush-eval <crush-eval@example.com> 2000-01-01T00:00:00Z",
		git(t, src, "log", "-1", "--format=%an <%ae> %aI"))
	require.Empty(t, git(t, src, "status", "--porcelain"))

	require.Equal(t, "100755", strings.Fields(git(t, src, "ls-files", "-s", "run.sh"))[0])
	require.Equal(t, "120000", strings.Fields(git(t, src, "ls-files", "-s", "link.txt"))[0])
	link, err := os.Readlink(filepath.Join(src, "link.txt"))
	require.NoError(t, err)
	require.Equal(t, "a.txt", link)

	require.FileExists(t, filepath.Join(src, "hidden.txt"))
	require.FileExists(t, filepath.Join(src, "sub", "b.txt"))

	require.NoFileExists(t, filepath.Join(src, "later.txt"))
	cmd := exec.CommandContext(context.Background(), "git", "-C", src, "cat-file", "-e", r.later)
	require.Error(t, cmd.Run())
}

func TestPrepareWithoutCrushJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")

	blob, err := checkout.Prepare(context.Background(), dir, "HEAD", t.TempDir())
	require.NoError(t, err)
	require.Empty(t, blob)
}

func TestPrepareUnknownBase(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	_, err := checkout.Prepare(context.Background(), r.dir, "0123456789abcdef0123456789abcdef01234567", t.TempDir())
	require.Error(t, err)
}

// write writes one file, creating its directory.
func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// git runs one git command with the user's config kept out of it.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	args = append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}
