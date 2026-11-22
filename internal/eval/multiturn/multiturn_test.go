package multiturn

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// fakeBin is the stand-in crush binary, built once for the package.
var fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "crush-eval-multiturn-")
	if err != nil {
		panic(err)
	}
	fakeBin = filepath.Join(dir, "crush")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", fakeBin,
		"../run/testdata/fake")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeVars are the variables the fake binary reads.
var fakeVars = []string{"FAKE_ARGV", "FAKE_PROMPT", "FAKE_OUTPUT", "FAKE_EXIT", "FAKE_WRITE", "FAKE_CONTENT"}

const (
	buggySource = "package pkg\n\nfunc Add(a, b int) int { return a - b }\n"
	fixedSource = "package pkg\n\nfunc Add(a, b int) int { return a + b }\n"
	hiddenTest  = `package pkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatalf("Add(2, 3) = %d", Add(2, 3))
	}
}
`
)

func TestRunOneSendsEveryPromptOnOneSession(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	write(t, filepath.Join(repo, "go.mod"), "module example.com/task\n\ngo 1.21\n")
	write(t, filepath.Join(repo, "crush.json"), "{}\n")
	write(t, filepath.Join(repo, "pkg", "pkg.go"), buggySource)
	git(t, repo, "add", "-A")
	git(t, repo, "-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "-m", "task base")
	base := git(t, repo, "rev-parse", "HEAD")
	blob := blobOf(t, repo, hiddenTest)

	scratch := t.TempDir()
	argv := filepath.Join(scratch, "argv")
	h := home.At(t.TempDir())
	env := map[string]string{}
	for _, name := range fakeVars {
		env[name] = ""
	}
	env["FAKE_ARGV"] = argv
	env["FAKE_OUTPUT"] = outputLine("completed")
	env["FAKE_WRITE"] = "pkg/pkg.go"
	env["FAKE_CONTENT"] = fixedSource
	parent := os.Environ()
	for name, value := range env {
		parent = append(parent, name+"="+value)
	}
	opts := Options{
		Home: h,
		Route: &route.Route{
			Name:       "fake",
			Family:     "deepseek",
			Provider:   "commandcode",
			Model:      "deepseek/deepseek-v4.1-flash",
			SmallModel: "deepseek/deepseek-v4.1-flash",
			Env:        fakeVars,
		},
		BinPath:     fakeBin,
		Ref:         "abc12345",
		Batch:       "b1",
		RepoDir:     repo,
		Footer:      "Make this change in the repository.",
		Reps:        1,
		Arms:        []Arm{{Name: "background", Margin: 0.1}},
		TurnTimeout: 30 * time.Second,
		ParentEnv:   parent,
	}
	tk := &task.Task{
		Schema: 1,
		ID:     "pr-0010",
		Source: task.Source{Repo: "pbtrudel/crush", Base: base},
		Kind:   task.KindPlain,
		Prompt: "Fix the bug.",
		Grade: task.Grade{
			Packages: []string{"./pkg"},
			Tests:    []string{"TestAdd"},
			Hidden:   []task.Hidden{{Path: "pkg/pkg_test.go", Blob: blob}},
			Timeout:  task.Duration(2 * time.Minute),
		},
	}
	run, err := RunOne(context.Background(), opts, tk, Arm{Name: "background", Margin: 0.1}, 1)
	require.NoError(t, err)

	require.Len(t, run.Turns, len(FollowUps)+1)
	require.Equal(t, "s1", run.SessionID)
	require.Zero(t, run.Margin-0.1)
	require.Positive(t, run.Wall)
	for _, turn := range run.Turns {
		require.Equal(t, 0, turn.Exit)
		require.Equal(t, "completed", turn.Reason)
		require.Equal(t, 2, turn.Steps)
	}
	// Every turn after the first continues the session the first one named.
	args := strings.Split(strings.TrimSpace(read(t, argv)), "\n")
	require.Contains(t, args, "--session")
	require.Equal(t, "s1", args[len(args)-1])
	require.True(t, run.Pass)
	require.True(t, run.Grade.Touched)

	// The overlay both arms share, and the arm's own margin.
	cfg := read(t, filepath.Join(h.BatchDir("b1"), "runs", "pr-0010-background-r1", "config", "crush", "crush.json"))
	require.Contains(t, cfg, `"compact_background_margin": 0.1`)
	require.Contains(t, cfg, `"auto_compact_threshold": 0.3`)

	// One line per run, holding the per-turn record.
	lines, err := os.ReadFile(ResultPath(h, "b1"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(lines), "\n"))
}

// outputLine is the object the fake binary prints: one turn that completed.
func outputLine(reason string) string {
	obj := map[string]any{
		"schema":      1,
		"session_id":  "s1",
		"reason":      reason,
		"duration_ms": 412000,
		"session": map[string]any{
			"steps":      2,
			"tool_calls": 3,
			"tokens":     map[string]int64{"input": 10, "output": 20},
		},
	}
	data, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func blobOf(t *testing.T, dir, content string) string {
	t.Helper()
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
