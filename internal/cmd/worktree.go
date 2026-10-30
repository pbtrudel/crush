package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/crush/internal/gitutil"
	"github.com/spf13/cobra"
)

// applyWorktreeFlag creates a Git worktree when --worktree is set and
// returns the directory Crush should use. An empty flag leaves cwd alone.
func applyWorktreeFlag(cmd *cobra.Command, cwd string) (string, error) {
	name, _ := cmd.Flags().GetString("worktree")
	if name == "" {
		return cwd, nil
	}

	path, branch, err := gitutil.CreateWorktree(cmd.Context(), cwd, name)
	if err != nil {
		return "", fmt.Errorf("failed to create worktree: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Using worktree %s on branch %s\n", path, branch)
	return path, nil
}
