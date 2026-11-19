package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/stretchr/testify/require"
)

// TestBaselineWritesCounts writes a baseline from hand-written result lines:
// every variant of the route is pooled, the final attempt of a key is the
// one counted, invalid and contaminated runs are left out, and only counts
// reach the file.
func TestBaselineWritesCounts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	writeBatch(t, dir, "2026-11-20-aa", []string{
		// pr-0001: one pass, one stopped short, and one pass from the
		// other variant.
		`{"schema":1,"batch":"2026-11-20-aa","task":"pr-0001","variant":"main@abc12345","route":"deepseek-flash","rep":1,"exit":0,"grade":{"touched":true,"packages":true}}`,
		`{"schema":1,"batch":"2026-11-20-aa","task":"pr-0001","variant":"main@abc12345","route":"deepseek-flash","rep":2,"exit":2,"grade":{"touched":false,"packages":false}}`,
		`{"schema":1,"batch":"2026-11-20-aa","task":"pr-0001","variant":"w8@def67890","route":"deepseek-flash","rep":1,"exit":0,"grade":{"touched":true,"packages":true}}`,
		// pr-0002: a retried key, whose final attempt is the one counted.
		`{"schema":1,"batch":"2026-11-20-aa","task":"pr-0002","variant":"main@abc12345","route":"deepseek-flash","rep":1,"attempt":1,"exit":1,"grade":{}}`,
		`{"schema":1,"batch":"2026-11-20-aa","task":"pr-0002","variant":"main@abc12345","route":"deepseek-flash","rep":1,"attempt":2,"exit":0,"grade":{"touched":true,"packages":true}}`,
		`{"schema":1,"batch":"2026-11-20-aa","task":"pr-0002","variant":"w8@def67890","route":"deepseek-flash","rep":1,"exit":0,"grade":{"touched":true,"packages":true}}`,
		// pr-0003 was contaminated and has no valid run left.
		`{"schema":1,"batch":"2026-11-20-aa","task":"pr-0003","variant":"main@abc12345","route":"deepseek-flash","rep":1,"exit":0,"contaminated":true,"grade":{"touched":true,"packages":true}}`,
		// pr-0004 ran on another route.
		`{"schema":1,"batch":"2026-11-20-aa","task":"pr-0004","variant":"main@abc12345","route":"glm-5.3","rep":1,"exit":0,"grade":{"touched":true,"packages":true}}`,
	})

	out := filepath.Join(dir, "baseline.json")
	var stdout, stderr bytes.Buffer
	code := baselineCmd([]string{"2026-11-20-aa", "--route", "deepseek-flash", "--out", out}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	base, err := score.LoadBaseline(out)
	require.NoError(t, err)
	require.Equal(t, &score.Baseline{
		Schema: 1,
		Date:   time.Now().Format(time.DateOnly),
		Route:  "deepseek-flash",
		Tasks: []score.TaskPassCount{
			{Task: "pr-0001", Passes: 2, Runs: 3},
			{Task: "pr-0002", Passes: 2, Runs: 2},
		},
	}, base)

	var printed struct {
		Batch  string `json:"batch"`
		Route  string `json:"route"`
		Out    string `json:"out"`
		Tasks  int    `json:"tasks"`
		Passes int    `json:"passes"`
		Runs   int    `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &printed))
	require.Equal(t, "2026-11-20-aa", printed.Batch)
	require.Equal(t, "deepseek-flash", printed.Route)
	require.Equal(t, out, printed.Out)
	require.Equal(t, 2, printed.Tasks)
	require.Equal(t, 4, printed.Passes)
	require.Equal(t, 5, printed.Runs)
	require.NotContains(t, stdout.String(), "pr-0001", "the output holds counts, not task text")
}

// TestBaselineNeedsOneRoute checks that a batch with two routes must state
// one, and that the default file is evals/baselines/<date>-<route>.json.
func TestBaselineNeedsOneRoute(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	writeBatch(t, dir, "two", []string{
		`{"schema":1,"batch":"two","task":"pr-0001","variant":"main@abc12345","route":"deepseek-flash","rep":1,"exit":0,"grade":{"touched":true,"packages":true}}`,
		`{"schema":1,"batch":"two","task":"pr-0002","variant":"main@abc12345","route":"glm-5.3","rep":1,"exit":0,"grade":{"touched":true,"packages":true}}`,
	})

	var stdout, stderr bytes.Buffer
	code := baselineCmd([]string{"two"}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "deepseek-flash, glm-5.3", "the error names the routes")
	require.Contains(t, stderr.String(), "--route")

	stdout.Reset()
	stderr.Reset()
	code = baselineCmd([]string{"two", "--route", "glm-5.3"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	path := filepath.Join("evals", "baselines", time.Now().Format(time.DateOnly)+"-glm-5.3.json")
	base, err := score.LoadBaseline(path)
	require.NoError(t, err)
	require.Equal(t, []score.TaskPassCount{{Task: "pr-0002", Passes: 1, Runs: 1}}, base.Tasks)
}

// writeBatch writes a batch record and its hand-written result lines under
// the eval state directory and returns the batch directory.
func writeBatch(t *testing.T, dir, id string, lines []string) string {
	t.Helper()
	batchDir := filepath.Join(dir, "state", "crush-eval", "batches", id)
	writeTestFile(t, filepath.Join(batchDir, "batch.json"),
		fmt.Sprintf(`{"schema":1,"id":%q,"seed":1,"suite":"tree1","toolchain":"go1.27.2",`+
			`"experiment":"evals/experiments/2026-11-20-aa.yaml"}`, id))
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	writeTestFile(t, filepath.Join(batchDir, "results.jsonl"), body)
	return batchDir
}
