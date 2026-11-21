package run

import (
	"context"
	"fmt"
	"path/filepath"

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
}

// HostPaths derives the host paths of a run from the repository and suite it
// uses, its eval home and its own run directory. The scan of a finished batch
// rebuilds them from the same arguments.
func HostPaths(ctx context.Context, repoDir, suiteDir string, h *home.Home, runDir string) (Paths, error) {
	paths := Paths{Suite: suiteDir, Repo: repoDir, Own: runDir}
	top, err := gitx.Output(ctx, repoDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Paths{}, fmt.Errorf("locate the host checkout: %w", err)
	}
	paths.Checkout = top
	paths.Worktrees = worktreesParent(ctx, top)
	if h != nil {
		paths.Batches = filepath.Join(h.Dir(), "batches")
	}
	return paths, nil
}

// worktreesParent is the directory that holds the sibling worktrees of a
// linked worktree, and empty for the repository itself: only a linked
// worktree has a git directory apart from the common one, and only there do
// siblings sit beside the checkout.
func worktreesParent(ctx context.Context, top string) string {
	gitDir, err := gitx.Output(ctx, top, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ""
	}
	common, err := gitx.Output(ctx, top, "rev-parse", "--git-common-dir")
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(top, common)
	}
	if filepath.Clean(gitDir) == filepath.Clean(common) {
		return ""
	}
	return filepath.Dir(top)
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
