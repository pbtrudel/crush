package task_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// TestReplaceBlock replaces a top-level block of a YAML file and leaves every
// other byte alone.
func TestReplaceBlock(t *testing.T) {
	t.Parallel()

	const block = "limits:\n  max_steps: 40\n  timeout: 10m\n"

	t.Run("replaces the block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0001.yaml")
		before := "schema: 1\nlimits:\n  max_steps: 120\n  timeout: 25m\nrisks: []\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0o644))

		require.NoError(t, task.ReplaceBlock(path, "limits", []byte(block)))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "schema: 1\n"+block+"risks: []\n", string(got))
	})

	t.Run("keeps a column-0 comment after the block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0001.yaml")
		before := "schema: 1\nlimits:\n  max_steps: 120\n\n# A comment.\nrisks: []\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0o644))

		require.NoError(t, task.ReplaceBlock(path, "limits", []byte(block)))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "schema: 1\n"+block+"\n# A comment.\nrisks: []\n", string(got))
	})

	t.Run("keeps a column-0 comment inside the block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0001.yaml")
		before := "schema: 1\nlimits:\n# Limits chosen by hand.\n  max_steps: 120\nrisks: []\n"
		require.NoError(t, os.WriteFile(path, []byte(before), 0o644))

		require.NoError(t, task.ReplaceBlock(path, "limits", []byte(block)))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "schema: 1\n"+block+"risks: []\n", string(got))
	})

	t.Run("appends a missing block", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "pr-0001.yaml")
		require.NoError(t, os.WriteFile(path, []byte("schema: 1\nid: pr-0001"), 0o644))

		require.NoError(t, task.ReplaceBlock(path, "limits", []byte(block)))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "schema: 1\nid: pr-0001\n"+block, string(got))
	})
}
