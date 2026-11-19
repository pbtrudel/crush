package task_test

import (
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// writeSuite writes a small suite and returns its repository root and its
// evals directory.
func writeSuite(t *testing.T) (root, evals string) {
	t.Helper()
	root = t.TempDir()
	evals = filepath.Join(root, "evals")
	writeFile(t, filepath.Join(evals, "tasks", "pr-0010.yaml"), "schema: 1\nid: pr-0010\n")
	writeFile(t, filepath.Join(evals, "tasks", "pr-0051.yaml"), "schema: 1\nid: pr-0051\n")
	writeFile(t, filepath.Join(evals, "footer.md"), "Make this change in the repository in the current directory.\n")
	return root, evals
}

func TestSuiteVersionMatchesGit(t *testing.T) {
	t.Parallel()
	root, evals := writeSuite(t)
	writeFile(t, filepath.Join(evals, "README.md"), "not part of the suite version\n")

	version, err := task.SuiteVersion(evals)
	require.NoError(t, err)
	require.Len(t, version, 40)

	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", "evals/tasks", "evals/footer.md")
	tree := git(t, root, "write-tree")
	require.Equal(t, version, git(t, root, "rev-parse", tree+":evals"))
}

func TestSuiteVersionChanges(t *testing.T) {
	t.Parallel()

	t.Run("with a task", func(t *testing.T) {
		t.Parallel()
		_, evals := writeSuite(t)
		before, err := task.SuiteVersion(evals)
		require.NoError(t, err)

		writeFile(t, filepath.Join(evals, "tasks", "pr-0010.yaml"), "schema: 1\nid: pr-0010\nwhy: changed\n")
		after, err := task.SuiteVersion(evals)
		require.NoError(t, err)
		require.NotEqual(t, before, after)
	})

	t.Run("with the footer", func(t *testing.T) {
		t.Parallel()
		_, evals := writeSuite(t)
		before, err := task.SuiteVersion(evals)
		require.NoError(t, err)

		writeFile(t, filepath.Join(evals, "footer.md"), "Another footer.\n")
		after, err := task.SuiteVersion(evals)
		require.NoError(t, err)
		require.NotEqual(t, before, after)
	})

	t.Run("not with other files", func(t *testing.T) {
		t.Parallel()
		_, evals := writeSuite(t)
		before, err := task.SuiteVersion(evals)
		require.NoError(t, err)

		writeFile(t, filepath.Join(evals, "README.md"), "readme\n")
		writeFile(t, filepath.Join(evals, "matrix.yaml"), "families: {}\n")
		after, err := task.SuiteVersion(evals)
		require.NoError(t, err)
		require.Equal(t, before, after)
	})
}
