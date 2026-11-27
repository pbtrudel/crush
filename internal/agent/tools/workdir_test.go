package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
)

func TestWorkingDirFrom(t *testing.T) {
	t.Parallel()
	require.Equal(t, "/lead", WorkingDirFrom(t.Context(), "/lead"))
	require.Equal(t, "/tree", WorkingDirFrom(WithWorkingDir(t.Context(), "/tree"), "/lead"))
	require.Equal(t, "/lead", WorkingDirFrom(WithWorkingDir(t.Context(), ""), "/lead"), "an empty directory changes nothing")
}

// TestGlobReadsTheContextWorkingDir pins that a tool reads its working
// directory per call, including for a relative path argument.
func TestGlobReadsTheContextWorkingDir(t *testing.T) {
	t.Parallel()
	base, other := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, "found.txt"), []byte("x"), 0o644))
	tool := NewGlobTool(base, config.ToolGlob{})
	ctx := WithWorkingDir(t.Context(), other)

	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "g1", Name: GlobToolName, Input: `{"pattern":"*.txt"}`})
	require.NoError(t, err)
	require.Contains(t, resp.Content, "found.txt")

	resp, err = tool.Run(ctx, fantasy.ToolCall{ID: "g2", Name: GlobToolName, Input: `{"pattern":"*.txt","path":"."}`})
	require.NoError(t, err)
	require.Contains(t, resp.Content, "found.txt", "a relative path resolves inside the call's working directory")
}

// resolvedPath resolves symlinks and normalizes slashes, so a directory and
// the same directory as the shell prints it compare equal on every platform.
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.ToSlash(path)
}

// TestBashResolvesWorkingDirAgainstTheContextWorkingDir pins the bash half of
// a teammate working in a task worktree: a relative working_dir resolves
// inside the call's working directory, not the process directory, so a
// teammate's shell starts in its worktree.
func TestBashResolvesWorkingDirAgainstTheContextWorkingDir(t *testing.T) {
	t.Parallel()
	base, tree := t.TempDir(), t.TempDir()
	sub := filepath.Join(tree, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	tool := newBashToolForTest(base)
	ctx := context.WithValue(WithWorkingDir(t.Context(), tree), SessionIDContextKey, "test-session")

	input, err := json.Marshal(BashParams{Command: "pwd", Description: "where", WorkingDir: "pkg"})
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "b1", Name: BashToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, resolvedPath(t, sub),
		"a relative working_dir resolves inside the call's working directory")

	// An absolute working_dir is still honored as given.
	input, err = json.Marshal(BashParams{Command: "pwd", Description: "where", WorkingDir: sub})
	require.NoError(t, err)
	resp, err = tool.Run(ctx, fantasy.ToolCall{ID: "b2", Name: BashToolName, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, resolvedPath(t, sub))
}
