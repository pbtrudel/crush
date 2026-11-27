package gitutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// mergeCleanupTimeout bounds the merge cleanup a caller no longer waits for,
// so a wedged git cannot hold the call forever.
const mergeCleanupTimeout = 30 * time.Second

// RepoPrefix returns dir's path inside its repository, in slash form with a
// trailing slash, or "" at the repository root. It is what git rev-parse
// --show-prefix prints.
func RepoPrefix(ctx context.Context, dir string) (string, error) {
	out, err := gitOutput(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-prefix: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// CommitAll stages every change in the work tree at dir, ignored files
// excepted, and commits it with message, or stages and commits only the
// changes pathspecs name. It reports false with no error when nothing was
// staged. Author, committer, signing and hooks follow the repository's git
// config, so a failing hook refuses the commit.
func CommitAll(ctx context.Context, dir, message string, pathspecs []string) (bool, error) {
	specs := pathspecs
	if len(specs) > 0 {
		var err error
		specs, err = existingPathspecs(ctx, dir, specs)
		if err != nil {
			return false, err
		}
		if len(specs) == 0 {
			return false, nil
		}
	}
	if len(specs) == 0 {
		if _, err := gitOutput(ctx, dir, "add", "-A"); err != nil {
			return false, fmt.Errorf("git add -A: %w", err)
		}
	} else if _, err := gitLiteral(ctx, dir, append([]string{"add", "-A", "--"}, specs...)...); err != nil {
		return false, fmt.Errorf("git add -A: %w", err)
	}
	// diff --quiet exits 1 when something is staged and 0 when nothing is.
	// With pathspecs it looks at those paths only, and the commit that
	// follows commits only them too, so a change another path already has
	// staged stays out of the commit.
	diffArgs := []string{"diff", "--cached", "--quiet"}
	commitArgs := []string{"commit", "-m", message}
	if len(specs) > 0 {
		diffArgs = append(append(diffArgs, "--"), specs...)
		commitArgs = append(append(commitArgs, "--"), specs...)
	}
	err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, diffArgs...)...).Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return false, fmt.Errorf("git diff --cached: %w", err)
	}
	if _, err := gitOutput(ctx, dir, commitArgs...); err != nil {
		return false, fmt.Errorf("git commit: %w", err)
	}
	return true, nil
}

// existingPathspecs keeps the literal pathspecs that match something in the
// work tree or the index. git refuses the whole add when one pathspec matches
// nothing, and a write scope the task never wrote to is legitimate.
func existingPathspecs(ctx context.Context, dir string, pathspecs []string) ([]string, error) {
	kept := make([]string, 0, len(pathspecs))
	for _, spec := range pathspecs {
		if spec == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(spec))); err == nil {
			kept = append(kept, spec)
			continue
		}
		// A path the work tree no longer holds may still be in the index:
		// deleting it inside a scope is a change to commit.
		cmd := exec.CommandContext(ctx, "git", "-C", dir, "--literal-pathspecs", "ls-files", "--error-unmatch", "--", spec)
		err := cmd.Run()
		if err == nil {
			kept = append(kept, spec)
			continue
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return nil, fmt.Errorf("git ls-files --error-unmatch %s: %w", spec, err)
		}
	}
	return kept, nil
}

// MergeError is a merge git refused or could not finish. The work tree is
// as it was before: a merge that started is aborted, and a merge that was
// already in progress is left untouched.
type MergeError struct {
	// Reason says why, in words a notice can quote.
	Reason string
	// Output is git's own output for the failed step.
	Output string
	// AbortFailed reports that git could not abort the merge it started, so
	// the tree is still mid-merge. A notice must not claim otherwise.
	AbortFailed bool
}

func (e *MergeError) Error() string {
	if e.Output == "" {
		return e.Reason
	}
	return e.Reason + ": " + e.Output
}

// MergeNoFF merges branch into the branch checked out at dir with a merge
// commit, even when a fast-forward is possible. A merge already in
// progress at dir is refused and left untouched, and a detached HEAD is
// refused before anything runs. A merge that stops on a conflict is
// aborted, and one git refuses to start (a dirty file it would overwrite)
// changes nothing; both return a *MergeError carrying git's output.
func MergeNoFF(ctx context.Context, dir, branch, message string) error {
	// Never disturb a merge the Lead started and has not finished.
	if mergeInProgress(ctx, dir) {
		return &MergeError{Reason: "a merge is already in progress, so the merge was refused"}
	}
	if _, err := gitOutput(ctx, dir, "symbolic-ref", "-q", "HEAD"); err != nil {
		return &MergeError{Reason: "the Lead's HEAD is detached, so there is no branch to merge into"}
	}
	out, err := gitOutput(ctx, dir, "merge", "--no-ff", "-m", message, "--end-of-options", branch)
	if err == nil {
		return nil
	}
	output := strings.TrimSpace(out)
	// A caller that gave up must not leave the merge half-done, so the
	// check and the abort run without its cancellation, on a bound of their
	// own.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), mergeCleanupTimeout)
	defer cancel()
	if mergeInProgress(cleanup, dir) {
		mergeErr := &MergeError{Reason: "the merge has conflicts and was aborted"}
		if abortOut, abortErr := gitOutput(cleanup, dir, "merge", "--abort"); abortErr != nil {
			output += "\n\ngit merge --abort failed: " + strings.TrimSpace(abortOut)
			mergeErr.AbortFailed = true
			// The tree is still mid-merge, so the reason must not claim the
			// merge was aborted.
			mergeErr.Reason = "the merge has conflicts and could not be aborted"
		}
		mergeErr.Output = output
		return mergeErr
	}
	return &MergeError{Reason: "git refused the merge", Output: output}
}

// mergeInProgress reports whether a merge stopped half-way at dir.
func mergeInProgress(ctx context.Context, dir string) bool {
	_, err := gitOutput(ctx, dir, "rev-parse", "-q", "--verify", "MERGE_HEAD")
	return err == nil
}
