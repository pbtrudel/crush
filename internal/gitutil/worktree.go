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

// Worktree describes one registered Git worktree.
type Worktree struct {
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	Head     string `json:"head,omitempty"`
	Bare     bool   `json:"bare,omitempty"`
	Detached bool   `json:"detached,omitempty"`
	Locked   bool   `json:"locked,omitempty"`
	Prunable bool   `json:"prunable,omitempty"`
	Current  bool   `json:"current,omitempty"`
}

// CreateWorktree creates a new Git worktree for repoDir on a new branch
// named after name, based on the current HEAD. The worktree lives in a
// sibling directory so it never appears in the repository's working tree.
// It returns the worktree path and the branch it checked out.
func CreateWorktree(ctx context.Context, repoDir, name string) (path, branch string, err error) {
	return CreateWorktreeFrom(ctx, repoDir, name, "HEAD")
}

// CreateWorktreeFrom is CreateWorktree with an explicit base ref.
func CreateWorktreeFrom(ctx context.Context, repoDir, name, base string) (path, branch string, err error) {
	root, err := RepoRoot(ctx, repoDir)
	if err != nil {
		return "", "", err
	}
	base = strings.TrimSpace(base)
	if base == "" {
		base = "HEAD"
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

	if _, err := gitOutput(ctx, root, "worktree", "add", "-b", branch, path, base); err != nil {
		return "", "", fmt.Errorf("git worktree add: %w", err)
	}
	return path, branch, nil
}

// CurrentWorktree reports the worktree containing dir and whether it is a
// linked worktree rather than the repository's main checkout.
func CurrentWorktree(ctx context.Context, dir string) (Worktree, bool, error) {
	root, err := RepoRoot(ctx, dir)
	if err != nil {
		return Worktree{}, false, err
	}
	gitDir, err := gitOutput(ctx, root, "rev-parse", "--git-dir")
	if err != nil {
		return Worktree{}, false, fmt.Errorf("git rev-parse --git-dir: %w", err)
	}
	commonDir, err := gitOutput(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		return Worktree{}, false, fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	branch, _ := gitOutput(ctx, root, "branch", "--show-current")
	worktree := Worktree{
		Path:    root,
		Branch:  strings.TrimSpace(branch),
		Current: true,
	}
	linked := !samePath(resolveGitDir(root, gitDir), resolveGitDir(root, commonDir))
	return worktree, linked, nil
}

// ListWorktrees returns every worktree registered for the repository
// containing dir.
func ListWorktrees(ctx context.Context, dir string) ([]Worktree, error) {
	root, err := RepoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	out, err := gitOutput(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}

	var worktrees []Worktree
	var current Worktree
	flush := func() {
		if current.Path == "" {
			return
		}
		current.Current = samePath(current.Path, root)
		worktrees = append(worktrees, current)
		current = Worktree{}
	}
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			current.Path = value
		case "HEAD":
			current.Head = value
		case "branch":
			current.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "bare":
			current.Bare = true
		case "detached":
			current.Detached = true
		case "locked":
			current.Locked = true
		case "prunable":
			current.Prunable = true
		}
	}
	flush()
	return worktrees, nil
}

// RemoveWorktree removes the worktree at path. It refuses to remove a
// worktree with uncommitted changes unless force is true, and never removes
// the worktree containing dir.
func RemoveWorktree(ctx context.Context, dir, path string, force bool) error {
	root, err := RepoRoot(ctx, dir)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if samePath(abs, root) {
		return errors.New("refusing to remove the worktree containing the current directory")
	}
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, pruneErr := gitOutput(ctx, root, "worktree", "prune")
			return pruneErr
		}
		return err
	}
	if !force {
		out, err := gitOutput(ctx, abs, "status", "--porcelain")
		if err != nil {
			return fmt.Errorf("inspect worktree %s: %w", abs, err)
		}
		if strings.TrimSpace(out) != "" {
			return fmt.Errorf("worktree %s has uncommitted changes; commit or discard them, or pass force", abs)
		}
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, abs)
	if _, err := gitOutput(ctx, root, args...); err != nil {
		return fmt.Errorf("git worktree remove: %w", err)
	}
	return nil
}

// DeleteBranch deletes the local branch. It uses the safe -d form unless
// force is true.
func DeleteBranch(ctx context.Context, dir, branch string, force bool) error {
	root, err := RepoRoot(ctx, dir)
	if err != nil {
		return err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("branch is required")
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	if _, err := gitOutput(ctx, root, "branch", flag, branch); err != nil {
		return fmt.Errorf("git branch %s %s: %w", flag, branch, err)
	}
	return nil
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return filepath.Clean(absA) == filepath.Clean(absB)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func resolveGitDir(root, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(root, value)
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
