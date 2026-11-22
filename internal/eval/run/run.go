// Package run executes one eval run: one task, one variant, one route and
// one repetition, as design section 3.4 describes. It prepares the
// checkout, runs the crush binary under test, grades the result, scans the
// session database for contamination and appends one result line.
package run

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/crush/internal/eval/catalog"
	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/charmbracelet/crush/internal/eval/gitx"
	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// DefaultWatchdogGrace is how long after the task's timeout the watchdog
// kills the process group.
const DefaultWatchdogGrace = 5 * time.Minute

// Options describe one run.
type Options struct {
	Task    *task.Task
	Variant string
	BinPath string
	Route   *route.Route
	Home    *home.Home
	Batch   string
	// Suite is the suite version, recorded in the line.
	Suite string
	// SuiteDir is the suite directory, one of the contamination markers.
	SuiteDir string
	Rep      int
	Attempt  int
	RepoDir  string
	// Footer is the prompt footer appended to the task's prompt.
	Footer string
	// Overlay is the variant's config overlay, merged into the generated
	// global config.
	Overlay map[string]any
	// MaxSteps and Timeout override the task's limits when non-zero.
	MaxSteps int
	Timeout  time.Duration
	// WatchdogGrace is how long after the timeout the process group is
	// killed. Zero means DefaultWatchdogGrace.
	WatchdogGrace time.Duration
	// GradeSem bounds how many grades run at once. When non-nil, the run
	// holds one slot while it grades, so a batch can keep the machine's
	// grade cap across concurrent runs.
	GradeSem chan struct{}
	// ParentEnv is the environment the child environment is built from.
	// Nil means os.Environ().
	ParentEnv []string
	// Network is the run's network mode, netjail.ModeJail or ModeHost.
	// Empty means host.
	Network string
	// RefreshToken is the host-side OAuth exchange a jailed run uses before
	// it starts. Nil means route.DefaultRefreshToken.
	RefreshToken route.RefreshTokenFunc
	// Preflight checks that the jail can start. Nil means netjail.Preflight.
	Preflight func() error
}

// runPaths are the directories of one run under its run directory on the
// host.
type runPaths struct {
	// Run is the run directory itself.
	Run string
	// Src is the checkout the child works in.
	Src string
	// Home is the child's HOME.
	Home string
	// Config is CRUSH_GLOBAL_CONFIG.
	Config string
	// Data is the child's data directory.
	Data string
}

// runPathsFor names the directories of a run under dir.
func runPathsFor(dir string) runPaths {
	return runPaths{
		Run:    dir,
		Src:    filepath.Join(dir, "src"),
		Home:   filepath.Join(dir, "home"),
		Config: filepath.Join(dir, "config", "crush"),
		Data:   filepath.Join(dir, "data"),
	}
}

// childPathsFor are the directories the child is given. A jailed child sees
// its run directory at the jail's neutral path, under no name that leads to
// the batch, so it has no reason to look at the runs beside it; a child on
// the host is given the host paths unchanged.
func childPathsFor(network string, host runPaths) runPaths {
	if network == netjail.ModeJail {
		return runPathsFor(netjail.JailRunPath)
	}
	return host
}

// jailRun is what a jailed child gets besides its masks: the proxy it reaches
// the allowlisted host through, and the bind that shows its run directory at
// the neutral path it is told about.
type jailRun struct {
	sock string
	port int
	bind netjail.Bind
}

// Run performs one run and appends its result line to the batch's
// results.jsonl. It returns the line it appended.
func Run(ctx context.Context, opts Options) (result.Result, error) {
	grace := opts.WatchdogGrace
	if grace <= 0 {
		grace = DefaultWatchdogGrace
	}
	maxSteps := opts.Task.Limits.MaxSteps
	if opts.MaxSteps > 0 {
		maxSteps = opts.MaxSteps
	}
	timeout := time.Duration(opts.Task.Limits.Timeout)
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	line := result.Result{
		Schema:  1,
		Batch:   opts.Batch,
		Suite:   opts.Suite,
		Task:    opts.Task.ID,
		Kind:    opts.Task.Kind,
		Variant: opts.Variant,
		Route:   opts.Route.Name,
		Family:  opts.Route.Family,
		Rep:     opts.Rep,
		Attempt: opts.Attempt,
		Started: time.Now().UTC(),
		Network: networkMode(opts.Network),
		// The line always holds a list, so a host run and a jail run with
		// no refusals have the same shape.
		NetDeniedHosts: []string{},
	}

	if line.Network == netjail.ModeJail {
		preflight := opts.Preflight
		if preflight == nil {
			preflight = netjail.Preflight
		}
		if err := preflight(); err != nil {
			return line, err
		}
	}
	if err := opts.Home.Ensure(); err != nil {
		return line, err
	}
	// A model selection left in the eval home's data config outranks the
	// route's own model, so the run would use a provider the route does not
	// configure. A jailed child's token refresh rewrites that file mid-batch,
	// so clear the selection here, on the host, before every run.
	if err := route.StripDataConfig(opts.Home); err != nil {
		return line, err
	}
	catalogs, err := catalog.Read(opts.Home)
	if err != nil {
		return line, err
	}
	line.Catalog = catalogs
	runDir := filepath.Join(opts.Home.BatchDir(opts.Batch), "runs", runDirName(opts))
	// A retried attempt starts from a fresh checkout.
	if err := os.RemoveAll(runDir); err != nil {
		return line, fmt.Errorf("clear run directory: %w", err)
	}
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return line, fmt.Errorf("create run directory: %w", err)
	}
	paths := basePaths(opts.RepoDir, opts.SuiteDir, opts.Home, runDir)
	blob, err := checkout.Prepare(ctx, opts.RepoDir, opts.Task.Source.Base, runDir)
	if err != nil {
		return line, err
	}
	line.ProjectConfig = blob

	// A child on the host works in the run directory; a jailed child is given
	// the same directories under the jail's neutral path, so no path it reads
	// advertises the batch layout and the sibling runs beside its own.
	host := runPathsFor(runDir)
	child := childPathsFor(line.Network, host)
	src := host.Src
	base, err := gitx.Output(ctx, src, "rev-parse", "HEAD")
	if err != nil {
		return line, err
	}
	// The generated config belongs to this run alone: two runs of different
	// variants start within milliseconds of each other, and a shared file
	// would hand each child the other's provider and overlay.
	if err := os.MkdirAll(host.Home, 0o700); err != nil {
		return line, fmt.Errorf("create the child's home: %w", err)
	}
	if err := os.Chmod(host.Home, 0o700); err != nil {
		return line, fmt.Errorf("tighten the child's home: %w", err)
	}
	if err := route.WriteGlobalConfig(filepath.Join(host.Config, "crush.json"), opts.Route, opts.Overlay); err != nil {
		return line, err
	}
	if line.Network == netjail.ModeJail {
		refresh := opts.RefreshToken
		if refresh == nil {
			refresh = route.DefaultRefreshToken
		}
		if err := opts.Route.PrepareToken(ctx, opts.Home, host.Data, timeout, refresh); err != nil {
			return line, err
		}
	}
	var proxy *netjail.Proxy
	var jail jailRun
	if line.Network == netjail.ModeJail {
		var cleanup func()
		proxy, jail.port, cleanup, err = startProxy(ctx, opts)
		if err != nil {
			return line, err
		}
		jail.sock = proxy.Path()
		defer cleanup()
		defer proxy.Close() //nolint:errcheck // The counts are read first.
	}
	env, err := opts.Route.ChildEnv(opts.Home, route.Dirs{Home: child.Home, Config: child.Config}, parentEnv(opts.ParentEnv))
	if err != nil {
		return line, err
	}
	env = append(env, networkEnv(line.Network, jail.port)...)

	// Only a jailed run hides paths, and only it needs git to say where the
	// checkout is: a run on the host must not fail on a repository that has
	// no work tree.
	var masks []netjail.Mask
	if line.Network == netjail.ModeJail {
		paths, err = HostPaths(ctx, opts.RepoDir, opts.SuiteDir, opts.Home, runDir,
			visiblePaths(opts.Home, env)...)
		if err != nil {
			return line, err
		}
		paths.Jail = child.Run
		masks = jailMasks(paths)
		// The run directory stays writable on the host, where the runner
		// reads it, and the jail shows it to the child at the neutral path,
		// the only path the child is given.
		jail.bind = netjail.Bind{Host: host.Run, Jail: child.Run, Dir: child.Src}
	}
	stdoutPath := filepath.Join(runDir, "stdout.json")
	exit, killed, err := execute(ctx, opts.BinPath, src,
		commandArgs(opts, child.Src, child.Data, maxSteps, timeout),
		env, task.Prompt(opts.Task, opts.Footer), stdoutPath,
		filepath.Join(runDir, "stderr.log"), timeout+grace, jail,
		masks)
	if err != nil {
		return line, err
	}
	if proxy != nil {
		line.NetDenied, line.NetDeniedHosts = proxy.Denied()
	}
	if err := ctx.Err(); err != nil {
		// A cancelled run stops here: nothing after the child is worth
		// recording, and the batch is stopping too.
		return line, err
	}

	out, ok := parseOutput(stdoutPath)
	switch {
	case ok:
		line.Exit = exit
		line.Reason = out.Reason
		line.Model = out.Model
		line.Provider = out.Provider
		line.Build = out.Build
		line.Steps = out.Tree.Steps
		line.ToolCalls = out.Tree.ToolCalls
		line.Tokens = out.Tree.Tokens
		line.TurnEnds = out.Tree.TurnEnds
		line.UndeliveredReports = out.Tree.UndeliveredReports
		line.Gate = out.Gate
		if out.Edits != nil {
			line.Edits = *out.Edits
		}
		line.Estimated = out.Estimated
		line.Cost = out.Cost
		line.DurationMS = out.DurationMS
		line.TextLength = len(out.Text)
		line.UsageLimited = exit == 3 && usageLimited(out.Error)
	case killed:
		// A killed run is treated like exit 3.
		line.Exit = 3
		line.Reason = "watchdog"
	default:
		line.Exit = 3
		line.Reason = "bad_output"
	}

	line.Diff, line.Worktrees = treeStats(ctx, src, base)
	line.ContaminatedEvidence, err = Matches(
		filepath.Join(runDir, "data", "crush.db"), opts.Task, paths,
	)
	if err != nil {
		return line, err
	}
	line.ContaminatedMatches = len(line.ContaminatedEvidence)
	line.Contaminated = line.ContaminatedMatches > 0

	if line.Exit == 0 || line.Exit == 2 {
		graded, err := gradeWithSlot(ctx, opts, src, base, runDir)
		if err != nil {
			return line, err
		}
		line.Grade = result.Grade{
			Touched:           graded.Touched,
			Packages:          graded.Packages,
			CompileError:      graded.CompileError,
			ModelTestsRemoved: graded.ModelTestsRemoved,
			Reason:            graded.Reason,
		}
	}
	line.Pass = (line.Exit == 0 || line.Exit == 2) && line.Grade.Passed()
	line.FalseCompletion = line.Exit == 0 && !line.Grade.Passed()

	if line.Pass {
		if err := os.RemoveAll(src); err != nil {
			return line, fmt.Errorf("remove the checkout of a passing run: %w", err)
		}
	}
	return line, result.Append(filepath.Join(opts.Home.BatchDir(opts.Batch), "results.jsonl"), line)
}

// commandArgs is the command line of design 3.4.
func commandArgs(opts Options, src, dataDir string, maxSteps int, timeout time.Duration) []string {
	model := opts.Route.Provider + "/" + opts.Route.Model
	args := []string{
		"--cwd", src, "--data-dir", dataDir, "run", "--json", "--quiet",
		"-m", model, "--small-model", opts.Route.Provider + "/" + opts.Route.SmallModel,
	}
	if opts.Route.ReasoningEffort != "" {
		args = append(args, "--reasoning-effort", opts.Route.ReasoningEffort)
	}
	return append(args, "--max-steps", strconv.Itoa(maxSteps), "--timeout", timeout.String())
}

// execute runs the binary in dir, the checkout it works on, with limit as
// its deadline and kills its process group when the deadline passes. It
// returns the process exit code and whether the watchdog fired. jail.sock
// names the jail's proxy when the run is jailed, and jail.bind is the neutral
// path the child is given; a run on the host ignores both. masks are what the
// jail hides.
func execute(ctx context.Context, bin, dir string, args, env []string, prompt, stdoutPath, stderrPath string, limit time.Duration, jail jailRun, masks []netjail.Mask) (int, bool, error) {
	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, false, fmt.Errorf("create stdout: %w", err)
	}
	defer stdout.Close() //nolint:errcheck // Read by the caller afterwards.
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, false, fmt.Errorf("create stderr: %w", err)
	}
	defer stderr.Close() //nolint:errcheck // Read-only log.

	cmd := exec.CommandContext(ctx, bin, args...)
	// The jail helper starts in the checkout on the host: a working directory
	// the masks cover fails a helper that resolves it, and the masks are not
	// in place yet. A jailed helper moves the child to the neutral path the
	// bind gives it once the mount is there.
	cmd.Dir = dir
	// Holding a killed process's output pipe open must not keep Wait
	// waiting after the group is gone.
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = env
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	kill := processGroup(cmd)
	if jail.sock != "" {
		if err := netjail.WrapBound(cmd, jail.sock, jail.port, jail.bind, masks...); err != nil {
			return 0, false, err
		}
	}
	var killed atomic.Bool
	// A cancelled context kills the whole process group, tools included,
	// instead of leaving crush and its children running.
	cmd.Cancel = func() error {
		killed.Store(true)
		return kill()
	}
	if err := cmd.Start(); err != nil {
		return 0, false, fmt.Errorf("start %s: %w", bin, err)
	}
	timer := time.AfterFunc(limit, func() {
		killed.Store(true)
		_ = kill()
	})
	_ = cmd.Wait()
	timer.Stop()
	// The child is gone but its own children may still hold the group.
	_ = kill()

	if stdout.Close() != nil || stderr.Close() != nil {
		return 0, false, errors.New("close the run's output files")
	}
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if code < 0 {
		// Killed by a signal.
		code = 3
	}
	return code, killed.Load(), nil
}

// runOutput is the object `crush run --json` prints, reduced to the fields
// the line keeps.
type runOutput struct {
	Schema     int    `json:"schema"`
	Reason     string `json:"reason"`
	Text       string `json:"text"`
	DurationMS int64  `json:"duration_ms"`
	Model      string `json:"model"`
	Provider   string `json:"provider"`
	Build      string `json:"build"`
	Tree       struct {
		Steps              int            `json:"steps"`
		ToolCalls          int            `json:"tool_calls"`
		Tokens             result.Tokens  `json:"tokens"`
		TurnEnds           map[string]int `json:"turn_ends"`
		UndeliveredReports int            `json:"undelivered_reports"`
	} `json:"tree"`
	Gate struct {
		Continuations  int `json:"continuations"`
		Claims         int `json:"claims"`
		UnbackedClaims int `json:"unbacked_claims"`
	} `json:"gate"`
	// Edits is set only for a run that called edit or multiedit.
	Edits     *result.Edits `json:"edits,omitempty"`
	Estimated bool          `json:"estimated"`
	Cost      float64       `json:"cost"`
	Error     string        `json:"error"`
}

// parseOutput reads the object and reports whether the file holds exactly
// one JSON object of schema 1.
func parseOutput(path string) (runOutput, bool) {
	var out runOutput
	data, err := os.ReadFile(path)
	if err != nil {
		return out, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&out); err != nil {
		return out, false
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return out, false
	}
	if out.Schema != 1 {
		return out, false
	}
	return out, true
}

// usageLimited reports whether an exit 3 error carries a usage-limit message.
func usageLimited(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "you've hit your") || strings.Contains(lower, "usage limit")
}

// gradeWithSlot grades the checkout while holding one slot of the batch's
// grade semaphore.
func gradeWithSlot(ctx context.Context, opts Options, src, base, runDir string) (grade.Result, error) {
	if opts.GradeSem != nil {
		select {
		case opts.GradeSem <- struct{}{}:
			defer func() { <-opts.GradeSem }()
		case <-ctx.Done():
			return grade.Result{}, ctx.Err()
		}
	}
	return gradeRun(ctx, opts, src, base, runDir)
}

// gradeRun grades the checkout with an empty HOME, so the tests and go
// never read the user's files.
func gradeRun(ctx context.Context, opts Options, src, base, runDir string) (grade.Result, error) {
	emptyHome, err := os.MkdirTemp(runDir, "grade-home-")
	if err != nil {
		return grade.Result{}, fmt.Errorf("create grading home: %w", err)
	}
	defer os.RemoveAll(emptyHome) //nolint:errcheck // Temporary home.
	return grade.Grade(ctx, opts.Task, opts.RepoDir, src, base, grade.Env(emptyHome), opts.Network == netjail.ModeJail)
}

// treeStats is the size of the change against the task base, untracked
// files included, and the number of worktrees the model created. A checkout
// git cannot read leaves both zero: the grade says what the model broke,
// and a diagnosis of the diff is not worth stopping the batch.
func treeStats(ctx context.Context, src, base string) (result.Diff, int) {
	var diff result.Diff
	if _, err := gitx.Output(ctx, src, "add", "-N", "."); err != nil {
		return diff, 0
	}
	numstat, err := gitx.Output(ctx, src, "diff", "--numstat", base)
	if err != nil {
		return diff, 0
	}
	for _, row := range strings.Split(numstat, "\n") {
		fields := strings.SplitN(row, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		diff.Files++
		diff.Added += number(fields[0])
		diff.Deleted += number(fields[1])
	}
	list, err := gitx.Output(ctx, src, "worktree", "list", "--porcelain")
	if err != nil {
		return diff, 0
	}
	return diff, max(0, strings.Count(list, "worktree ")-1)
}

// number parses a numstat count, where "-" marks a binary file.
func number(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// runDirName is the run's directory name under runs/.
func runDirName(opts Options) string {
	return DirName(opts.Task.ID, opts.Variant, opts.Route.Name, opts.Rep)
}

// DirName is the run's directory name under a batch's runs/. The variant may
// hold characters that a path cannot, so every other character becomes a
// dash.
func DirName(taskID, variant, route string, rep int) string {
	name := fmt.Sprintf("%s-%s-%s-r%d", taskID, variant, route, rep)
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, name)
}

// networkMode is the run's network mode, with the host as the default.
func networkMode(mode string) string {
	if mode == netjail.ModeJail {
		return netjail.ModeJail
	}
	return netjail.ModeHost
}

// networkEnv is the proxy environment a jailed run adds. A host run adds
// nothing.
func networkEnv(network string, port int) []string {
	if network != netjail.ModeJail {
		return nil
	}
	return netjail.Env(port)
}

// maxSocketPath is the longest proxy path a unix address can hold, with
// room for the terminator.
const maxSocketPath = 100

// proxySocket creates the run's own 0700 directory under the eval home and
// returns a socket path in it with the function that removes the directory.
// The directory sits outside the paths the jail masks, so the jailed child
// can still reach the socket.
func proxySocket(homeDir string) (string, func(), error) {
	dir, err := os.MkdirTemp(homeDir, "net-")
	if err != nil {
		return "", nil, fmt.Errorf("create the proxy's directory: %w", err)
	}
	path := filepath.Join(dir, "net.sock")
	if len(path) > maxSocketPath {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("proxy socket path %q is too long for a unix address", path)
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}

// startProxy starts the host side of the network jail for one run and
// returns it with the local port the jail forwards to it and the function
// that removes its directory.
func startProxy(ctx context.Context, opts Options) (*netjail.Proxy, int, func(), error) {
	host, err := opts.Route.APIHost()
	if err != nil {
		return nil, 0, nil, err
	}
	path, cleanup, err := proxySocket(opts.Home.Dir())
	if err != nil {
		return nil, 0, nil, err
	}
	proxy, err := netjail.Listen(ctx, path, []string{host})
	if err != nil {
		cleanup()
		return nil, 0, nil, err
	}
	return proxy, netjail.ProxyPort, cleanup, nil
}

// parentEnv is the environment the child environment is built from.
func parentEnv(env []string) []string {
	if env != nil {
		return env
	}
	return os.Environ()
}
