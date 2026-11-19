package run

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	dir, err := os.MkdirTemp("", "crush-eval-run-")
	if err != nil {
		panic(err)
	}
	fakeBin = filepath.Join(dir, "crush")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", fakeBin, "./testdata/fake")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(dir)
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeVars are the variables the fake binary reads. The route names them, so
// the scrubbed child environment carries them through.
var fakeVars = []string{
	"FAKE_ARGV", "FAKE_PROMPT", "FAKE_ENV", "FAKE_OUTPUT", "FAKE_EXIT", "FAKE_SLEEP_MS",
	"FAKE_WRITE", "FAKE_CONTENT", "FAKE_REMOVE", "FAKE_DB_SQL", "FAKE_DB_PART", "FAKE_PID",
	"FAKE_CHILD_OUT",
}

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
	// messagesSchema is the messages table of the first migration.
	messagesSchema = `CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    parts TEXT NOT NULL default '[]',
    model TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    finished_at INTEGER,
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE
);`
	// twoContaminatedCalls is one message with three tool calls, two of them
	// naming a marker: design 3.7 counts tool calls.
	twoContaminatedCalls = `[{"type":"tool_call","data":{"id":"t1","name":"bash","input":"{\"command\":\"gh pr view 51\"}"}},{"type":"tool_call","data":{"id":"t2","name":"bash","input":"{\"command\":\"gh api repos/x\"}"}},{"type":"tool_call","data":{"id":"t3","name":"bash","input":"{\"command\":\"ls\"}"}}]`
	contaminatedPart     = `[{"type":"tool_call","data":{"id":"t1","name":"bash","input":"{\"command\":\"gh pr view 51\"}","provider_executed":false,"finished":true}}]`
)

// fixture is a throwaway repository with one buggy package and the hidden
// test the task grades with.
type fixture struct {
	repo  string
	base  string
	blob  string
	home  *home.Home
	route *route.Route
	task  *task.Task
	env   map[string]string
	grace time.Duration
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	write(t, filepath.Join(repo, "go.mod"), "module example.com/task\n\ngo 1.21\n")
	write(t, filepath.Join(repo, "crush.json"), "{}\n")
	write(t, filepath.Join(repo, "pkg", "pkg.go"), buggySource)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "task base")
	base := git(t, repo, "rev-parse", "HEAD")
	blob := blobOf(t, repo, hiddenTest)

	f := &fixture{
		repo: repo,
		base: base,
		blob: blob,
		home: home.At(t.TempDir()),
		route: &route.Route{
			Name:       "fake",
			Family:     "deepseek",
			Provider:   "commandcode",
			Model:      "deepseek/deepseek-v4.1-flash",
			SmallModel: "deepseek/deepseek-v4.1-flash",
			Env:        fakeVars,
		},
		env:   map[string]string{},
		grace: 2 * time.Second,
	}
	for _, name := range fakeVars {
		f.env[name] = ""
	}
	scratch := t.TempDir()
	f.env["FAKE_ARGV"] = filepath.Join(scratch, "argv")
	f.env["FAKE_PROMPT"] = filepath.Join(scratch, "prompt")

	f.task = &task.Task{
		Schema: 1,
		ID:     "pr-0010",
		Source: task.Source{Base: base},
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
	return f
}

// options is the run of the fixture's task under its fake route.
func (f *fixture) options() Options {
	env := os.Environ()
	for name, value := range f.env {
		env = append(env, name+"="+value)
	}
	return Options{
		Task:          f.task,
		Variant:       "main@abc12345",
		BinPath:       fakeBin,
		Route:         f.route,
		Home:          f.home,
		Batch:         "b1",
		Suite:         "tree1",
		SuiteDir:      filepath.Join(f.repo, "evals"),
		Rep:           1,
		Attempt:       1,
		RepoDir:       f.repo,
		Footer:        "Make this change in the repository in the current directory. When you are done, the work should be in the working tree of this checkout.",
		ParentEnv:     env,
		WatchdogGrace: f.grace,
	}
}

func (f *fixture) run(t *testing.T) result.Result {
	t.Helper()
	line, err := Run(context.Background(), f.options())
	require.NoError(t, err)
	return line
}

// dir is the one run directory the fixture wrote.
func (f *fixture) dir(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(f.home.BatchDir("b1"), "runs", "*"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	return matches[0]
}

func TestRunCompletedPass(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")
	f.env["FAKE_WRITE"] = "pkg/pkg.go"
	f.env["FAKE_CONTENT"] = fixedSource

	line := f.run(t)

	require.True(t, line.Pass)
	require.False(t, line.FalseCompletion)
	require.Equal(t, 0, line.Exit)
	require.Equal(t, "completed", line.Reason)
	require.Equal(t, "deepseek/deepseek-v4.1-flash", line.Model)
	require.Equal(t, "commandcode", line.Provider)
	require.Equal(t, "abc1234", line.Build)
	require.Equal(t, 12, line.Steps)
	require.Equal(t, 20, line.ToolCalls)
	require.Equal(t, result.Tokens{Input: 100, CacheRead: 10, CacheWrite: 20, Output: 30}, line.Tokens)
	require.Equal(t, map[string]int{"completed": 2}, line.TurnEnds)
	require.InDelta(t, 0.25, line.Cost, 0.0001)
	require.Equal(t, int64(412000), line.DurationMS)
	require.Equal(t, 5, line.TextLength)
	require.True(t, line.Grade.Touched)
	require.True(t, line.Grade.Packages)
	require.False(t, line.Grade.CompileError)
	require.Equal(t, result.Diff{Files: 1, Added: 1, Deleted: 1}, line.Diff)
	require.Zero(t, line.Worktrees)
	require.False(t, line.Contaminated)
	require.NotEmpty(t, line.ProjectConfig)
	require.Equal(t, "tree1", line.Suite)
	require.Equal(t, "main@abc12345", line.Variant)
	require.Equal(t, "pr-0010", line.Task)
	require.Equal(t, task.KindPlain, line.Kind)
	require.Equal(t, "b1", line.Batch)
	require.Equal(t, "fake", line.Route)
	require.Equal(t, "deepseek", line.Family)

	dir := f.dir(t)
	require.NoFileExists(t, filepath.Join(dir, "src"))
	require.FileExists(t, filepath.Join(dir, "stdout.json"))
	require.FileExists(t, filepath.Join(dir, "stderr.log"))

	argv := strings.Split(strings.TrimSpace(read(t, f.env["FAKE_ARGV"])), "\n")
	require.Len(t, argv, 15)
	require.Equal(t, "--cwd", argv[0])
	require.Equal(t, filepath.Join(dir, "src"), argv[1])
	require.Equal(t, "--data-dir", argv[2])
	require.Equal(t, filepath.Join(dir, "data"), argv[3])
	require.Equal(t, []string{
		"run", "--json", "--quiet",
		"-m", "commandcode/deepseek/deepseek-v4.1-flash",
		"--small-model", "commandcode/deepseek/deepseek-v4.1-flash",
		"--max-steps", "120", "--timeout", "25m0s",
	}, argv[4:])

	require.Equal(t, "Fix the bug.\n\nMake this change in the repository in the current directory. When you are done, the work should be in the working tree of this checkout.",
		strings.TrimSuffix(read(t, f.env["FAKE_PROMPT"]), "\n"))

	lines, _, err := result.ReadFile(filepath.Join(f.home.BatchDir("b1"), "results.jsonl"))
	require.NoError(t, err)
	require.Equal(t, []result.Result{line}, lines)
}

func TestRunCompletedFail(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")

	line := f.run(t)

	require.False(t, line.Pass)
	require.True(t, line.FalseCompletion)
	require.Equal(t, 0, line.Exit)
	require.False(t, line.Grade.Touched)
	require.DirExists(t, filepath.Join(f.dir(t), "src"))
}

func TestRunStoppedShortWithOverrides(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.route.ReasoningEffort = "high"
	f.env["FAKE_OUTPUT"] = outputLine("step_limit", "")
	f.env["FAKE_EXIT"] = "2"
	opts := f.options()
	opts.MaxSteps = 40
	opts.Timeout = 10 * time.Minute

	line, err := Run(context.Background(), opts)
	require.NoError(t, err)

	require.Equal(t, 2, line.Exit)
	require.Equal(t, "step_limit", line.Reason)
	require.False(t, line.Pass)
	require.False(t, line.FalseCompletion)
	require.False(t, line.UsageLimited)

	argv := strings.Split(strings.TrimSpace(read(t, f.env["FAKE_ARGV"])), "\n")
	require.Equal(t, []string{
		"--cwd", argv[1], "--data-dir", argv[3],
		"run", "--json", "--quiet",
		"-m", "commandcode/deepseek/deepseek-v4.1-flash",
		"--small-model", "commandcode/deepseek/deepseek-v4.1-flash",
		"--reasoning-effort", "high",
		"--max-steps", "40", "--timeout", "10m0s",
	}, argv)
}

func TestRunUsageLimited(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = outputLine("error", "You've hit your 5-hour limit, try again later")
	f.env["FAKE_EXIT"] = "3"

	line := f.run(t)

	require.Equal(t, 3, line.Exit)
	require.Equal(t, "error", line.Reason)
	require.True(t, line.UsageLimited)
	require.False(t, line.Retryable())
	require.False(t, line.Pass)
	require.Zero(t, line.Grade, "a run that says nothing about the model is not graded")
}

func TestRunWatchdog(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.grace = 100 * time.Millisecond
	f.task.Limits.Timeout = task.Duration(100 * time.Millisecond)
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")
	f.env["FAKE_SLEEP_MS"] = "5000"

	start := time.Now()
	line := f.run(t)
	require.Less(t, time.Since(start), 4*time.Second)

	require.Equal(t, 3, line.Exit)
	require.Equal(t, "watchdog", line.Reason)
	require.False(t, line.Pass)
	require.False(t, line.FalseCompletion)
}

func TestRunBadOutput(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = "this is not json"

	line := f.run(t)

	require.Equal(t, 3, line.Exit)
	require.Equal(t, "bad_output", line.Reason)
	require.False(t, line.Pass)
	require.False(t, line.Contaminated)
}

func TestRunContamination(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")
	f.env["FAKE_WRITE"] = "pkg/pkg.go"
	f.env["FAKE_CONTENT"] = fixedSource
	f.env["FAKE_DB_SQL"] = messagesSchema
	f.env["FAKE_DB_PART"] = contaminatedPart

	line := f.run(t)

	require.True(t, line.Contaminated)
	require.Equal(t, 1, line.ContaminatedMatches)
	require.True(t, line.Pass)
}

// TestRunContaminationCountsToolCalls counts tool calls, not messages: one
// message that names a marker twice contributes two.
func TestRunContaminationCountsToolCalls(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")
	f.env["FAKE_DB_SQL"] = messagesSchema
	f.env["FAKE_DB_PART"] = twoContaminatedCalls

	line := f.run(t)

	require.Equal(t, 2, line.ContaminatedMatches, "two tool calls in one message count twice")
	require.True(t, line.Contaminated)
}

// TestRunWritesItsOwnConfigPerRun prepares two runs with different overlays
// at once: each child must read the global config its own run wrote.
func TestRunWritesItsOwnConfigPerRun(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")
	overlays := []map[string]any{{"agent_teams": true}, {"agent_teams": false}}
	variants := []string{"alpha@abc12345", "beta@abc12345"}

	var wg sync.WaitGroup
	lines := make([]result.Result, len(variants))
	errs := make([]error, len(variants))
	envFiles := make([]string, len(variants))
	for i, variant := range variants {
		envFiles[i] = filepath.Join(t.TempDir(), "env")
		opts := f.options()
		opts.Variant = variant
		opts.Overlay = overlays[i]
		opts.ParentEnv = append(opts.ParentEnv, "FAKE_ENV="+envFiles[i])
		wg.Add(1)
		go func() {
			defer wg.Done()
			lines[i], errs[i] = Run(context.Background(), opts)
		}()
	}
	wg.Wait()

	for i, variant := range variants {
		require.NoError(t, errs[i])
		require.Equal(t, variant, lines[i].Variant)

		configDir := childEnv(t, envFiles[i])["CRUSH_GLOBAL_CONFIG"]
		runDir := "pr-0010-" + strings.ReplaceAll(variant, "@", "-") + "-fake-r1"
		require.Equal(t, filepath.Join(f.home.BatchDir("b1"), "runs", runDir, "config", "crush"), configDir)

		var doc struct {
			Options map[string]any `json:"options"`
		}
		require.NoError(t, json.Unmarshal([]byte(read(t, filepath.Join(configDir, "crush.json"))), &doc))
		require.Equal(t, overlays[i]["agent_teams"], doc.Options["agent_teams"])
	}
}

// TestRunModelBrokeTheCheckout runs a model that deletes the checkout's
// .git. The run is a failed grade with a reason, not an error that stops the
// batch.
func TestRunModelBrokeTheCheckout(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")
	f.env["FAKE_REMOVE"] = ".git"

	line, err := Run(context.Background(), f.options())
	require.NoError(t, err, "a broken checkout does not stop the runner")
	require.False(t, line.Pass)
	require.False(t, line.Grade.Touched)
	require.False(t, line.Grade.Packages)
	require.Contains(t, line.Grade.Reason, "not a git repository")
}

// TestRunGivesTheChildItsOwnHome gives the child an empty HOME inside its
// run directory, so its tools cannot read the user's own configuration.
func TestRunGivesTheChildItsOwnHome(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.env["FAKE_OUTPUT"] = outputLine("completed", "")
	f.env["FAKE_WRITE"] = "pkg/pkg.go"
	f.env["FAKE_CONTENT"] = fixedSource
	envFile := filepath.Join(t.TempDir(), "env")
	opts := f.options()
	opts.ParentEnv = append(opts.ParentEnv, "FAKE_ENV="+envFile)

	line, err := Run(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, line.Pass)

	homeDir := filepath.Join(f.dir(t), "home")
	require.Equal(t, homeDir, childEnv(t, envFile)["HOME"], "the child's HOME is its run's home")
	info, err := os.Stat(homeDir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	entries, err := os.ReadDir(homeDir)
	require.NoError(t, err)
	require.Empty(t, entries, "the child's home starts empty")
}

// childEnv reads the environment a run's child saw from the file the fake
// binary dumped it into.
func childEnv(t *testing.T, path string) map[string]string {
	t.Helper()
	env := map[string]string{}
	for _, line := range strings.Split(read(t, path), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			env[key] = value
		}
	}
	return env
}

// outputLine is the object `crush run --json` prints, with the error on a
// run that failed.
func outputLine(reason, errText string) string {
	obj := map[string]any{
		"schema":      1,
		"reason":      reason,
		"text":        "done!",
		"duration_ms": 412000,
		"model":       "deepseek/deepseek-v4.1-flash",
		"provider":    "commandcode",
		"build":       "abc1234",
		"session":     map[string]any{"steps": 2},
		"tree": map[string]any{
			"sessions":            1,
			"steps":               12,
			"tool_calls":          20,
			"tokens":              map[string]int64{"input": 100, "cache_read": 10, "cache_write": 20, "output": 30},
			"turn_ends":           map[string]int{"completed": 2},
			"undelivered_reports": 0,
		},
		"estimated": false,
		"cost":      0.25,
	}
	if errText != "" {
		obj["error"] = errText
	}
	data, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// git runs one git command in dir with a fixed identity.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out := gitRun(t, dir, nil, args...)
	return out
}

// blobOf writes content as a blob in the repository and returns its hash.
func blobOf(t *testing.T, repo, content string) string {
	t.Helper()
	return gitRun(t, repo, strings.NewReader(content), "hash-object", "-w", "--stdin")
}

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

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
