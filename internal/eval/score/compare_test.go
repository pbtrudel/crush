package score_test

import (
	"testing"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/stretchr/testify/require"
)

func comparison(runs []result.Result, opts score.Options) score.Comparison {
	if opts.Metric == "" {
		opts.Metric = score.MetricPassRate
	}
	cmp, err := score.Compare(runs, opts)
	if err != nil {
		panic(err)
	}
	return cmp
}

func TestCompareIdenticalVariants(t *testing.T) {
	t.Parallel()

	var runs []result.Result
	for _, task := range []string{"t1", "t2", "t3", "t4"} {
		for _, variant := range []string{"control", "variant"} {
			for rep := 1; rep <= 2; rep++ {
				passed := task == "t1" || (task == "t2" && rep == 1)
				runs = append(runs, run(variant, task, result.KindPlain, 0, passed))
			}
		}
	}

	cmp := comparison(runs, score.Options{Control: "control", Variant: "variant", Margin: 0.05})
	require.Equal(t, 4, cmp.Units)
	require.Zero(t, cmp.Dropped)
	require.Zero(t, cmp.Estimate)
	require.LessOrEqual(t, cmp.Lower, 0.0)
	require.GreaterOrEqual(t, cmp.Upper, 0.0)
	require.True(t, cmp.GuardPassed)
	require.False(t, cmp.Void)
}

func TestCompareClearWin(t *testing.T) {
	t.Parallel()

	var runs []result.Result
	for i := 1; i <= 8; i++ {
		task := "t" + string(rune('0'+i))
		for rep := 1; rep <= 2; rep++ {
			runs = append(runs,
				run("control", task, result.KindPlain, 0, false),
				run("variant", task, result.KindPlain, 0, true),
			)
		}
	}

	cmp := comparison(runs, score.Options{Control: "control", Variant: "variant", Margin: 0.05})
	require.Equal(t, 8, cmp.Units)
	require.InDelta(t, 1.0, cmp.Estimate, 1e-9)
	require.InDelta(t, 1.0, cmp.Lower, 1e-9)
	require.InDelta(t, 1.0, cmp.Upper, 1e-9)
	require.True(t, cmp.GuardPassed)
}

func TestCompareClearLossFailsGuard(t *testing.T) {
	t.Parallel()

	var runs []result.Result
	for i := 1; i <= 8; i++ {
		task := "t" + string(rune('0'+i))
		runs = append(runs,
			run("control", task, result.KindPlain, 0, true),
			run("variant", task, result.KindPlain, 0, false),
		)
	}

	cmp := comparison(runs, score.Options{Control: "control", Variant: "variant", Margin: 0.05})
	require.InDelta(t, -1.0, cmp.Estimate, 1e-9)
	require.InDelta(t, -1.0, cmp.Upper, 1e-9)
	require.False(t, cmp.GuardPassed)
}

func TestCompareIsDeterministic(t *testing.T) {
	t.Parallel()

	var runs []result.Result
	for i := 1; i <= 12; i++ {
		task := "t" + string(rune('a'+i))
		runs = append(runs,
			run("control", task, result.KindPlain, 0, i%2 == 0),
			run("variant", task, result.KindPlain, 0, i%3 == 0),
		)
	}

	first := comparison(runs, score.Options{Control: "control", Variant: "variant", Margin: 0.05, Seed: 7})
	second := comparison(runs, score.Options{Control: "control", Variant: "variant", Margin: 0.05, Seed: 7})
	require.Equal(t, first.Estimate, second.Estimate)
	require.Equal(t, first.Lower, second.Lower)
	require.Equal(t, first.Upper, second.Upper)
	require.Less(t, first.Lower, first.Estimate)
	require.Greater(t, first.Upper, first.Estimate)
}

func TestCompareVoid(t *testing.T) {
	t.Parallel()

	clean := func(n int) []result.Result {
		var runs []result.Result
		for i := 0; i < n; i++ {
			task := "t" + string(rune('a'+i))
			runs = append(runs,
				run("control", task, result.KindPlain, 0, false),
				run("variant", task, result.KindPlain, 0, true),
			)
		}
		return runs
	}

	invalid := clean(10)
	invalid = append(invalid, run("control", "bad1", result.KindPlain, 1, false))
	cmp := comparison(invalid, score.Options{Control: "control", Variant: "variant", Margin: 0.05})
	require.False(t, cmp.Void, "1 of 21 runs is below 5 percent")

	invalid = append(invalid, run("variant", "bad1", result.KindPlain, 3, false))
	cmp = comparison(invalid, score.Options{Control: "control", Variant: "variant", Margin: 0.05})
	require.True(t, cmp.Void)
	require.Contains(t, cmp.VoidReason, "invalid")
	require.InDelta(t, 2.0/22.0, cmp.InvalidRate, 1e-9)

	contaminated := clean(10)
	dirty := run("control", "dirty", result.KindPlain, 0, false)
	dirty.Contaminated = true
	contaminated = append(contaminated, dirty)
	cmp = comparison(contaminated, score.Options{Control: "control", Variant: "variant", Margin: 0.05})
	require.True(t, cmp.Void)
	require.Contains(t, cmp.VoidReason, "contaminated")
}

func TestCompareDroppedUnits(t *testing.T) {
	t.Parallel()

	runs := []result.Result{
		run("control", "t1", result.KindPlain, 0, true),
		run("variant", "t1", result.KindPlain, 0, true),
		run("control", "t2", result.KindPlain, 0, true),
	}
	cmp := comparison(runs, score.Options{Control: "control", Variant: "variant", Margin: 0.05})
	require.Equal(t, 1, cmp.Units)
	require.Equal(t, 1, cmp.Dropped)
}

func TestCompareMetrics(t *testing.T) {
	t.Parallel()

	var runs []result.Result
	for i := 1; i <= 3; i++ {
		task := "t" + string(rune('0'+i))
		control := run("control", task, result.KindPlain, 0, true)
		control.Steps = 10
		control.Tokens = result.Tokens{Input: 100, Output: 20}
		variant := run("variant", task, result.KindPlain, 0, true)
		variant.Steps = 20
		variant.Tokens = result.Tokens{Input: 50, Output: 10}
		runs = append(runs, control, variant)
	}

	steps := comparison(runs, score.Options{Control: "control", Variant: "variant", Metric: score.MetricSteps, Margin: 0.05})
	require.InDelta(t, 10.0, steps.Estimate, 1e-9)

	tokens := comparison(runs, score.Options{Control: "control", Variant: "variant", Metric: score.MetricTokens, Margin: 0.05})
	require.InDelta(t, -60.0, tokens.Estimate, 1e-9)
}

func TestComparePoolsRoutes(t *testing.T) {
	t.Parallel()

	var runs []result.Result
	for _, task := range []string{"t1", "t2"} {
		for _, route := range []string{"r1", "r2"} {
			control := run("control", task, result.KindPlain, 0, false)
			control.Route = route
			variant := run("variant", task, result.KindPlain, 0, true)
			variant.Route = route
			runs = append(runs, control, variant)
		}
	}

	pooled := comparison(runs, score.Options{Control: "control", Variant: "variant", Margin: 0.05})
	require.Equal(t, 4, pooled.Units)

	one := comparison(runs, score.Options{Control: "control", Variant: "variant", Route: "r1", Margin: 0.05})
	require.Equal(t, 2, one.Units)
}

func TestCompareRejectsBadOptions(t *testing.T) {
	t.Parallel()

	_, err := score.Compare(nil, score.Options{Control: "control", Variant: "variant", Metric: "latency"})
	require.ErrorContains(t, err, "metric")

	_, err = score.Compare(nil, score.Options{Metric: score.MetricPassRate})
	require.ErrorContains(t, err, "control")
}
