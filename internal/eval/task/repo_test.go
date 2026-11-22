package task_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// TestRepositorySuite validates the suite of this repository. The repository
// check runs only for a task whose merge commit is present, which is not the
// case in a shallow clone.
func TestRepositorySuite(t *testing.T) {
	t.Parallel()
	root := git(t, ".", "rev-parse", "--show-toplevel")
	tasks, err := task.LoadDir(filepath.Join(root, "evals", "tasks"))
	require.NoError(t, err)
	require.NotEmpty(t, tasks)

	for _, tk := range tasks {
		t.Run(tk.ID, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, tk.Validate())
			if !hasCommit(t, root, tk.Source.Merge) {
				t.Skipf("merge commit %s is not in this repository", tk.Source.Merge)
			}
			require.NoError(t, tk.ValidateRepo(root))
		})
	}
}

// TestFooter pins the prompt footer to the text of the design, because every
// task and variant shares it and a change would change the suite version.
func TestFooter(t *testing.T) {
	t.Parallel()
	root := git(t, ".", "rev-parse", "--show-toplevel")
	footer, err := os.ReadFile(filepath.Join(root, "evals", "footer.md"))
	require.NoError(t, err)
	require.Equal(t,
		"Make this change in the repository in the current directory. When you are done, the work should be in the working tree of this checkout.",
		strings.TrimSpace(string(footer)))
}

// hasCommit reports whether the repository at dir holds the commit.
func hasCommit(t *testing.T, dir, sha string) bool {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "-C", dir, "cat-file", "-e", sha+"^{commit}")
	return cmd.Run() == nil
}
