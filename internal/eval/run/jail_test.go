package run

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/stretchr/testify/require"
)

// TestHostPaths locates what a jailed run hides: the git toplevel the tasks
// come from, the worktrees beside a linked worktree, and the eval home's
// batches, with the run's own directory under them.
func TestHostPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	main := filepath.Join(root, "main")
	require.NoError(t, os.MkdirAll(main, 0o700))
	git(t, main, "init", "-q", "-b", "main")
	write(t, filepath.Join(main, "go.mod"), "module example.com/main\n\ngo 1.21\n")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-q", "-m", "base")

	git(t, main, "remote", "add", "origin", "https://github.com/charmbracelet/crush.git")
	git(t, main, "remote", "add", "upstream", "git@github.com:pbtrudel/crush.git")

	worktree := filepath.Join(root, "worktrees", "task")
	git(t, main, "worktree", "add", "-q", "--detach", worktree)

	h := home.At(filepath.Join(root, "home"))
	runDir := filepath.Join(h.BatchDir("b1"), "runs", "pr-0010-main-fake-r1")

	paths, err := HostPaths(context.Background(), worktree, filepath.Join(worktree, "evals"), h, runDir)
	require.NoError(t, err)
	require.Equal(t, worktree, paths.Repo)
	require.Equal(t, worktree, paths.Checkout)
	require.Equal(t, filepath.Join(worktree, "evals"), paths.Suite)
	require.Equal(t, filepath.Dir(worktree), paths.Worktrees,
		"the parent of a linked worktree holds its siblings")
	require.Equal(t, filepath.Join(h.Dir(), "batches"), paths.Batches)
	require.Equal(t, runDir, paths.Own)
	require.Equal(t, []string{"charmbracelet/crush", "pbtrudel/crush"}, paths.Remotes,
		"the checkout's origin and upstream remotes")

	// The repository itself is not a linked worktree, so no directory of
	// sibling worktrees sits beside it.
	plain, err := HostPaths(context.Background(), main, "", h, runDir)
	require.NoError(t, err)
	require.Equal(t, main, plain.Checkout)
	require.Empty(t, plain.Worktrees)
}

// TestJailMasks checks the mask list of a jailed run: the jail's socket
// masks stay hidden, the host checkout and the worktrees beside it are
// hidden, and the eval home's batches are hidden except the run's own
// directory, which the child keeps writing.
func TestJailMasks(t *testing.T) {
	t.Parallel()

	paths := Paths{
		Checkout:  "/work/crush",
		Worktrees: "/work/worktrees",
		Batches:   "/state/crush-eval/batches",
		Own:       "/state/crush-eval/batches/b1/runs/pr-0010-r1",
	}
	masks := jailMasks(paths)

	byPath := make(map[string]netjail.Mask, len(masks))
	for _, mask := range masks {
		byPath[mask.Path] = mask
	}
	for _, path := range netjail.DefaultMasks {
		require.Contains(t, byPath, path, "the jail still hides the sockets under %s", path)
	}
	require.Contains(t, byPath, paths.Checkout)
	require.Contains(t, byPath, paths.Worktrees)
	require.Equal(t, []string{paths.Own}, byPath[paths.Batches].Keep)

	// A path the runner could not derive is not a mask: masking an empty
	// path would hide everything.
	for _, mask := range jailMasks(Paths{}) {
		require.NotEmpty(t, mask.Path)
		require.Empty(t, mask.Keep)
	}
}
