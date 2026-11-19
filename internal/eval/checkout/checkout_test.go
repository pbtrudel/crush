package checkout_test

import (
	"archive/tar"
	"bytes"
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

// TestExtract checks the one extractor both the task checkout and the
// variant build use: it writes regular files and symlinks inside dest, and
// refuses an entry that would land outside it.
func TestExtract(t *testing.T) {
	t.Parallel()

	build := func(entries ...*tar.Header) *bytes.Buffer {
		var buf bytes.Buffer
		w := tar.NewWriter(&buf)
		for _, hdr := range entries {
			require.NoError(t, w.WriteHeader(hdr))
			if hdr.Typeflag == tar.TypeReg {
				_, err := w.Write([]byte("content of " + hdr.Name + "\n"))
				require.NoError(t, err)
			}
		}
		require.NoError(t, w.Close())
		return &buf
	}
	regular := func(name string, mode int64) *tar.Header {
		return &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len("content of " + name + "\n"))}
	}

	t.Run("writes files and symlinks", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		archive := build(
			regular("a.txt", 0o644),
			regular("sub/run.sh", 0o755),
			&tar.Header{Name: "link.txt", Typeflag: tar.TypeSymlink, Linkname: "a.txt"},
			&tar.Header{Name: "sub", Typeflag: tar.TypeDir, Mode: 0o755},
		)
		require.NoError(t, checkout.Extract(archive, dest))
		require.FileExists(t, filepath.Join(dest, "a.txt"))
		info, err := os.Stat(filepath.Join(dest, "sub", "run.sh"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
		target, err := os.Readlink(filepath.Join(dest, "link.txt"))
		require.NoError(t, err)
		require.Equal(t, "a.txt", target)
	})

	t.Run("refuses a path outside the checkout", func(t *testing.T) {
		t.Parallel()
		dest := t.TempDir()
		require.ErrorContains(t, checkout.Extract(build(regular("../escape.txt", 0o644)), dest), "escapes the checkout")
		require.NoFileExists(t, filepath.Join(filepath.Dir(dest), "escape.txt"))
	})

	t.Run("refuses a symlink that points outside the checkout", func(t *testing.T) {
		t.Parallel()
		for _, target := range []string{"/etc/passwd", "../../outside", "../sub/../../outside"} {
			archive := build(&tar.Header{Name: "sub/link", Typeflag: tar.TypeSymlink, Linkname: target})
			require.ErrorContains(t, checkout.Extract(archive, t.TempDir()), "points outside the checkout", target)
		}
		archive := build(&tar.Header{Name: "sub/link", Typeflag: tar.TypeSymlink, Linkname: "../a.txt"})
		require.NoError(t, checkout.Extract(archive, t.TempDir()))
	})

	t.Run("refuses a type it cannot write", func(t *testing.T) {
		t.Parallel()
		archive := build(&tar.Header{Name: "dev", Typeflag: tar.TypeChar})
		require.ErrorContains(t, checkout.Extract(archive, t.TempDir()), "unsupported type")
	})
}
