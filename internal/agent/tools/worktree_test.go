package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func initWorktreeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644))
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
}

func runWorktreeTool(t *testing.T, tool fantasy.AgentTool, params WorktreeParams) worktreeResponse {
	t.Helper()
	data, err := json.Marshal(params)
	require.NoError(t, err)
	response, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "call-1",
		Name:  tool.Info().Name,
		Input: string(data),
	})
	require.NoError(t, err)
	require.False(t, response.IsError, response.Content)
	var result worktreeResponse
	require.NoError(t, json.Unmarshal([]byte(response.Content), &result))
	return result
}

func TestWorktreeToolCreateListRemove(t *testing.T) {
	t.Parallel()
	repo := initWorktreeRepo(t)
	tool := NewWorktreeTool(repo)

	created := runWorktreeTool(t, tool, WorktreeParams{Action: "create", Name: "Fix Login"})
	require.Equal(t, "create", created.Action)
	require.Equal(t, "crush/fix-login", created.Branch)
	require.DirExists(t, created.Path)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(created.Path)) })

	listed := runWorktreeTool(t, tool, WorktreeParams{Action: "list"})
	require.Len(t, listed.Worktrees, 2)

	removed := runWorktreeTool(t, tool, WorktreeParams{
		Action:       "remove",
		Path:         created.Path,
		DeleteBranch: true,
	})
	require.True(t, removed.Removed)
	require.Equal(t, created.Branch, removed.DeletedBranch)
	require.NoDirExists(t, created.Path)
}

func TestWorktreeToolAlreadyInWorktree(t *testing.T) {
	t.Parallel()
	repo := initWorktreeRepo(t)
	mainTool := NewWorktreeTool(repo)
	created := runWorktreeTool(t, mainTool, WorktreeParams{Action: "create", Name: "feature"})
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(created.Path)) })

	worktreeTool := NewWorktreeTool(created.Path)
	result := runWorktreeTool(t, worktreeTool, WorktreeParams{Action: "create", Name: "another"})
	require.True(t, result.AlreadyInWorktree)
	require.Equal(t, created.Path, result.Path)
	require.Equal(t, created.Branch, result.Branch)
}

func TestWorktreeToolRejectsUnknownAction(t *testing.T) {
	t.Parallel()
	repo := initWorktreeRepo(t)
	tool := NewWorktreeTool(repo)

	data, err := json.Marshal(WorktreeParams{Action: "delete"})
	require.NoError(t, err)
	response, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "call-1",
		Name:  tool.Info().Name,
		Input: string(data),
	})
	require.NoError(t, err)
	require.True(t, response.IsError)
}
