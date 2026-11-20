// Package batch runs an experiment end to end: it builds each variant's
// binary, plans the interleaved order of design section 3.6, grades every
// task's reference before any run, and schedules the runs under the route and
// machine caps, resuming a batch that was stopped.
package batch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/eval/run"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// Limits of a batch. The machine caps match design 3.6: at most six live
// runs and three grades at once on twelve cores.
const (
	// Schema is the version of the batch record.
	Schema = 1
	// DefaultMaxLiveRuns bounds the live runs of one batch.
	DefaultMaxLiveRuns = 6
	// DefaultMaxGrades bounds the grades running at once.
	DefaultMaxGrades = 3
	// DefaultRetryPause is the pause before a retried attempt.
	DefaultRetryPause = 30 * time.Second
	// DefaultUsagePause is the pause after a usage-limited attempt on a
	// route that states no window of its own.
	DefaultUsagePause = time.Hour
	// DefaultSeed shuffles the task order when none is given.
	DefaultSeed uint64 = 1
)

// systemConfigPath is where Crush reads the machine's system config. The
// runner cannot switch it off, so the batch records whether one exists.
const systemConfigPath = "/etc/crush/crush.json"

// Record is batches/<id>/batch.json: what a batch is, written before it
// starts so a resumed batch knows its seed, suite and experiment.
type Record struct {
	Schema int    `json:"schema"`
	ID     string `json:"id"`
	Seed   uint64 `json:"seed"`
	Suite  string `json:"suite"`
	// Toolchain is the go version the batch runs with, as `go version`
	// prints it, so a batch under another toolchain is visible.
	Toolchain string `json:"toolchain"`
	// Network is the network mode its runs and grades use, jail or host.
	Network    string    `json:"network"`
	Experiment string    `json:"experiment"`
	Started    time.Time `json:"started"`
	// SystemConfig is whether this machine has a /etc/crush config, which
	// every run reads as well as the generated one.
	SystemConfig bool `json:"system_config"`
}

// systemConfig reports whether the machine has a system config.
func systemConfig() bool {
	_, err := os.Stat(systemConfigPath)
	return err == nil
}

// Write records the batch in the eval home.
func (r *Record) Write(h *home.Home) error {
	dir := h.BatchDir(r.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create batch directory: %w", err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal batch record: %w", err)
	}
	path := filepath.Join(dir, "batch.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write batch record: %w", err)
	}
	return nil
}

// Load reads the record of a batch from the eval home.
func Load(h *home.Home, id string) (*Record, error) {
	if !validID(id) {
		return nil, fmt.Errorf("invalid batch id %q", id)
	}
	data, err := os.ReadFile(filepath.Join(h.BatchDir(id), "batch.json"))
	if err != nil {
		return nil, fmt.Errorf("read batch record: %w", err)
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse batch record: %w", err)
	}
	if r.Schema != Schema {
		return nil, fmt.Errorf("batch record schema %d: want %d", r.Schema, Schema)
	}
	if r.ID != id {
		return nil, fmt.Errorf("batch record is for %q, not %q", r.ID, id)
	}
	return &r, nil
}

// Options describe one batch.
type Options struct {
	ID         string
	Experiment *score.Experiment
	// Path is the experiment file, recorded in the batch.
	Path  string
	Seed  uint64
	Tasks []*task.Task
	// Routes maps the experiment's route names to loaded routes.
	Routes   map[string]*route.Route
	RepoDir  string
	Home     *home.Home
	Suite    string
	SuiteDir string
	Footer   string
	// Toolchain is the go version the batch runs with, as `go version`
	// prints it. The record holds it.
	Toolchain string
	// ParentEnv is the environment a run's child environment is built from.
	// Nil means os.Environ().
	ParentEnv []string
	// Network is the network mode of the batch's runs and grades,
	// netjail.ModeJail or ModeHost. Empty means host.
	Network string
	// NetworkSet is whether the caller passed --network explicitly, which
	// a resumed batch checks against its record.
	NetworkSet bool
	// Preflight checks that the jail can start. Nil means netjail.Preflight.
	Preflight func() error
	// Env is the environment reference grades run go with.
	Env []string
	// Scratch is the parent of the reference checkouts. Empty means the
	// eval home.
	Scratch string
	// Build returns the binary of the variant at ref. Nil means route.Build.
	Build func(ctx context.Context, ref string) (string, error)
	// GradeReference grades a task's merge tree. Nil means prepare the merge
	// checkout under Scratch and grade it with Env.
	GradeReference func(ctx context.Context, tk *task.Task) (grade.Result, error)
	// Run executes one run. Nil means run.Run.
	Run func(ctx context.Context, opts run.Options) (result.Result, error)
	// MaxLiveRuns and MaxGrades bound concurrency. Zero means the defaults.
	MaxLiveRuns int
	MaxGrades   int
	// RetryPause is the pause before a retried attempt. Zero means
	// DefaultRetryPause.
	RetryPause time.Duration
	// UsagePause is the pause after a usage-limited attempt on a route
	// without a window. Zero means DefaultUsagePause.
	UsagePause time.Duration
	// Now and Sleep are the clock and the wait of the scheduler. Tests
	// replace both to keep pauses free.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

// Summary is what one batch did.
type Summary struct {
	Seed    uint64 `json:"seed"`
	Planned int    `json:"planned"`
	Resumed int    `json:"resumed"`
	Ran     int    `json:"ran"`
	Invalid int    `json:"invalid"`
}

// job is one run key the scheduler may start.
type job struct {
	key       result.Key
	attempts  int
	counted   int
	notBefore time.Time
}

// outcome is the attempt one job finished with.
type outcome struct {
	job  *job
	line result.Result
	err  error
}

// runner is a batch in progress.
type runner struct {
	opts       Options
	byID       map[string]*task.Task
	bins       map[string]string
	overlays   map[string]map[string]any
	retryPause time.Duration
	usagePause time.Duration
	gradeSem   chan struct{}

	queue    []*job
	final    map[result.Key]result.Result
	attempts map[result.Key]int
	counted  map[result.Key]int

	// Only the scheduler goroutine touches the fields below.
	running   int
	live      int
	routeLive map[string]int
	paused    map[string]time.Time
	starts    map[string][]time.Time
	summary   Summary
}

// Run executes the batch described by opts.
func Run(ctx context.Context, opts Options) (Summary, error) {
	r, err := prepare(ctx, opts)
	if err != nil {
		return Summary{}, err
	}
	return r.exec(ctx)
}

// prepare validates the options, builds the variants, grades the references
// and plans the runs, as steps a, b and c of the batch design do.
func prepare(ctx context.Context, opts Options) (*runner, error) {
	if opts.Experiment == nil {
		return nil, errors.New("batch has no experiment")
	}
	if opts.Home == nil {
		return nil, errors.New("batch has no eval home")
	}
	if !validID(opts.ID) {
		return nil, fmt.Errorf("invalid batch id %q", opts.ID)
	}
	if len(opts.Tasks) == 0 {
		return nil, errors.New("batch has no tasks")
	}
	if err := opts.Experiment.Validate(); err != nil {
		return nil, err
	}
	for _, name := range opts.Experiment.Routes {
		rt := opts.Routes[name]
		if rt == nil {
			return nil, fmt.Errorf("batch has no route %q", name)
		}
		if rt.Concurrency < 1 {
			return nil, fmt.Errorf("route %q: concurrency must be at least 1", name)
		}
	}
	if opts.Network == "" {
		opts.Network = netjail.ModeHost
	}
	if opts.Network != netjail.ModeJail && opts.Network != netjail.ModeHost {
		return nil, fmt.Errorf("network %q: want %s or %s", opts.Network, netjail.ModeJail, netjail.ModeHost)
	}
	if opts.Seed == 0 {
		opts.Seed = DefaultSeed
	}
	if opts.MaxLiveRuns <= 0 {
		opts.MaxLiveRuns = DefaultMaxLiveRuns
	}
	if opts.MaxGrades <= 0 {
		opts.MaxGrades = DefaultMaxGrades
	}
	if opts.RetryPause <= 0 {
		opts.RetryPause = DefaultRetryPause
	}
	if opts.UsagePause <= 0 {
		opts.UsagePause = DefaultUsagePause
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = sleep
	}
	if opts.Build == nil {
		opts.Build = func(ctx context.Context, ref string) (string, error) {
			return route.Build(ctx, opts.Home, opts.RepoDir, ref)
		}
	}
	if err := opts.Home.Ensure(); err != nil {
		return nil, err
	}

	// A resumed batch keeps the seed, suite and experiment it started with,
	// and refuses a different suite: its lines would mix two suites.
	record, err := Load(opts.Home, opts.ID)
	switch {
	case err == nil:
		if record.Suite != opts.Suite {
			return nil, fmt.Errorf("batch %s ran suite %s, not %s", opts.ID, record.Suite, opts.Suite)
		}
		opts.Seed = record.Seed
		mode := record.Network
		if mode == "" {
			// A record written before the flag existed ran on the host.
			mode = netjail.ModeHost
		}
		if opts.NetworkSet && opts.Network != mode {
			return nil, fmt.Errorf("batch %s ran network %s, not %s", opts.ID, mode, opts.Network)
		}
		opts.Network = mode
	case errors.Is(err, fs.ErrNotExist):
		record := Record{
			Schema:       Schema,
			ID:           opts.ID,
			Seed:         opts.Seed,
			Suite:        opts.Suite,
			Toolchain:    opts.Toolchain,
			Network:      opts.Network,
			Experiment:   opts.Path,
			Started:      opts.Now().UTC(),
			SystemConfig: systemConfig(),
		}
		if err := record.Write(opts.Home); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}

	if opts.Network == netjail.ModeJail {
		preflight := opts.Preflight
		if preflight == nil {
			preflight = netjail.Preflight
		}
		if err := preflight(); err != nil {
			return nil, err
		}
	}

	r := &runner{
		opts:       opts,
		byID:       make(map[string]*task.Task, len(opts.Tasks)),
		bins:       make(map[string]string),
		overlays:   make(map[string]map[string]any),
		retryPause: opts.RetryPause,
		usagePause: opts.UsagePause,
		gradeSem:   make(chan struct{}, opts.MaxGrades),
		routeLive:  make(map[string]int),
		paused:     make(map[string]time.Time),
		starts:     make(map[string][]time.Time),
		summary:    Summary{Seed: opts.Seed},
	}
	for _, tk := range opts.Tasks {
		r.byID[tk.ID] = tk
	}

	// (a) Build every variant's binary once.
	built := make(map[string]string, len(opts.Experiment.Variants))
	labels := make(map[string]string, len(opts.Experiment.Variants))
	for _, variant := range opts.Experiment.Variants {
		bin, ok := built[variant.Ref]
		if !ok {
			var err error
			bin, err = opts.Build(ctx, variant.Ref)
			if err != nil {
				return nil, fmt.Errorf("build %s: %w", variant.Ref, err)
			}
			built[variant.Ref] = bin
		}
		v := variant
		label := v.Name + "@" + ShortSHA(bin)
		labels[v.Name] = label
		r.bins[label] = bin
		overlay := make(map[string]any, len(v.Overlay))
		for key, value := range v.Overlay {
			overlay[key] = value
		}
		r.overlays[label] = overlay
	}

	// (d) Resume: every key with a final line is done.
	if err := r.readResults(); err != nil {
		return nil, err
	}

	// (c) Plan the run keys, then drop the keys that are already final.
	for _, key := range plan(opts.Tasks, opts.Experiment, labels, opts.Seed) {
		key.Batch = opts.ID
		r.summary.Planned++
		if line, ok := r.final[key]; ok {
			r.summary.Resumed++
			if line.Exit != 0 && line.Exit != 2 {
				r.summary.Invalid++
			}
			continue
		}
		r.queue = append(r.queue, &job{
			key:      key,
			attempts: r.attempts[key],
			counted:  r.counted[key],
		})
	}
	if len(r.queue) == 0 {
		return r, nil
	}

	// (b) Grade every task's reference once, before any run, so a change in
	// the machine shows as a failing reference instead of a drop in pass
	// rate.
	for _, tk := range opts.Tasks {
		res, err := r.gradeReference(ctx, tk)
		if err != nil {
			return nil, fmt.Errorf("reference of task %s: %w", tk.ID, err)
		}
		if !res.Pass() {
			message := fmt.Sprintf("reference of task %s fails its grade", tk.ID)
			if len(res.Failed) > 0 {
				message += ": " + strings.Join(res.Failed, ", ")
			}
			return nil, errors.New(message)
		}
	}
	return r, nil
}

// validID reports whether id can name a directory under batches/.
func validID(id string) bool {
	return id != "" && filepath.Base(id) == id && !strings.ContainsAny(id, `/\`)
}

// ShortSHA is the abbreviated commit a binary was built from, which names
// its bin directory.
func ShortSHA(bin string) string {
	sha := filepath.Base(filepath.Dir(bin))
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// gradeReference grades the merge tree of one task, which is what
// `crush-eval grade --reference` does.
func (r *runner) gradeReference(ctx context.Context, tk *task.Task) (grade.Result, error) {
	if r.opts.GradeReference != nil {
		return r.opts.GradeReference(ctx, tk)
	}
	scratch := r.opts.Scratch
	if scratch == "" {
		scratch = r.opts.Home.Dir()
	}
	dir, err := os.MkdirTemp(scratch, "reference-")
	if err != nil {
		return grade.Result{}, err
	}
	defer os.RemoveAll(dir) //nolint:errcheck // Temporary checkout.
	return grade.Reference(ctx, tk, r.opts.RepoDir, dir, r.opts.Env, r.opts.Network == netjail.ModeJail)
}

// readResults loads the batch's result lines and the finality state of every
// run key.
func (r *runner) readResults() error {
	r.final = make(map[result.Key]result.Result)
	r.attempts = make(map[result.Key]int)
	r.counted = make(map[result.Key]int)

	path := filepath.Join(r.opts.Home.BatchDir(r.opts.ID), "results.jsonl")
	lines, _, err := result.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	r.final = result.Final(lines)
	for _, line := range lines {
		key := line.Key()
		r.attempts[key]++
		if !line.UsageLimited && !line.Final() {
			r.counted[key]++
		}
	}
	return nil
}

// exec schedules the batch's jobs in plan order under the caps of design 3.6:
// the route's live runs and start window, the machine's live runs and the
// grade semaphore of every run.
func (r *runner) exec(ctx context.Context) (Summary, error) {
	done := make(chan outcome)
	var firstErr error
	for len(r.queue) > 0 || r.running > 0 {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			r.queue = nil
		}

		started := false
		for i := 0; i < len(r.queue) && r.live < r.opts.MaxLiveRuns && firstErr == nil; {
			j := r.queue[i]
			concurrency := r.opts.Routes[j.key.Route].Concurrency
			if r.routeLive[j.key.Route] >= concurrency || !r.ready(j) {
				i++
				continue
			}
			r.queue = append(r.queue[:i], r.queue[i+1:]...)
			r.live++
			r.routeLive[j.key.Route]++
			r.starts[j.key.Route] = append(r.starts[j.key.Route], r.opts.Now())
			r.running++
			started = true
			go func(j *job) { done <- r.attempt(ctx, j) }(j)
		}

		if r.running == 0 {
			if len(r.queue) == 0 {
				break
			}
			// Nothing is live and nothing may start: wait for the next
			// moment one of the queued jobs may.
			if err := r.wait(ctx, r.queue); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				break
			}
			continue
		}
		if !started {
			r.finish(<-done, &firstErr)
		}
	}
	return r.summary, firstErr
}

// finish records one finished attempt and decides whether its key runs
// again.
func (r *runner) finish(out outcome, firstErr *error) {
	r.running--
	r.live--
	r.routeLive[out.job.key.Route]--
	r.summary.Ran++

	if out.err != nil {
		if *firstErr == nil {
			*firstErr = out.err
		}
		r.queue = nil
		return
	}
	line := out.line
	switch {
	case line.Final():
		if line.Exit != 0 && line.Exit != 2 {
			r.summary.Invalid++
		}
		return
	case line.UsageLimited:
		// The route pauses for its window, or for the default pause when it
		// states none, and the attempt does not count.
		pause := r.windowLength(out.job.key.Route)
		if pause == 0 {
			pause = r.usagePause
		}
		r.paused[out.job.key.Route] = r.opts.Now().Add(pause)
	case line.Retryable():
		out.job.counted++
		if out.job.counted >= result.MaxAttempts {
			r.summary.Invalid++
			return
		}
		out.job.notBefore = r.opts.Now().Add(r.retryPause)
	default:
		// A non-final line the batch does not retry stays as it is.
		return
	}
	r.queue = append(r.queue, out.job)
}

// ready reports whether a job may start now.
func (r *runner) ready(j *job) bool {
	now := r.opts.Now()
	if j.notBefore.After(now) {
		return false
	}
	if paused := r.paused[j.key.Route]; paused.After(now) {
		return false
	}
	return r.windowReady(j.key.Route)
}

// wait sleeps until the earliest moment a queued job may start: every
// queued job's own limits are considered, and the first one to open decides
// the pause.
func (r *runner) wait(ctx context.Context, queue []*job) error {
	now := r.opts.Now()
	next := now
	found := false
	for _, j := range queue {
		for _, t := range []time.Time{j.notBefore, r.paused[j.key.Route], r.windowNext(j.key.Route)} {
			if !t.After(now) {
				continue
			}
			if !found || t.Before(next) {
				next, found = t, true
			}
		}
	}
	d := next.Sub(now)
	if d <= 0 {
		d = time.Millisecond
	}
	return r.opts.Sleep(ctx, d)
}

// windowLength is the length of a route's window, zero when it has none.
func (r *runner) windowLength(name string) time.Duration {
	seconds := r.opts.Routes[name].Limits.Window
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// windowReady reports whether the route may start a run now.
func (r *runner) windowReady(name string) bool {
	count := r.opts.Routes[name].Limits.RunsPerWindow
	window := r.windowLength(name)
	if count <= 0 || window == 0 {
		return true
	}
	now := r.opts.Now()
	kept := r.starts[name][:0]
	for _, started := range r.starts[name] {
		if now.Sub(started) < window {
			kept = append(kept, started)
		}
	}
	r.starts[name] = kept
	return len(kept) < count
}

// windowNext is the moment the route may start its next run.
func (r *runner) windowNext(name string) time.Time {
	count := r.opts.Routes[name].Limits.RunsPerWindow
	window := r.windowLength(name)
	starts := r.starts[name]
	if count <= 0 || window == 0 || len(starts) < count {
		return r.opts.Now()
	}
	return starts[len(starts)-count].Add(window)
}

// attempt runs one attempt of a job.
func (r *runner) attempt(ctx context.Context, j *job) outcome {
	j.attempts++
	opts := run.Options{
		Task:      r.byID[j.key.Task],
		Variant:   j.key.Variant,
		BinPath:   r.bins[j.key.Variant],
		Route:     r.opts.Routes[j.key.Route],
		Home:      r.opts.Home,
		Batch:     r.opts.ID,
		Suite:     r.opts.Suite,
		SuiteDir:  r.opts.SuiteDir,
		Rep:       j.key.Rep,
		Attempt:   j.attempts,
		RepoDir:   r.opts.RepoDir,
		Footer:    r.opts.Footer,
		Overlay:   r.overlays[j.key.Variant],
		GradeSem:  r.gradeSem,
		ParentEnv: r.opts.ParentEnv,
		Network:   r.opts.Network,
	}
	runFn := run.Run
	if r.opts.Run != nil {
		runFn = r.opts.Run
	}
	line, err := runFn(ctx, opts)
	return outcome{job: j, line: line, err: err}
}

// sleep waits for d or until the context ends.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
