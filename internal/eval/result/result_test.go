package result_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/stretchr/testify/require"
)

const specLine = `{"schema":1,"batch":"2026-12-01","suite":"tree1","task":"pr-0051","kind":"plain","variant":"main@3f18401c","route":"deepseek-flash","family":"deepseek","rep":2,"attempt":1,"started":"2026-12-01T10:00:00Z","exit":0,"reason":"completed","build":"3f18401c","model":"deepseek/deepseek-v4.1-flash","provider":"commandcode","steps":38,"tool_calls":71,"tokens":{"input":1000,"cache_read":2000,"cache_write":3000,"output":400},"estimated":false,"cost":0.25,"duration_ms":412000,"text_length":5,"undelivered_reports":0,"turn_ends":{"completed":2},"usage_limited":false,"grade":{"touched":true,"packages":true,"compile_error":false,"model_tests_removed":0},"pass":true,"false_completion":false,"diff":{"files":3,"added":41,"deleted":6},"worktrees":0,"contaminated":false,"contaminated_matches":0,"project_config":"blob1"}`

func keys(t *testing.T, v any) []string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	var obj map[string]any
	require.NoError(t, json.Unmarshal(data, &obj))
	out := make([]string, 0, len(obj))
	for k := range obj {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sorted(s ...string) []string {
	sort.Strings(s)
	return s
}

func TestLineShape(t *testing.T) {
	t.Parallel()

	var r result.Result
	require.NoError(t, json.Unmarshal([]byte(specLine), &r))
	require.Equal(t, 1, r.Schema)
	require.Equal(t, "pr-0051", r.Task)
	require.Equal(t, result.KindPlain, r.Kind)
	require.Equal(t, 2, r.Rep)
	require.Equal(t, 1, r.Attempt)
	require.Equal(t, time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC), r.Started)
	require.Equal(t, "completed", r.Reason)
	require.Equal(t, 38, r.Steps)
	require.Equal(t, 71, r.ToolCalls)
	require.Equal(t, result.Tokens{Input: 1000, CacheRead: 2000, CacheWrite: 3000, Output: 400}, r.Tokens)
	require.Equal(t, int64(6400), r.Tokens.Total())
	require.Equal(t, 0.25, r.Cost)
	require.Equal(t, int64(412000), r.DurationMS)
	require.Equal(t, 5, r.TextLength)
	require.Equal(t, map[string]int{"completed": 2}, r.TurnEnds)
	require.Equal(t, result.Grade{Touched: true, Packages: true}, r.Grade)
	require.True(t, r.Grade.Passed())
	require.True(t, r.Pass)
	require.Equal(t, result.Diff{Files: 3, Added: 41, Deleted: 6}, r.Diff)
	require.Equal(t, "blob1", r.ProjectConfig)

	require.Equal(t, sorted("attempt", "batch", "build", "contaminated", "contaminated_matches", "cost", "diff",
		"duration_ms", "estimated", "exit", "family", "false_completion", "grade", "kind",
		"model", "net_denied", "net_denied_hosts", "network", "pass", "project_config", "provider",
		"reason", "rep", "route", "schema", "started", "steps", "suite", "task", "text_length",
		"tool_calls", "tokens", "turn_ends", "undelivered_reports", "usage_limited", "variant",
		"worktrees"), keys(t, r))
	require.Equal(t, sorted("cache_read", "cache_write", "input", "output"), keys(t, r.Tokens))
	require.Equal(t, sorted("compile_error", "model_tests_removed", "packages", "touched"), keys(t, r.Grade))
	require.Equal(t, sorted("added", "deleted", "files"), keys(t, r.Diff))
}

func TestGradePassed(t *testing.T) {
	t.Parallel()

	require.True(t, result.Grade{Touched: true, Packages: true}.Passed())
	require.False(t, result.Grade{Touched: false, Packages: true}.Passed())
	require.False(t, result.Grade{Touched: true, Packages: false}.Passed())
	require.False(t, result.Grade{Touched: true, Packages: true, CompileError: true}.Passed())
}

func TestAppendAndReadFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "results.jsonl")
	first := result.Result{Schema: 1, Task: "pr-0051", Route: "r", Rep: 1, Started: time.Unix(1, 0).UTC()}
	second := result.Result{Schema: 1, Task: "pr-0051", Route: "r", Rep: 2, Started: time.Unix(2, 0).UTC()}
	require.NoError(t, result.Append(path, first))
	require.NoError(t, result.Append(path, second))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	lines, ignored, err := result.ReadFile(path)
	require.NoError(t, err)
	require.Zero(t, ignored)
	require.Equal(t, []result.Result{first, second}, lines)
}

func TestReadFileTruncatedLastLine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "results.jsonl")
	require.NoError(t, result.Append(path, result.Result{Schema: 1, Task: "a"}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(data, []byte(`{"schema":1,"task":"b"`)...), 0o600))

	lines, ignored, err := result.ReadFile(path)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	require.Equal(t, "a", lines[0].Task)
	require.Equal(t, 1, ignored)
}

func TestReadFileOnlyTruncatedLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "results.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"schema":`), 0o600))

	lines, ignored, err := result.ReadFile(path)
	require.NoError(t, err)
	require.Empty(t, lines)
	require.Equal(t, 1, ignored)
}

func TestReadFileRejectsBadMiddleLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "results.jsonl")
	body := `{"schema":1,"task":"a"}` + "\n" + "not json\n" + `{"schema":1,"task":"b"}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	_, _, err := result.ReadFile(path)
	require.ErrorContains(t, err, "line 2")
}

func TestFinality(t *testing.T) {
	t.Parallel()

	run := func(task string, exit int, reason string, usageLimited bool) result.Result {
		return result.Result{Batch: "b", Task: task, Variant: "v", Route: "r", Rep: 1, Exit: exit, Reason: reason, UsageLimited: usageLimited}
	}
	key := func(task string) result.Key {
		return result.Key{Batch: "b", Task: task, Variant: "v", Route: "r", Rep: 1}
	}

	t.Run("exit zero is final", func(t *testing.T) {
		t.Parallel()
		final := result.Final([]result.Result{run("a", 0, "completed", false)})
		require.Equal(t, 0, final[key("a")].Exit)
	})

	t.Run("exit two is final", func(t *testing.T) {
		t.Parallel()
		final := result.Final([]result.Result{run("a", 2, "max_steps", false)})
		require.Contains(t, final, key("a"))
	})

	t.Run("two retryable attempts are not final", func(t *testing.T) {
		t.Parallel()
		final := result.Final([]result.Result{run("a", 1, "error", false), run("a", 3, "bad_output", false)})
		require.Empty(t, final)
	})

	t.Run("three retryable attempts are final invalid", func(t *testing.T) {
		t.Parallel()
		final := result.Final([]result.Result{
			run("a", 3, "provider_error", false),
			run("a", -1, "watchdog", false),
			run("a", 1, "error", false),
		})
		require.Equal(t, 1, final[key("a")].Exit)
	})

	t.Run("usage limited attempts do not count", func(t *testing.T) {
		t.Parallel()
		lines := []result.Result{
			run("a", 3, "provider_error", true),
			run("a", 1, "error", false),
			run("a", 3, "provider_error", true),
			run("a", 1, "error", false),
		}
		require.Empty(t, result.Final(lines))
		lines = append(lines, run("a", 1, "error", false))
		final := result.Final(lines)
		require.Equal(t, 1, final[key("a")].Exit)
	})

	t.Run("a later success wins", func(t *testing.T) {
		t.Parallel()
		final := result.Final([]result.Result{
			run("a", 1, "error", false),
			run("a", 0, "completed", false),
		})
		require.Equal(t, 0, final[key("a")].Exit)
	})

	t.Run("keys are independent", func(t *testing.T) {
		t.Parallel()
		final := result.Final([]result.Result{
			run("a", 0, "completed", false),
			run("b", 1, "error", false),
		})
		require.Contains(t, final, key("a"))
		require.NotContains(t, final, key("b"))
	})
}

func TestRetryable(t *testing.T) {
	t.Parallel()

	require.True(t, result.Result{Exit: 1}.Retryable())
	require.True(t, result.Result{Exit: 3}.Retryable())
	require.True(t, result.Result{Exit: -1, Reason: "watchdog"}.Retryable())
	require.True(t, result.Result{Exit: 3, Reason: "bad_output"}.Retryable())
	require.False(t, result.Result{Exit: 0, Reason: "completed"}.Retryable())
	require.False(t, result.Result{Exit: 2, Reason: "max_steps"}.Retryable())
	require.False(t, result.Result{Exit: 3, Reason: "provider_error", UsageLimited: true}.Retryable())
}
