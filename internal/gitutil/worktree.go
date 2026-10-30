package gitutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var worktreeSlugPattern = regexp.MustCompile(`[^a-z0-9._-]+`)

// RepoRoot returns the absolute path of the repository containing dir.
func RepoRoot(ctx context.Context, dir string) (string, error) {
	out, err := gitOutput(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", errors.New("git did not report a repository root")
	}
	return root, nil
}

// CreateWorktree creates a new Git worktree for repoDir on a new branch
// named after name. The worktree lives in a sibling directory so it never
// appears in the repository's working tree. It returns the worktree path
// and the branch it checked out.
func CreateWorktree(ctx context.Context, repoDir, name string) (path, branch string, err error) {
	root, err := RepoRoot(ctx, repoDir)
	if err != nil {
		return "", "", err
	}

	slug := WorktreeSlug(name)
	parent := filepath.Join(filepath.Dir(root), "."+filepath.Base(root)+"-worktrees")
	for i := 1; ; i++ {
		candidate := slug
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", slug, i)
		}
		candidatePath := filepath.Join(parent, candidate)
		candidateBranch := "crush/" + candidate
		if pathExists(candidatePath) || branchExists(ctx, root, candidateBranch) {
			continue
		}
		path, branch = candidatePath, candidateBranch
		break
	}

	if _, err := gitOutput(ctx, root, "worktree", "add", "-b", branch, path, "HEAD"); err != nil {
		return "", "", fmt.Errorf("git worktree add: %w", err)
	}
	return path, branch, nil
}

// WorktreeSlug converts a user-provided worktree name into a branch- and
// path-safe slug. An empty or fully sanitized name becomes "session".
func WorktreeSlug(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = worktreeSlugPattern.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-._")
	if slug == "" {
		return "session"
	}
	return slug
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func branchExists(ctx context.Context, repoDir, branch string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
