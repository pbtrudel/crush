# `crush run --max-steps` and `--timeout` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the in-process `crush run` a step budget (`--max-steps N`) and a deadline (`--timeout D`) for its whole session tree, record the runs they stop as `step_limit` and `time_limit`, and report both through `--json` with exit code 2 (delivery item 6 of the measurement design).

**Architecture:** The limits live in a registry on the coordinator, keyed by the tree's root session (`runLimits`, `internal/agent/run_limits.go`). Every session agent the coordinator builds shares it; a run finds its tree's limits through its session's parent chain when it starts, carries them on its call, counts each step against the shared budget, stops after its current step once the budget is spent, and is refused outright once a limit is reached. The deadline is a timer the coordinator arms when the command sets the limits: when it fires it cancels the session, its teammates through the Team service and any run woken on a teammate's session, and every run cancelled after the deadline is stamped `time_limit` because the stamp reads the deadline, not the cancel. The command sets the limits just before it sends the prompt; the existing wait for the session tree then ends on its own, because a stopped tree refuses new runs and goes idle.

**Tech Stack:** Go 1.27, `charm.land/fantasy` v0.45.5, cobra and fang, testify `require`.

**Spec:** `docs/superpowers/specs/2026-11-17-measurement-design.md`, section 1.2 (the `step_limit` and `time_limit` rows and the precedence), section 3 (3.2 the `reason` rule for limits, 3.3 `--max-steps`, 3.4 `--timeout`, 3.5 exit codes, the refusal against a server), Failure behavior, Validation (the `crush run` bullet and the precedence pairs), Scope, Alternatives 4, Delivery item 6. Item 4 (`docs/superpowers/plans/2026-11-18-w0-run-waits.md`) and item 5 (`docs/superpowers/plans/2026-11-18-w0-run-json.md`) are merged before this work starts.

## Global Constraints

- The work is two pull requests, each merged on its own, the second started after the first has merged. Part 1 is Tasks 1 to 4 (the limits in the agent loop and the two turn-end values; nothing sets a limit yet, so no behavior changes; about 400 changed lines). Part 2 is Tasks 5 to 8 (the coordinator stops a tree at its deadline, the JSON reason and exit codes, the two flags; about 360 changed lines). Each PR description states its real size.
- For each part, work in a fresh worktree created from `origin/main` after `git fetch origin` (`worktree create` with name `w0-run-limits-1` for Part 1 and `w0-run-limits-2` for Part 2, base `origin/main`). Paths below are relative to its root.
- Base: `origin/main` with items 4 and 5 merged. Item 4 provides `internal/agent/session_tree.go` (`SessionTree`, `TreeWatcher`, `WaitForSessionTree`, `treeSignal`), the `runEnded treeSignal` field at the end of the `coordinator` struct, `internal/app/run_wait.go` (`runAndWait`) and the test helpers `gatedModel`, `waitForTree`, `requireTreeIdle` in `internal/agent/session_tree_test.go`. Item 5 provides `internal/app/run_json.go` (`summarizeRun`), `internal/app/run_exit.go` (`runExitCode`), `refuseOnServer(jsonOutput bool)` in `internal/cmd/run.go` and the `asJSON` parameter of `RunNonInteractive`. If `internal/agent/session_tree.go` or `internal/app/run_exit.go` does not exist in the worktree, stop and report: the base has not merged.
- Only the in-process command changes behavior. The client/server path (`CRUSH_CLIENT_SERVER=1`, `runNonInteractive` in `internal/cmd/run.go`) is untouched apart from refusing `--max-steps` and `--timeout` (when not zero) before anything is read, connected or started: the refusal exits 1 and prints nothing on standard output.
- The step budget is the whole tree's, not the Lead's (spec 3.3 and Alternatives 4): every agent-loop step of every run in the tree draws on it, the Lead's own runs and continuations, subagents, `agentic_fetch`, teammates and runs woken by a background job. Compaction summaries, title generation and side questions are not agent-loop steps and do not count. A step is counted when it starts; runs that step at the same time each finish the step they started, so the tree can go past N by at most the number of runs stepping at once, less one.
- Precedence (spec 1.2): `time_limit` is the first test of tier 1, before `repetition_guard` and `canceled`, and applies to any error once the tree's deadline has passed. `step_limit` is the last test of tier 3, after `context_guard`, `tool_stop`, `loop_guard` and the finish-reason cases the turn-end PR added, and applies only to a step that finished for its tool calls (`tool_use`): only then would the loop have gone on. After a required compaction, a hook's block stays `compaction_blocked`; any other compaction error once the deadline has passed reads `time_limit`. A plain outer `context.DeadlineExceeded` with no tree deadline still reads `canceled`, as today.
- Once a tree has spent its budget or passed its deadline, every run of the tree that would start is refused before it writes anything (`errTreeLimitReached`): a continuation, a teammate started again, a run woken by a background job, a queued prompt. That is how "after the deadline no run in the tree is started or started again" (3.4) is met, and why a limit reached while the command waits for teammates also ends the wait: the stopped tree goes idle.
- The deadline is enforced by cancelling the tree when a timer fires, not by a context deadline on `Run` or `WaitForSessionTree` (the hook item 4 named). A cancel goes through the paths that already persist and stamp a cancelled turn, and a teammate's detached context can only be reached through the Team service. The wait keeps the command's own context, so a signal still ends it at once and still exits 130.
- The timeout counts from the moment the command sends its prompt (`runAndWait`), not from process start, so start-up time does not eat into it.
- `reason` in the JSON object (3.2): when any run in the tree ended with `time_limit` or `step_limit`, that value is the reason; `time_limit` wins when both occurred. Item 5's object gains no field and `runSchema` stays 1; `runExitCode` gains the two values in its code-2 case.
- Without `--json` the flags still apply and the command keeps today's exit codes (spec 3.5): a run stopped by the deadline reads as a cancel and exits 0, as does one stopped by the budget.
- Zero, the default, means no limit. A negative `--max-steps` or `--timeout` is refused with exit 1 before anything starts.
- Never open or print any `crush.json` or `crushrc` file (they hold API keys). The live checks print only the command's own output.
- Style: gofumpt (`/home/pbtrudel/.local/bin/gofumpt`); comments on their own line start with a capital, end with a period, wrap at 78 columns; no em dashes anywhere; testify `require`; log messages start with a capital letter.
- Commits: one per task, semantic subjects, no attribution lines. Commits are authorized for this work.

## Review Focus

1. `--max-steps N` on a prompt the model answers within the budget, or answers on exactly the step that spends it: the run completed and must exit 0, not 2. Pinned in Task 2 by the "final answer on the step that spends the budget" rows of `TestTurnEndForStep` and in Task 3 by `TestStepLimitLeavesAFinalAnswer`.
2. A teammate started again after a limit (a queued message, a woken session) must neither run nor hold the command's wait. Pinned in Task 3 by `TestStepLimitStopsTheTree` (a child session's run is refused once the budget is spent) and in Task 5 by `TestTimeLimitStopsATeammate` (the wait ends after the deadline cancels a teammate).
3. `--timeout 30s` with a start-up (MCP, provider) that takes ten seconds: the timeout counts from the prompt, so the run still gets its thirty seconds. Pinned in Task 7 by `TestRunAndWaitLimitsTheTreeFirst`.
4. A compaction the deadline interrupts: it must read `time_limit`, not `canceled` or `compaction_failed`, while a hook that blocked it still reads `compaction_blocked`. Pinned in Task 2 by the deadline rows of `TestTurnEndAfterCompaction`.
5. A negative value or the flags against a server: exit 1, nothing on standard output, the message names the flag. Pinned in Task 7 by `TestCheckRunLimits` and `TestRunRefusesLimitsAgainstAServer`.

---

# Part 1: the limits in the agent loop

Branch `w0-run-limits-1`. When merged, nothing sets a limit yet; every run behaves exactly as before.

### Task 1: A session tree's limits and where a run finds them

**Files:**
- Create: `internal/agent/run_limits.go`
- Test: `internal/agent/run_limits_test.go` (new)

**Interfaces:**
- Consumes: `session.Service.Get(ctx, id) (session.Session, error)` and `session.Session.ParentSessionID`; test setup `testEnv(t)` (`internal/agent/common_test.go`, fields `sessions`, `messages`), `session.Service.Create`, `session.Service.CreateTaskSession(ctx, id, parentID, title)`.
- Produces:
  - `type RunLimits struct { MaxSteps int; Timeout time.Duration }`
  - `type treeLimits struct` (unexported fields) with nil-safe methods `takeStep()`, `stepsSpent() bool`, `deadlinePassed() bool`, `reached() bool`, and `func newTreeLimits(limits RunLimits, now time.Time) *treeLimits`
  - `type runLimits struct` (zero value usable) with `func (r *runLimits) set(sessionID string, limits RunLimits) *treeLimits` and `func (r *runLimits) forSession(ctx context.Context, sessions session.Service, sessionID string) *treeLimits` (nil-safe)
  - `const maxTreeDepth = 32`

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/run_limits_test.go`:

```go
package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTreeLimits(t *testing.T) {
	t.Parallel()

	var none *treeLimits
	none.takeStep()
	require.False(t, none.reached(), "a tree without limits is never stopped")

	unlimited := newTreeLimits(RunLimits{}, time.Now())
	unlimited.takeStep()
	require.False(t, unlimited.reached())

	budget := newTreeLimits(RunLimits{MaxSteps: 2}, time.Now())
	budget.takeStep()
	require.False(t, budget.stepsSpent())
	budget.takeStep()
	require.True(t, budget.stepsSpent())
	require.False(t, budget.deadlinePassed())
	require.True(t, budget.reached())

	future := newTreeLimits(RunLimits{Timeout: time.Hour}, time.Now())
	require.False(t, future.deadlinePassed())
	past := newTreeLimits(RunLimits{Timeout: time.Second}, time.Now().Add(-time.Minute))
	require.True(t, past.deadlinePassed())
	require.False(t, past.stepsSpent())
	require.True(t, past.reached())
}

// A run finds its tree's limits through its session's parent chain, so a
// teammate's subagent draws on the budget the command set on the Lead.
func TestRunLimitsFollowTheParentChain(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()
	root, err := env.sessions.Create(ctx, "Lead")
	require.NoError(t, err)
	teammate, err := env.sessions.CreateTaskSession(ctx, "team-1", root.ID, "Teammate")
	require.NoError(t, err)
	child, err := env.sessions.CreateTaskSession(ctx, "call-1", teammate.ID, "Subagent")
	require.NoError(t, err)
	other, err := env.sessions.Create(ctx, "Other")
	require.NoError(t, err)

	var registry runLimits
	require.Nil(t, registry.forSession(ctx, nil, child.ID), "nothing is read while no tree has limits")

	tree := registry.set(root.ID, RunLimits{MaxSteps: 3})
	require.Same(t, tree, registry.forSession(ctx, env.sessions, root.ID))
	require.Same(t, tree, registry.forSession(ctx, env.sessions, teammate.ID))
	require.Same(t, tree, registry.forSession(ctx, env.sessions, child.ID))
	require.Nil(t, registry.forSession(ctx, env.sessions, other.ID))
	require.Nil(t, registry.forSession(ctx, env.sessions, "no-such-session"))

	var none *runLimits
	require.Nil(t, none.forSession(ctx, env.sessions, child.ID))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'TestTreeLimits|TestRunLimitsFollowTheParentChain' -v`
Expected: FAIL to compile with `undefined: treeLimits`, `undefined: newTreeLimits`, `undefined: RunLimits` and `undefined: runLimits`.

- [ ] **Step 3: Write the implementation**

Create `internal/agent/run_limits.go`:

```go
package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/crush/internal/session"
)

// RunLimits bound the work of one session tree for the command that set
// them: a budget of steps that every run in the tree draws on, and a time
// after which no run in the tree goes on. Zero means no limit.
type RunLimits struct {
	MaxSteps int
	Timeout  time.Duration
}

// treeLimits is one session tree's limits and the steps its runs took.
// Every method is safe on a nil receiver, which has no limit.
type treeLimits struct {
	maxSteps int64
	deadline time.Time
	steps    atomic.Int64
}

// newTreeLimits starts the limits at now: the deadline is now plus the
// timeout.
func newTreeLimits(limits RunLimits, now time.Time) *treeLimits {
	tree := &treeLimits{maxSteps: int64(limits.MaxSteps)}
	if limits.Timeout > 0 {
		tree.deadline = now.Add(limits.Timeout)
	}
	return tree
}

// takeStep counts one step a run of the tree is about to take.
func (l *treeLimits) takeStep() {
	if l != nil {
		l.steps.Add(1)
	}
}

// stepsSpent reports whether the tree's runs took every step of the budget.
func (l *treeLimits) stepsSpent() bool {
	return l != nil && l.maxSteps > 0 && l.steps.Load() >= l.maxSteps
}

// deadlinePassed reports whether the tree's deadline has passed.
func (l *treeLimits) deadlinePassed() bool {
	return l != nil && !l.deadline.IsZero() && !time.Now().Before(l.deadline)
}

// reached reports whether a limit stops the tree's runs.
func (l *treeLimits) reached() bool {
	return l.stepsSpent() || l.deadlinePassed()
}

// maxTreeDepth bounds the walk up a parent chain, so a cycle in the session
// table cannot hold a run.
const maxTreeDepth = 32

// runLimits holds the limits set on session trees, by the tree's root
// session. The zero value holds none.
type runLimits struct {
	mu    sync.Mutex
	trees map[string]*treeLimits
}

// set gives the tree of sessionID the limits, counted from now.
func (r *runLimits) set(sessionID string, limits RunLimits) *treeLimits {
	tree := newTreeLimits(limits, time.Now())
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.trees == nil {
		r.trees = make(map[string]*treeLimits)
	}
	r.trees[sessionID] = tree
	return tree
}

// forSession returns the limits of the tree sessionID belongs to, or nil
// when it belongs to no tree with limits. A run finds them through its
// session's parent chain, not through its context: a teammate started
// again and a run woken by a background job begin on fresh contexts.
func (r *runLimits) forSession(ctx context.Context, sessions session.Service, sessionID string) *treeLimits {
	if r == nil {
		return nil
	}
	id := sessionID
	for range maxTreeDepth {
		r.mu.Lock()
		tree, limited := r.trees[id], len(r.trees) > 0
		r.mu.Unlock()
		if tree != nil || !limited {
			return tree
		}
		sess, err := sessions.Get(ctx, id)
		if err != nil || sess.ParentSessionID == "" {
			return nil
		}
		id = sess.ParentSessionID
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/agent/ -run 'TestTreeLimits|TestRunLimitsFollowTheParentChain' -v`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
/home/pbtrudel/.local/bin/gofumpt -w internal/agent/run_limits.go internal/agent/run_limits_test.go
git add internal/agent/run_limits.go internal/agent/run_limits_test.go
git commit -m "feat(agent): hold step and time limits per session tree"
```

---

### Task 2: The `step_limit` and `time_limit` turn ends

**Files:**
- Modify: `internal/message/content.go` (the `TurnEnd` constants, after `TurnEndCompactionFailed`)
- Modify: `internal/agent/turn_end.go` (`stepEnding`, `turnEndForError`, `turnEndForStep`, `turnEndAfterCompaction`)
- Modify: `internal/agent/agent.go` (`SessionAgentCall`, before the `Accepted` field; `persistCanceledTurn`; the three turn-end call sites in `sessionAgent.Run`)
- Test: `internal/agent/turn_end_test.go`

**Interfaces:**
- Consumes: Task 1's `treeLimits` and its nil-safe `deadlinePassed()` and `stepsSpent()`.
- Produces:
  - `message.TurnEndStepLimit TurnEnd = "step_limit"`, `message.TurnEndTimeLimit TurnEnd = "time_limit"`
  - `stepEnding.stepsSpent bool`
  - `func turnEndForError(err error, deadlinePassed bool) message.TurnEnd`
  - `func turnEndAfterCompaction(end message.TurnEnd, summarizeErr error, deadlinePassed bool) message.TurnEnd`
  - `SessionAgentCall.limits *treeLimits` (unexported; nil until Task 3 sets it, which has no limit)

- [ ] **Step 1: Write the failing tests**

In `internal/agent/turn_end_test.go`, in `TestTurnEndForError`, replace

```go
	tests := []struct {
		name string
		err  error
		want message.TurnEnd
	}{
		{name: "repetition", err: errRepetitionDetected, want: message.TurnEndRepetitionGuard},
```

with

```go
	tests := []struct {
		name           string
		err            error
		deadlinePassed bool
		want           message.TurnEnd
	}{
		// The command cancels a tree whose deadline passed; that cancel
		// carries no cause, so the deadline itself decides.
		{name: "cancel after the deadline", err: context.Canceled, deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "repetition after the deadline", err: errRepetitionDetected, deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "provider failure after the deadline", err: &fantasy.ProviderError{StatusCode: 500, Message: "boom"}, deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "caller deadline after the tree's deadline", err: fmt.Errorf("teammate turn: %w", context.DeadlineExceeded), deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "repetition", err: errRepetitionDetected, want: message.TurnEndRepetitionGuard},
```

and replace

```go
			require.Equal(t, tt.want, turnEndForError(tt.err))
```

with

```go
			require.Equal(t, tt.want, turnEndForError(tt.err, tt.deadlinePassed))
```

In `TestTurnEndForStep`, replace

```go
		{name: "tool call cut short by the provider", step: stepEnding{finish: message.FinishReasonError, hasToolCalls: true}, want: message.TurnEndProviderError},
	}
```

with

```go
		{name: "tool call cut short by the provider", step: stepEnding{finish: message.FinishReasonError, hasToolCalls: true}, want: message.TurnEndProviderError},
		{name: "step budget spent", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, stepsSpent: true}, want: message.TurnEndStepLimit},
		// The model had already stopped on the step that spent the budget.
		{name: "final answer on the step that spends the budget", step: stepEnding{finish: message.FinishReasonEndTurn, hasText: true, stepsSpent: true}, want: message.TurnEndCompleted},
		{name: "provider ended the step that spends the budget", step: stepEnding{finish: message.FinishReasonEndTurn, hasToolCalls: true, hasText: true, stepsSpent: true}, want: message.TurnEndCompleted},
		{name: "halting tool on the step that spends the budget", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, toolStopped: true, stepsSpent: true}, want: message.TurnEndToolStop},
		{name: "loop on the step that spends the budget", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, loopRepeated: true, stepsSpent: true}, want: message.TurnEndLoopGuard},
		{name: "guard on the step that spends the budget", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, guardTripped: true, stepsSpent: true}, want: message.TurnEndContextGuard},
		{name: "tool call cut at the output limit as the budget is spent", step: stepEnding{finish: message.FinishReasonMaxTokens, hasToolCalls: true, stepsSpent: true}, want: message.TurnEndMaxTokens},
	}
```

In `TestTurnEndAfterCompaction`, replace

```go
	tests := []struct {
		name         string
		end          message.TurnEnd
		summarizeErr error
		want         message.TurnEnd
	}{
```

with

```go
	tests := []struct {
		name           string
		end            message.TurnEnd
		summarizeErr   error
		deadlinePassed bool
		want           message.TurnEnd
	}{
		{name: "guard compaction stopped by the deadline", end: message.TurnEndContextGuard, summarizeErr: context.Canceled, deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "overflow compaction failed after the deadline", end: message.TurnEndContextOverflow, summarizeErr: failed, deadlinePassed: true, want: message.TurnEndTimeLimit},
		// A hook's decision is not the deadline's.
		{name: "guard compaction blocked after the deadline", end: message.TurnEndContextGuard, summarizeErr: blocked, deadlinePassed: true, want: message.TurnEndCompactionBlocked},
		{name: "compaction after a completed turn stopped by the deadline", end: message.TurnEndCompleted, summarizeErr: context.Canceled, deadlinePassed: true, want: message.TurnEndCompleted},
		{name: "compaction after a step limit failed", end: message.TurnEndStepLimit, summarizeErr: failed, want: message.TurnEndStepLimit},
```

and replace

```go
			require.Equal(t, tt.want, turnEndAfterCompaction(tt.end, tt.summarizeErr))
```

with

```go
			require.Equal(t, tt.want, turnEndAfterCompaction(tt.end, tt.summarizeErr, tt.deadlinePassed))
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'TestTurnEndForError|TestTurnEndForStep|TestTurnEndAfterCompaction' -v`
Expected: FAIL to compile with `undefined: message.TurnEndTimeLimit`, `undefined: message.TurnEndStepLimit`, `unknown field stepsSpent in struct literal of type stepEnding` and `too many arguments in call to turnEndForError`.

- [ ] **Step 3: Write the values and the precedence**

In `internal/message/content.go`, replace

```go
	// The compaction the run required failed.
	TurnEndCompactionFailed TurnEnd = "compaction_failed"
```

with

```go
	// The compaction the run required failed.
	TurnEndCompactionFailed TurnEnd = "compaction_failed"
	// The caller's step budget for the session tree was spent.
	TurnEndStepLimit TurnEnd = "step_limit"
	// The caller's deadline for the session tree passed.
	TurnEndTimeLimit TurnEnd = "time_limit"
```

In `internal/agent/turn_end.go`, replace

```go
	loopRepeated bool
}
```

with

```go
	loopRepeated bool
	stepsSpent   bool
}
```

Replace

```go
// turnEndForError decides how a run ended when its step loop returned an
// error. The order matters: the repetition detector stops a run by
// cancelling it, so it is tested before the cancel.
func turnEndForError(err error) message.TurnEnd {
```

with

```go
// turnEndForError decides how a run ended when its step loop returned an
// error. The order matters: once the deadline of the run's session tree
// has passed, the run was stopped by it, whatever error the cancel left;
// and the repetition detector stops a run by cancelling it, so it is
// tested before the cancel.
func turnEndForError(err error, deadlinePassed bool) message.TurnEnd {
```

Replace

```go
	switch {
	case errors.Is(err, errRepetitionDetected):
```

with

```go
	switch {
	case deadlinePassed:
		return message.TurnEndTimeLimit
	case errors.Is(err, errRepetitionDetected):
```

Replace

```go
	case step.finish == message.FinishReasonError:
		return message.TurnEndProviderError
	default:
		// The provider ended a step that called tools, and no guard
```

with

```go
	case step.finish == message.FinishReasonError:
		return message.TurnEndProviderError
	case step.stepsSpent && step.finish == message.FinishReasonToolUse:
		// The loop would have gone on to run the next step.
		return message.TurnEndStepLimit
	default:
		// The provider ended a step that called tools, and no guard
```

Replace

```go
// turnEndAfterCompaction applies the rules that follow the precedence:
// only a compaction the run required changes how the run ended. A cancel
// during that compaction reads canceled, since the run was stopped, not
// failed.
func turnEndAfterCompaction(end message.TurnEnd, summarizeErr error) message.TurnEnd {
	if end != message.TurnEndContextGuard && end != message.TurnEndContextOverflow {
		return end
	}
	switch {
	case errors.Is(summarizeErr, context.Canceled):
		return message.TurnEndCanceled
	case errors.Is(summarizeErr, ErrCompactionBlocked):
		return message.TurnEndCompactionBlocked
```

with

```go
// turnEndAfterCompaction applies the rules that follow the precedence:
// only a compaction the run required changes how the run ended. That
// compaction stopped after the tree's deadline reads time_limit, and a
// cancel during it reads canceled, since the run was stopped, not failed.
func turnEndAfterCompaction(end message.TurnEnd, summarizeErr error, deadlinePassed bool) message.TurnEnd {
	if end != message.TurnEndContextGuard && end != message.TurnEndContextOverflow {
		return end
	}
	switch {
	case errors.Is(summarizeErr, ErrCompactionBlocked):
		return message.TurnEndCompactionBlocked
	case summarizeErr != nil && deadlinePassed:
		return message.TurnEndTimeLimit
	case errors.Is(summarizeErr, context.Canceled):
		return message.TurnEndCanceled
```

(The two lines after it, `case summarizeErr != nil:` returning `TurnEndCompactionFailed` and `default:` returning `end`, are unchanged.)

- [ ] **Step 4: Pass the tree's limits to the precedence**

In `internal/agent/agent.go`, in the `SessionAgentCall` struct, replace

```go
	// Accepted, when non-nil, is the accept reservation taken by
```

with

```go
	// limits are the step budget and the deadline of the session tree the
	// call runs in, found when the run starts. Nil has no limit.
	limits *treeLimits
	// Accepted, when non-nil, is the accept reservation taken by
```

In `persistCanceledTurn`, replace

```go
	assistant.SetTurnEnd(message.TurnEndCanceled)
	return a.messages.Update(writeCtx, assistant)
```

with

```go
	assistant.SetTurnEnd(turnEndForError(context.Canceled, call.limits.deadlinePassed()))
	return a.messages.Update(writeCtx, assistant)
```

In `sessionAgent.Run`, replace

```go
			currentAssistant.SetTurnEnd(turnEndForError(err))
```

with

```go
			currentAssistant.SetTurnEnd(turnEndForError(err, call.limits.deadlinePassed()))
```

Replace

```go
	} else if currentAssistant != nil {
		turnEnd = turnEndForStep(lastStepEnding(currentAssistant, stepResults, shouldSummarize))
	}
```

with

```go
	} else if currentAssistant != nil {
		ending := lastStepEnding(currentAssistant, stepResults, shouldSummarize)
		ending.stepsSpent = call.limits.stepsSpent()
		turnEnd = turnEndForStep(ending)
	}
```

Replace

```go
		turnEnd = turnEndAfterCompaction(turnEnd, summarizeErr)
```

with

```go
		turnEnd = turnEndAfterCompaction(turnEnd, summarizeErr, call.limits.deadlinePassed())
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go build ./... && go test ./internal/agent/ -run 'TestTurnEnd|TestLastStepEnding' -v`
Expected: build prints nothing; PASS, including every row of `TestTurnEnd_Runs` (nothing sets a limit yet, so every run ends as before).

- [ ] **Step 6: Format and commit**

```bash
/home/pbtrudel/.local/bin/gofumpt -w internal/message/content.go internal/agent/turn_end.go internal/agent/turn_end_test.go internal/agent/agent.go
git add internal/message/content.go internal/agent/turn_end.go internal/agent/turn_end_test.go internal/agent/agent.go
git commit -m "feat(agent): add the step_limit and time_limit turn ends"
```

---

### Task 3: Every run in a tree draws on its limits

**Files:**
- Modify: `internal/agent/run_limits.go` (import block; new `errTreeLimitReached` before `treeLimits`)
- Modify: `internal/agent/agent.go` (`sessionAgent` struct after `titles`; `SessionAgentOptions` after `titles`; `NewSessionAgent`; `sessionAgent.Run`: after the `Ephemeral` check, the `PrepareStep` closure, the `StopWhen` list)
- Modify: `internal/agent/coordinator.go` (`coordinator` struct, after `runEnded`; the `NewSessionAgent` call in `buildAgentWithModels`)
- Modify: `internal/agent/agentic_fetch_tool.go` (its `NewSessionAgent` call)
- Test: `internal/agent/run_limits_run_test.go` (new)

**Interfaces:**
- Consumes: Task 1's `runLimits.set`, `runLimits.forSession`, `treeLimits.takeStep`, `stepsSpent`, `reached`, `deadlinePassed`; Task 2's `SessionAgentCall.limits`; test helpers `newCompactionAgent` (`internal/agent/compaction_test.go`), `scriptedModel`, `scriptedCall`, `answer`, `callTool`, `turnEnds` (`internal/agent/turn_end_run_test.go`), `recordingTool`, `gatedModel` and `finishStreamModel` (item 4, `internal/agent/session_tree_test.go`, and `internal/agent/dispatch_cancel_test.go`).
- Produces:
  - `var errTreeLimitReached` (unexported error returned by `sessionAgent.Run` for a refused run)
  - `sessionAgent.limits *runLimits`, `SessionAgentOptions.limits *runLimits` (unexported, like `titles`)
  - `coordinator.limits runLimits`, shared with every agent the coordinator builds
  - test helper `func stepCount(t *testing.T, env fakeEnv, sessionID string) int`, used again in Task 5

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/run_limits_run_test.go`:

```go
package agent

import (
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
)

// stepCount counts the agent steps of a session: its assistant rows other
// than compaction summaries.
func stepCount(t *testing.T, env fakeEnv, sessionID string) int {
	t.Helper()
	msgs, err := env.messages.List(t.Context(), sessionID)
	require.NoError(t, err)
	steps := 0
	for _, msg := range msgs {
		if msg.Role == message.Assistant && !msg.IsSummaryMessage {
			steps++
		}
	}
	return steps
}

// A model that keeps calling tools is stopped once the tree's budget is
// spent, and no run of the tree starts after that.
func TestStepLimitStopsTheTree(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, &scriptedModel{script: []scriptedCall{callTool("alpha", 1_000)}}, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{MaxSteps: 3})

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)
	require.Equal(t, []message.TurnEnd{message.TurnEndStepLimit}, turnEnds(t, env, sess.ID))
	require.Equal(t, 3, stepCount(t, env, sess.ID))

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "again"})
	require.ErrorIs(t, err, errTreeLimitReached)
	child, err := env.sessions.CreateTaskSession(t.Context(), "call-1", sess.ID, "Subagent")
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: child.ID, Prompt: "go"})
	require.ErrorIs(t, err, errTreeLimitReached)
	require.Equal(t, 3, stepCount(t, env, sess.ID))
	require.Zero(t, stepCount(t, env, child.ID))
}

// A run continued after the context guard is a new run, but the budget is
// the tree's, so the continuation gets only what is left of it.
func TestStepLimitSpansAContinuation(t *testing.T) {
	env := testEnv(t)
	summary := answer("# Conversation Summary\n\nAll good.", fantasy.FinishReasonStop, 1_000)
	model := &scriptedModel{script: []scriptedCall{callTool("alpha", 190_000), summary, callTool("alpha", 1_000)}}
	agent := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{MaxSteps: 3})

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)
	require.Equal(t, []message.TurnEnd{message.TurnEndContextGuard, message.TurnEndStepLimit}, turnEnds(t, env, sess.ID))
	require.Equal(t, 3, stepCount(t, env, sess.ID), "the summary does not draw on the budget")
}

// The command cancels a tree whose deadline passed. That cancel carries no
// cause, so the run reads time_limit from the deadline, not canceled.
func TestTimeLimitOutranksTheCancel(t *testing.T) {
	env := testEnv(t)
	model := &gatedModel{finishStreamModel: finishStreamModel{text: "never"}, release: make(chan struct{})}
	agent := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	tree := agent.limits.set(sess.ID, RunLimits{Timeout: 100 * time.Millisecond})

	done := make(chan error, 1)
	go func() {
		_, runErr := agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		done <- runErr
	}()
	require.Eventually(t, tree.deadlinePassed, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return agent.IsSessionBusy(sess.ID) }, 5*time.Second, 10*time.Millisecond)
	agent.Cancel(sess.ID)
	select {
	case runErr := <-done:
		require.Error(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not stop when it was cancelled")
	}
	require.Equal(t, []message.TurnEnd{message.TurnEndTimeLimit}, turnEnds(t, env, sess.ID))

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "again"})
	require.ErrorIs(t, err, errTreeLimitReached)
}

// A model that answers on the step that spends the budget had already
// stopped: the run is completed, not cut short.
func TestStepLimitLeavesAFinalAnswer(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, &scriptedModel{script: []scriptedCall{answer("done", fantasy.FinishReasonStop, 1_000)}}, nil, false, &recordingTool{name: "alpha"})
	agent.limits = &runLimits{}
	sess, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	agent.limits.set(sess.ID, RunLimits{MaxSteps: 1})

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)
	require.Equal(t, []message.TurnEnd{message.TurnEndCompleted}, turnEnds(t, env, sess.ID))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agent/ -run 'TestStepLimit|TestTimeLimitOutranksTheCancel' -v`
Expected: FAIL to compile with `agent.limits undefined (type *sessionAgent has no field or method limits)` and `undefined: errTreeLimitReached`.

- [ ] **Step 3: Write the implementation**

In `internal/agent/run_limits.go`, replace

```go
	"context"
	"sync"
```

with

```go
	"context"
	"errors"
	"sync"
```

and replace

```go
// treeLimits is one session tree's limits and the steps its runs took.
```

with

```go
// errTreeLimitReached refuses a run whose session tree has spent its step
// budget or passed its deadline.
var errTreeLimitReached = errors.New("the session tree reached its step or time limit")

// treeLimits is one session tree's limits and the steps its runs took.
```

In `internal/agent/agent.go`, in the `sessionAgent` struct, replace

```go
	// titles tracks in-flight title generation so CancelAll can drain it.
	titles *titleTracker
```

with

```go
	// titles tracks in-flight title generation so CancelAll can drain it.
	titles *titleTracker
	// limits are the limits callers set on session trees. Nil holds none.
	limits *runLimits
```

In `SessionAgentOptions`, replace

```go
	// titles is shared by a coordinator's agents so one drain covers all.
	titles *titleTracker
}
```

with

```go
	// titles is shared by a coordinator's agents so one drain covers all.
	titles *titleTracker
	// limits is shared by a coordinator's agents, so every run in a session
	// tree draws on the limits set on it.
	limits *runLimits
}
```

In `NewSessionAgent`, replace

```go
		titles:                 cmp.Or(opts.titles, new(titleTracker)),
```

with

```go
		titles:                 cmp.Or(opts.titles, new(titleTracker)),
		limits:                 opts.limits,
```

In `sessionAgent.Run`, replace

```go
	if call.Ephemeral {
		return a.runEphemeral(ctx, call)
	}

	if call.Channel != "" && call.channelMeta == nil {
```

with

```go
	if call.Ephemeral {
		return a.runEphemeral(ctx, call)
	}

	// Once its session tree has spent its step budget or passed its
	// deadline, no run in the tree starts, a teammate's included.
	if call.limits == nil {
		call.limits = a.limits.forSession(ctx, a.sessions, call.SessionID)
	}
	if call.limits.reached() {
		slog.Info("Run refused: its session tree reached a limit", "session_id", call.SessionID)
		return nil, errTreeLimitReached
	}

	if call.Channel != "" && call.channelMeta == nil {
```

Replace (in the `PrepareStep` closure of the agent stream)

```go
			modelSteps++
			base := options.Messages
```

with

```go
			modelSteps++
			call.limits.takeStep()
			base := options.Messages
```

Replace (the end of the `StopWhen` list)

```go
			func(steps []fantasy.StepResult) bool {
				return hasRepeatedToolCalls(steps, loopDetectionWindowSize, loopDetectionMaxRepeats)
			},
		},
	})
```

with

```go
			func(steps []fantasy.StepResult) bool {
				return hasRepeatedToolCalls(steps, loopDetectionWindowSize, loopDetectionMaxRepeats)
			},
			// Once the tree's step budget is spent, every run in the
			// tree stops after its current step.
			func(_ []fantasy.StepResult) bool {
				return call.limits.stepsSpent()
			},
		},
	})
```

In `internal/agent/coordinator.go`, in the `coordinator` struct, replace

```go
	// runEnded signals every return of a run started here, so a wait for a
	// session tree reads the tree again after a run that woke the session.
	runEnded treeSignal
}
```

with

```go
	// runEnded signals every return of a run started here, so a wait for a
	// session tree reads the tree again after a run that woke the session.
	runEnded treeSignal

	// limits are the limits callers set on session trees, shared by every
	// agent this coordinator builds.
	limits runLimits
}
```

In `buildAgentWithModels`, replace

```go
		Hooks:                c.hookRunner,
		titles:               &c.titles,
	})
```

with

```go
		Hooks:                c.hookRunner,
		titles:               &c.titles,
		limits:               &c.limits,
	})
```

In `internal/agent/agentic_fetch_tool.go`, replace

```go
				Tools:                fetchTools,
				titles:               &c.titles,
			})
```

with

```go
				Tools:                fetchTools,
				titles:               &c.titles,
				limits:               &c.limits,
			})
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go build ./... && go test ./internal/agent/ -run 'TestStepLimit|TestTimeLimitOutranksTheCancel|TestTreeLimits|TestRunLimits' -v`
Expected: build prints nothing; PASS.

Then the race check and the whole package: `go test -race -count=3 ./internal/agent/ -run 'TestStepLimit|TestTimeLimit'` and `go test ./internal/agent/`
Expected: `ok` both times and no `DATA RACE` report. If `-race` is unavailable (no cgo toolchain), say so in the report.

- [ ] **Step 5: Format and commit**

```bash
/home/pbtrudel/.local/bin/gofumpt -w internal/agent/run_limits.go internal/agent/run_limits_run_test.go internal/agent/agent.go internal/agent/coordinator.go internal/agent/agentic_fetch_tool.go
git add internal/agent/run_limits.go internal/agent/run_limits_run_test.go internal/agent/agent.go internal/agent/coordinator.go internal/agent/agentic_fetch_tool.go
git commit -m "feat(agent): stop the runs of a session tree at its limits"
```

---

### Task 4: Part 1 verification

**Files:**
- None changed.

**Interfaces:**
- Consumes: Tasks 1 to 3.
- Produces: the evidence for the Part 1 pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/agent/ ./internal/message/ && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m ./internal/agent/... ./internal/message/...`, then `./scripts/check_log_capitalization.sh`, then `/home/pbtrudel/.local/bin/gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD` and `git diff origin/main...HEAD | grep -c "$(printf '\342\200\224')"` (the em dash)
Expected: about 400 changed lines in `internal/agent/run_limits.go`, `internal/agent/run_limits_test.go`, `internal/agent/run_limits_run_test.go`, `internal/agent/turn_end.go`, `internal/agent/turn_end_test.go`, `internal/agent/agent.go`, `internal/agent/coordinator.go`, `internal/agent/agentic_fetch_tool.go` and `internal/message/content.go` only; the count is `0`. Report the real numbers in the pull request.

- [ ] **Step 4: Live check: the command is unchanged**

Never open or print `crush.json` or any `crushrc` file.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" . && mkdir "$scratch/proj"
"$scratch/crush" run --json -y -q -c "$scratch/proj" "List the files in this directory with the bash tool, then reply with the single word done."; echo "exit $?"
rm -rf "$scratch"
```

Expected: one line of JSON with `"reason":"completed"` and `"tool_calls"` of at least 1, then `exit 0`, exactly as before this part: nothing sets a limit yet.

The pull request is opened with the `pr-discipline` skill only when the user asks for it.

---

# Part 2: the command sets the limits

Branch `w0-run-limits-2`, started after Part 1 has merged. Base: `origin/main` with Part 1 merged; if `internal/agent/run_limits.go` does not define `errTreeLimitReached`, stop and report.

### Task 5: The coordinator stops a tree at its deadline

**Files:**
- Modify: `internal/agentteam/team.go` (new `CancelTree` directly before `Changed`)
- Modify: `internal/agent/session_tree.go` (import block; new code after `var _ TreeWatcher = (*coordinator)(nil)`)
- Test: `internal/agentteam/tree_test.go` (append), `internal/agent/session_tree_limits_test.go` (new)

**Interfaces:**
- Consumes: Part 1's `runLimits.set`, `RunLimits`, `stepCount` test helper; item 4's `waitForTree`, `requireTreeIdle`, `gatedModel`, `teamRunner`, `agentteam.Service.Tree`; existing `coordinator.Cancel(sessionID)`, `newGateTestCoordinator`, `coordinatorModels`, `SpawnTeammateToolName`, `ListAgentsToolName`; agentteam test helpers `newTestService`, `fakeRunner.finish`.
- Produces:
  - `func (s *agentteam.Service) CancelTree(sessionID string)`
  - `type TreeLimiter interface { LimitSessionTree(sessionID string, limits RunLimits) }`
  - `func (c *coordinator) LimitSessionTree(sessionID string, limits RunLimits)`
  - unexported `func (c *coordinator) stopSessionTree(sessionID string)`

- [ ] **Step 1: Write the failing tests**

Append to `internal/agentteam/tree_test.go`:

```go
// CancelTree stops the turns of one Lead session's team and leaves another
// session's teammates running.
func TestCancelTreeStopsTheSessionsTeam(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	for _, req := range []SpawnRequest{
		{Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a"},
		{Name: "writer", Description: "d", Prompt: "p", ParentID: "session-a"},
		{Name: "other", Description: "d", Prompt: "p", ParentID: "session-b"},
	} {
		_, err := service.SpawnTeammate(context.Background(), "lead", req)
		require.NoError(t, err)
	}

	service.CancelTree("session-a")
	require.Eventually(t, func() bool {
		return service.Tree("session-a").Active == 0
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 2, service.Tree("session-a").UnreadByLead, "each cancelled turn reports")
	require.Equal(t, 1, service.Tree("session-b").Active)

	runner.finish("other")
	require.Eventually(t, func() bool {
		return service.Tree("session-b").Active == 0
	}, time.Second, 10*time.Millisecond)
}
```

Create `internal/agent/session_tree_limits_test.go`:

```go
package agent

import (
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
)

// startLimitedTeam runs a Lead turn that spawns one teammate and then ends,
// with limits set on the Lead's session first, as `crush run` sets them.
// The teammate runs on teammateModel. It returns the coordinator, the
// Lead's session id and the teammate's.
func startLimitedTeam(t *testing.T, limits RunLimits, teammateModel fantasy.LanguageModel) (*coordinator, string, string) {
	t.Helper()
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())
	// Cancel reads the detached subagents, which the gate coordinator
	// leaves unset.
	coord.dispatched = csync.NewMap[string, *dispatchedAgent]()
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, nil)
	require.NoError(t, err)
	service.SetRunner(&teamRunner{c: coord})
	coord.team = service

	spawn := scriptedCall{parts: []fantasy.StreamPart{
		{
			Type:          fantasy.StreamPartTypeToolCall,
			ID:            "call-spawn",
			ToolCallName:  SpawnTeammateToolName,
			ToolCallInput: `{"name":"worker","description":"Does the work","prompt":"Do the work."}`,
		},
		{
			Type:         fantasy.StreamPartTypeFinish,
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        fantasy.Usage{InputTokens: 1000, OutputTokens: 10, TotalTokens: 1010},
		},
	}}
	leadModel := coord.mainModels().Large
	leadModel.CatwalkCfg.ContextWindow = 200_000
	worker := leadModel
	leadModel.Model = &scriptedModel{script: []scriptedCall{spawn, answer("The worker is on it.", fantasy.FinishReasonStop, 1000)}}
	worker.Model = teammateModel
	coord.models.Set(coordinatorModels{large: leadModel, subLarge: worker, small: leadModel})

	lead := coord.coderAgent()
	leadTools, err := coord.buildTools(t.Context(), coord.cfg.Config().Agents[config.AgentCoder], roleLead, lead.Model)
	require.NoError(t, err)
	lead.SetTools(leadTools)

	sess, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	coord.LimitSessionTree(sess.ID, limits)
	_, err = lead.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "Start the worker.", MaxOutputTokens: 1000})
	require.NoError(t, err)
	teammates := service.Tree(sess.ID).Sessions
	require.Len(t, teammates, 1)
	return coord, sess.ID, teammates[0]
}

// The deadline passes while the command waits for a teammate. The
// teammate's detached turn is cancelled through the Team service, reads
// time_limit, and the wait ends.
func TestTimeLimitStopsATeammate(t *testing.T) {
	never := &gatedModel{finishStreamModel: finishStreamModel{text: "never"}, release: make(chan struct{})}
	coord, leadID, teammateID := startLimitedTeam(t, RunLimits{Timeout: time.Second}, never)
	env := fakeEnv{messages: coord.messages}

	tree := requireTreeIdle(t, waitForTree(t, coord, leadID))
	require.Equal(t, 1, tree.Team.UnreadByLead, "the stopped teammate still reports")
	require.Equal(t, []message.TurnEnd{message.TurnEndCompleted}, turnEnds(t, env, leadID))
	require.Equal(t, []message.TurnEnd{message.TurnEndTimeLimit}, turnEnds(t, env, teammateID))
}

// The budget is the tree's: the teammate draws on what the Lead left of it
// and stops once it is spent, after the Lead's turn completed.
func TestStepLimitSpansATeammate(t *testing.T) {
	busy := &scriptedModel{script: []scriptedCall{callTool(ListAgentsToolName, 1000)}}
	coord, leadID, teammateID := startLimitedTeam(t, RunLimits{MaxSteps: 4}, busy)
	env := fakeEnv{messages: coord.messages}

	requireTreeIdle(t, waitForTree(t, coord, leadID))
	require.Equal(t, []message.TurnEnd{message.TurnEndCompleted}, turnEnds(t, env, leadID))
	require.Equal(t, []message.TurnEnd{message.TurnEndStepLimit}, turnEnds(t, env, teammateID))
	// Runs that step at once each finish the step they started, so the
	// tree can go one step past the budget, never more.
	steps := stepCount(t, env, leadID) + stepCount(t, env, teammateID)
	require.GreaterOrEqual(t, steps, 4)
	require.LessOrEqual(t, steps, 5)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/agentteam/ -run 'TestCancelTree' -v` and `go test ./internal/agent/ -run 'TestTimeLimitStopsATeammate|TestStepLimitSpansATeammate' -v`
Expected: FAIL to compile with `service.CancelTree undefined (type *Service has no field or method CancelTree)` and `coord.LimitSessionTree undefined (type *coordinator has no field or method LimitSessionTree)`.

- [ ] **Step 3: Write the implementation**

In `internal/agentteam/team.go`, replace

```go
// Changed returns a channel that is closed at the next change to the
```

with

```go
// CancelTree cancels the running turns of the team that sessionID leads.
// The members stay on the roster; each turn settles and reports as usual.
func (s *Service) CancelTree(sessionID string) {
	s.mu.Lock()
	var runs []Run
	for _, member := range s.visibleMembersLocked(s.scopeLocked(leadName, sessionID)) {
		if run, ok := s.runs[member.Name]; ok {
			runs = append(runs, run)
		}
	}
	s.mu.Unlock()
	for _, run := range runs {
		run.Cancel()
	}
}

// Changed returns a channel that is closed at the next change to the
```

In `internal/agent/session_tree.go`, replace

```go
import (
	"context"
	"sync"
```

with

```go
import (
	"context"
	"log/slog"
	"sync"
	"time"
```

and replace

```go
var _ TreeWatcher = (*coordinator)(nil)
```

with

```go
var _ TreeWatcher = (*coordinator)(nil)

// TreeLimiter is implemented by coordinators that can bound the work of a
// session tree.
type TreeLimiter interface {
	// LimitSessionTree sets the limits that every run in the tree of
	// sessionID draws on from now. When the timeout passes, every run in
	// the tree is cancelled, its teammates' included.
	LimitSessionTree(sessionID string, limits RunLimits)
}

var _ TreeLimiter = (*coordinator)(nil)

// LimitSessionTree implements TreeLimiter. The deadline is counted from
// now. A run of the tree that would start once a limit is reached is
// refused, so a stopped tree goes idle and a wait for it ends.
func (c *coordinator) LimitSessionTree(sessionID string, limits RunLimits) {
	c.limits.set(sessionID, limits)
	if limits.Timeout > 0 {
		time.AfterFunc(limits.Timeout, func() { c.stopSessionTree(sessionID) })
	}
}

// stopSessionTree cancels every run in the tree of sessionID: the
// session's own with its subagents, its teammates' through the Team
// service, which is the only way to reach their detached contexts, and a
// run a background job woke on a teammate's session.
func (c *coordinator) stopSessionTree(sessionID string) {
	slog.Info("Session tree passed its deadline", "session_id", sessionID)
	c.Cancel(sessionID)
	if c.team == nil {
		return
	}
	c.team.CancelTree(sessionID)
	for _, id := range c.team.Tree(sessionID).Sessions {
		c.Cancel(id)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go build ./... && go test ./internal/agentteam/ -run 'TestCancelTree|TestTree' -v && go test ./internal/agent/ -run 'TestTimeLimitStopsATeammate|TestStepLimitSpansATeammate|TestWaitForSessionTree' -v`
Expected: build prints nothing; PASS.

If `TestStepLimitSpansATeammate` fails for a reason other than the code under test (for example the teammate's palette lacks `list_agents`), fix the test setup, not the production code, and say so in the report: read `buildTools` and `effectiveToolNames` in `internal/agent/coordinator.go`.

Then the race check and both packages: `go test -race -count=3 ./internal/agent/ ./internal/agentteam/ -run 'TestTimeLimit|TestStepLimit|TestCancelTree'` and `go test ./internal/agent/ ./internal/agentteam/`
Expected: `ok` and no `DATA RACE` report.

- [ ] **Step 5: Format and commit**

```bash
/home/pbtrudel/.local/bin/gofumpt -w internal/agentteam/team.go internal/agentteam/tree_test.go internal/agent/session_tree.go internal/agent/session_tree_limits_test.go
git add internal/agentteam/team.go internal/agentteam/tree_test.go internal/agent/session_tree.go internal/agent/session_tree_limits_test.go
git commit -m "feat(agent): cancel a session tree when its deadline passes"
```

---

### Task 6: A limit is the run's reason and exits 2

**Files:**
- Modify: `internal/app/run_exit.go` (`runExitCode`, the code-2 case)
- Modify: `internal/app/run_json.go` (`summarizeRun`, after the loop)
- Test: `internal/app/run_exit_test.go`, `internal/app/run_json_test.go` (append)

**Interfaces:**
- Consumes: Part 1's `message.TurnEndStepLimit`, `message.TurnEndTimeLimit`; item 5's `summarizeRun`, `runExitCode`, `runExitError` and test helpers `userRow`, `stepRow`.
- Produces: no new names; `summarizeRun` and `runExitCode` gain behavior.

- [ ] **Step 1: Write the failing tests**

In `internal/app/run_exit_test.go`, replace

```go
		{message.TurnEndCompactionBlocked, 2},
```

with

```go
		{message.TurnEndCompactionBlocked, 2},
		{message.TurnEndStepLimit, 2},
		{message.TurnEndTimeLimit, 2},
```

Append to `internal/app/run_json_test.go`:

```go
// A limit that stopped a teammate after the session completed is the
// reason; the text stays the session's own.
func TestSummarizeRunReportsALimitInTheTree(t *testing.T) {
	t.Parallel()
	rows := []message.Message{
		userRow("root", "Fix the bug.", false),
		stepRow("root", "Started the worker.", 0, message.TurnEndCompleted, nil),
		userRow("team-1", "Do the work.", false),
		stepRow("team-1", "", 1, message.TurnEndStepLimit, nil),
	}

	result := summarizeRun("root", rows)
	require.Equal(t, message.TurnEndStepLimit, result.Reason)
	require.Equal(t, "Started the worker.", result.Text)

	rows = append(rows,
		userRow("team-2", "Review it.", false),
		stepRow("team-2", "", 1, message.TurnEndTimeLimit, nil),
	)
	require.Equal(t, message.TurnEndTimeLimit, summarizeRun("root", rows).Reason, "the deadline is read first")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/app/ -run 'TestRunExitCode|TestSummarizeRunReportsALimitInTheTree' -v`
Expected: FAIL: `TestRunExitCode/step_limit` and `TestRunExitCode/time_limit` with `expected: 2, actual: 3`, and `TestSummarizeRunReportsALimitInTheTree` with `expected: "step_limit", actual: "completed"`.

- [ ] **Step 3: Write the implementation**

In `internal/app/run_exit.go`, replace

```go
		message.TurnEndCompactionBlocked:
		return 2
```

with

```go
		message.TurnEndCompactionBlocked, message.TurnEndStepLimit,
		message.TurnEndTimeLimit:
		return 2
```

In `internal/app/run_json.go`, at the end of `summarizeRun`, replace

```go
		opened = false
	}
	result.Tree.Sessions = len(sessions)
	return result
}
```

with

```go
		opened = false
	}
	// A limit that stopped any run in the tree is the reason, so a turn
	// it cut short in a child session is not reported as completed. The
	// deadline is read first, as in the turn-end rules.
	for _, limit := range []message.TurnEnd{message.TurnEndTimeLimit, message.TurnEndStepLimit} {
		if result.Tree.TurnEnds[limit] > 0 {
			result.Reason = limit
			break
		}
	}
	result.Tree.Sessions = len(sessions)
	return result
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app/ -run 'TestRunExitCode|TestSummarizeRun' -v`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
/home/pbtrudel/.local/bin/gofumpt -w internal/app/run_exit.go internal/app/run_exit_test.go internal/app/run_json.go internal/app/run_json_test.go
git add internal/app/run_exit.go internal/app/run_exit_test.go internal/app/run_json.go internal/app/run_json_test.go
git commit -m "feat(run): report a step or time limit as the run's reason"
```

---

### Task 7: `--max-steps` and `--timeout`

**Files:**
- Modify: `internal/app/run_wait.go` (`runAndWait`, its doc comment and signature)
- Modify: `internal/app/app.go` (`RunNonInteractive`, its doc comment, signature and the `runAndWait` call)
- Modify: `internal/cmd/run.go` (import block; `Example` text; `RunE` flag block and the `RunNonInteractive` call; `init`; `refuseOnServer`; new `checkRunLimits`)
- Test: `internal/app/run_wait_test.go` (two calls and an appended test), `internal/cmd/run_json_flag_test.go` (three calls and the import block), `internal/cmd/run_limits_flag_test.go` (new)

**Interfaces:**
- Consumes: Task 5's `agent.TreeLimiter`; Part 1's `agent.RunLimits{MaxSteps int; Timeout time.Duration}`; item 4's `runAndWait` and test double `plainCoordinator` (`internal/app/run_wait_test.go`); item 5's `refuseOnServer` and `RunNonInteractive(..., useLast, asJSON bool)`; `useClientServer()` in `internal/cmd/root.go`.
- Produces:
  - `func runAndWait(ctx context.Context, coordinator agent.Coordinator, sessionID, prompt string, limits agent.RunLimits) runResponse`
  - `RunNonInteractive(ctx, output, prompt, largeModel, smallModel, reasoningEffort string, hideSpinner bool, continueSessionID string, useLast, asJSON bool, limits agent.RunLimits) error`
  - `func refuseOnServer(jsonOutput bool, limits agent.RunLimits) error` and `func checkRunLimits(limits agent.RunLimits) error` in package `cmd`; the `--max-steps` (int) and `--timeout` (duration) flags on `runCmd`

- [ ] **Step 1: Write the failing tests**

In `internal/app/run_wait_test.go`, replace

```go
runAndWait(ctx, coordinator, "session-root", "go")
```

with

```go
runAndWait(ctx, coordinator, "session-root", "go", agent.RunLimits{})
```

and replace

```go
runAndWait(t.Context(), plainCoordinator{}, "session-root", "go")
```

with

```go
runAndWait(t.Context(), plainCoordinator{}, "session-root", "go", agent.RunLimits{})
```

Append to `internal/app/run_wait_test.go`:

```go
// limitCoordinator records the limits the command set on the tree and how
// many prompts had run by then.
type limitCoordinator struct {
	plainCoordinator
	runs      int
	limits    []agent.RunLimits
	runsFirst int
}

func (c *limitCoordinator) Run(context.Context, string, string, ...message.Attachment) (*fantasy.AgentResult, error) {
	c.runs++
	return &fantasy.AgentResult{}, nil
}

func (c *limitCoordinator) LimitSessionTree(_ string, limits agent.RunLimits) {
	c.limits = append(c.limits, limits)
	c.runsFirst = c.runs
}

// The limits are on the tree before the prompt runs, so the timeout counts
// from the prompt and the first step already draws on the budget.
func TestRunAndWaitLimitsTheTreeFirst(t *testing.T) {
	t.Parallel()
	limits := agent.RunLimits{MaxSteps: 5, Timeout: time.Minute}
	limited := &limitCoordinator{}
	runAndWait(t.Context(), limited, "session-root", "go", limits)
	require.Equal(t, []agent.RunLimits{limits}, limited.limits)
	require.Zero(t, limited.runsFirst, "the limits were set before the prompt ran")
	require.Equal(t, 1, limited.runs)

	unlimited := &limitCoordinator{}
	runAndWait(t.Context(), unlimited, "session-root", "go", agent.RunLimits{})
	require.Empty(t, unlimited.limits, "no limit, nothing to set")
}
```

In `internal/cmd/run_json_flag_test.go`, replace

```go
	"github.com/stretchr/testify/require"
)
```

with

```go
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent"
)
```

then replace both occurrences of

```go
refuseOnServer(true)
```

with

```go
refuseOnServer(true, agent.RunLimits{})
```

and replace

```go
refuseOnServer(false)
```

with

```go
refuseOnServer(false, agent.RunLimits{})
```

Create `internal/cmd/run_limits_flag_test.go`:

```go
package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent"
)

// The client/server path does not implement the limits, so their flags are
// refused there before anything is read or started.
func TestRunRefusesLimitsAgainstAServer(t *testing.T) {
	for name, want := range map[string][2]string{"max-steps": {"int", "0"}, "timeout": {"duration", "0s"}} {
		flag := runCmd.Flags().Lookup(name)
		require.NotNil(t, flag, "crush run has a --%s flag", name)
		require.Equal(t, want[0], flag.Value.Type())
		require.Equal(t, want[1], flag.DefValue, "no limit by default")
	}

	t.Setenv("CRUSH_CLIENT_SERVER", "1")
	require.EqualError(t, refuseOnServer(false, agent.RunLimits{MaxSteps: 3}), "the --max-steps flag is not supported with CRUSH_CLIENT_SERVER=1; unset it to use the flag")
	require.EqualError(t, refuseOnServer(false, agent.RunLimits{Timeout: time.Minute}), "the --timeout flag is not supported with CRUSH_CLIENT_SERVER=1; unset it to use the flag")
	require.NoError(t, refuseOnServer(false, agent.RunLimits{}))

	t.Setenv("CRUSH_CLIENT_SERVER", "0")
	require.NoError(t, refuseOnServer(true, agent.RunLimits{MaxSteps: 3, Timeout: time.Minute}))
}

func TestCheckRunLimits(t *testing.T) {
	t.Parallel()
	require.NoError(t, checkRunLimits(agent.RunLimits{}))
	require.NoError(t, checkRunLimits(agent.RunLimits{MaxSteps: 1, Timeout: time.Second}))
	require.EqualError(t, checkRunLimits(agent.RunLimits{MaxSteps: -1}), "the --max-steps value must be zero or more")
	require.EqualError(t, checkRunLimits(agent.RunLimits{Timeout: -time.Second}), "the --timeout value must be zero or more")
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/app/ -run 'TestRunAndWait' -v` and `go test ./internal/cmd/ -run 'TestRunRefuses|TestCheckRunLimits' -v`
Expected: FAIL to compile with `too many arguments in call to runAndWait`, `too many arguments in call to refuseOnServer` and `undefined: checkRunLimits`.

- [ ] **Step 3: Write the implementation**

In `internal/app/run_wait.go`, replace

```go
// runAndWait runs prompt on sessionID, then waits for the session's tree to
// go idle, so the command does not end while a teammate the run started is
// still working. The response carries the first run's outcome: a teammate's
// report does not wake the session. The wait ends early only when ctx is
// done, which is how a signal still stops the command at once.
func runAndWait(ctx context.Context, coordinator agent.Coordinator, sessionID, prompt string) runResponse {
	var response runResponse
```

with

```go
// runAndWait runs prompt on sessionID, then waits for the session's tree to
// go idle, so the command does not end while a teammate the run started is
// still working. The response carries the first run's outcome: a teammate's
// report does not wake the session. The wait ends early only when ctx is
// done, which is how a signal still stops the command at once.
//
// Limits are set on the tree before the prompt runs, so the timeout counts
// from here. A tree stopped by a limit refuses new runs and goes idle, so
// the same wait ends it.
func runAndWait(ctx context.Context, coordinator agent.Coordinator, sessionID, prompt string, limits agent.RunLimits) runResponse {
	if limiter, ok := coordinator.(agent.TreeLimiter); ok && limits != (agent.RunLimits{}) {
		limiter.LimitSessionTree(sessionID, limits)
	}
	var response runResponse
```

In `internal/app/app.go`, replace

```go
// that describes the run instead of its text, and returns an [ExitError]
// when the run did not complete.
func (app *App) RunNonInteractive(ctx context.Context, output io.Writer, prompt, largeModel, smallModel, reasoningEffort string, hideSpinner bool, continueSessionID string, useLast, asJSON bool) error {
```

with

```go
// that describes the run instead of its text, and returns an [ExitError]
// when the run did not complete. Limits bound the steps and the time of the
// session's tree.
func (app *App) RunNonInteractive(ctx context.Context, output io.Writer, prompt, largeModel, smallModel, reasoningEffort string, hideSpinner bool, continueSessionID string, useLast, asJSON bool, limits agent.RunLimits) error {
```

and replace

```go
		done <- runAndWait(ctx, app.AgentCoordinator, sess.ID, prompt)
```

with

```go
		done <- runAndWait(ctx, app.AgentCoordinator, sess.ID, prompt, limits)
```

(`app.go` already imports `internal/agent`.)

In `internal/cmd/run.go`, replace

```go
	"charm.land/log/v2"
	"github.com/charmbracelet/crush/internal/client"
```

with

```go
	"charm.land/log/v2"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/client"
```

In the `Example` text of `runCmd`, replace

```go
# Print the outcome as one JSON object; exit 0, 2 or 3 by how it ended
crush run --json "Fix the failing test"
```

with

```go
# Print the outcome as one JSON object; exit 0, 2 or 3 by how it ended
crush run --json "Fix the failing test"

# Stop after 40 steps or 20 minutes, whichever comes first
crush run --json --max-steps 40 --timeout 20m "Fix the failing test"
```

In `RunE`, replace

```go
			jsonOutput, _      = cmd.Flags().GetBool("json")
		)
		if err := refuseOnServer(jsonOutput); err != nil {
			return err
		}
```

with

```go
			jsonOutput, _      = cmd.Flags().GetBool("json")
			maxSteps, _        = cmd.Flags().GetInt("max-steps")
			timeout, _         = cmd.Flags().GetDuration("timeout")
		)
		limits := agent.RunLimits{MaxSteps: maxSteps, Timeout: timeout}
		if err := refuseOnServer(jsonOutput, limits); err != nil {
			return err
		}
		if err := checkRunLimits(limits); err != nil {
			return err
		}
```

At the end of `RunE`, replace

```go
sessionID, useLast, jsonOutput)
```

with

```go
sessionID, useLast, jsonOutput, limits)
```

In `init`, replace

```go
	runCmd.Flags().Bool("json", false, "Print one JSON object describing the run instead of its text, and exit 0 when it completed, 2 when the model stopped short, 3 when the run should be repeated")
```

with

```go
	runCmd.Flags().Bool("json", false, "Print one JSON object describing the run instead of its text, and exit 0 when it completed, 2 when the model stopped short, 3 when the run should be repeated")
	runCmd.Flags().Int("max-steps", 0, "Stop the session and every session it starts once they took this many steps in all; zero means no limit")
	runCmd.Flags().Duration("timeout", 0, "Cancel the session and every session it starts this long after the prompt is sent, for example 20m; zero means no limit")
```

Replace `refuseOnServer`

```go
// refuseOnServer refuses the flags that only the in-process command
// implements, before anything is read or started.
func refuseOnServer(jsonOutput bool) error {
	if jsonOutput && useClientServer() {
		return errors.New("the --json flag is not supported with CRUSH_CLIENT_SERVER=1; unset it to use the flag")
	}
	return nil
}
```

with

```go
// refuseOnServer refuses the flags that only the in-process command
// implements, before anything is read or started.
func refuseOnServer(jsonOutput bool, limits agent.RunLimits) error {
	if !useClientServer() {
		return nil
	}
	for _, flag := range []struct {
		name string
		set  bool
	}{
		{name: "--json", set: jsonOutput},
		{name: "--max-steps", set: limits.MaxSteps != 0},
		{name: "--timeout", set: limits.Timeout != 0},
	} {
		if flag.set {
			return fmt.Errorf("the %s flag is not supported with CRUSH_CLIENT_SERVER=1; unset it to use the flag", flag.name)
		}
	}
	return nil
}

// checkRunLimits refuses a limit that no run could meet.
func checkRunLimits(limits agent.RunLimits) error {
	switch {
	case limits.MaxSteps < 0:
		return errors.New("the --max-steps value must be zero or more")
	case limits.Timeout < 0:
		return errors.New("the --timeout value must be zero or more")
	}
	return nil
}
```

The messages start with `the`, not with the flag: fang capitalises the first letter of an error it prints.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go build ./... && go test ./internal/app/ -run 'TestRunAndWait' -v && go test ./internal/cmd/ -run 'TestRunRefuses|TestCheckRunLimits' -v`
Expected: build prints nothing; PASS.

Then both packages once: `go test ./internal/app/ ./internal/cmd/`
Expected: `ok` for both.

- [ ] **Step 5: Format and commit**

```bash
/home/pbtrudel/.local/bin/gofumpt -w internal/app/run_wait.go internal/app/run_wait_test.go internal/app/app.go internal/cmd/run.go internal/cmd/run_json_flag_test.go internal/cmd/run_limits_flag_test.go
git add internal/app/run_wait.go internal/app/run_wait_test.go internal/app/app.go internal/cmd/run.go internal/cmd/run_json_flag_test.go internal/cmd/run_limits_flag_test.go
git commit -m "feat(run): add --max-steps and --timeout for the session tree"
```

---

### Task 8: Part 2 verification and live checks

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything above.
- Produces: the evidence for the Part 2 pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/agent/ ./internal/agentteam/ ./internal/app/ ./internal/cmd/ && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=5m ./internal/agent/... ./internal/agentteam/... ./internal/app/... ./internal/cmd/...`, then `./scripts/check_log_capitalization.sh`, then `/home/pbtrudel/.local/bin/gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD` and `git diff origin/main...HEAD | grep -c "$(printf '\342\200\224')"` (the em dash)
Expected: about 360 changed lines in `internal/agentteam/team.go`, `internal/agentteam/tree_test.go`, `internal/agent/session_tree.go`, `internal/agent/session_tree_limits_test.go`, `internal/app/run_exit.go`, `internal/app/run_exit_test.go`, `internal/app/run_json.go`, `internal/app/run_json_test.go`, `internal/app/run_wait.go`, `internal/app/run_wait_test.go`, `internal/app/app.go`, `internal/cmd/run.go`, `internal/cmd/run_json_flag_test.go` and `internal/cmd/run_limits_flag_test.go` only; the count is `0`. Report the real numbers in the pull request.

- [ ] **Step 4: Live check: the step budget, the deadline and the refusal**

Never open or print `crush.json` or any `crushrc` file.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" . && mkdir "$scratch/proj"
"$scratch/crush" run --json -y -q -c "$scratch/proj" --max-steps 1 "List the files in this directory with the bash tool, then create notes.txt containing the word hello with the write tool, then read it back with the view tool."; echo "exit $?"
"$scratch/crush" run --json -y -q -c "$scratch/proj" --timeout 5s "Write a 3000-word essay about the history of the printing press, then save it to essay.txt with the write tool."; echo "exit $?"
CRUSH_CLIENT_SERVER=1 "$scratch/crush" run --max-steps 3 -q -c "$scratch/proj" "Reply with ok." > "$scratch/stdout.txt"; echo "exit $?"; echo "stdout bytes: $(wc -c < "$scratch/stdout.txt")"
CRUSH_CLIENT_SERVER=1 "$scratch/crush" run --timeout 1m -q -c "$scratch/proj" "Reply with ok." > "$scratch/stdout.txt"; echo "exit $?"; echo "stdout bytes: $(wc -c < "$scratch/stdout.txt")"
"$scratch/crush" run --max-steps -1 -q -c "$scratch/proj" "Reply with ok." > "$scratch/stdout.txt"; echo "exit $?"; echo "stdout bytes: $(wc -c < "$scratch/stdout.txt")"
"$scratch/crush" run -y -q -c "$scratch/proj" --max-steps 1 "List the files in this directory with the bash tool, then create notes2.txt containing hello with the write tool."; echo "exit $?"
rm -rf "$scratch"
```

Expected:
- The first prints one line of JSON with `"reason":"step_limit"`, `"session":{"steps":1,...}` with `tool_calls` of at least 1, `"turn_ends":{"step_limit":1}`, then `exit 2`.
- The second prints one line of JSON with `"reason":"time_limit"`, `"turn_ends":{"time_limit":1}` and an `error` field, then `exit 2`; `duration_ms` is about 5000.
- The third and fourth print `The --max-steps flag is not supported with CRUSH_CLIENT_SERVER=1; unset it to use the flag.` and `The --timeout flag is not supported ...` on standard error, `exit 1` and `stdout bytes: 0`.
- The fifth prints `The --max-steps value must be zero or more.` on standard error, `exit 1` and `stdout bytes: 0`.
- The sixth, without `--json`, prints whatever text the model wrote on its one step and `exit 0`: today's codes (spec 3.5).

If the model answers the first prompt without calling a tool, the run reads `completed`: report it as it happened and rerun once. Record the output in the pull request.

What the live checks show: on a real model, the budget stops the run after its step and the command exits 2 with `step_limit`; the deadline cancels the run after five seconds and the command exits 2 with `time_limit`; the flags are refused against a server and for negative values; without `--json` the codes are unchanged. What they cannot show: the budget across a teammate or a continuation, the deadline stopping a teammate, the refused restart, and the precedence pairs. The tests in Tasks 2, 3 and 5 cover those.

The pull request is opened with the `pr-discipline` skill only when the user asks for it.

---

## Noticed, not changed here

- The deadline timer is not stopped when the command ends before it fires. The process ends with the command, so it never fires late in `crush run`; a long-lived caller of `LimitSessionTree` would need a stop function.
- Without `--json`, a run stopped by the deadline is a cancel to the streaming path (`errors.Is(result.err, context.Canceled)` in `RunNonInteractive`) and exits 0, like any cancel that is not a signal (Evidence 8). The spec keeps today's codes there on purpose.
- A run refused by a reached limit writes nothing, so a teammate refused on restart shows only in the Team service's report to the Lead ("error: the session tree reached its step or time limit"), which `tree.undelivered_reports` counts.
- `stopSessionTree` cancels teammate sessions on the main agent as well, because a background job that a teammate started wakes the teammate's session there (`deliverNotice` treats `team-` ids as top-level sessions).

## Self-Review

1. **Spec coverage.** 1.2: `step_limit` and `time_limit` values (Task 2); `time_limit` first in tier 1 and decided from the deadline (Tasks 2 and 3, `TestTimeLimitOutranksTheCancel`); `step_limit` last in tier 3 (Task 2); the after-compaction rules with a deadline (Task 2); child sessions stamped the same way (Task 5 teammate tests). 3.2: a limit anywhere in the tree is the reason (Task 6). 3.3: the budget belongs to the tree and every run draws on it, found through the parent chain (Tasks 1 and 3), across a continuation (Task 3) and a teammate (Task 5), stops after the current step (Task 3). 3.4: the deadline cancels the session's runs and its teammates through the Team service (Task 5), cancels carry no cause so the stamp reads the deadline (Tasks 2 and 3), no run starts after it (Task 3), a signal still wins (item 4's wait and item 5's exit code are unchanged; Task 7 keeps the command's context for the wait). 3.5: the two rows of code 2 (Task 6), unchanged codes without `--json` (Task 7; live check). Section 3's refusal against a server (Task 7). Failure behavior: `--timeout` is the bound on the wait (Task 5). Validation: the step budget across a continuation and a teammate, the deadline stopping a teammate, a limit that stops a teammate after the session completed (Tasks 3, 5, 6), the refusal (Task 7), the precedence pairs "final answer on the spending step", "halting tool on that step", "deadline against a cancel" (Tasks 2 and 3); live `step_limit` and `time_limit` (Task 8; `completed` was item 5's).
2. **Placeholder scan.** Every code step carries its code and every test step its test; no step defers work.
3. **Type consistency.** `RunLimits{MaxSteps, Timeout}`, `treeLimits` and its methods, `runLimits.set` and `forSession` (Task 1) are used with the same names in Tasks 2, 3 and 5; `SessionAgentCall.limits` (Task 2) is set in Task 3; `errTreeLimitReached` (Task 3) is asserted in Task 3's tests; `stepCount` (Task 3) is reused in Task 5; `TreeLimiter.LimitSessionTree` (Task 5) is called by `runAndWait` (Task 7) and recorded by `limitCoordinator` (Task 7's test); `refuseOnServer(jsonOutput, limits)` and `checkRunLimits(limits)` match their tests and the call in `RunE`; `RunNonInteractive(..., useLast, asJSON bool, limits agent.RunLimits)` matches the call in `run.go`.
4. **Review Focus.** Each of the five lines has its test in the task that owns the code: Tasks 2 and 3 (final answer on the spending step), Tasks 3 and 5 (refused restart, wait ends), Task 7 (timeout counts from the prompt), Task 2 (compaction interrupted by the deadline), Task 7 (negative values and the server).
