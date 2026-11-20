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

	if err := opts.Home.Ensure(); err != nil {
		return line, err
	}
	runDir := filepath.Join(opts.Home.BatchDir(opts.Batch), "runs", runDirName(opts))
	// A retried attempt starts from a fresh checkout.
	if err := os.RemoveAll(runDir); err != nil {
		return line, fmt.Errorf("clear run directory: %w", err)
	}
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return line, fmt.Errorf("create run directory: %w", err)
	}
	blob, err := checkout.Prepare(ctx, opts.RepoDir, opts.Task.Source.Base, runDir)
	if err != nil {
		return line, err
	}
	line.ProjectConfig = blob

	src := filepath.Join(runDir, "src")
	base, err := gitx.Output(ctx, src, "rev-parse", "HEAD")
	if err != nil {
		return line, err
	}
	// The generated config belongs to this run alone: two runs of different
	// variants start within milliseconds of each other, and a shared file
	// would hand each child the other's provider and overlay.
	dirs := route.Dirs{
		Home:   filepath.Join(runDir, "home"),
		Config: filepath.Join(runDir, "config", "crush"),
	}
	if err := os.MkdirAll(dirs.Home, 0o700); err != nil {
		return line, fmt.Errorf("create the child's home: %w", err)
	}
	if err := os.Chmod(dirs.Home, 0o700); err != nil {
		return line, fmt.Errorf("tighten the child's home: %w", err)
	}
	if err := route.WriteGlobalConfig(filepath.Join(dirs.Config, "crush.json"), opts.Route, opts.Overlay); err != nil {
		return line, err
	}
	var proxy *netjail.Proxy
	jailSock := ""
	jailPort := 0
	if line.Network == netjail.ModeJail {
		var cleanup func()
		proxy, jailPort, cleanup, err = startProxy(ctx, opts)
		if err != nil {
			return line, err
		}
		jailSock = proxy.Path()
		defer cleanup()
		defer proxy.Close() //nolint:errcheck // The counts are read first.
	}
	env, err := opts.Route.ChildEnv(opts.Home, dirs, parentEnv(opts.ParentEnv))
	if err != nil {
		return line, err
	}
	env = append(env, networkEnv(line.Network, jailPort)...)

	stdoutPath := filepath.Join(runDir, "stdout.json")
	exit, killed, err := execute(ctx, opts.BinPath,
		commandArgs(opts, src, filepath.Join(runDir, "data"), maxSteps, timeout),
		env, task.Prompt(opts.Task, opts.Footer), stdoutPath,
		filepath.Join(runDir, "stderr.log"), timeout+grace, jailSock, jailPort)
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
	line.ContaminatedMatches, err = Matches(
		filepath.Join(runDir, "data", "crush.db"),
		opts.Task, opts.SuiteDir, opts.RepoDir,
	)
	if err != nil {
		return line, err
	}
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

// execute runs the binary with limit as its deadline and kills its process
// group when the deadline passes. It returns the process exit code and
// whether the watchdog fired.
func execute(ctx context.Context, bin string, args, env []string, prompt, stdoutPath, stderrPath string, limit time.Duration, jailSock string, jailPort int) (int, bool, error) {
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
	cmd.Env = env
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	kill := processGroup(cmd)
	if jailSock != "" {
		if err := netjail.Wrap(cmd, jailSock, jailPort); err != nil {
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
	Estimated bool    `json:"estimated"`
	Cost      float64 `json:"cost"`
	Error     string  `json:"error"`
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
	port, err := netjail.Port()
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
	return proxy, port, cleanup, nil
}

// parentEnv is the environment the child environment is built from.
func parentEnv(env []string) []string {
	if env != nil {
		return env
	}
	return os.Environ()
}
