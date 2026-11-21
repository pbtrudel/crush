// Package multiturn runs one task over several prompts in the same session.
//
// A single-prompt run cannot show what a background compaction saves: the
// summary it writes is adopted at a later turn that crosses the limit, and a
// `crush run` process ends at its only turn. This driver sends the task's own
// prompt and then follow-up prompts on the same session, so both arms reach
// the turn where the limit is crossed and the time it takes is measured.
package multiturn

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/charmbracelet/crush/internal/logreport"
)

// Schema numbers the line this driver appends.
const Schema = 1

// The compaction settings both arms share. DefaultThreshold is a share of the
// window the route declares, so on the 200k route the limit is 60k: a session
// that reaches it compacts, and the margin under test decides whether the
// summary is written ahead of that limit. A band below the limit is what a
// background summary needs, and a limit at 0.2 of the window would already be
// crossed by most first turns, which leaves the margin nothing to do.
const (
	DefaultThreshold = 0.3
	tailTokens       = 8000
)

// disabledTools are off in both arms: these models treat the team tools as
// work of their own, which drowns the turn timings this driver measures.
var disabledTools = []string{
	"fetch", "agentic_fetch", "download", "sourcegraph", "web_search", "web_fetch",
	"spawn_teammate", "send_message", "list_agents", "wait_agent", "interrupt_agent",
	"team_task_create", "team_task_list", "team_task_get", "team_task_update",
}

// FollowUps are the prompts after the task's own. Each one is a further step
// of the same task, so the session keeps working and its context keeps
// growing instead of the session going idle after the first answer.
var FollowUps = []string{
	"Check what you just did against the task in your first message, fix anything you missed, then stop.",
	"Run the tests of the package you changed and fix any failure in the code you touched, then stop.",
	"State in one sentence what you changed, then stop.",
}

// Arm is one value of the option under test.
type Arm struct {
	Name   string
	Margin float64
}

// Options describe one driver run.
type Options struct {
	Home        *home.Home
	Route       *route.Route
	BinPath     string
	Ref         string
	Batch       string
	RepoDir     string
	Footer      string
	Reps        int
	Arms        []Arm
	Tasks       []*task.Task
	Concurrency int
	// TurnTimeout bounds one prompt; zero means DefaultTurnTimeout.
	TurnTimeout time.Duration
	// MaxSteps is the step budget of one session, counted across its turns.
	// Zero means DefaultMaxSteps.
	MaxSteps int
	// Threshold is the auto-compaction threshold both arms share. Zero means
	// DefaultThreshold.
	Threshold float64
	ParentEnv []string
}

// Defaults bound one driver run when Options leave them zero.
const (
	DefaultTurnTimeout = 8 * time.Minute
	DefaultMaxSteps    = 240
)

// Turn is what one prompt did.
type Turn struct {
	Index  int           `json:"index"`
	Wall   int64         `json:"wall_ms"`
	Exit   int           `json:"exit"`
	Steps  int           `json:"steps"`
	Tools  int           `json:"tool_calls"`
	Tokens result.Tokens `json:"tokens"`
	// FirstOutputMS is the delay from the prompt to the first assistant row
	// the turn wrote. Those rows carry whole seconds, so this is a
	// one-second-resolution proxy, and a step that runs tools lands its row
	// when the step ends.
	FirstOutputMS int64 `json:"first_output_ms"`
	// Summaries counts the summary rows written during the turn, and
	// SummaryMS is the delay to the first of them.
	Summaries int   `json:"summaries"`
	SummaryMS int64 `json:"summary_ms,omitempty"`
	// Context is the session's total context tokens when the turn ended,
	// which is the number the compaction limit is compared against.
	Context int64  `json:"context_tokens"`
	Reason  string `json:"reason"`
	Error   string `json:"error,omitempty"`
}

// Compactions is the logreport view of the run's session log: how many
// summaries were written, by trigger. "background" is a summary that was
// adopted at the limit, "background_unused" one that was written and never
// used.
type Compactions struct {
	Count     int            `json:"count"`
	ByTrigger map[string]int `json:"by_trigger"`
	Failed    int            `json:"failed"`
}

// Run is one task, one arm, one repetition.
type Run struct {
	Schema    int           `json:"schema"`
	Batch     string        `json:"batch"`
	Ref       string        `json:"ref"`
	Task      string        `json:"task"`
	Arm       string        `json:"arm"`
	Margin    float64       `json:"margin"`
	Rep       int           `json:"rep"`
	Route     string        `json:"route"`
	SessionID string        `json:"session_id"`
	Started   time.Time     `json:"started"`
	Turns     []Turn        `json:"turns"`
	Wall      int64         `json:"wall_ms"`
	Tokens    result.Tokens `json:"tokens"`
	Pass      bool          `json:"pass"`
	Grade     grade.Result  `json:"grade"`
	Compacted Compactions   `json:"compactions"`
	Error     string        `json:"error,omitempty"`
}

// ResultPath is the file a batch's driver runs append to.
func ResultPath(h *home.Home, batch string) string {
	return filepath.Join(h.BatchDir(batch), "multiturn.jsonl")
}

// RunOne runs one task under one arm once and appends its line.
func RunOne(ctx context.Context, opts Options, tk *task.Task, arm Arm, rep int) (Run, error) {
	turnTimeout := opts.TurnTimeout
	if turnTimeout <= 0 {
		turnTimeout = DefaultTurnTimeout
	}
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	threshold := opts.Threshold
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	parent := opts.ParentEnv
	if parent == nil {
		parent = os.Environ()
	}
	run := Run{
		Schema:  Schema,
		Batch:   opts.Batch,
		Ref:     opts.Ref,
		Task:    tk.ID,
		Arm:     arm.Name,
		Margin:  arm.Margin,
		Rep:     rep,
		Route:   opts.Route.Name,
		Started: time.Now().UTC(),
		Turns:   []Turn{},
	}
	runDir := filepath.Join(opts.Home.BatchDir(opts.Batch), "runs",
		fmt.Sprintf("%s-%s-r%d", tk.ID, arm.Name, rep))
	if err := os.RemoveAll(runDir); err != nil {
		return run, fmt.Errorf("clear run directory: %w", err)
	}
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return run, fmt.Errorf("create run directory: %w", err)
	}
	if _, err := checkout.Prepare(ctx, opts.RepoDir, tk.Source.Base, runDir); err != nil {
		return run, err
	}
	src := filepath.Join(runDir, "src")
	// The grade diffs against the commit Prepare wrote, not the task's base.
	base, err := checkout.Head(ctx, src)
	if err != nil {
		return run, err
	}
	dataDir := filepath.Join(runDir, "data")
	dirs := route.Dirs{
		Home:   filepath.Join(runDir, "home"),
		Config: filepath.Join(runDir, "config", "crush"),
	}
	if err := os.MkdirAll(dirs.Home, 0o700); err != nil {
		return run, fmt.Errorf("create the child's home: %w", err)
	}
	overlay := map[string]any{
		"auto_compact_threshold":    threshold,
		"compact_tail_tokens":       tailTokens,
		"compact_background_margin": arm.Margin,
		"disabled_tools":            disabledTools,
	}
	if err := route.WriteGlobalConfig(filepath.Join(dirs.Config, "crush.json"), opts.Route, overlay); err != nil {
		return run, err
	}
	env, err := opts.Route.ChildEnv(opts.Home, dirs, parent)
	if err != nil {
		return run, err
	}

	prompts := append([]string{task.Prompt(tk, opts.Footer)}, FollowUps...)
	lastRow := int64(0)
	sessionID := ""
	for i, prompt := range prompts {
		if err := ctx.Err(); err != nil {
			return run, err
		}
		started := time.Now()
		stdout := filepath.Join(runDir, fmt.Sprintf("turn-%d.json", i+1))
		args := turnArgs(opts.Route, src, dataDir, sessionID, maxSteps, turnTimeout)
		exit, err := execute(ctx, opts.BinPath, src, args, env, prompt, stdout,
			filepath.Join(runDir, fmt.Sprintf("turn-%d.log", i+1)), turnTimeout+2*time.Minute)
		turn := Turn{Index: i + 1, Wall: time.Since(started).Milliseconds(), Exit: exit}
		if err != nil {
			turn.Error = err.Error()
			run.Turns = append(run.Turns, turn)
			break
		}
		out, ok := parseOutput(stdout)
		if !ok {
			turn.Error = "bad_output"
			run.Turns = append(run.Turns, turn)
			break
		}
		if i == 0 {
			sessionID = out.SessionID
		}
		turn.Reason = out.Reason
		turn.Steps = out.Session.Steps
		turn.Tools = out.Session.ToolCalls
		turn.Tokens = out.Session.Tokens
		contextDB := filepath.Join(dataDir, "crush.db")
		if c, err := sessionContext(ctx, contextDB, sessionID); err == nil {
			turn.Context = c
		}
		if sampled, rows, err := sampleTurn(ctx, contextDB, sessionID, lastRow, started); err == nil {
			turn.FirstOutputMS = sampled.firstAssistantMS
			turn.Summaries = len(sampled.summaries)
			if len(sampled.summaries) > 0 {
				turn.SummaryMS = sampled.summaries[0]
			}
			lastRow = rows
		}
		run.Tokens.Input += turn.Tokens.Input
		run.Tokens.CacheRead += turn.Tokens.CacheRead
		run.Tokens.CacheWrite += turn.Tokens.CacheWrite
		run.Tokens.Output += turn.Tokens.Output
		run.Wall += turn.Wall
		run.Turns = append(run.Turns, turn)
		if exit != 0 && exit != 2 {
			turn.Error = fmt.Sprintf("exit %d", exit)
			break
		}
	}
	run.SessionID = sessionID

	emptyHome, err := os.MkdirTemp(runDir, "grade-home-")
	if err != nil {
		return run, fmt.Errorf("create grading home: %w", err)
	}
	defer os.RemoveAll(emptyHome) //nolint:errcheck // Temporary home.
	g, err := grade.Grade(ctx, tk, opts.RepoDir, src, base, grade.Env(emptyHome), false)
	if err != nil {
		run.Error = err.Error()
	} else {
		run.Grade = g
		run.Pass = g.Pass()
	}
	if c, err := compactionCounts(ctx, filepath.Join(dataDir, "crush.db")); err == nil {
		run.Compacted = c
	}
	if line, err := json.Marshal(run); err == nil {
		if f, ferr := os.OpenFile(ResultPath(opts.Home, opts.Batch), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); ferr == nil {
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
	}
	return run, nil
}

// RunAll runs every task, arm and repetition, at most Concurrency at once.
func RunAll(ctx context.Context, opts Options) ([]Run, error) {
	conc := opts.Concurrency
	if conc <= 0 {
		conc = 1
	}
	type job struct {
		tk  *task.Task
		arm Arm
		rep int
	}
	jobs := make(chan job)
	runs := make([]Run, 0, len(opts.Tasks)*len(opts.Arms)*opts.Reps)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				r, err := RunOne(ctx, opts, j.tk, j.arm, j.rep)
				mu.Lock()
				runs = append(runs, r)
				if err != nil && firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	for _, tk := range opts.Tasks {
		for _, arm := range opts.Arms {
			for rep := 1; rep <= opts.Reps; rep++ {
				jobs <- job{tk: tk, arm: arm, rep: rep}
			}
		}
	}
	close(jobs)
	wg.Wait()
	return runs, firstErr
}

// turnArgs is the command line of one prompt. A turn after the first names
// the session it continues, so the whole driver run is one session.
func turnArgs(r *route.Route, src, dataDir, sessionID string, maxSteps int, timeout time.Duration) []string {
	args := []string{
		"--cwd", src, "--data-dir", dataDir, "run", "--json", "--quiet",
		"-m", r.Provider + "/" + r.Model,
		"--small-model", r.Provider + "/" + r.SmallModel,
	}
	if r.ReasoningEffort != "" {
		args = append(args, "--reasoning-effort", r.ReasoningEffort)
	}
	args = append(args, "--max-steps", strconv.Itoa(maxSteps), "--timeout", timeout.String())
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	return args
}

// execute runs the child with prompt on its stdin, in its own process group
// so a deadline kills the tools it started, and returns its exit code.
func execute(ctx context.Context, bin, dir string, args, env []string, prompt, stdoutPath, stderrPath string, limit time.Duration) (int, error) {
	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create stdout: %w", err)
	}
	defer stdout.Close() //nolint:errcheck // Read by the caller afterwards.
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create stderr log: %w", err)
	}
	defer stderr.Close() //nolint:errcheck // Read-only log.

	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = env
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	kill := processGroup(cmd)
	cmd.Cancel = kill
	err = cmd.Run()
	_ = kill()
	if runCtx.Err() != nil {
		return 0, fmt.Errorf("turn timed out after %s", limit)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}

// runOutput is the object `crush run --json` prints, as far as the driver
// reads it.
type runOutput struct {
	Schema    int    `json:"schema"`
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
	Session   struct {
		Steps     int           `json:"steps"`
		ToolCalls int           `json:"tool_calls"`
		Tokens    result.Tokens `json:"tokens"`
	} `json:"session"`
	Error string `json:"error"`
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

// sample is what the session log says about one turn.
type sample struct {
	firstAssistantMS int64
	summaries        []int64
}

// sampleTurn reads the rows the turn wrote in the top-level session: when the
// first assistant row landed, and when each summary row landed. The rows
// carry whole seconds, so the offsets are seconds.
func sampleTurn(ctx context.Context, dbPath, sessionID string, sinceRowID int64, started time.Time) (sample, int64, error) {
	var s sample
	conn, err := db.ConnectReadOnly(ctx, dbPath)
	if err != nil {
		return s, sinceRowID, err
	}
	defer conn.Close() //nolint:errcheck // Read-only connection.
	rows, err := conn.QueryContext(ctx,
		`select role, is_summary_message, updated_at from messages
		 where session_id = ? and rowid > ? order by rowid`, sessionID, sinceRowID)
	if err != nil {
		return s, sinceRowID, err
	}
	defer rows.Close() //nolint:errcheck // Read-only query.
	first := int64(-1)
	for rows.Next() {
		var role string
		var summary int
		var updated int64
		if err := rows.Scan(&role, &summary, &updated); err != nil {
			return s, sinceRowID, err
		}
		offset := (updated - started.Unix()) * 1000
		if summary != 0 {
			s.summaries = append(s.summaries, offset)
			continue
		}
		if role == "assistant" && first < 0 {
			first = offset
		}
	}
	if err := rows.Err(); err != nil {
		return s, sinceRowID, err
	}
	if first > 0 {
		s.firstAssistantMS = first
	}
	var lastID sql.NullInt64
	if err := conn.QueryRowContext(ctx, `select max(rowid) from messages`).Scan(&lastID); err != nil {
		return s, sinceRowID, err
	}
	if lastID.Valid {
		return s, lastID.Int64, nil
	}
	return s, sinceRowID, nil
}

// sessionContext is the number the auto-compaction limit is compared against:
// the last request's prompt and completion tokens, as the session row keeps
// them. total_context_tokens is cumulative spend, not the window's fill.
func sessionContext(ctx context.Context, dbPath, sessionID string) (int64, error) {
	conn, err := db.ConnectReadOnly(ctx, dbPath)
	if err != nil {
		return 0, err
	}
	defer conn.Close() //nolint:errcheck // Read-only connection.
	var total int64
	var prompt, completion int64
	if err := conn.QueryRowContext(ctx,
		`select prompt_tokens, completion_tokens from sessions where id = ?`, sessionID).Scan(&prompt, &completion); err != nil {
		return 0, err
	}
	total = prompt + completion
	return total, nil
}

// compactionCounts reads the run's session log the way `crush stats report`
// does and keeps the compaction counts.
func compactionCounts(ctx context.Context, dbPath string) (Compactions, error) {
	conn, err := db.ConnectReadOnly(ctx, dbPath)
	if err != nil {
		return Compactions{}, err
	}
	defer conn.Close() //nolint:errcheck // Read-only connection.
	rep, err := logreport.Run(ctx, conn, logreport.Options{})
	if err != nil {
		return Compactions{}, err
	}
	counts := Compactions{Count: rep.Compactions.Count, Failed: rep.Compactions.Failed, ByTrigger: map[string]int{}}
	for _, c := range rep.Compactions.ByTrigger {
		counts.ByTrigger[c.Trigger] = c.Count
	}
	return counts, nil
}
