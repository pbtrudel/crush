package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/crush/internal/eval/batch"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/score"
)

const compareUsage = `usage:
  crush-eval compare <batch-id> [--json]`

// aggregateRow is one row of the per-variant table of design 4.2.
type aggregateRow struct {
	Variant      string  `json:"variant"`
	Route        string  `json:"route"`
	Kind         string  `json:"kind"`
	Runs         int     `json:"runs"`
	Valid        int     `json:"valid"`
	Invalid      int     `json:"invalid"`
	Contaminated int     `json:"contaminated"`
	Completed    int     `json:"completed"`
	PassRate     float64 `json:"pass_rate"`
	// FalseCompletion is the share of valid runs, and
	// FalseCompletionCompleted the share of the completed ones.
	FalseCompletion          float64 `json:"false_completion_rate"`
	FalseCompletionCompleted float64 `json:"false_completion_rate_completed"`
	// Flagged is the count of false completions that left reports
	// undelivered.
	Flagged      int     `json:"flagged_false_completions"`
	StoppedShort float64 `json:"stopped_short_rate"`
	Steps        float64 `json:"steps_median"`
	ToolCalls    float64 `json:"tool_calls_median"`
	DurationMS   float64 `json:"duration_ms_median"`
	TokensInput  float64 `json:"tokens_input_median"`
	TokensRead   float64 `json:"tokens_cache_read_median"`
	TokensWrite  float64 `json:"tokens_cache_write_median"`
	TokensOutput float64 `json:"tokens_output_median"`
	Tokens       int64   `json:"tokens_total"`
	// GateContinuations, GateClaims and GateUnbacked are what the turn-end
	// gate did over the valid runs, and UnbackedRuns names the runs whose
	// answer claimed a check it never ran.
	GateContinuations int      `json:"gate_continuations"`
	GateClaims        int      `json:"gate_claims"`
	GateUnbacked      int      `json:"gate_unbacked_claims"`
	UnbackedRuns      []string `json:"gate_unbacked_runs,omitempty"`
	// Cost is the total cost when every run priced itself.
	Cost *float64 `json:"cost,omitempty"`
}

// comparisonRow is the paired comparison of one variant against the control,
// on one route or over every route.
type comparisonRow struct {
	Variant           string         `json:"variant"`
	Route             string         `json:"route"`
	Metric            score.Metric   `json:"metric"`
	Units             int            `json:"units"`
	Dropped           int            `json:"dropped"`
	Estimate          float64        `json:"estimate"`
	Lower             float64        `json:"lower"`
	Upper             float64        `json:"upper"`
	Guard             score.Interval `json:"guard"`
	Margin            float64        `json:"margin"`
	GuardPassed       bool           `json:"guard_passed"`
	Void              bool           `json:"void"`
	VoidReason        string         `json:"void_reason,omitempty"`
	InvalidRate       float64        `json:"invalid_rate"`
	ContaminationRate float64        `json:"contamination_rate"`
}

// contaminationRow is one contaminated run of the batch: the markers its
// tool calls matched and the excerpts the result line kept.
type contaminationRow struct {
	Variant  string   `json:"variant"`
	Run      string   `json:"run"`
	Markers  []string `json:"markers"`
	Excerpts []string `json:"excerpts"`
}

// compareReport is what compare prints with --json.
type compareReport struct {
	Batch        string             `json:"batch"`
	Suite        string             `json:"suite"`
	Control      string             `json:"control"`
	Aggregates   []aggregateRow     `json:"aggregates"`
	Comparisons  []comparisonRow    `json:"comparisons"`
	Contaminated []contaminationRow `json:"contaminated,omitempty"`
}

// compareCmd prints the per-variant tables of a batch and the paired
// comparison of each variant against the experiment's control.
func compareCmd(args []string, stdout, stderr io.Writer) int {
	// The batch id comes first in the usage, but the flag package stops at
	// the first positional argument, so move it behind the flags.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(args[1:], args[0])
	}
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOut := flags.Bool("json", false, "print JSON instead of tables")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, compareUsage)
		return 2
	}

	h, err := home.Default()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	record, err := batch.Load(h, flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	exp, err := score.LoadExperiment(record.Experiment)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	lines, _, err := result.ReadFile(filepath.Join(h.BatchDir(record.ID), "results.jsonl"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	runs := finalRuns(lines)
	labels, err := variantLabels(exp, runs)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	control := labels[exp.Variants[0].Name]

	report := compareReport{
		Batch:        record.ID,
		Suite:        record.Suite,
		Control:      control,
		Aggregates:   aggregateRows(runs),
		Contaminated: contaminatedRows(runs),
	}

	comparisons, failed, err := compareRows(exp, runs, control, labels)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	report.Comparisons = comparisons

	if *jsonOut {
		if code := print(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		printCompare(stdout, report)
	}
	if failed {
		return 1
	}
	return 0
}

// compareRows compares every non-control variant against the control on the
// experiment's primary metric, on every route and pooled over all of them
// when there is more than one. It reports whether any comparison failed.
func compareRows(exp *score.Experiment, runs []result.Result, control string, labels map[string]string) ([]comparisonRow, bool, error) {
	var rows []comparisonRow
	failed := false
	for _, variant := range exp.Variants[1:] {
		routes := append([]string{}, exp.Routes...)
		if len(exp.Routes) > 1 {
			// Families that share a variant are pooled, and the unit is
			// then (task, route).
			routes = append(routes, "")
		}
		for _, name := range routes {
			// The primary metric is reported on its own, and the guard of
			// 4.3 rule 2 is always the pass rate: its margin is a share of
			// the tasks, which says nothing about steps or tokens.
			primary, err := score.Compare(runs, score.Options{
				Control: control,
				Variant: labels[variant.Name],
				Route:   name,
				Metric:  exp.PrimaryMetric,
			})
			if err != nil {
				return nil, false, err
			}
			guard, err := score.Compare(runs, score.Options{
				Control: control,
				Variant: labels[variant.Name],
				Route:   name,
				Metric:  score.MetricPassRate,
				Margin:  exp.Margin,
			})
			if err != nil {
				return nil, false, err
			}
			rows = append(rows, comparisonRow{
				Variant:           labels[variant.Name],
				Route:             name,
				Metric:            exp.PrimaryMetric,
				Units:             primary.Units,
				Dropped:           primary.Dropped,
				Estimate:          primary.Estimate,
				Lower:             primary.Lower,
				Upper:             primary.Upper,
				Guard:             score.Interval{Estimate: guard.Estimate, Lower: guard.Lower, Upper: guard.Upper},
				Margin:            exp.Margin,
				GuardPassed:       guard.GuardPassed,
				Void:              primary.Void,
				VoidReason:        primary.VoidReason,
				InvalidRate:       primary.InvalidRate,
				ContaminationRate: primary.ContaminationRate,
			})
			if primary.Void || !guard.GuardPassed {
				failed = true
			}
		}
	}
	return rows, failed, nil
}

// contaminatedRows is one row per contaminated final run, in the order of
// the lines, so a comparison that a contamination rate voids can be
// diagnosed without the session databases.
func contaminatedRows(runs []result.Result) []contaminationRow {
	var rows []contaminationRow
	for _, r := range runs {
		if !r.Contaminated {
			continue
		}
		row := contaminationRow{
			Variant: r.Variant,
			Run:     fmt.Sprintf("%s/%s/%s#%d", r.Task, r.Variant, r.Route, r.Rep),
		}
		seen := make(map[string]bool, len(r.ContaminatedEvidence))
		for _, evidence := range r.ContaminatedEvidence {
			for _, name := range evidence.Markers {
				if !seen[name] {
					seen[name] = true
					row.Markers = append(row.Markers, name)
				}
			}
			row.Excerpts = append(row.Excerpts, evidence.Excerpt)
		}
		rows = append(rows, row)
	}
	return rows
}

// aggregateRows is the per-variant table of 4.2, one row per variant, route
// and task kind.
func aggregateRows(runs []result.Result) []aggregateRow {
	aggs := score.Aggregates(runs)
	rows := make([]aggregateRow, 0, len(aggs))
	for _, agg := range aggs {
		rows = append(rows, aggregateRow{
			Variant:                  agg.Variant,
			Route:                    agg.Route,
			Kind:                     agg.Kind,
			Runs:                     agg.Runs,
			Valid:                    agg.Valid,
			Invalid:                  agg.Invalid,
			Contaminated:             agg.Contaminated,
			Completed:                agg.Completed,
			PassRate:                 agg.PassRate,
			FalseCompletion:          agg.FalseCompletionRate,
			FalseCompletionCompleted: agg.FalseCompletionRateCompleted,
			Flagged:                  agg.FlaggedFalseCompletions,
			StoppedShort:             agg.StoppedShortRate,
			Steps:                    agg.Steps.Median,
			ToolCalls:                agg.ToolCalls.Median,
			DurationMS:               agg.DurationMS.Median,
			TokensInput:              agg.Tokens.Input.Median,
			TokensRead:               agg.Tokens.CacheRead.Median,
			TokensWrite:              agg.Tokens.CacheWrite.Median,
			TokensOutput:             agg.Tokens.Output.Median,
			Tokens: agg.Tokens.Input.Total + agg.Tokens.CacheRead.Total +
				agg.Tokens.CacheWrite.Total + agg.Tokens.Output.Total,
			Cost:              agg.Cost,
			GateContinuations: agg.GateContinuations,
			GateClaims:        agg.GateClaims,
			GateUnbacked:      agg.GateUnbacked,
			UnbackedRuns:      agg.UnbackedRuns,
		})
	}
	return rows
}

// printCompare writes the tables and the comparisons as plain text: the
// rates and medians of 4.2 first, then the figures that only make sense
// beside them, then one block per comparison.
func printCompare(stdout io.Writer, report compareReport) {
	fmt.Fprintf(stdout, "batch %s  suite %s  control %s\n\n", report.Batch, report.Suite, report.Control)

	rates := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(rates, "variant\troute\tkind\truns\tvalid\tinvalid\tcontaminated\tpass\tfalse\tshort\tsteps\ttokens")
	for _, row := range report.Aggregates {
		fmt.Fprintf(rates, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\t%s\t%s\t%.0f\t%d\n",
			row.Variant, row.Route, kindName(row.Kind), row.Runs, row.Valid, row.Invalid,
			row.Contaminated, percent(row.PassRate), percent(row.FalseCompletion),
			percent(row.StoppedShort), row.Steps, row.Tokens)
	}
	if err := rates.Flush(); err != nil {
		return
	}

	figures := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(figures, "\nvariant\troute\tkind\tfalse/completed\tflagged\tcompleted\ttools\tduration_s\ttok_in\ttok_read\ttok_write\ttok_out\tcost")
	for _, row := range report.Aggregates {
		cost := "n/a"
		if row.Cost != nil {
			cost = fmt.Sprintf("%.2f", *row.Cost)
		}
		fmt.Fprintf(figures, "%s\t%s\t%s\t%s\t%d\t%d\t%.0f\t%.1f\t%.0f\t%.0f\t%.0f\t%.0f\t%s\n",
			row.Variant, row.Route, kindName(row.Kind), percent(row.FalseCompletionCompleted),
			row.Flagged, row.Completed, row.ToolCalls, row.DurationMS/1000,
			row.TokensInput, row.TokensRead, row.TokensWrite, row.TokensOutput, cost)
	}
	if err := figures.Flush(); err != nil {
		return
	}

	gate := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(gate, "\nvariant\troute\tkind\tcontinuations\tclaims\tunbacked")
	for _, row := range report.Aggregates {
		fmt.Fprintf(gate, "%s\t%s\t%s\t%d\t%d\t%d\n",
			row.Variant, row.Route, kindName(row.Kind),
			row.GateContinuations, row.GateClaims, row.GateUnbacked)
	}
	if err := gate.Flush(); err != nil {
		return
	}
	for _, row := range report.Aggregates {
		for _, id := range row.UnbackedRuns {
			fmt.Fprintf(stdout, "unbacked claims: %s %s\n", row.Variant, id)
		}
	}
	for _, row := range report.Contaminated {
		fmt.Fprintf(stdout, "contaminated: %s %s  markers: %s\n",
			row.Variant, row.Run, strings.Join(row.Markers, ", "))
		for _, text := range row.Excerpts {
			fmt.Fprintf(stdout, "  %s\n", text)
		}
	}

	for _, row := range report.Comparisons {
		route := row.Route
		if route == "" {
			route = "all routes"
		}
		guard := "pass"
		if !row.GuardPassed {
			guard = "fail"
		}
		fmt.Fprintf(stdout, "\n%s vs %s on %s\n", report.Control, row.Variant, route)
		fmt.Fprintf(stdout, "  %s: units %d  dropped %d  estimate %+.4f  95%% [%+.4f, %+.4f]\n",
			row.Metric, row.Units, row.Dropped, row.Estimate, row.Lower, row.Upper)
		fmt.Fprintf(stdout, "  pass_rate guard: estimate %+.4f  95%% [%+.4f, %+.4f]  margin %.4f  %s\n",
			row.Guard.Estimate, row.Guard.Lower, row.Guard.Upper, row.Margin, guard)
		if row.Void {
			fmt.Fprintf(stdout, "  void: %s\n", row.VoidReason)
		}
	}
}

// kindName names the overall row of a table.
func kindName(kind string) string {
	if kind == "" {
		return "all"
	}
	return kind
}

// percent formats a rate as a whole percentage.
func percent(rate float64) string {
	return fmt.Sprintf("%.0f%%", 100*rate)
}

// finalRuns returns the final line of every run key, sorted so the output is
// the same on every call.
func finalRuns(lines []result.Result) []result.Result {
	final := result.Final(lines)
	runs := make([]result.Result, 0, len(final))
	for _, line := range final {
		runs = append(runs, line)
	}
	sort.Slice(runs, func(i, j int) bool {
		a, b := runs[i], runs[j]
		if a.Task != b.Task {
			return a.Task < b.Task
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.Route != b.Route {
			return a.Route < b.Route
		}
		return a.Rep < b.Rep
	})
	return runs
}

// variantLabels maps each experiment variant to the run label it produced.
// A label is the variant name and the commit its binary was built from, so
// two batches from different commits stay apart.
func variantLabels(exp *score.Experiment, runs []result.Result) (map[string]string, error) {
	labels := make(map[string]string, len(exp.Variants))
	for _, variant := range exp.Variants {
		for _, r := range runs {
			if r.Variant != variant.Name && !strings.HasPrefix(r.Variant, variant.Name+"@") {
				continue
			}
			if label := labels[variant.Name]; label != "" && label != r.Variant {
				return nil, fmt.Errorf("variant %q ran as both %s and %s", variant.Name, label, r.Variant)
			}
			labels[variant.Name] = r.Variant
		}
		if labels[variant.Name] == "" {
			return nil, fmt.Errorf("variant %q has no results in the batch", variant.Name)
		}
	}
	return labels, nil
}
