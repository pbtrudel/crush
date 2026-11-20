package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// git runs one git command in dir, as the test repository's author.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-C", dir,
		"-c", "user.name=Test",
		"-c", "user.email=test@example.com",
	}, args...)
	out, err := exec.CommandContext(t.Context(), "git", full...).CombinedOutput()
	require.NoError(t, err, string(out))
	return string(out)
}

// newRepo makes dir a repository with one commit holding one file.
func newRepo(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("one\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "renamed.txt"), []byte("two\n"), 0o644))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
}

func TestSnapshotCleanTree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newRepo(t, dir)

	state, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, state.Head)
	require.Empty(t, state.Entries)

	paths, headFrom, headTo := state.Changed(state)
	require.Empty(t, paths)
	require.Equal(t, state.Head, headFrom)
	require.Equal(t, state.Head, headTo)
}

func TestSnapshotReportsEachKindOfChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newRepo(t, dir)
	before, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)

	// Modified in the working tree only.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("one, edited\n"), 0o644))
	// Added to the index.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "added.txt"), []byte("new\n"), 0o644))
	git(t, dir, "add", "added.txt")
	// Untracked, inside an untracked directory.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dir", "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dir", "sub", "deep.txt"), []byte("deep\n"), 0o644))
	// Deleted from the working tree and the index.
	// Renamed, staged, so porcelain v2 reports it with its original path.
	git(t, dir, "mv", "renamed.txt", "moved.txt")

	after, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)

	paths, headFrom, headTo := before.Changed(after)
	require.Equal(t, []string{"added.txt", "dir/sub/deep.txt", "moved.txt", "tracked.txt"}, paths)
	require.Equal(t, before.Head, headFrom)
	require.Equal(t, before.Head, headTo)
	require.Contains(t, after.Entries["moved.txt"], "renamed.txt", "the entry keeps the original path")
}

func TestSnapshotReportsADeletedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newRepo(t, dir)
	before, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)

	require.NoError(t, os.Remove(filepath.Join(dir, "tracked.txt")))

	after, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)
	paths, _, _ := before.Changed(after)
	require.Equal(t, []string{"tracked.txt"}, paths)
}

func TestSnapshotReportsACommit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newRepo(t, dir)
	before, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("two\n"), 0o644))
	git(t, dir, "commit", "-q", "-a", "-m", "change")

	after, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)
	paths, headFrom, headTo := before.Changed(after)
	require.Empty(t, paths, "a committed change leaves a clean tree")
	require.NotEqual(t, headFrom, headTo)
	require.Equal(t, before.Head, headFrom)
	require.Equal(t, after.Head, headTo)
}

func TestSnapshotOutsideARepository(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, err := Snapshot(t.Context(), dir)
	require.Error(t, err)
}

// A path with a space is one entry, not two: the porcelain output separates
// entries with NUL, not the space the path may hold.
func TestSnapshotKeepsAPathWithASpace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newRepo(t, dir)
	before, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a file.txt"), []byte("x\n"), 0o644))

	after, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)
	paths, _, _ := before.Changed(after)
	require.Equal(t, []string{"a file.txt"}, paths)
}

// A submodule whose work tree was touched is not a change of this tree, so
// a formatter that rewrites a vendored checkout does not make every turn
// look like it changed files.
func TestSnapshotIgnoresDirtySubmodules(t *testing.T) {
	t.Parallel()
	sub := filepath.Join(t.TempDir(), "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	git(t, sub, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(sub, "inner.txt"), []byte("one\n"), 0o644))
	git(t, sub, "add", ".")
	git(t, sub, "commit", "-q", "-m", "init")

	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "sub")
	git(t, dir, "commit", "-q", "-m", "add submodule")

	before, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)
	// The submodule's work tree now differs from its commit.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "inner.txt"), []byte("two\n"), 0o644))

	after, err := Snapshot(t.Context(), dir)
	require.NoError(t, err)
	paths, _, _ := before.Changed(after)
	require.Empty(t, paths, "a dirty submodule is not a change")
}
