package score

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/result"
)

// Metric is the primary metric of an experiment, one of the four of 4.3.
type Metric string

// The primary metrics of an experiment.
const (
	MetricPassRate         Metric = "pass_rate"
	MetricFalseCompletions Metric = "false_completions"
	MetricSteps            Metric = "steps"
	MetricTokens           Metric = "tokens"
)

// Valid reports whether m is an experiment metric.
func (m Metric) Valid() bool {
	switch m {
	case MetricPassRate, MetricFalseCompletions, MetricSteps, MetricTokens:
		return true
	}
	return false
}

// Paired bootstrap defaults. Compare is deterministic for a fixed seed, so
// two runs over the same lines give the same interval.
const (
	// DefaultDraws is the bootstrap size of 4.3.
	DefaultDraws = 10000
	// DefaultSeed seeds the bootstrap when none is given.
	DefaultSeed = 20261119
)

// Void limits of 4.3, as shares of the batch.
const (
	InvalidLimit      = 0.05
	ContaminatedLimit = 0.02
)

// Options states what to compare before it runs.
type Options struct {
	// Control and Variant name the two variants.
	Control, Variant string
	// Route restricts the comparison to one route. Empty pools every route
	// and the unit is then (task, route).
	Route  string
	Metric Metric
	// Margin is the pass-rate guard M of 4.3.
	Margin float64
	// Seed and Draws default to DefaultSeed and DefaultDraws.
	Seed  uint64
	Draws int
}

// Comparison is the paired comparison of 4.3.
type Comparison struct {
	Control, Variant string
	Metric           Metric
	// Units is the number of paired tasks or (task, route) units; zero
	// means nothing could be paired and there is no interval.
	Units int
	// Dropped counts the units with a valid run in only one variant.
	Dropped  int
	Estimate float64
	Lower    float64
	Upper    float64
	Margin   float64
	// GuardPassed is the margin rule: Lower > -Margin.
	GuardPassed bool
	Void        bool
	VoidReason  string
	// InvalidRate and ContaminationRate are shares of the batch.
	InvalidRate       float64
	ContaminationRate float64
}

// Compare pairs a variant against a control over the task means, as stated
// in 4.3. Runs that are contaminated or invalid are excluded from the pairs
// but count toward the void rule, which is over every line given: pass the
// batch's final lines.
func Compare(runs []result.Result, opts Options) (Comparison, error) {
	if !opts.Metric.Valid() {
		return Comparison{}, fmt.Errorf("metric %q: want pass_rate, false_completions, steps or tokens", opts.Metric)
	}
	if opts.Control == "" || opts.Variant == "" {
		return Comparison{}, errors.New("compare needs a control and a variant")
	}
	if opts.Draws <= 0 {
		opts.Draws = DefaultDraws
	}
	if opts.Seed == 0 {
		opts.Seed = DefaultSeed
	}

	cmp := Comparison{
		Control: opts.Control,
		Variant: opts.Variant,
		Metric:  opts.Metric,
		Margin:  opts.Margin,
	}

	var batch, invalid, contaminated int
	for _, r := range runs {
		batch++
		if r.Contaminated {
			contaminated++
		}
		if r.Exit != 0 && r.Exit != 2 {
			invalid++
		}
	}
	if batch > 0 {
		cmp.InvalidRate = float64(invalid) / float64(batch)
		cmp.ContaminationRate = float64(contaminated) / float64(batch)
		var reasons []string
		if cmp.InvalidRate > InvalidLimit {
			reasons = append(reasons, fmt.Sprintf("invalid runs %.1f%% above %.0f%%", 100*cmp.InvalidRate, 100*InvalidLimit))
		}
		if cmp.ContaminationRate > ContaminatedLimit {
			reasons = append(reasons, fmt.Sprintf("contaminated runs %.1f%% above %.0f%%", 100*cmp.ContaminationRate, 100*ContaminatedLimit))
		}
		cmp.Void = len(reasons) > 0
		cmp.VoidReason = strings.Join(reasons, "; ")
	}

	type unit struct{ task, route string }
	type sample struct {
		sum float64
		n   int
	}
	samples := make(map[unit]map[string]*sample)
	for _, r := range runs {
		if r.Contaminated || (r.Exit != 0 && r.Exit != 2) {
			continue
		}
		if r.Variant != opts.Control && r.Variant != opts.Variant {
			continue
		}
		if opts.Route != "" && r.Route != opts.Route {
			continue
		}
		u := unit{r.Task, r.Route}
		if samples[u] == nil {
			samples[u] = make(map[string]*sample)
		}
		if samples[u][r.Variant] == nil {
			samples[u][r.Variant] = &sample{}
		}
		s := samples[u][r.Variant]
		s.sum += metricScore(r, opts.Metric)
		s.n++
	}

	var diffs []float64
	for _, byVariant := range samples {
		control, controlOK := byVariant[opts.Control]
		variant, variantOK := byVariant[opts.Variant]
		if !controlOK || !variantOK {
			cmp.Dropped++
			continue
		}
		diffs = append(diffs, variant.sum/float64(variant.n)-control.sum/float64(control.n))
	}
	cmp.Units = len(diffs)
	if len(diffs) == 0 {
		return cmp, nil
	}
	// The units came from a map, so sort them: the fixed seed must give the
	// same interval on every run.
	sort.Float64s(diffs)
	var sum float64
	for _, d := range diffs {
		sum += d
	}
	cmp.Estimate = sum / float64(len(diffs))
	cmp.Lower, cmp.Upper = bootstrap(diffs, opts.Draws, opts.Seed)
	cmp.GuardPassed = cmp.Lower > -opts.Margin
	return cmp, nil
}

// metricScore is the per-run score of one metric: pass and false completion
// are indicators, steps and tokens are counts, and tokens sums every kind.
func metricScore(r result.Result, m Metric) float64 {
	switch m {
	case MetricPassRate:
		if Classify(r) == Pass {
			return 1
		}
		return 0
	case MetricFalseCompletions:
		if Classify(r) == FalseCompletion {
			return 1
		}
		return 0
	case MetricSteps:
		return float64(r.Steps)
	case MetricTokens:
		return float64(r.Tokens.Total())
	}
	return 0
}

// bootstrap returns the 95% interval of the mean by resampling diffs with
// replacement. The second PCG word keeps seeds from correlating.
func bootstrap(diffs []float64, draws int, seed uint64) (float64, float64) {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	means := make([]float64, draws)
	for i := range means {
		var sum float64
		for range len(diffs) {
			sum += diffs[rng.IntN(len(diffs))]
		}
		means[i] = sum / float64(len(diffs))
	}
	sort.Float64s(means)
	return percentile(means, 0.025), percentile(means, 0.975)
}

// percentile returns the nearest-rank percentile of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	return sorted[int(math.Round(p*float64(len(sorted)-1)))]
}
