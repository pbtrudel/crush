package batch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/eval/run"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// newExperiment is an experiment of two variants over one route.
func newExperiment(reps int) *score.Experiment {
	return &score.Experiment{
		Date:          "2026-11-19",
		Name:          "test",
		Variants:      []score.Variant{{Name: "control", Ref: "base"}, {Name: "variant", Ref: "topic"}},
		Routes:        []string{"r1"},
		Repetitions:   reps,
		PrimaryMetric: score.MetricPassRate,
		Margin:        0.05,
		Baseline:      "baseline.json",
	}
}

// newTasks returns tasks whose ids count from 1.
func newTasks(n int) []*task.Task {
	tasks := make([]*task.Task, n)
	for i := range tasks {
		tasks[i] = &task.Task{ID: fmt.Sprintf("pr-%04d", i+1), Kind: task.KindPlain, Prompt: "fix"}
	}
	return tasks
}

// fixture is a batch whose builds, reference grades and runs are faked, with
// a clock the sleep hook advances so pauses cost no time.
type fixture struct {
	opts       Options
	results    string
	builds     []string
	refCalls   []string
	refErr     map[string]error
	sleeps     []time.Duration
	clock      time.Time
	respond    func(run.Options) result.Result
	delay      time.Duration
	peak       atomic.Int32
	live       atomic.Int32
	mu         sync.Mutex
	calls      []run.Options
	refNotPass map[string]bool
}

func newFixture(t *testing.T, tasks, reps int) *fixture {
	t.Helper()

	h := home.At(filepath.Join(t.TempDir(), "state"))
	f := &fixture{
		clock:      time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		refErr:     map[string]error{},
		refNotPass: map[string]bool{},
		respond: func(run.Options) result.Result {
			return result.Result{Exit: 0, Reason: "completed", Grade: result.Grade{Touched: true, Packages: true}}
		},
	}
	f.opts = Options{
		ID:         "b1",
		Experiment: newExperiment(reps),
		Path:       "evals/experiments/2026-11-19-test.yaml",
		Seed:       7,
		Tasks:      newTasks(tasks),
		Routes: map[string]*route.Route{
			"r1": {Name: "r1", Provider: "p", Model: "m", SmallModel: "m", Concurrency: 2},
		},
		Home:     h,
		Suite:    "tree1",
		SuiteDir: filepath.Join(t.TempDir(), "evals"),
		Footer:   "footer",
		Build: func(_ context.Context, ref string) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.builds = append(f.builds, ref)
			return filepath.Join(t.TempDir(), ref, "crush"), nil
		},
		GradeReference: func(_ context.Context, tk *task.Task) (grade.Result, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.refCalls = append(f.refCalls, tk.ID)
			if err := f.refErr[tk.ID]; err != nil {
				return grade.Result{}, err
			}
			if f.refNotPass[tk.ID] {
				return grade.Result{Failed: []string{"TestAdd"}}, nil
			}
			return grade.Result{Touched: true, Packages: true}, nil
		},
		Run: f.run,
		Now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.clock
		},
		Sleep: func(_ context.Context, d time.Duration) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.sleeps = append(f.sleeps, d)
			f.clock = f.clock.Add(d)
			return nil
		},
	}
	f.results = filepath.Join(h.BatchDir("b1"), "results.jsonl")
	return f
}

// run is the fake runner: it records the call, tracks live runs and appends
// the line the responder returns.
func (f *fixture) run(_ context.Context, opts run.Options) (result.Result, error) {
	n := f.live.Add(1)
	for {
		peak := f.peak.Load()
		if n <= peak || f.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	defer f.live.Add(-1)

	f.mu.Lock()
	f.calls = append(f.calls, opts)
	respond, delay := f.respond, f.delay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}

	line := respond(opts)
	line.Batch, line.Suite = opts.Batch, opts.Suite
	line.Task, line.Variant, line.Route = opts.Task.ID, opts.Variant, opts.Route.Name
	line.Rep, line.Attempt = opts.Rep, opts.Attempt
	if err := result.Append(f.results, line); err != nil {
		return line, err
	}
	return line, nil
}

// appendLine writes one line into the batch's results file.
func (f *fixture) appendLine(t *testing.T, line result.Result) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(f.results), 0o700))
	require.NoError(t, result.Append(f.results, line))
}

// batchRun runs the fixture's batch.
func (f *fixture) batchRun(t *testing.T) Summary {
	t.Helper()
	sum, err := Run(context.Background(), f.opts)
	require.NoError(t, err)
	return sum
}

func TestPlanInterleavesVariants(t *testing.T) {
	t.Parallel()

	exp := newExperiment(2)
	exp.Routes = []string{"r1", "r2"}
	labels := map[string]string{"control": "control@aaaa", "variant": "variant@bbbb"}
	keys := plan(newTasks(3), exp, labels, 7)

	require.Len(t, keys, 3*2*2*2)
	seen := make(map[string]bool)
	for i := 0; i < len(keys); {
		id := keys[i].Task
		require.False(t, seen[id], "the task blocks are not contiguous")
		seen[id] = true
		var want []result.Key
		for rep := 1; rep <= exp.Repetitions; rep++ {
			for _, name := range exp.Routes {
				for _, variant := range exp.Variants {
					want = append(want, result.Key{Task: id, Variant: labels[variant.Name], Route: name, Rep: rep})
				}
			}
		}
		require.Equal(t, want, keys[i:i+len(want)])
		i += len(want)
	}
	require.Len(t, seen, 3)
}

func TestPlanSeedIsDeterministic(t *testing.T) {
	t.Parallel()

	exp := newExperiment(1)
	labels := map[string]string{"control": "control@aaaa", "variant": "variant@bbbb"}
	tasks := newTasks(4)
	orders := func(seed uint64) []string {
		var order []string
		for _, key := range plan(tasks, exp, labels, seed) {
			if !slices.Contains(order, key.Task) {
				order = append(order, key.Task)
			}
		}
		return order
	}

	require.Equal(t, plan(tasks, exp, labels, 7), plan(tasks, exp, labels, 7))
	var differs bool
	for seed := uint64(1); seed <= 20; seed++ {
		if !slices.Equal(orders(7), orders(seed)) {
			differs = true
			break
		}
	}
	require.True(t, differs, "every seed gave the same task order")
}

func TestRunBuildsOnceAndResumesFinalKeys(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 2)
	f.appendLine(t, result.Result{
		Schema: 1, Batch: "b1", Task: "pr-0001", Variant: "control@base", Route: "r1", Rep: 1,
		Attempt: 1, Exit: 0, Reason: "completed", Grade: result.Grade{Touched: true, Packages: true},
	})
	f.appendLine(t, result.Result{
		Schema: 1, Batch: "b1", Task: "pr-0001", Variant: "variant@topic", Route: "r1", Rep: 1,
		Attempt: 1, Exit: 1, Reason: "error",
	})

	sum := f.batchRun(t)

	require.Equal(t, []string{"base", "topic"}, f.builds, "each variant is built once")
	require.Equal(t, 4, sum.Planned)
	require.Equal(t, 1, sum.Resumed)
	require.Equal(t, 3, sum.Ran)
	require.Len(t, f.calls, 3)

	for _, call := range f.calls {
		require.False(t, call.Task.ID == "pr-0001" && call.Variant == "control@base" && call.Rep == 1,
			"a key with a final line was run again")
		if call.Variant == "variant@topic" && call.Rep == 1 {
			require.Equal(t, 2, call.Attempt, "a resumed key continues its attempt count")
		}
	}
}

func TestRunFullyResumedSkipsBuildsAndReferences(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	require.NoError(t, os.MkdirAll(filepath.Dir(f.results), 0o700))
	for _, label := range []string{"control@base", "variant@topic"} {
		f.appendLine(t, result.Result{
			Schema: 1, Batch: "b1", Task: "pr-0001", Variant: label, Route: "r1", Rep: 1,
			Attempt: 1, Exit: 0, Reason: "completed", Grade: result.Grade{Touched: true, Packages: true},
		})
	}

	sum := f.batchRun(t)

	require.Equal(t, 2, sum.Planned)
	require.Equal(t, 2, sum.Resumed)
	require.Zero(t, sum.Ran)
	require.Empty(t, f.calls)
	require.Empty(t, f.refCalls)
}

func TestRunRefusesAnotherSuiteOnResume(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	require.NoError(t, os.MkdirAll(f.opts.Home.BatchDir("b1"), 0o700))
	require.NoError(t, (&Record{Schema: Schema, ID: "b1", Seed: 7, Suite: "other", Experiment: f.opts.Path}).Write(f.opts.Home))

	_, err := Run(context.Background(), f.opts)
	require.ErrorContains(t, err, "other")
	require.Empty(t, f.calls)
}

func TestRunRetriesThreeAttemptsThenInvalid(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	f.respond = func(run.Options) result.Result {
		return result.Result{Exit: 1, Reason: "error"}
	}
	f.opts.RetryPause = 5 * time.Millisecond

	sum := f.batchRun(t)

	require.Len(t, f.calls, 6, "two keys, three attempts each")
	require.Equal(t, 6, sum.Ran)
	require.Equal(t, 2, sum.Invalid)

	require.Equal(t, []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}, f.sleeps,
		"each round of retries pauses between two attempts")

	lines, _, err := result.ReadFile(f.results)
	require.NoError(t, err)
	require.Len(t, result.Final(lines), 2, "a key is final after three attempts")
}

func TestRunUsageLimitPausesWithoutCounting(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	f.opts.Routes["r1"].Limits = route.Limits{Window: 1}
	var limited atomic.Int32
	f.respond = func(opts run.Options) result.Result {
		if strings.HasPrefix(opts.Variant, "control") {
			return result.Result{Exit: 0, Reason: "completed", Grade: result.Grade{Touched: true, Packages: true}}
		}
		if limited.Add(1) <= 3 {
			return result.Result{Exit: 3, Reason: "error", UsageLimited: true}
		}
		return result.Result{Exit: 0, Reason: "completed", Grade: result.Grade{Touched: true, Packages: true}}
	}

	sum := f.batchRun(t)

	require.Len(t, f.calls, 5, "three usage-limited attempts are retried without counting")
	require.Zero(t, sum.Invalid)

	var paused time.Duration
	for _, d := range f.sleeps {
		paused += d
	}
	require.Equal(t, 3*time.Second, paused, "the route pauses for its window per usage-limit attempt")

	lines, _, err := result.ReadFile(f.results)
	require.NoError(t, err)
	final := result.Final(lines)
	require.Len(t, final, 2)
	for _, line := range final {
		require.Equal(t, 0, line.Exit)
	}
}

// TestRunWaitsForTheEarliestStart queues two routes whose windows end at
// different moments. The scheduler must wake for the earlier one: waiting
// for the latest delays a run that could already start.
func TestRunWaitsForTheEarliestStart(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	f.opts.Experiment.Routes = []string{"r1", "r2"}
	f.opts.Routes["r1"].Concurrency = 1
	f.opts.Routes["r1"].Limits = route.Limits{RunsPerWindow: 1, Window: 10}
	f.opts.Routes["r2"] = &route.Route{
		Name: "r2", Provider: "p", Model: "m", SmallModel: "m", Concurrency: 1,
		Limits: route.Limits{RunsPerWindow: 1, Window: 100},
	}

	sum := f.batchRun(t)

	require.Equal(t, 4, sum.Ran)
	require.Equal(t, []time.Duration{10 * time.Second, 90 * time.Second}, f.sleeps,
		"the first pause ends with r1's window, the second with r2's")
}

// TestRunUsageLimitPausesWithoutWindow pauses a route that has no window of
// its own for the default hour: without a pause, an exhausted route is
// retried in a tight loop of real runs.
func TestRunUsageLimitPausesWithoutWindow(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	var limited atomic.Int32
	f.respond = func(opts run.Options) result.Result {
		if strings.HasPrefix(opts.Variant, "variant") && limited.Add(1) <= 2 {
			return result.Result{Exit: 3, Reason: "error", UsageLimited: true}
		}
		return result.Result{Exit: 0, Reason: "completed", Grade: result.Grade{Touched: true, Packages: true}}
	}

	sum := f.batchRun(t)

	require.Len(t, f.calls, 4, "two usage-limited attempts and two completed runs, not a tight loop")
	require.Equal(t, []time.Duration{time.Hour, time.Hour}, f.sleeps, "each usage limit pauses for an hour")
	require.Zero(t, sum.Invalid)

	lines, _, err := result.ReadFile(f.results)
	require.NoError(t, err)
	require.Len(t, result.Final(lines), 2)
}

func TestRunBoundsRouteConcurrency(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 3, 2)
	f.opts.Routes["r1"].Concurrency = 2
	f.delay = 20 * time.Millisecond

	sum := f.batchRun(t)

	require.Equal(t, 12, sum.Ran)
	require.LessOrEqual(t, f.peak.Load(), int32(2), "the route's concurrency was exceeded")
	require.Greater(t, f.peak.Load(), int32(1), "the runs never overlapped")
}

func TestRunBoundsRouteStarts(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 2, 1)
	f.opts.Routes["r1"].Limits = route.Limits{RunsPerWindow: 2, Window: 10}
	f.delay = 5 * time.Millisecond

	sum := f.batchRun(t)

	require.Equal(t, 4, sum.Ran)
	require.Equal(t, []time.Duration{10 * time.Second}, f.sleeps, "the route waits for its window")
	require.LessOrEqual(t, f.peak.Load(), int32(2))
}

func TestRunStopsOnFailingReference(t *testing.T) {
	t.Parallel()

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, 2, 1)
		f.refErr["pr-0002"] = errors.New("no toolchain")

		_, err := Run(context.Background(), f.opts)
		require.ErrorContains(t, err, "pr-0002")
		require.Equal(t, []string{"pr-0001", "pr-0002"}, f.refCalls)
		require.Empty(t, f.calls)
		require.NoFileExists(t, f.results)
	})

	t.Run("not passing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, 2, 1)
		f.refNotPass["pr-0002"] = true

		_, err := Run(context.Background(), f.opts)
		require.ErrorContains(t, err, "pr-0002")
		require.ErrorContains(t, err, "TestAdd")
		require.Empty(t, f.calls)
		require.NoFileExists(t, f.results)
	})
}
