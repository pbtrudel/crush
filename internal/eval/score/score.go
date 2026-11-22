// Package score classifies eval runs and compares variants.
package score

import (
	"fmt"
	"sort"

	"github.com/charmbracelet/crush/internal/eval/result"
)

// Outcome is the classification of one run of 4.1.
type Outcome int

const (
	// Pass is a run that exited on its own or stopped short and passed grading.
	Pass Outcome = iota
	// FalseCompletion is a run that exited on its own and failed grading.
	FalseCompletion
	// StoppedShort is a run that stopped short and failed grading.
	StoppedShort
	// Invalid is a final run that never exited 0 or 2.
	Invalid
)

// Classify returns the classification of 4.1 for one final line. A
// completed run that left reports undelivered still ended on its own; the
// aggregate counts it as flagged.
func Classify(r result.Result) Outcome {
	if r.Exit != 0 && r.Exit != 2 {
		return Invalid
	}
	if r.Grade.Passed() {
		return Pass
	}
	if r.Exit == 0 {
		return FalseCompletion
	}
	return StoppedShort
}

// Distribution is the median and total of one measure over the valid runs
// of a group.
type Distribution struct {
	Median float64
	Total  int64
}

// TokenDistributions holds one distribution per token kind.
type TokenDistributions struct {
	Input      Distribution
	CacheRead  Distribution
	CacheWrite Distribution
	Output     Distribution
}

// Aggregate holds the figures of 4.2 for one variant and route, overall
// (Kind empty) or for one task kind. Contaminated and invalid runs are
// excluded from the figures and counted.
type Aggregate struct {
	Variant string
	Route   string
	Kind    string

	Runs         int
	Contaminated int
	Invalid      int
	Valid        int
	Passes       int
	// FalseCompletions is the count over valid runs.
	FalseCompletions int
	// FlaggedFalseCompletions is the count of false completions with
	// undelivered reports.
	FlaggedFalseCompletions int
	StoppedShort            int
	// Completed is the count of exit 0 runs among the valid runs.
	Completed int

	PassRate                     float64
	FalseCompletionRate          float64
	FalseCompletionRateCompleted float64
	StoppedShortRate             float64

	// GateContinuations, GateClaims and GateUnbacked sum what the turn-end
	// gate did over the valid runs of the group.
	GateContinuations int
	GateClaims        int
	GateUnbacked      int
	// UnbackedRuns names the runs whose answer claimed a check it never
	// ran, sorted, so a batch report can list them.
	UnbackedRuns []string

	// EditAttempted, EditRefused and EditUnreadApplied sum the runs' edit
	// and multiedit calls over the valid runs of the group, and
	// EditRefusalRate is refused over attempted.
	EditAttempted     int
	EditRefused       int
	EditUnreadApplied int
	EditRefusalRate   float64

	Steps      Distribution
	ToolCalls  Distribution
	DurationMS Distribution
	Tokens     TokenDistributions
	// Cost is the total cost, nil unless every valid run has a non-zero,
	// non-estimated cost.
	Cost *float64
}

// Aggregates returns the figures of 4.2 for every variant and route, and the
// same split by task kind.
func Aggregates(runs []result.Result) []Aggregate {
	type groupKey struct {
		variant, route, kind string
	}
	groups := make(map[groupKey][]result.Result)
	for _, r := range runs {
		all := groupKey{r.Variant, r.Route, ""}
		groups[all] = append(groups[all], r)
		if r.Kind == result.KindPlain || r.Kind == result.KindInterface {
			byKind := groupKey{r.Variant, r.Route, r.Kind}
			groups[byKind] = append(groups[byKind], r)
		}
	}

	aggs := make([]Aggregate, 0, len(groups))
	for key, group := range groups {
		aggs = append(aggs, aggregate(key.variant, key.route, key.kind, group))
	}
	sort.Slice(aggs, func(i, j int) bool {
		a, b := aggs[i], aggs[j]
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.Route != b.Route {
			return a.Route < b.Route
		}
		return kindOrder(a.Kind) < kindOrder(b.Kind)
	})
	return aggs
}

// kindOrder sorts the overall row before the plain and interface rows.
func kindOrder(kind string) int {
	switch kind {
	case "":
		return 0
	case result.KindPlain:
		return 1
	default:
		return 2
	}
}

func aggregate(variant, route, kind string, runs []result.Result) Aggregate {
	agg := Aggregate{Variant: variant, Route: route, Kind: kind}
	var (
		steps, tools, durations              []int64
		input, cacheRead, cacheWrite, output []int64
		costTotal                            float64
		costShown                            = true
	)
	for _, r := range runs {
		agg.Runs++
		if r.Contaminated {
			agg.Contaminated++
			continue
		}
		if r.Exit != 0 && r.Exit != 2 {
			agg.Invalid++
			continue
		}
		agg.Valid++
		if r.Exit == 0 {
			agg.Completed++
		}
		switch Classify(r) {
		case Pass:
			agg.Passes++
		case FalseCompletion:
			agg.FalseCompletions++
			if r.UndeliveredReports > 0 {
				agg.FlaggedFalseCompletions++
			}
		case StoppedShort:
			agg.StoppedShort++
		}
		agg.GateContinuations += r.Gate.Continuations
		agg.GateClaims += r.Gate.Claims
		agg.GateUnbacked += r.Gate.UnbackedClaims
		agg.EditAttempted += r.Edits.Attempted
		agg.EditRefused += r.Edits.Refused
		agg.EditUnreadApplied += r.Edits.UnreadApplied
		if r.Gate.UnbackedClaims > 0 {
			agg.UnbackedRuns = append(agg.UnbackedRuns, runID(r))
		}
		steps = append(steps, int64(r.Steps))
		tools = append(tools, int64(r.ToolCalls))
		durations = append(durations, r.DurationMS)
		input = append(input, r.Tokens.Input)
		cacheRead = append(cacheRead, r.Tokens.CacheRead)
		cacheWrite = append(cacheWrite, r.Tokens.CacheWrite)
		output = append(output, r.Tokens.Output)
		costTotal += r.Cost
		if r.Estimated || r.Cost <= 0 {
			costShown = false
		}
	}

	if agg.Valid > 0 {
		agg.PassRate = float64(agg.Passes) / float64(agg.Valid)
		agg.FalseCompletionRate = float64(agg.FalseCompletions) / float64(agg.Valid)
		agg.StoppedShortRate = float64(agg.StoppedShort) / float64(agg.Valid)
	}
	if agg.EditAttempted > 0 {
		agg.EditRefusalRate = float64(agg.EditRefused) / float64(agg.EditAttempted)
	}
	if agg.Completed > 0 {
		agg.FalseCompletionRateCompleted = float64(agg.FalseCompletions) / float64(agg.Completed)
	}
	agg.Steps = distribution(steps)
	agg.ToolCalls = distribution(tools)
	agg.DurationMS = distribution(durations)
	agg.Tokens = TokenDistributions{
		Input:      distribution(input),
		CacheRead:  distribution(cacheRead),
		CacheWrite: distribution(cacheWrite),
		Output:     distribution(output),
	}
	if costShown && agg.Valid > 0 {
		total := costTotal
		agg.Cost = &total
	}
	sort.Strings(agg.UnbackedRuns)
	return agg
}

// runID names one run in a report: its task, variant, route and repetition.
func runID(r result.Result) string {
	return fmt.Sprintf("%s/%s/%s#%d", r.Task, r.Variant, r.Route, r.Rep)
}

// distribution returns the median and total of values, which it sorts.
func distribution(values []int64) Distribution {
	if len(values) == 0 {
		return Distribution{}
	}
	var total int64
	for _, v := range values {
		total += v
	}
	return Distribution{Median: Median(values), Total: total}
}

// Median returns the middle value of values, or the mean of the two middle
// ones for an even count, and zero for no value. It sorts values.
func Median(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	n := len(values)
	if n%2 == 1 {
		return float64(values[n/2])
	}
	return float64(values[n/2-1]+values[n/2]) / 2
}
