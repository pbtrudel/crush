// Package grade grades a finished checkout against a task's hidden tests.
package grade

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/charmbracelet/crush/internal/eval/gitx"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// Result is the grade object of a result line plus the timings, the failing
// test names and the work the model moved out of the checkout.
type Result struct {
	Touched           bool     `json:"touched"`
	Packages          bool     `json:"packages"`
	CompileError      bool     `json:"compile_error"`
	ModelTestsRemoved int      `json:"model_tests_removed"`
	TouchedMS         int64    `json:"touched_ms"`
	PackagesMS        int64    `json:"packages_ms"`
	Failed            []string `json:"failed,omitempty"`
	Worktrees         int      `json:"worktrees"`
	Branches          int      `json:"branches"`
}

// Pass reports whether the grade passes: the touched tests and every test of
// the graded packages.
func (r Result) Pass() bool { return r.Touched && r.Packages }

// Reference prepares the merge commit's tree of tk in dest and grades it,
// which is what `crush-eval grade --reference` does.
func Reference(ctx context.Context, tk *task.Task, repoDir, dest string, env []string) (Result, error) {
	if _, err := checkout.Prepare(ctx, repoDir, tk.Source.Merge, dest); err != nil {
		return Result{}, err
	}
	return Grade(ctx, tk, repoDir, filepath.Join(dest, "src"), env)
}

// Grade grades the working tree of srcDir against tk. It removes the test
// files the model added to the graded packages, writes the task's hidden
// files, runs the touched tests and then every test of the graded packages,
// and counts the worktrees and branches the model created. It reads the
// hidden blobs from repoDir and runs go with env.
func Grade(ctx context.Context, tk *task.Task, repoDir, srcDir string, env []string) (Result, error) {
	var res Result

	base, err := gitx.Output(ctx, srcDir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return res, fmt.Errorf("find the task base commit: %w", err)
	}
	removed, err := removeModelTests(ctx, srcDir, lastLine(base), tk.Grade.Packages)
	if err != nil {
		return res, err
	}
	res.ModelTestsRemoved = len(removed)

	if err := ApplyHidden(ctx, repoDir, srcDir, tk.Grade.Hidden, tk.Grade.Deleted); err != nil {
		return res, err
	}

	touched, err := runGoTest(ctx, srcDir, env, testArgs(tk, touchedPattern(tk.Grade.Tests)))
	if err != nil {
		return res, err
	}
	all, err := runGoTest(ctx, srcDir, env, testArgs(tk, skipArgs(tk.Grade.Exclude)))
	if err != nil {
		return res, err
	}

	res.TouchedMS = touched.duration.Milliseconds()
	res.PackagesMS = all.duration.Milliseconds()
	res.CompileError = touched.compile || all.compile
	res.Packages = !all.compile && !all.packageFailed && len(all.failed) == 0

	// Every named test must report pass. A named test that never ran is a
	// failure, unless the package did not build.
	var missing []string
	if !touched.compile {
		for _, name := range tk.Grade.Tests {
			if !touched.passed[name] {
				missing = append(missing, name)
			}
		}
	}
	res.Touched = !touched.compile && !touched.packageFailed && len(missing) == 0
	res.Failed = failedNames(missing, touched.failed, all.failed)

	res.Worktrees, res.Branches = gitCounts(ctx, srcDir)
	return res, nil
}

// testArgs is the go test command for one grading pass, with the pattern or
// the skip list and nothing else.
func testArgs(tk *task.Task, filter []string) []string {
	args := []string{"test", "-count=1", "-json", "-timeout", tk.Grade.Timeout.String()}
	args = append(args, filter...)
	return append(args, tk.Grade.Packages...)
}

// touchedPattern runs only the task's tests.
func touchedPattern(tests []string) []string {
	return []string{"-run", "^(" + strings.Join(tests, "|") + ")$"}
}

// skipArgs skips the excluded tests, or nothing when the task excludes none.
func skipArgs(exclude []string) []string {
	if len(exclude) == 0 {
		return nil
	}
	return []string{"-skip", "^(" + strings.Join(exclude, "|") + ")$"}
}

// failedNames is the sorted union of the test names that did not pass.
func failedNames(missing, touched, all []string) []string {
	names := map[string]bool{}
	for _, list := range [][]string{missing, touched, all} {
		for _, name := range list {
			names[name] = true
		}
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	if len(sorted) == 0 {
		return nil
	}
	return sorted
}

// removeModelTests deletes the test files of the graded packages that the
// model added, tracked or not, and returns their paths.
func removeModelTests(ctx context.Context, srcDir, base string, packages []string) ([]string, error) {
	dirs := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		dirs[strings.TrimPrefix(pkg, "./")] = true
	}

	untracked, err := gitx.Output(ctx, srcDir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	// No rename detection, so a model that renames a test file still shows
	// the new path as added.
	added, err := gitx.Output(ctx, srcDir, "diff", "--no-renames", "--name-only", "--diff-filter=A", "-z", base)
	if err != nil {
		return nil, err
	}

	var removed []string
	for _, list := range [][]string{strings.Split(untracked, "\x00"), strings.Split(added, "\x00")} {
		for _, file := range list {
			if !strings.HasSuffix(file, "_test.go") || !dirs[path.Dir(file)] {
				continue
			}
			if err := os.Remove(filepath.Join(srcDir, filepath.FromSlash(file))); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			removed = append(removed, file)
		}
	}
	sort.Strings(removed)
	return removed, nil
}

// ApplyHidden writes the hidden files into srcDir and deletes the files the
// task says the pull request removed. It checks every blob against its hash.
func ApplyHidden(ctx context.Context, repoDir, srcDir string, hidden []task.Hidden, deleted []string) error {
	for _, file := range hidden {
		data, err := gitx.Bytes(ctx, repoDir, "cat-file", "blob", file.Blob)
		if err != nil {
			return fmt.Errorf("read hidden %s: %w", file.Path, err)
		}
		sha, err := gitx.Stdin(ctx, srcDir, data, "hash-object", "--stdin")
		if err != nil {
			return err
		}
		if sha != file.Blob {
			return fmt.Errorf("hidden %s: blob %s hashes to %s", file.Path, file.Blob, sha)
		}
		path := filepath.Join(srcDir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	for _, name := range deleted {
		if err := os.Remove(filepath.Join(srcDir, filepath.FromSlash(name))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// outcome is what one go test run says about its package.
type outcome struct {
	compile       bool
	packageFailed bool
	passed        map[string]bool
	failed        []string
	duration      time.Duration
}

// runGoTest runs one go test command in srcDir and parses its test2json
// stream.
func runGoTest(ctx context.Context, srcDir string, env, args []string) (outcome, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = srcDir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	out := outcome{duration: time.Since(start), passed: map[string]bool{}}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if errors.Is(runErr, exec.ErrNotFound) {
		return out, runErr
	}

	failed := map[string]bool{}
	events := 0
	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var event struct {
			Action      string
			Test        string
			FailedBuild string
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		events++
		switch {
		case event.Action == "build-fail", event.Action == "fail" && event.FailedBuild != "":
			out.compile = true
		case event.Action == "pass" && event.Test != "":
			out.passed[event.Test] = true
		case event.Action == "fail" && event.Test != "":
			failed[event.Test] = true
		case event.Action == "fail":
			out.packageFailed = true
		}
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	// A go command that failed before it ran a test binary, as when the
	// package does not load, is a build failure too.
	if events == 0 && runErr != nil {
		out.compile = true
	}
	for name := range failed {
		out.failed = append(out.failed, name)
	}
	sort.Strings(out.failed)
	return out, nil
}

// gitCounts returns how many worktrees and branches beyond the initial one
// the checkout has.
func gitCounts(ctx context.Context, srcDir string) (worktrees, branches int) {
	list, err := gitx.Output(ctx, srcDir, "worktree", "list", "--porcelain")
	if err == nil {
		worktrees = max(0, strings.Count(list, "worktree ")-1)
	}
	refs, err := gitx.Output(ctx, srcDir, "for-each-ref", "--format=%(refname)", "refs/heads")
	if err == nil {
		branches = max(0, len(strings.Fields(refs))-1)
	}
	return worktrees, branches
}

// lastLine returns the last non-empty line of s.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
