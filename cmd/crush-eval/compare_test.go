package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/stretchr/testify/require"
)

func TestVariantLabels(t *testing.T) {
	t.Parallel()

	exp := &score.Experiment{Variants: []score.Variant{
		{Name: "control"}, {Name: "variant"}, {Name: "control2"},
	}}
	runs := []result.Result{
		{Variant: "control@aaaa", Task: "t1"},
		{Variant: "variant@bbbb", Task: "t1"},
		{Variant: "control2@aaaa", Task: "t1"},
	}
	labels, err := variantLabels(exp, runs)
	require.NoError(t, err)
	require.Equal(t, "control@aaaa", labels["control"])
	require.Equal(t, "variant@bbbb", labels["variant"])
	require.Equal(t, "control2@aaaa", labels["control2"], "a name that prefixes another stays apart")

	_, err = variantLabels(exp, runs[:2])
	require.ErrorContains(t, err, `"control2"`)

	mixed := append(runs, result.Result{Variant: "control@cccc", Task: "t1"})
	_, err = variantLabels(exp, mixed)
	require.ErrorContains(t, err, "both")
}

func TestWriteDetectableEffect(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "experiment.yaml")
	require.NoError(t, os.WriteFile(path, []byte("date: 2026-11-19\n# a comment\ndetectable_effect: 0\nmargin: 0.05\n"), 0o644))
	require.NoError(t, writeDetectableEffect(path, 0.03125))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "date: 2026-11-19\n# a comment\ndetectable_effect: 0.03125\nmargin: 0.05\n", string(data))

	other := filepath.Join(dir, "other.yaml")
	require.NoError(t, os.WriteFile(other, []byte("margin: 0.05"), 0o644))
	require.NoError(t, writeDetectableEffect(other, 0.5))
	data, err = os.ReadFile(other)
	require.NoError(t, err)
	require.Equal(t, "margin: 0.05\ndetectable_effect: 0.5\n", string(data))
}

// guardExperiment is an experiment of two variants over one route whose
// primary metric is metric.
func guardExperiment(metric score.Metric) *score.Experiment {
	return &score.Experiment{
		Date:          "2026-11-19",
		Name:          "guard",
		Variants:      []score.Variant{{Name: "control", Ref: "base"}, {Name: "variant", Ref: "topic"}},
		Routes:        []string{"r1"},
		Repetitions:   1,
		PrimaryMetric: metric,
		Margin:        0.05,
		Baseline:      "baseline.json",
	}
}

// runLine is one final result line of a run on the experiment's route.
func runLine(variant, task string, passed bool) result.Result {
	return result.Result{
		Schema:  1,
		Batch:   "b1",
		Task:    task,
		Kind:    result.KindPlain,
		Variant: variant,
		Route:   "r1",
		Rep:     1,
		Exit:    0,
		Grade:   result.Grade{Touched: passed, Packages: passed},
	}
}

// TestCompareRowsGuardIsThePassRate checks the guard of 4.3 rule 2: it is
// always the pass rate, and the primary metric is reported with its own
// interval and no margin rule, so a cheaper variant is not failed for
// spending fewer tokens.
func TestCompareRowsGuardIsThePassRate(t *testing.T) {
	t.Parallel()

	labels := map[string]string{"control": "control", "variant": "variant"}

	t.Run("a token win keeps the guard", func(t *testing.T) {
		t.Parallel()

		var runs []result.Result
		for i := 1; i <= 8; i++ {
			task := fmt.Sprintf("pr-%04d", i)
			control := runLine("control", task, true)
			control.Tokens = result.Tokens{Input: 1000, Output: 100}
			variant := runLine("variant", task, true)
			variant.Tokens = result.Tokens{Input: 100, Output: 10}
			runs = append(runs, control, variant)
		}

		rows, failed, err := compareRows(guardExperiment(score.MetricTokens), runs, "control", labels)
		require.NoError(t, err)
		require.False(t, failed, "the variant passes every task and uses fewer tokens")
		require.Len(t, rows, 1)
		require.Equal(t, score.MetricTokens, rows[0].Metric)
		require.InDelta(t, -990.0, rows[0].Estimate, 1e-9, "the primary metric is the token estimate")
		require.InDelta(t, 0.0, rows[0].Guard.Estimate, 1e-9, "the guard is the pass rate")
		require.True(t, rows[0].GuardPassed)
	})

	t.Run("a pass-rate loss fails the guard", func(t *testing.T) {
		t.Parallel()

		var runs []result.Result
		for i := 1; i <= 8; i++ {
			task := fmt.Sprintf("pr-%04d", i)
			control := runLine("control", task, true)
			control.Steps = 20
			variant := runLine("variant", task, i > 2)
			variant.Steps = 10
			runs = append(runs, control, variant)
		}

		rows, failed, err := compareRows(guardExperiment(score.MetricSteps), runs, "control", labels)
		require.NoError(t, err)
		require.True(t, failed, "two tasks of eight stopped passing")
		require.Len(t, rows, 1)
		require.Equal(t, score.MetricSteps, rows[0].Metric)
		require.InDelta(t, -10.0, rows[0].Estimate, 1e-9)
		require.InDelta(t, -0.25, rows[0].Guard.Estimate, 1e-9)
		require.False(t, rows[0].GuardPassed)
	})
}
