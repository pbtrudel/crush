package score_test

import (
	"testing"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/stretchr/testify/require"
)

func run(variant, task, kind string, exit int, passed bool) result.Result {
	return result.Result{
		Schema:  1,
		Batch:   "2026-12-01",
		Task:    task,
		Kind:    kind,
		Variant: variant,
		Route:   "deepseek-flash",
		Rep:     1,
		Exit:    exit,
		Grade:   result.Grade{Touched: passed, Packages: passed},
	}
}

func find(t *testing.T, aggs []score.Aggregate, variant, route, kind string) score.Aggregate {
	t.Helper()
	for _, agg := range aggs {
		if agg.Variant == variant && agg.Route == route && agg.Kind == kind {
			return agg
		}
	}
	t.Fatalf("no aggregate for %s/%s/%q", variant, route, kind)
	return score.Aggregate{}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		line result.Result
		want score.Outcome
	}{
		{"completed and graded", run("a", "t", result.KindPlain, 0, true), score.Pass},
		{"stopped short and graded", run("a", "t", result.KindPlain, 2, true), score.Pass},
		{"completed and failed", run("a", "t", result.KindPlain, 0, false), score.FalseCompletion},
		{"stopped short and failed", run("a", "t", result.KindPlain, 2, false), score.StoppedShort},
		{"never final", run("a", "t", result.KindPlain, 1, false), score.Invalid},
		{"bad output with a passing grade", run("a", "t", result.KindPlain, 3, true), score.Invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, score.Classify(tc.line))
		})
	}
}

func TestAggregates(t *testing.T) {
	t.Parallel()

	pass := run("main", "pr-0001", result.KindPlain, 0, true)
	pass.Steps, pass.ToolCalls, pass.DurationMS = 10, 1, 100
	pass.Tokens = result.Tokens{Input: 5, Output: 1}
	pass.Cost = 0.1
	pass.Gate = result.Gate{Continuations: 1, Claims: 2}

	falseCompletion := run("main", "pr-0001", result.KindPlain, 0, false)
	falseCompletion.Rep = 2
	falseCompletion.Steps, falseCompletion.ToolCalls, falseCompletion.DurationMS = 20, 2, 200
	falseCompletion.Tokens = result.Tokens{Input: 10, Output: 2}
	falseCompletion.UndeliveredReports = 3
	falseCompletion.Cost = 0.2
	falseCompletion.Gate = result.Gate{Continuations: 2, Claims: 3, UnbackedClaims: 1}

	stopped := run("main", "pr-0002", result.KindInterface, 2, false)
	stopped.Steps, stopped.ToolCalls, stopped.DurationMS = 30, 3, 300
	stopped.Tokens = result.Tokens{Input: 20, Output: 3}
	stopped.Cost = 0.3
	stopped.Gate = result.Gate{Continuations: 1, Claims: 4, UnbackedClaims: 1}

	invalid := run("main", "pr-0002", result.KindInterface, 1, false)
	invalid.Rep = 2
	invalid.Steps = 999
	// An invalid run is excluded from every figure, the gate's included.
	invalid.Gate = result.Gate{Continuations: 9, Claims: 9, UnbackedClaims: 9}

	contaminated := run("main", "pr-0003", result.KindPlain, 0, true)
	contaminated.Steps = 888
	contaminated.Contaminated = true

	runs := []result.Result{pass, falseCompletion, stopped, invalid, contaminated}
	require.Len(t, score.Aggregates(runs), 3)

	all := find(t, score.Aggregates(runs), "main", "deepseek-flash", "")
	require.Equal(t, 5, all.Runs)
	require.Equal(t, 1, all.Contaminated)
	require.Equal(t, 1, all.Invalid)
	require.Equal(t, 3, all.Valid)
	require.Equal(t, 1, all.Passes)
	require.Equal(t, 1, all.FalseCompletions)
	require.Equal(t, 1, all.FlaggedFalseCompletions)
	require.Equal(t, 1, all.StoppedShort)
	require.Equal(t, 2, all.Completed)
	require.InDelta(t, 1.0/3.0, all.PassRate, 1e-9)
	require.InDelta(t, 1.0/3.0, all.FalseCompletionRate, 1e-9)
	require.InDelta(t, 0.5, all.FalseCompletionRateCompleted, 1e-9)
	require.InDelta(t, 1.0/3.0, all.StoppedShortRate, 1e-9)
	require.Equal(t, score.Distribution{Median: 20, Total: 60}, all.Steps)
	require.Equal(t, score.Distribution{Median: 2, Total: 6}, all.ToolCalls)
	require.Equal(t, score.Distribution{Median: 200, Total: 600}, all.DurationMS)
	require.Equal(t, score.Distribution{Median: 10, Total: 35}, all.Tokens.Input)
	require.Equal(t, score.Distribution{Median: 2, Total: 6}, all.Tokens.Output)
	require.NotNil(t, all.Cost)
	require.InDelta(t, 0.6, *all.Cost, 1e-9)

	// The gate's counts cover the valid runs only, and the runs whose
	// answer claimed a check it never ran are named.
	require.Equal(t, 4, all.GateContinuations)
	require.Equal(t, 9, all.GateClaims)
	require.Equal(t, 2, all.GateUnbacked)
	require.Equal(t, []string{"pr-0001/main/deepseek-flash#2", "pr-0002/main/deepseek-flash#1"}, all.UnbackedRuns)

	plain := find(t, score.Aggregates(runs), "main", "deepseek-flash", result.KindPlain)
	require.Equal(t, 3, plain.Runs)
	require.Equal(t, 1, plain.Contaminated)
	require.Equal(t, 2, plain.Valid)
	require.InDelta(t, 0.5, plain.PassRate, 1e-9)
	require.Equal(t, 1, plain.FlaggedFalseCompletions)

	iface := find(t, score.Aggregates(runs), "main", "deepseek-flash", result.KindInterface)
	require.Equal(t, 2, iface.Runs)
	require.Equal(t, 1, iface.Invalid)
	require.Equal(t, 1, iface.Valid)
	require.Equal(t, 1, iface.StoppedShort)
	require.Zero(t, iface.Passes)
}

func TestAggregateCostNeedsEveryRun(t *testing.T) {
	t.Parallel()

	base := run("main", "pr-0001", result.KindPlain, 0, true)
	base.Cost = 0.5
	estimated := run("main", "pr-0002", result.KindPlain, 0, true)
	estimated.Cost, estimated.Estimated = 0.5, true
	free := run("main", "pr-0003", result.KindPlain, 0, true)

	for name, runs := range map[string][]result.Result{
		"an estimated run": {base, estimated},
		"a zero cost run":  {base, free},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			agg := find(t, score.Aggregates(runs), "main", "deepseek-flash", "")
			require.Nil(t, agg.Cost)
		})
	}
}

func TestAggregateSortsAndSkipsEmptyKinds(t *testing.T) {
	t.Parallel()

	plain := run("a", "pr-0001", result.KindPlain, 0, true)
	other := run("b", "pr-0001", result.KindInterface, 0, true)
	aggs := score.Aggregates([]result.Result{other, plain})
	require.Len(t, aggs, 4)
	require.Equal(t, []string{"", "plain"}, []string{aggs[0].Kind, aggs[1].Kind})
	require.Equal(t, []string{"", "interface"}, []string{aggs[2].Kind, aggs[3].Kind})
	require.Equal(t, "a", aggs[0].Variant)
	require.Equal(t, "b", aggs[2].Variant)
}
