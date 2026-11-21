package run

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/gitx"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/netjail"
)

// Paths are the host directories a jailed run hides from its child, and that
// the contamination scan flags when a tool call names one of them.
type Paths struct {
	// Suite is the suite directory the tasks come from.
	Suite string
	// Repo is the repository the runner resolves.
	Repo string
	// Checkout is the git toplevel of Repo: its history holds every
	// answer.
	Checkout string
	// Worktrees is the directory that holds this checkout's sibling
	// worktrees, empty when it is not a linked worktree.
	Worktrees string
	// Batches is the eval home's batches directory.
	Batches string
	// Own is the run's own directory under Batches. The child writes its
	// checkout, home, config and data there, so naming it is not contact
	// with another batch.
	Own string
	// Remotes are the owner/repo names of the checkout's origin and
	// upstream remotes, when they exist.
	Remotes []string
}

// basePaths are the host paths of a run that no repository is needed for.
func basePaths(repoDir, suiteDir string, h *home.Home, runDir string) Paths {
	paths := Paths{Suite: suiteDir, Repo: repoDir, Own: runDir}
	if h != nil {
		paths.Batches = filepath.Join(h.Dir(), "batches")
	}
	return paths
}

// HostPaths derives the host paths of a jailed run from the repository and
// suite it uses, its eval home and its own run directory. Only a jailed run
// needs them: a repository without a work tree has no checkout to hide, and a
// run on the host must not fail on one. protect names the directories the run
// reads, which a worktrees parent must not hide. The scan of a finished batch
// rebuilds them from the same arguments.
func HostPaths(ctx context.Context, repoDir, suiteDir string, h *home.Home, runDir string, protect ...string) (Paths, error) {
	paths := basePaths(repoDir, suiteDir, h, runDir)
	top, err := gitx.Output(ctx, repoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Paths{}, fmt.Errorf("locate the host checkout: %w", err)
	}
	paths.Checkout = top
	if parent, refused := worktreesParent(ctx, top, protect); refused {
		slog.Warn("Netjail: the worktrees parent holds a directory the run reads, so the checkout is masked alone",
			"parent", parent)
	} else {
		paths.Worktrees = parent
	}
	paths.Remotes = repoRemotes(ctx, top)
	return paths, nil
}

// repoRemotes is the owner/repo name of the checkout's origin and upstream
// remotes, when they exist: a gh command that names one of them counts as a
// lookup too, since an upstream pull request can hold the fix.
func repoRemotes(ctx context.Context, top string) []string {
	var names []string
	for _, remote := range []string{"origin", "upstream"} {
		url, err := gitx.Output(ctx, top, "remote", "get-url", remote)
		if err != nil {
			continue
		}
		if name := repoName(url); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// repoName is the owner/repo a git remote URL names, or empty for a URL that
// names none, such as a local path or a subgroup.
func repoName(remote string) string {
	url := strings.TrimSpace(remote)
	if i := strings.Index(url, "://"); i >= 0 {
		url = url[i+3:]
		slash := strings.Index(url, "/")
		if slash < 0 {
			return ""
		}
		url = url[slash+1:]
	} else if i := strings.Index(url, ":"); i >= 0 {
		// The scp-like git@host:owner/repo form.
		url = url[i+1:]
	}
	url = strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	parts := strings.Split(url, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// visiblePaths are the directories a jailed run reads from the eval home and
// from go, which a worktrees parent must not hide: the eval home, the go
// toolchain and its caches.
func visiblePaths(h *home.Home, env []string) []string {
	var paths []string
	if h != nil {
		paths = append(paths, h.Dir())
	}
	for _, name := range []string{"GOROOT", "GOCACHE", "GOMODCACHE"} {
		for _, kv := range env {
			if value, ok := strings.CutPrefix(kv, name+"="); ok && value != "" {
				paths = append(paths, value)
			}
		}
	}
	// The child's PATH starts with the bin directory of the toolchain it
	// selected, whose parent is the GOROOT it reads.
	for _, kv := range env {
		if path, ok := strings.CutPrefix(kv, "PATH="); ok {
			first, _, _ := strings.Cut(path, string(os.PathListSeparator))
			if first != "" {
				paths = append(paths, filepath.Dir(first))
			}
		}
	}
	return paths
}

// worktreesParent is the directory that holds the sibling worktrees of a
// linked worktree, and empty for the repository itself: only a linked
// worktree has a git directory apart from the common one, and only there do
// siblings sit beside the checkout. refused reports a parent the jail must
// not hide.
func worktreesParent(ctx context.Context, top string, protect []string) (parent string, refused bool) {
	gitDir, err := gitx.Output(ctx, top, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", false
	}
	common, err := gitx.Output(ctx, top, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(top, common)
	}
	if filepath.Clean(gitDir) == filepath.Clean(common) {
		return "", false
	}
	parent = filepath.Dir(top)
	return parent, unsafeParent(parent, protect)
}

// unsafeParent reports whether parent is a directory the jail must not hide:
// the root, the home directory, or a directory that holds one of protect, the
// paths the run reads.
func unsafeParent(parent string, protect []string) bool {
	clean := filepath.Clean(parent)
	if clean == string(filepath.Separator) {
		return true
	}
	if homeDir, err := os.UserHomeDir(); err == nil && filepath.Clean(homeDir) == clean {
		return true
	}
	for _, path := range protect {
		if path == "" {
			continue
		}
		if filepath.Clean(path) == clean || strings.HasPrefix(filepath.Clean(path), clean+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// jailMasks is what a jailed run hides: the jail's own masks, the host
// checkout the tasks come from, the worktrees beside it and the eval home's
// batches. The run's own directory stays visible and writable under the
// batches mask, since the child writes its checkout, home, config and data
// there.
func jailMasks(paths Paths) []netjail.Mask {
	masks := make([]netjail.Mask, 0, len(netjail.DefaultMasks)+3)
	for _, path := range netjail.DefaultMasks {
		masks = append(masks, netjail.Mask{Path: path})
	}
	for _, path := range []string{paths.Checkout, paths.Worktrees} {
		if path != "" {
			masks = append(masks, netjail.Mask{Path: path})
		}
	}
	if paths.Batches != "" {
		mask := netjail.Mask{Path: paths.Batches}
		if paths.Own != "" {
			mask.Keep = []string{paths.Own}
		}
		masks = append(masks, mask)
	}
	return masks
}
