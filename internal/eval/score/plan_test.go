package score_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/stretchr/testify/require"
)

func baseline() *score.Baseline {
	return &score.Baseline{
		Schema: 1,
		Date:   "2026-11-20",
		Route:  "deepseek-flash",
		Tasks: []score.TaskPassCount{
			{Task: "pr-0001", Passes: 3, Runs: 3},
			{Task: "pr-0002", Passes: 0, Runs: 3},
			{Task: "pr-0003", Passes: 1, Runs: 2},
			{Task: "pr-0004", Passes: 1, Runs: 2},
			{Task: "pr-0005", Passes: 2, Runs: 3},
		},
	}
}

// TestNewBaseline counts one route over every variant: invalid and
// contaminated runs are left out, a task with no valid run is left out, and
// the tasks come sorted.
func TestNewBaseline(t *testing.T) {
	t.Parallel()

	last := run("main@abc12345", "pr-0006", result.KindPlain, 0, true)
	pass := run("main@abc12345", "pr-0001", result.KindPlain, 0, true)
	stopped := run("main@abc12345", "pr-0002", result.KindPlain, 2, false)
	otherVariant := run("w8@abc12345", "pr-0002", result.KindPlain, 0, true)
	invalid := run("w8@abc12345", "pr-0003", result.KindPlain, 1, true)
	contaminated := run("main@abc12345", "pr-0004", result.KindPlain, 0, true)
	contaminated.Contaminated = true
	otherRoute := run("main@abc12345", "pr-0005", result.KindPlain, 0, true)
	otherRoute.Route = "glm-5.3"

	got, err := score.NewBaseline("2026-11-20", "deepseek-flash",
		[]result.Result{last, pass, stopped, otherVariant, invalid, contaminated, otherRoute})
	require.NoError(t, err)
	require.Equal(t, &score.Baseline{
		Schema: 1,
		Date:   "2026-11-20",
		Route:  "deepseek-flash",
		Tasks: []score.TaskPassCount{
			{Task: "pr-0001", Passes: 1, Runs: 1},
			{Task: "pr-0002", Passes: 1, Runs: 2},
			{Task: "pr-0006", Passes: 1, Runs: 1},
		},
	}, got)

	_, err = score.NewBaseline("2026-11-20", "glm-5.3", []result.Result{invalid})
	require.ErrorContains(t, err, "no valid runs")
}

func TestDetectableEffect(t *testing.T) {
	t.Parallel()

	// p_t is passes over runs: 1, 0, 0.5, 0.5, 2/3.
	// Each task adds 2*p*(1-p)/R with R=3, so:
	//   1    -> 0
	//   0    -> 0
	//   0.5  -> 2*0.5*0.5/3 = 0.1666666667
	//   0.5  -> 0.1666666667
	//   2/3  -> 2*(2/3)*(1/3)/3 = 0.1481481481
	// sum = 0.4814814815, sqrt = 0.6938898499, T=5
	// SE = 0.6938898499/5 = 0.1387777333
	// effect = 2.8*SE = 0.3885776532
	b := baseline()
	se, effect, err := b.DetectableEffect(3)
	require.NoError(t, err)
	require.InDelta(t, 0.1387777333, se, 1e-9)
	require.InDelta(t, 0.3885776532, effect, 1e-9)
}

func TestDetectableEffectAlwaysPassOrFail(t *testing.T) {
	t.Parallel()

	b := &score.Baseline{Tasks: []score.TaskPassCount{
		{Task: "t1", Passes: 3, Runs: 3},
		{Task: "t2", Passes: 0, Runs: 3},
	}}
	se, effect, err := b.DetectableEffect(2)
	require.NoError(t, err)
	require.Zero(t, se)
	require.Zero(t, effect)
}

func TestDetectableEffectRejectsBadInput(t *testing.T) {
	t.Parallel()

	_, _, err := baseline().DetectableEffect(0)
	require.ErrorContains(t, err, "repetitions")

	_, _, err = (&score.Baseline{}).DetectableEffect(1)
	require.ErrorContains(t, err, "tasks")
}

func TestBaselineRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "2026-11-20-deepseek-flash.json")

	want := baseline()
	require.NoError(t, want.WriteFile(path))
	got, err := score.LoadBaseline(path)
	require.NoError(t, err)
	require.Equal(t, want, got)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Equal(t, sortedKeys("date", "route", "schema", "tasks"), mapKeys(t, raw))
	task := raw["tasks"].([]any)[0].(map[string]any)
	require.Equal(t, sortedKeys("passes", "runs", "task"), mapKeys(t, task))
}

func TestLoadBaselineRejectsBadCounts(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "b.json")
	bad := baseline()
	bad.Tasks[0].Passes = 4
	require.NoError(t, bad.WriteFile(path))
	_, err := score.LoadBaseline(path)
	require.ErrorContains(t, err, "passes")

	require.NoError(t, os.WriteFile(path, []byte(`{"schema":2}`), 0o600))
	_, err = score.LoadBaseline(path)
	require.ErrorContains(t, err, "schema")
}

const experimentYAML = `date: 2026-12-01
name: w8-delegation
variants:
  - name: main
    ref: main@3f18401c
  - name: w8
    ref: w8
    overlay:
      reasoning_effort: high
routes: [deepseek-flash, copilot]
repetitions: 3
primary_metric: pass_rate
margin: 0.5
baseline: evals/baselines/2026-11-20-deepseek-flash.json
detectable_effect: 0
`

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadExperiment(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "2026-12-01-w8-delegation.yaml")
	require.NoError(t, os.WriteFile(path, []byte(experimentYAML), 0o600))

	exp, err := score.LoadExperiment(path)
	require.NoError(t, err)
	require.Equal(t, "w8-delegation", exp.Name)
	require.Equal(t, []score.Variant{
		{Name: "main", Ref: "main@3f18401c"},
		{Name: "w8", Ref: "w8", Overlay: map[string]any{"reasoning_effort": "high"}},
	}, exp.Variants)
	require.Equal(t, []string{"deepseek-flash", "copilot"}, exp.Routes)
	require.Equal(t, 3, exp.Repetitions)
	require.Equal(t, score.MetricPassRate, exp.PrimaryMetric)
	require.Equal(t, 0.5, exp.Margin)
	require.Equal(t, "evals/baselines/2026-11-20-deepseek-flash.json", exp.Baseline)
}

func TestExperimentValidation(t *testing.T) {
	t.Parallel()

	for name, change := range map[string][2]string{
		"one variant":     {"  - name: w8\n    ref: w8\n    overlay:\n      reasoning_effort: high\n", ""},
		"bad metric":      {"primary_metric: pass_rate", "primary_metric: latency"},
		"negative margin": {"margin: 0.5", "margin: -0.1"},
		"missing ref":     {"    ref: w8", "    ref: \"\""},
		"bad date":        {"date: 2026-12-01", "date: 12/01/2026"},
		"no repetitions":  {"repetitions: 3", "repetitions: 0"},
		"no baseline":     {"baseline: evals/baselines/2026-11-20-deepseek-flash.json", "baseline: \"\""},
		"unknown field":   {"margin: 0.5", "margin: 0.5\nseed: 4"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := experimentYAML
			require.Contains(t, body, change[0])
			body = replaceOnce(body, change[0], change[1])
			_, err := score.LoadExperiment(writeFile(t, "exp.yaml", body))
			require.Error(t, err)
		})
	}
}

func TestDuplicateVariantName(t *testing.T) {
	t.Parallel()

	body := experimentYAML
	body = replaceOnce(body, "  - name: w8\n    ref: w8", "  - name: main\n    ref: w8")
	_, err := score.LoadExperiment(writeFile(t, "exp.yaml", body))
	require.ErrorContains(t, err, "duplicate variant")
}

func TestFillDetectableEffect(t *testing.T) {
	t.Parallel()

	exp, err := score.LoadExperiment(writeFile(t, "exp.yaml", experimentYAML))
	require.NoError(t, err)
	require.NoError(t, exp.FillDetectableEffect(baseline()))
	require.InDelta(t, 0.3885776532, exp.DetectableEffect, 1e-9)

	exp.Margin = 0.1
	err = exp.FillDetectableEffect(baseline())
	require.ErrorContains(t, err, "below detectable effect")
}

func replaceOnce(s, old, new string) string {
	return strings.Replace(s, old, new, 1)
}

func mapKeys(t *testing.T, obj map[string]any) []string {
	t.Helper()
	out := make([]string, 0, len(obj))
	for k := range obj {
		out = append(out, k)
	}
	return sortedKeys(out...)
}

func sortedKeys(keys ...string) []string {
	sort.Strings(keys)
	return keys
}
