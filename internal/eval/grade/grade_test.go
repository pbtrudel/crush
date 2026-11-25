package grade_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/charmbracelet/crush/internal/eval/evaltest"
	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

const (
	goMod = "module example.com/task\n\ngo 1.27.0\n"
	// baseP is the code before the fix.
	baseP = "package p\n\nfunc Answer() int { return 1 }\n"
	// fixedP is the code after the fix.
	fixedP = "package p\n\nfunc Answer() int { return 2 }\n"
	// neighborTest passes before and after the fix.
	neighborTest = "package p\n\nimport \"testing\"\n\nfunc TestNeighbor(t *testing.T) {\n\tif Answer() <= 0 {\n\t\tt.Fatalf(\"Answer() = %d\", Answer())\n\t}\n}\n"
	// hiddenFix is the hidden test the fix must make pass.
	hiddenFix = "package p\n\nimport \"testing\"\n\nfunc TestFix(t *testing.T) {\n\tif Answer() != 2 {\n\t\tt.Fatalf(\"Answer() = %d, want 2\", Answer())\n\t}\n}\n"
	// flakyTest fails until it is excluded.
	flakyTest = "package p\n\nimport \"testing\"\n\nfunc TestFlaky(t *testing.T) { t.Fatal(\"flaky\") }\n"
)

func TestGrade(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		base     map[string]string
		hidden   string
		names    []string
		exclude  []string
		change   func(t *testing.T, src string)
		want     grade.Result
		wantFail []string
	}{
		{
			name:   "passes",
			hidden: hiddenFix,
			names:  []string{"TestFix"},
			change: fix,
			want:   grade.Result{Touched: true, Packages: true},
		},
		{
			name:     "named test fails",
			hidden:   hiddenFix,
			names:    []string{"TestFix"},
			want:     grade.Result{},
			wantFail: []string{"TestFix"},
		},
		{
			name:     "named test missing",
			hidden:   hiddenFix,
			names:    []string{"TestFix", "TestGone"},
			change:   fix,
			want:     grade.Result{Touched: false, Packages: true},
			wantFail: []string{"TestGone"},
		},
		{
			name:   "neighbor test fails",
			hidden: hiddenFix,
			names:  []string{"TestFix"},
			change: func(t *testing.T, src string) {
				fix(t, src)
				write(t, filepath.Join(src, "p", "neighbor_test.go"),
					"package p\n\nimport \"testing\"\n\nfunc TestNeighbor(t *testing.T) { t.Fatal(\"broken\") }\n")
			},
			want:     grade.Result{Touched: true, Packages: false},
			wantFail: []string{"TestNeighbor"},
		},
		{
			name:   "compile error",
			hidden: hiddenFix,
			names:  []string{"TestFix"},
			change: func(t *testing.T, src string) {
				write(t, filepath.Join(src, "p", "p.go"), "package p\n\nfunc Answer() int { return \"two\" }\n")
			},
			want: grade.Result{CompileError: true},
		},
		{
			name:   "model tests removed",
			hidden: hiddenFix,
			names:  []string{"TestFix"},
			change: func(t *testing.T, src string) {
				fix(t, src)
				write(t, filepath.Join(src, "p", "extra_test.go"),
					"package p\n\nimport \"testing\"\n\nfunc TestFix(t *testing.T) { t.Fatal(\"model copy\") }\n")
				write(t, filepath.Join(src, "p", "committed_test.go"),
					"package p\n\nimport \"testing\"\n\nfunc TestCommitted(t *testing.T) {}\n")
				git(t, src, "add", "-A")
				git(t, src, "commit", "-q", "-m", "model work")
			},
			want: grade.Result{Touched: true, Packages: true, ModelTestsRemoved: 2},
		},
		{
			name:    "excluded test skipped",
			base:    map[string]string{"p/flaky_test.go": flakyTest},
			hidden:  hiddenFix,
			names:   []string{"TestFix"},
			exclude: []string{"TestFlaky"},
			change:  fix,
			want:    grade.Result{Touched: true, Packages: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo, src, blob := prepare(t, tt.base, tt.hidden)
			base := git(t, src, "rev-parse", "HEAD")
			if tt.change != nil {
				tt.change(t, src)
			}

			got, err := grade.Grade(context.Background(), newTask(blob, tt.names, tt.exclude), repo, src, base, goEnv(t), false)
			require.NoError(t, err)
			require.Equal(t, tt.want.Touched, got.Touched, "touched")
			require.Equal(t, tt.want.Packages, got.Packages, "packages")
			require.Equal(t, tt.want.CompileError, got.CompileError, "compile_error")
			require.Equal(t, tt.want.ModelTestsRemoved, got.ModelTestsRemoved, "model_tests_removed")
			require.Equal(t, tt.wantFail, got.Failed, "failed")
			require.Equal(t, tt.want.Touched && tt.want.Packages, got.Pass(), "pass")
			require.Greater(t, got.TouchedMS, int64(0), "touched_ms")
			require.Greater(t, got.PackagesMS, int64(0), "packages_ms")
		})
	}
}

// TestGradeIgnoresRepositoryHooks grades a checkout whose .git/config makes
// git run a fsmonitor script. The script records that it ran and the
// environment it saw: grading must not run it, because a model can write
// that config and the runner's environment holds API keys.
func TestGradeIgnoresRepositoryHooks(t *testing.T) {
	// The secret must be in the parent environment, so this test cannot run
	// in parallel with the others of the package.
	t.Setenv("CRUSH_EVAL_SECRET", "sk-eval-must-not-leak")

	repo, src, blob := prepare(t, nil, hiddenFix)
	fix(t, src)
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	script := filepath.Join(dir, "fsmonitor.sh")
	evaltest.WriteExecutable(t, script, []byte("#!/bin/sh\nenv > "+marker+"\n"))
	git(t, src, "config", "core.fsmonitor", script)

	// The control: git on its own runs the script, and the script sees the
	// runner's environment.
	git(t, src, "status", "--porcelain")
	require.FileExists(t, marker, "the control did not run the fsmonitor")
	require.Contains(t, readFile(t, marker), "CRUSH_EVAL_SECRET=sk-eval-must-not-leak")
	require.NoError(t, os.Remove(marker))

	base := git(t, src, "rev-parse", "HEAD")
	got, err := grade.Grade(context.Background(), newTask(blob, []string{"TestFix"}, nil), repo, src, base, goEnv(t), false)
	require.NoError(t, err)
	require.True(t, got.Pass())
	require.NoFileExists(t, marker, "grading ran the checkout's fsmonitor")
}

// TestGradeModelDeletedGit grades a checkout whose .git the model deleted.
// The grade fails with a reason instead of returning an error.
func TestGradeModelDeletedGit(t *testing.T) {
	t.Parallel()

	repo, src, blob := prepare(t, nil, hiddenFix)
	base := git(t, src, "rev-parse", "HEAD")
	fix(t, src)
	require.NoError(t, os.RemoveAll(filepath.Join(src, ".git")))

	got, err := grade.Grade(context.Background(), newTask(blob, []string{"TestFix"}, nil), repo, src, base, goEnv(t), false)
	require.NoError(t, err, "a model that deletes .git is a failed grade, not a runner error")
	require.False(t, got.Pass())
	require.Contains(t, got.Reason, "not a git repository")
}

// prepare builds a throwaway repository with the base module, stores the
// hidden test as a blob in it and writes a checkout of the base into a new
// directory. It returns the repository, the checkout's src directory and the
// blob hash.
func prepare(t *testing.T, extra map[string]string, hidden string) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	files := map[string]string{
		"go.mod":             goMod,
		"p/p.go":             baseP,
		"p/neighbor_test.go": neighborTest,
	}
	for path, content := range extra {
		files[path] = content
	}
	for path, content := range files {
		write(t, filepath.Join(repo, filepath.FromSlash(path)), content)
	}
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "base")
	blob := gitStdin(t, repo, hidden, "hash-object", "-w", "--stdin")

	dest := t.TempDir()
	_, err := checkout.Prepare(context.Background(), repo, "HEAD", dest)
	require.NoError(t, err)
	return repo, filepath.Join(dest, "src"), blob
}

// newTask is the task whose grade names the tests and the hidden blob.
func newTask(blob string, names, exclude []string) *task.Task {
	return &task.Task{
		ID: "pr-0001",
		Grade: task.Grade{
			Packages: []string{"./p"},
			Tests:    names,
			Exclude:  exclude,
			Hidden:   []task.Hidden{{Path: "p/fix_test.go", Blob: blob}},
			Timeout:  task.Duration(time.Minute),
		},
	}
}

// fix is the change the task asks for.
func fix(t *testing.T, src string) {
	t.Helper()
	write(t, filepath.Join(src, "p", "p.go"), fixedP)
}

// goEnv is the minimal environment grading runs go with.
func goEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"GOCACHE=" + goEnvValue(t, "GOCACHE"),
		"GOMODCACHE=" + goEnvValue(t, "GOMODCACHE"),
		"GOFLAGS=-buildvcs=false",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
	}
}

// goEnvValue reads one go environment value that the minimal environment
// would otherwise lose.
func goEnvValue(t *testing.T, name string) string {
	t.Helper()
	if value := os.Getenv(name); value != "" {
		return value
	}
	cmd := exec.CommandContext(context.Background(), "go", "env", name)
	out, err := cmd.Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// readFile reads a file of the test.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// write writes one file, creating its directory.
func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// git runs one git command with the user's config kept out of it.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitStdin(t, dir, "", args...)
}

// gitStdin runs one git command with stdin and returns its trimmed output.
func gitStdin(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	cmd.Stdin = bytes.NewReader([]byte(stdin))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}
