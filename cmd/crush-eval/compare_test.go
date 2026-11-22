package main

import (
	"bytes"
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

	t.Run("fewer false completions keep the guard", func(t *testing.T) {
		t.Parallel()

		var runs []result.Result
		for i := 1; i <= 8; i++ {
			task := fmt.Sprintf("pr-%04d", i)
			control := runLine("control", task, true)
			if i <= 2 {
				// Two tasks the control claimed to finish and did not.
				control.Grade = result.Grade{}
			}
			variant := runLine("variant", task, true)
			runs = append(runs, control, variant)
		}

		rows, failed, err := compareRows(guardExperiment(score.MetricFalseCompletions), runs, "control", labels)
		require.NoError(t, err)
		require.False(t, failed, "the variant removed two false completions and lost nothing")
		require.Len(t, rows, 1)
		require.Equal(t, score.MetricFalseCompletions, rows[0].Metric)
		require.InDelta(t, -0.25, rows[0].Estimate, 1e-9, "two false completions of eight tasks")
		require.InDelta(t, 0.25, rows[0].Guard.Estimate, 1e-9, "the guard is the pass rate")
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

// TestAggregateRowsReportTheFigures checks that the table of 4.2 carries
// every figure the aggregate computes, and that compare prints them.
func TestAggregateRowsReportTheFigures(t *testing.T) {
	t.Parallel()

	runs := []result.Result{
		passedLine("control", "pr-0001"),
		passedLine("control", "pr-0002"),
		falseLine("control", "pr-0003"),
		shortLine("control", "pr-0004"),
	}
	for i := range runs {
		runs[i].Cost = 0.25
	}
	runs[0].Gate = result.Gate{Continuations: 1, Claims: 2}
	runs[1].Gate = result.Gate{Continuations: 2, Claims: 3, UnbackedClaims: 1}
	runs[0].Edits = result.Edits{Attempted: 6, Refused: 1, NotRead: 1, UnreadApplied: 2}
	runs[1].Edits = result.Edits{Attempted: 2, Refused: 1, MultipleMatches: 1}

	rows := aggregateRows(runs)
	var row aggregateRow
	for _, candidate := range rows {
		if candidate.Kind == "" {
			row = candidate
		}
	}
	require.Equal(t, "control", row.Variant, "the table has an overall row")
	require.Equal(t, 4, row.Runs)
	require.Equal(t, 4, row.Valid)
	require.Equal(t, 3, row.Completed)
	require.InDelta(t, 0.25, row.FalseCompletion, 1e-9, "one false completion of four valid runs")
	require.InDelta(t, 1.0/3.0, row.FalseCompletionCompleted, 1e-9, "one false completion of three completed runs")
	require.Equal(t, 1, row.Flagged)
	require.InDelta(t, 0.25, row.StoppedShort, 1e-9)
	require.InDelta(t, 3, row.ToolCalls, 1e-9)
	require.InDelta(t, 1000, row.DurationMS, 1e-9)
	require.InDelta(t, 5, row.TokensInput, 1e-9)
	require.InDelta(t, 2, row.TokensRead, 1e-9)
	require.InDelta(t, 3, row.TokensWrite, 1e-9)
	require.InDelta(t, 7, row.TokensOutput, 1e-9)
	require.InDelta(t, 68, row.Tokens, 1e-9)
	require.NotNil(t, row.Cost)
	require.InDelta(t, 1.0, *row.Cost, 1e-9)
	require.Equal(t, 3, row.GateContinuations)
	require.Equal(t, 5, row.GateClaims)
	require.Equal(t, 1, row.GateUnbacked)
	require.Len(t, row.UnbackedRuns, 1)
	require.Equal(t, 8, row.EditAttempted)
	require.Equal(t, 2, row.EditRefused)
	require.Equal(t, 2, row.EditUnreadApplied)
	require.InDelta(t, 0.25, row.EditRefusalRate, 1e-9)

	var printed bytes.Buffer
	printCompare(&printed, compareReport{Batch: "b1", Suite: "tree1", Control: "control", Aggregates: rows})
	for _, want := range []string{
		"contaminated", "false/completed", "flagged", "completed", "tools",
		"duration_s", "tok_in", "tok_read", "tok_write", "tok_out", "cost",
		"continuations", "claims", "unbacked", "edits", "edit_refusals", "unread",
	} {
		require.Contains(t, printed.String(), want)
	}
	require.Contains(t, printed.String(), "unbacked claims: control "+row.UnbackedRuns[0])
	require.Contains(t, printed.String(), "33%", "the false-completion rate over completed runs")
	require.Contains(t, printed.String(), "1.00", "the total cost")
	require.Contains(t, printed.String(), "25%", "refused edits per attempted edit")
}

// TestContaminatedRunsInTheReport checks that compare names every
// contaminated run and the markers it matched, beside the count of the
// per-variant table.
func TestContaminatedRunsInTheReport(t *testing.T) {
	t.Parallel()

	runs := []result.Result{passedLine("control", "pr-0001")}
	dirty := passedLine("control", "pr-0002")
	dirty.Contaminated = true
	dirty.ContaminatedMatches = 2
	dirty.ContaminatedEvidence = []result.Contamination{
		{Markers: []string{"gh pr"}, Excerpt: "gh pr view 5 -R pbtrudel/crush"},
		{Markers: []string{"/host/crush", "git cat-file"}, Excerpt: "git cat-file -p /host/crush/internal/x.go"},
	}
	runs = append(runs, dirty)

	report := compareReport{
		Batch:        "b1",
		Suite:        "tree1",
		Control:      "control",
		Aggregates:   aggregateRows(runs),
		Contaminated: contaminatedRows(runs),
	}
	var printed bytes.Buffer
	printCompare(&printed, report)
	require.Contains(t, printed.String(), "contaminated: control pr-0002/control/r1#1  markers: gh pr, /host/crush, git cat-file")
	require.Contains(t, printed.String(), "gh pr view 5 -R pbtrudel/crush")
	require.Contains(t, printed.String(), "git cat-file -p /host/crush/internal/x.go")
}

// passedLine is a completed run that passed, with the figures the aggregate
// reports.
func passedLine(variant, task string) result.Result {
	line := runLine(variant, task, true)
	line.Steps, line.ToolCalls, line.DurationMS = 10, 3, 1000
	line.Tokens = result.Tokens{Input: 5, CacheRead: 2, CacheWrite: 3, Output: 7}
	return line
}

// falseLine is a completed run that did not pass and left a report
// undelivered.
func falseLine(variant, task string) result.Result {
	line := passedLine(variant, task)
	line.Grade = result.Grade{}
	line.UndeliveredReports = 1
	return line
}

// shortLine is a run that stopped short and did not pass.
func shortLine(variant, task string) result.Result {
	line := falseLine(variant, task)
	line.Exit = 2
	return line
}
