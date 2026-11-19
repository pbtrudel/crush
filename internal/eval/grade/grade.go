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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/charmbracelet/crush/internal/eval/gitx"
	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/charmbracelet/crush/internal/eval/toolchain"
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
	// Reason says why a checkout the model broke could not be graded. It is
	// empty for a grade that ran.
	Reason string `json:"reason,omitempty"`
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
	src := filepath.Join(dest, "src")
	base, err := checkout.Head(ctx, src)
	if err != nil {
		return Result{}, err
	}
	return Grade(ctx, tk, repoDir, src, base, env)
}

// Grade grades the working tree of srcDir against tk, from base, the commit
// the run started at, which the model's own history cannot change. It
// removes the test files the model added to the graded packages, writes the
// task's hidden files and runs the touched tests and then every test of the
// graded packages. It reads the hidden blobs from repoDir and runs go with
// env.
//
// A checkout the model broke, as when it deletes .git or moves its work to
// an orphan branch, is a failed grade with a reason, not an error: one bad
// run must not stop the batch.
func Grade(ctx context.Context, tk *task.Task, repoDir, srcDir, base string, env []string) (Result, error) {
	var res Result

	removed, err := removeModelTests(ctx, srcDir, base, tk.Grade.Packages)
	if err != nil {
		return failed(fmt.Errorf("find the test files the model added: %w", err)), nil
	}
	res.ModelTestsRemoved = len(removed)

	if err := ApplyHidden(ctx, repoDir, srcDir, tk.Grade.Hidden, tk.Grade.Deleted); err != nil {
		return failed(err), nil
	}

	touched, err := GoTest(ctx, srcDir, env, testArgs(tk, touchedPattern(tk.Grade.Tests)))
	if err != nil {
		return res, err
	}
	all, err := GoTest(ctx, srcDir, env, testArgs(tk, skipArgs(tk.Grade.Exclude)))
	if err != nil {
		return res, err
	}

	res.TouchedMS = touched.Duration.Milliseconds()
	res.PackagesMS = all.Duration.Milliseconds()
	res.CompileError = touched.Compile || all.Compile
	res.Packages = !all.Compile && !all.PackageFailed && len(all.Failed) == 0

	// Every named test must report pass. A named test that never ran is a
	// failure, unless the package did not build.
	var missing []string
	if !touched.Compile {
		for _, name := range tk.Grade.Tests {
			if !touched.Passed[name] {
				missing = append(missing, name)
			}
		}
	}
	res.Touched = !touched.Compile && !touched.PackageFailed && len(missing) == 0
	res.Failed = failedNames(missing, touched.Failed, all.Failed)
	return res, nil
}

// Env is the environment grading runs go with: PATH, home as HOME, the go
// caches, and no network or toolchain download. home is a scratch directory,
// so the tests and go never read the user's files. The selected toolchain
// comes first on PATH, so grading runs the go go.mod asks for even though
// GOTOOLCHAIN is local.
func Env(home string) []string {
	return env(home, os.Environ(), toolchain.GOROOT)
}

// env builds Env from parent, with resolve naming the toolchain that goes
// first on PATH.
func env(home string, parent []string, resolve toolchain.Resolver) []string {
	env := []string{
		"PATH=" + toolchain.Path(parent, resolve),
		"HOME=" + home,
		"GOFLAGS=-buildvcs=false",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
	}
	for _, name := range []string{"GOCACHE", "GOMODCACHE"} {
		env = append(env, name+"="+toolchain.Value(parent, name, resolve))
	}
	return env
}

// failed is the grade of a checkout git could not read. The reason says
// what the model broke.
func failed(err error) Result { return Result{Reason: err.Error()} }

// testArgs is the go test command for one grading pass, with the pattern or
// the skip list and nothing else.
func testArgs(tk *task.Task, filter []string) []string {
	args := []string{"test", "-count=1", "-json", "-timeout", tk.Grade.Timeout.String()}
	args = append(args, filter...)
	return append(args, tk.Grade.Packages...)
}

// touchedPattern runs only the task's tests.
func touchedPattern(tests []string) []string {
	return []string{"-run", Pattern(tests)}
}

// skipArgs skips the excluded tests, or nothing when the task excludes none.
func skipArgs(exclude []string) []string {
	if len(exclude) == 0 {
		return nil
	}
	return []string{"-skip", Pattern(exclude)}
}

// Pattern is the -run or -skip value that selects exactly the named tests. A
// name is literal text, so it is quoted: the task format allows only Test
// followed by word characters, and this keeps the pattern right even if that
// rule is ever widened.
func Pattern(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
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

// Outcome is what one go test run says about its packages.
type Outcome struct {
	// Compile is a package that did not build.
	Compile bool
	// PackageFailed is a package whose run failed without naming a test.
	PackageFailed bool
	// Passed holds the tests that reported pass.
	Passed map[string]bool
	// Failed holds the tests that reported fail.
	Failed   []string
	Duration time.Duration
	// Output is the text the run printed, compiler errors included.
	Output string
}

// GoTest runs one go command with args in dir and parses its test2json
// stream.
func GoTest(ctx context.Context, dir string, env, args []string) (Outcome, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, toolchain.Go(env), args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	out := Outcome{Duration: time.Since(start), Passed: map[string]bool{}}
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
			Output      string
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		events++
		out.Output += event.Output
		switch {
		case event.Action == "build-fail", event.Action == "fail" && event.FailedBuild != "":
			out.Compile = true
		case event.Action == "pass" && event.Test != "":
			out.Passed[event.Test] = true
		case event.Action == "fail" && event.Test != "":
			failed[event.Test] = true
		case event.Action == "fail":
			out.PackageFailed = true
		}
	}
	out.Output += stderr.String()
	if err := scanner.Err(); err != nil {
		return out, err
	}
	// A go command that failed before it ran a test binary, as when the
	// package does not load, is a build failure. A run that reported events
	// but named no failing test, as a panic outside a test function does,
	// still failed its package.
	switch {
	case runErr != nil && events == 0:
		out.Compile = true
	case runErr != nil && !out.PackageFailed && len(failed) == 0:
		out.PackageFailed = true
	}
	for name := range failed {
		out.Failed = append(out.Failed, name)
	}
	sort.Strings(out.Failed)
	return out, nil
}
