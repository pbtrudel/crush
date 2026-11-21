package run

import (
	"bytes"
	"context"
	"log/slog"
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

// TestRunOnTheHostNeedsNoCheckoutPath checks that a run on the host does not
// ask git where the checkout is: a bare repository has no work tree, and only
// a jailed run needs the path to hide.
func TestRunOnTheHostNeedsNoCheckoutPath(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	git(t, filepath.Dir(bare), "clone", "-q", "--bare", f.repo, bare)
	// A clone copies the reachable objects only, so the hidden test's blob
	// is written into the bare repository again.
	require.Equal(t, f.blob, blobOf(t, bare, hiddenTest))
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")
	f.env["FAKE_WRITE"] = "pkg/pkg.go"
	f.env["FAKE_CONTENT"] = fixedSource

	opts := f.options()
	opts.RepoDir = bare
	line, err := Run(context.Background(), opts)
	require.NoError(t, err, "a host run does not need the checkout path")
	require.True(t, line.Pass)
}

// TestUnsafeParent checks which directories a worktrees parent may not be:
// the root, the home directory, or a directory that holds something the run
// reads.
func TestUnsafeParent(t *testing.T) {
	t.Parallel()

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	tests := []struct {
		name    string
		parent  string
		protect []string
		want    bool
	}{
		{name: "the root", parent: "/", want: true},
		{name: "the home directory", parent: homeDir, want: true},
		{
			name:    "a parent holding the eval home",
			parent:  "/work/.crush-worktrees",
			protect: []string{"/work/.crush-worktrees/state/crush-eval"},
			want:    true,
		},
		{
			name:    "a parent holding the toolchain",
			parent:  "/work/.crush-worktrees",
			protect: []string{"/work/.crush-worktrees/go"},
			want:    true,
		},
		{
			name:    "a parent beside the protected paths",
			parent:  "/work/.crush-worktrees",
			protect: []string{"/work/.crush-worktrees-2/state", "/work/cache"},
			want:    false,
		},
		{name: "a plain parent", parent: "/work/.crush-worktrees", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, unsafeParent(test.parent, test.protect))
		})
	}
}

// TestHostPathsRefusesUnsafeWorktreesParent checks that a worktrees parent
// that holds a directory the run reads is not returned, so the jail masks the
// checkout alone, and that the refusal is logged at Warn.
func TestHostPathsRefusesUnsafeWorktreesParent(t *testing.T) {
	// Not parallel: it swaps the default logger.
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(previous)

	root := t.TempDir()
	main := filepath.Join(root, "main")
	require.NoError(t, os.MkdirAll(main, 0o700))
	git(t, main, "init", "-q", "-b", "main")
	write(t, filepath.Join(main, "go.mod"), "module example.com/main\n\ngo 1.21\n")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-q", "-m", "base")
	worktree := filepath.Join(root, "worktrees", "task")
	git(t, main, "worktree", "add", "-q", "--detach", worktree)

	paths, err := HostPaths(context.Background(), worktree, "", nil, "",
		filepath.Join(root, "worktrees", "toolchain"))
	require.NoError(t, err)
	require.Equal(t, worktree, paths.Checkout, "the checkout is still masked alone")
	require.Empty(t, paths.Worktrees, "the parent holds a directory the run reads")
	require.Contains(t, logged.String(), "level=WARN")
	require.Contains(t, logged.String(), "worktrees parent")
}
