// Package candidates selects merged pull requests of the fork that can become
// eval tasks and classifies their tests as plain or interface (design section
// 1.4, steps 1 and 2).
package candidates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/charmbracelet/crush/internal/eval/gitx"
)

// Classes of a classified pull request.
const (
	ClassPlain     = "plain"
	ClassInterface = "interface"
	ClassDropped   = "dropped"
)

// Limits of rules 2 and 4.
const (
	MaxSourceFiles     = 12
	MaxSourceAdditions = 600
	MaxInterfaceNames  = 4
)

// testTimeout bounds one classification run. It is well above rule 7's five
// minutes so that a slow but valid candidate still gets a class.
const testTimeout = "10m"

// File is one file of a pull request's diff.
type File struct {
	Path       string `json:"path"`
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	ChangeType string `json:"changeType"`
}

// PullRequest is a merged pull request as the GitHub queries report it.
type PullRequest struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	MergeCommit  Commit `json:"mergeCommit"`
	Additions    int    `json:"additions"`
	ChangedFiles int    `json:"changedFiles"`
	Files        []File `json:"files"`
	Body         string `json:"body"`
}

// Commit is the object GitHub returns for a commit field.
type Commit struct {
	OID string `json:"oid"`
}

// Merge is the oid of the pull request's merge commit.
func (p PullRequest) Merge() string { return p.MergeCommit.OID }

// GitHub is the read-only GitHub query set candidates needs. Tests use a
// fixture, never the gh command.
type GitHub interface {
	List(ctx context.Context, repo string) ([]PullRequest, error)
	View(ctx context.Context, repo string, number int) (PullRequest, error)
}

// GH runs the read-only gh queries.
type GH struct{}

// List returns the merged pull requests of repo.
func (GH) List(ctx context.Context, repo string) ([]PullRequest, error) {
	out, err := run(ctx, "gh", "pr", "list", "-R", repo, "--state", "merged", "--limit", "500",
		"--json", "number,title,mergeCommit,additions,changedFiles")
	if err != nil {
		return nil, err
	}
	var prs []PullRequest
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse gh pr list: %w", err)
	}
	return prs, nil
}

// View returns the files, body and merge commit of one pull request.
func (GH) View(ctx context.Context, repo string, number int) (PullRequest, error) {
	out, err := run(ctx, "gh", "pr", "view", strconv.Itoa(number), "-R", repo,
		"--json", "files,body,mergeCommit")
	if err != nil {
		return PullRequest{}, err
	}
	pr := PullRequest{Number: number}
	if err := json.Unmarshal(out, &pr); err != nil {
		return PullRequest{}, fmt.Errorf("parse gh pr view %d: %w", number, err)
	}
	return pr, nil
}

// Filter returns why pr fails rules 1 and 2, or "" when it passes them.
func Filter(pr PullRequest) string {
	if SkipTitle(pr.Title) {
		return "title is a sync, dependency bump, CI or documentation change"
	}
	var sources, tests []File
	for _, file := range pr.Files {
		switch {
		case strings.HasSuffix(file.Path, "_test.go"):
			tests = append(tests, file)
		case strings.HasSuffix(file.Path, ".go"):
			sources = append(sources, file)
		}
	}
	if len(sources) == 0 {
		return "no non-test Go file"
	}
	if len(tests) == 0 {
		return "no test file"
	}
	if len(sources) > MaxSourceFiles {
		return fmt.Sprintf("%d source files", len(sources))
	}
	added := 0
	for _, file := range sources {
		added += file.Additions
	}
	if added > MaxSourceAdditions {
		return fmt.Sprintf("%d added source lines", added)
	}
	return ""
}

var bumpTitle = regexp.MustCompile(`\bbump\b`)

// SkipTitle reports whether title marks the pull request as an upstream sync,
// a dependency bump, a CI or a documentation change.
func SkipTitle(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	for _, prefix := range []string{"ci:", "ci(", "docs:", "docs(", "chore(deps)", "chore: sync", "chore: bump"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.Contains(lower, "sync upstream") || strings.Contains(lower, "upstream sync") ||
		strings.Contains(lower, "dependabot") {
		return true
	}
	return bumpTitle.MatchString(lower)
}

// Candidate is one classified pull request: one JSON line of the command.
type Candidate struct {
	PR       int      `json:"pr"`
	Title    string   `json:"title,omitempty"`
	Merge    string   `json:"merge"`
	Base     string   `json:"base,omitempty"`
	Class    string   `json:"class"`
	Reason   string   `json:"reason,omitempty"`
	Names    []string `json:"names,omitempty"`
	Tests    []string `json:"tests,omitempty"`
	Packages []string `json:"packages,omitempty"`
}

// Classify classifies pr against the repository at repoDir. scratch is the
// parent directory of its temporary checkouts, and env is the environment go
// runs with.
func Classify(ctx context.Context, repoDir string, pr PullRequest, scratch string, env []string) (Candidate, error) {
	cand := Candidate{PR: pr.Number, Title: pr.Title, Merge: pr.Merge()}
	if reason := Filter(pr); reason != "" {
		cand.Class, cand.Reason = ClassDropped, reason
		return cand, nil
	}
	if cand.Merge == "" {
		return cand, fmt.Errorf("pr %d: no merge commit", pr.Number)
	}
	base, err := gitx.Output(ctx, repoDir, "rev-parse", pr.Merge()+"^1")
	if err != nil {
		return cand, err
	}
	cand.Base = base

	var testFiles []string
	for _, file := range pr.Files {
		if strings.HasSuffix(file.Path, "_test.go") {
			testFiles = append(testFiles, file.Path)
		}
	}
	cand.Tests, err = TouchedTests(ctx, repoDir, base, pr.Merge(), testFiles)
	if err != nil {
		return cand, err
	}
	cand.Packages = packages(testFiles)

	dir, err := os.MkdirTemp(scratch, "candidate-")
	if err != nil {
		return cand, err
	}
	defer os.RemoveAll(dir)

	// Rule 6 is checked first: a package whose tests already fail at the
	// merge cannot be graded, whatever the touched tests do.
	mergeSrc, err := plainTree(ctx, repoDir, pr.Merge(), filepath.Join(dir, "merge"))
	if err != nil {
		return cand, err
	}
	merged, err := runGoTest(ctx, mergeSrc, env, cand.Packages...)
	if err != nil {
		return cand, err
	}
	if !merged.passed {
		cand.Class, cand.Reason = ClassDropped, mergeReason(merged)
		return cand, nil
	}
	if len(cand.Tests) == 0 {
		cand.Class, cand.Reason = ClassDropped, "no touched tests"
		return cand, nil
	}

	parentSrc, err := plainTree(ctx, repoDir, base, filepath.Join(dir, "parent"))
	if err != nil {
		return cand, err
	}
	if err := overlayTests(ctx, repoDir, pr.Merge(), parentSrc, pr.Files); err != nil {
		return cand, err
	}
	parent, err := runGoTest(ctx, parentSrc, env, append([]string{"-run", pattern(cand.Tests)}, cand.Packages...)...)
	if err != nil {
		return cand, err
	}
	if parent.compile {
		cand.Names = MissingNames(parent.output)
		if len(cand.Names) > 0 && len(cand.Names) <= MaxInterfaceNames {
			cand.Class = ClassInterface
			return cand, nil
		}
		cand.Class = ClassDropped
		if len(cand.Names) == 0 {
			cand.Reason = "compile fails without a parsed name"
		} else {
			cand.Reason = fmt.Sprintf("compile fails on %d names", len(cand.Names))
		}
		return cand, nil
	}
	if !parent.passed {
		cand.Class = ClassPlain
		return cand, nil
	}
	cand.Class, cand.Reason = ClassDropped, "touched tests pass on parent"
	return cand, nil
}

// TouchedTests returns the sorted Test functions that the diff adds or whose
// bodies change in the given test files.
func TouchedTests(ctx context.Context, repoDir, base, merge string, files []string) ([]string, error) {
	var names []string
	for _, file := range files {
		oldSrc, _, err := fileAt(ctx, repoDir, base, file)
		if err != nil {
			return nil, err
		}
		newSrc, _, err := fileAt(ctx, repoDir, merge, file)
		if err != nil {
			return nil, err
		}
		touched, err := TouchedTestsIn(oldSrc, newSrc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		names = append(names, touched...)
	}
	sort.Strings(names)
	return names, nil
}

// TouchedTestsIn compares the Test functions of two versions of one test file
// and returns the names of the functions that are added or whose bodies
// change.
func TouchedTestsIn(oldSrc, newSrc []byte) ([]string, error) {
	oldFuncs, err := testFunctions(oldSrc)
	if err != nil {
		return nil, err
	}
	newFuncs, err := testFunctions(newSrc)
	if err != nil {
		return nil, err
	}
	var touched []string
	for name, body := range newFuncs {
		if old, ok := oldFuncs[name]; !ok || old != body {
			touched = append(touched, name)
		}
	}
	sort.Strings(touched)
	return touched, nil
}

// testFunctions maps each top-level Test function of a test file to its
// printed declaration. An empty source is a file that does not exist in that
// version.
func testFunctions(src []byte) (map[string]string, error) {
	funcs := map[string]string{}
	if len(bytes.TrimSpace(src)) == 0 {
		return funcs, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		return nil, err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, fn); err != nil {
			return nil, err
		}
		funcs[fn.Name.Name] = buf.String()
	}
	return funcs, nil
}

var missingPatterns = []*regexp.Regexp{
	regexp.MustCompile(`undefined: ([\w.]+)`),
	regexp.MustCompile(`has no field or method (\w+)`),
	regexp.MustCompile(`(?:too many|not enough) arguments in call to ([\w.]+)`),
}

// MissingNames returns the sorted, unique names the compiler reported as
// undefined, absent from a type, or called with the wrong argument count.
func MissingNames(output string) []string {
	seen := map[string]bool{}
	for _, re := range missingPatterns {
		for _, match := range re.FindAllStringSubmatch(output, -1) {
			name := match[1]
			if i := strings.LastIndex(name, "."); i >= 0 {
				name = name[i+1:]
			}
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// plainTree writes a commit's tree into dir the way design section 1.4 step 2
// extracts it: the files alone, with no repository metadata.
func plainTree(ctx context.Context, repoDir, rev, dir string) (string, error) {
	if _, err := checkout.Prepare(ctx, repoDir, rev, dir); err != nil {
		return "", err
	}
	src := filepath.Join(dir, "src")
	return src, os.RemoveAll(filepath.Join(src, ".git"))
}

// packages returns the sorted package paths of the given files.
func packages(files []string) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, file := range files {
		pkg := "./" + path.Dir(file)
		if path.Dir(file) == "." {
			pkg = "."
		}
		if !seen[pkg] {
			seen[pkg] = true
			pkgs = append(pkgs, pkg)
		}
	}
	sort.Strings(pkgs)
	return pkgs
}

// pattern selects the named tests the way the grade does.
func pattern(tests []string) string {
	return "^(" + strings.Join(tests, "|") + ")$"
}

// mergeReason is why the merge commit cannot be graded.
func mergeReason(run testRun) string {
	switch {
	case run.compile:
		return "merge does not compile"
	case len(run.failed) > 0:
		return "merge fails: " + strings.Join(run.failed, ", ")
	default:
		return "merge fails"
	}
}

// testRun is what one go test run says about its packages.
type testRun struct {
	passed  bool
	compile bool
	failed  []string
	output  string
}

// runGoTest runs go test on the given packages and parses its output. A
// command that never ran, as when go itself is missing, is an error; a
// failing test or build is not.
func runGoTest(ctx context.Context, dir string, env []string, args ...string) (testRun, error) {
	cmd := exec.CommandContext(ctx, "go", append([]string{"test", "-count=1", "-timeout", testTimeout}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	run := testRun{output: string(out), passed: err == nil}
	run.compile = strings.Contains(run.output, "[build failed]") || strings.Contains(run.output, "setup failed")
	run.failed = failedTests(run.output)
	if err == nil {
		return run, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return run, err
	}
	return run, nil
}

var failLine = regexp.MustCompile(`--- FAIL: (\w+)`)

// failedTests returns the sorted top-level names of the tests that failed.
func failedTests(output string) []string {
	seen := map[string]bool{}
	for _, match := range failLine.FindAllStringSubmatch(output, -1) {
		seen[match[1]] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// overlayTests writes the merge versions of the changed test files over the
// parent tree, deleting the test files the pull request deleted.
func overlayTests(ctx context.Context, repoDir, merge, srcDir string, files []File) error {
	for _, file := range files {
		if !strings.HasSuffix(file.Path, "_test.go") {
			continue
		}
		target := filepath.Join(srcDir, filepath.FromSlash(file.Path))
		if strings.HasPrefix(file.ChangeType, "DELET") {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		data, ok, err := fileAt(ctx, repoDir, merge, file.Path)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// fileAt returns the contents of a file at a commit, or false when the file
// does not exist there.
func fileAt(ctx context.Context, repoDir, rev, file string) ([]byte, bool, error) {
	out, err := gitx.Bytes(ctx, repoDir, "cat-file", "-p", rev+":"+file)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return out, true, nil
}

// run runs one command and returns its standard output.
func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
