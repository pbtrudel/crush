package batch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// fakeBin is the stand-in crush binary, built once for the package.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "crush-eval-batch-")
	if err != nil {
		panic(err)
	}
	fakeBin = filepath.Join(dir, "crush")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", fakeBin, "../run/testdata/fake")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const (
	e2eBuggy = "package pkg\n\nfunc Add(a, b int) int { return a - b }\n"
	e2eFixed = "package pkg\n\nfunc Add(a, b int) int { return a + b }\n"
	e2eTest  = `package pkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatalf("Add(2, 3) = %d", Add(2, 3))
	}
}
`
)

func TestRunEndToEnd(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	write(t, filepath.Join(repo, "go.mod"), "module example.com/task\n\ngo 1.21\n")
	write(t, filepath.Join(repo, "crush.json"), "{}\n")
	write(t, filepath.Join(repo, "pkg", "pkg.go"), e2eBuggy)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "task base")
	base := git(t, repo, "rev-parse", "HEAD")
	write(t, filepath.Join(repo, "pkg", "pkg.go"), e2eFixed)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "task fix")
	merge := git(t, repo, "rev-parse", "HEAD")
	blob := gitRun(t, repo, strings.NewReader(e2eTest), "hash-object", "-w", "--stdin")

	tk := &task.Task{
		Schema: 1,
		ID:     "pr-0010",
		Source: task.Source{Base: base, Merge: merge},
		Kind:   task.KindPlain,
		Prompt: "Fix the bug.",
		Grade: task.Grade{
			Packages: []string{"./pkg"},
			Tests:    []string{"TestAdd"},
			Hidden:   []task.Hidden{{Path: "pkg/pkg_test.go", Blob: blob}},
			Timeout:  task.Duration(2 * time.Minute),
		},
		Limits: task.Limits{MaxSteps: 120, Timeout: task.Duration(25 * time.Minute)},
	}
	rt := &route.Route{
		Name:        "r1",
		Family:      "deepseek",
		Provider:    "commandcode",
		Model:       "deepseek/deepseek-v4.1-flash",
		SmallModel:  "deepseek/deepseek-v4.1-flash",
		Env:         []string{"FAKE_OUTPUT", "FAKE_WRITE", "FAKE_CONTENT"},
		Concurrency: 2,
	}
	parent := append(os.Environ(),
		`FAKE_OUTPUT={"schema":1,"reason":"completed","text":"done","tree":{"steps":12,"tool_calls":20}}`,
		"FAKE_WRITE=pkg/pkg.go",
		"FAKE_CONTENT="+e2eFixed,
	)
	h := home.At(t.TempDir())
	exp := newExperiment(2)
	opts := Options{
		ID:         "e2e",
		Experiment: exp,
		Path:       "evals/experiments/2026-11-19-test.yaml",
		Seed:       3,
		Tasks:      []*task.Task{tk},
		Routes:     map[string]*route.Route{"r1": rt},
		RepoDir:    repo,
		Home:       h,
		Suite:      "tree1",
		SuiteDir:   filepath.Join(repo, "evals"),
		Toolchain:  "go1.27.2",
		Footer:     "Make this change in the working tree.",
		ParentEnv:  parent,
		Env:        goTestEnv(t),
		Build: func(context.Context, string) (string, error) {
			return fakeBin, nil
		},
		RetryPause: 10 * time.Millisecond,
	}

	sum, err := Run(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, 4, sum.Planned)
	require.Equal(t, 4, sum.Ran)
	require.Zero(t, sum.Invalid)

	lines, _, err := result.ReadFile(filepath.Join(h.BatchDir("e2e"), "results.jsonl"))
	require.NoError(t, err)
	require.Len(t, lines, 4)
	for _, line := range lines {
		require.True(t, line.Pass, "run %s rep %d did not pass", line.Variant, line.Rep)
		require.Equal(t, "tree1", line.Suite)
	}

	record, err := Load(h, "e2e")
	require.NoError(t, err)
	require.Equal(t, uint64(3), record.Seed)
	require.Equal(t, "tree1", record.Suite)
	require.Equal(t, "go1.27.2", record.Toolchain, "the record holds the toolchain the batch ran with")
	require.Equal(t, "evals/experiments/2026-11-19-test.yaml", record.Experiment)

	// A batch that is run again resumes every key.
	again, err := Run(context.Background(), opts)
	require.NoError(t, err)
	require.Equal(t, 4, again.Planned)
	require.Equal(t, 4, again.Resumed)
	require.Zero(t, again.Ran)
}

// goTestEnv is the environment a grade runs go with: an empty HOME and the
// caches of this machine.
func goTestEnv(t *testing.T) []string {
	t.Helper()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GOFLAGS=-buildvcs=false",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
	}
	for _, name := range []string{"GOCACHE", "GOMODCACHE"} {
		value := os.Getenv(name)
		if value == "" {
			out, err := exec.CommandContext(context.Background(), "go", "env", name).Output()
			require.NoError(t, err)
			value = strings.TrimSpace(string(out))
		}
		env = append(env, name+"="+value)
	}
	return env
}

// git runs one git command in dir with a fixed identity.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitRun(t, dir, nil, args...)
}

// gitRun runs one git command in dir and returns its trimmed output.
func gitRun(t *testing.T, dir string, stdin *strings.Reader, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// write writes a file in a test repository.
func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
