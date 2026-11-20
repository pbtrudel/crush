# Turn-End Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan PR by PR. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the Lead's turn one bounded continuation point at its end. `TurnEnd` hooks and built-in checks share that point. The first built-in check is the completion check, off by default and judged on the task suite (roadmap W3.1 and W3.2).

**Spec:** `docs/superpowers/specs/2026-11-20-turn-end-gate-design.md`. Section numbers below (§) refer to it. Roadmap: `docs/superpowers/plans/2026-11-17-model-intelligence-roadmap.md`, W3, Decision 4.

**Tech stack:** Go, `charm.land/fantasy` v0.45.5, testify `require`. The agent tests use the existing fakes in `internal/agent/common_test.go` (`testEnv`, `fakeEnv`) and `newStreamTestAgent` (`internal/agent/dispatch_cancel_test.go:60`). Hook tests use `recordingHooks` (`internal/agent/hooks_test.go:31-49`).

## Global Constraints

- **One PR per section below.** Each starts in a fresh worktree from `origin/main` after `git fetch origin` and lands in order; each one depends on the previous. Each stays near 400 changed lines including tests.
- **No regressions for users without `TurnEnd` hooks.** Behavior is unchanged for them until PR 4. After PR 4 the completion check is still off unless `turn_end_completion_check` or `CRUSH_TURN_END_COMPLETION_CHECK` turns it on.
- **Lead turns only.** Nothing in these PRs runs the gate for a call with `NonInteractive` set (§3).
- **The harness row.** It is a hidden user row whose text opens with `<turn_end_gate>` and carries the label sentence from §1 verbatim.
- **Two values never change.** The `crush run --json` `schema` stays 1, and fields are only added.
- **Style:** gofumpt; comments on their own line start with a capital letter, end with a period and wrap at 78 columns; no em dashes; testify `require`; `t.Parallel()` except in tests that call `t.Setenv`.
- **Checks before each PR:** `go build ./...`, `go vet ./...`, and `go test ./internal/agent/... ./internal/hooks/... ./internal/app/... ./internal/gitutil/... ./internal/eval/... ./internal/logreport/... ./internal/config/...`. PRs that change config options also run `task schema` (`Taskfile.yaml:145-151`) and commit `schema.json`.
- **Commits:** semantic subjects. Commit, push and open the PR only when the user has authorized it for that PR.
- **No live model runs** in any PR. The suite batch of PR 5 is started by the user.

## Review Focus (all PRs)

1. **Exactly once.** Each prompt gets exactly one channel reply, one `AgentFinished` notification and one terminal `RunComplete` per RunID, on every path: continued, continuation dropped, cancelled, limit reached.
2. **No hidden idle window.** The session never looks idle between the gate and the continuation for longer than the existing queue hand-off does.
3. **Endings are never overridden.** No ending other than `completed` is continued, and a hook halt wins over every other check.
4. **The exit code follows the continuation.** `crush run --json` takes its reason from the continuation's ending.

---

### PR 1: Gate core (§1)

Branch `crush/w31-gate-core`, about 380 lines.

**Files:**
- Create `internal/agent/turn_gate.go`, containing:
  - `gateInput`, `gateCheck`, `gateVerdict`;
  - `runTurnGate(ctx, call, …) (*SessionAgentCall, gateOutcome)`;
  - `gateMessage(verdicts) string`;
  - `userPromptQueued(sessionID) bool`;
  - `finishWithoutGate`, holding the stale note, the channel reply and the notification so the normal and the dropped paths share it.
- Create `internal/agent/turn_gate_test.go`.
- Modify `internal/agent/agent.go`:
  - add the fields `gateContinuations int` and `gateContinued bool` to `SessionAgentCall`, next to `overflowRetried` (`:218`);
  - add the gate step between the ending computation (`:1994-2002`) and the compaction block (`:2023`);
  - skip the threshold compaction, the stale note, the reply, the old `TurnEnd` firing and `AgentFinished` when `gateNext` is set;
  - make `gateNext` the hand-off candidate under `sessionMu` (`:2165`), and drop it on a cancel mark, a reached limit or a queued user prompt;
  - do not fire `TurnStart` for `gateContinued` (`:1384`);
  - add a test-only field `gateChecks []gateCheck` on `sessionAgent`. Production registers none in this PR.
- Modify `internal/config/config.go`: add `TurnEndMaxContinuations *int` (`turn_end_max_continuations`, default 1, 0 disables) and the getter `GetTurnEndMaxContinuations()`, following `GetRepeatToolReminders` (`:754-761`). Regenerate `schema.json`.
- Modify `internal/logreport/injections.go`: add `turn_end_gate` to `injectionTags` (`:19-25`).
- Modify `internal/logreport/turns.go`: `continues` (`:120-127`) treats a following `<turn_end_gate>` row as a continuation.
- Modify `internal/app/run_json.go`: `opensRun` (`:153-158`) returns true for a hidden row that opens with `<turn_end_gate>`.
- Modify `internal/agent/injection_tags_test.go`: assert that the gate row carries the tag.
- Modify `internal/app/run_json_test.go` and the matching logreport test.

**Tests** (in `turn_gate_test.go` unless noted). They use a fake `gateCheck` and a model that ends each run with text:
- [x] A check that returns a reason produces a second run. Its first row is hidden, opens with `<turn_end_gate>` and contains the reason under `[check: <name>]`.
- [x] A check that always asks yields exactly two runs with the default cap of 1, and three runs with the cap set to 2.
- [x] `turn_end_max_continuations: 0` gives one run, and the check is still called.
- [x] A user prompt queued before the gate means the check is not called, and the prompt runs next.
- [x] A user prompt queued while a blocking fake check runs drops the continuation. The reply is sent once (the channel fake counts sends) and the user prompt runs next.
- [x] A queued subagent report does not drop the continuation.
- [x] Cancelling during a blocking check ends with no continuation, one reply and one `RunComplete`.
- [x] With the tree's step limit reached, there is no continuation.
- [x] A run ending in `tool_stop`, `step_limit` or `loop_guard` is not continued, although the check is called.
- [x] `TurnStart` fires once for a continued prompt (uses `recordingHooks`).
- [x] One `RunComplete` per RunID across the continuation, using `newCancelTestAgentWithRunComplete` (`dispatch_cancel_test.go:206`).
- [x] The threshold compaction is put off to the continuation's end: no summary row between the two runs.
- [x] `run_json_test.go`: a first run stamped `completed` plus a gate continuation stamped `step_limit` gives reason `step_limit` and exit 2.
- [x] Logreport: the gate row counts as an injection and the second run as a continuation.

**Acceptance criteria:**
- With no checks registered, the existing agent, app and logreport tests pass unchanged.
- Every test above passes with `-race` (`go test -race ./internal/agent/ -run Gate`).
- `schema.json` lists `turn_end_max_continuations` with default 1.

---

### PR 2: `TurnEnd` hooks through the gate (§2)

Branch `crush/w31-turnend-hooks`, about 320 lines.

**Files:**
- Modify `internal/hooks/events.go`: on `Event`, add `StopHookActive bool`, `TurnEnd string`, `ChangedFiles []string` and `ContinuationsLeft *int`.
- Modify `internal/hooks/input.go`: on `Payload` (`:25-40`), add `stop_hook_active`, `turn_end`, `changed_files` and `continuations_left`, and fill them in `BuildPayload`. `stop_hook_active` is always sent for `TurnEnd`, even when false.
- Create `internal/hooks/turn_end.go` with `TurnEndVerdict(AggregateResult) (reason string, stop bool)`, implementing the table in §2. It also replaces the empty-reason and `blocked by hook` texts with `A TurnEnd hook asked you to continue before finishing.`.
- Create `internal/hooks/turn_end_test.go`.
- Modify `internal/agent/turn_gate.go`: add the hooks check, which is always the first check. Each reason is labelled `[hook: <display name>]` from `AggregateResult.Hooks`.
- Modify `internal/agent/turn_hooks.go` and `agent.go`:
  - `TurnEnd` fires from the gate on the normal path;
  - it stays at the old position (`agent.go:2139`) only for runs that skip the gate;
  - `fireTurnHook` keeps serving `TurnStart`, `Interrupt` and `StopFailure`.
- Modify `docs/hooks/README.md`:
  - the events table row (`:184`) and the "observe" paragraph;
  - the scope text "once per prompt" (`:196`);
  - a `TurnEnd` continue example next to `:296-303`;
  - the slow-hook note in the example and under Timeouts (`:526-533`): a slow `TurnEnd` hook keeps the session busy, so background notification work;
  - the two differences in "Where Crush differs" (`:709-723`);
  - the payload reference (`:830-842`).
- Modify `internal/skills/builtin/crush-hooks/SKILL.md`: the `TurnEnd` lines (`:21`, `:33`, `:37`).

**Tests:**
- [ ] `turn_end_test.go`, as a table over `TurnEndVerdict`:
  - exit 2 with stderr text;
  - `{"decision":"block","reason":…}`;
  - `{"decision":"deny"}` with no reason (gets the default text);
  - `hookSpecificOutput.additionalContext`;
  - Crush `context` as a string and as an array;
  - `{"continue":false}`, `{"halt":true}`, exit 49 (all stop);
  - exit 0 with no output, `allow`, exit 1 (all no opinion);
  - two hooks, one asking and one halting (stop);
  - two hooks both asking (reasons in config order).
- [ ] `input_test.go`: the `TurnEnd` payload carries the four new fields, and `stop_hook_active` is present when false.
- [ ] `hooks_test.go` in `internal/agent`, with a hook script on `TurnEnd` that exits 2 when `stop_hook_active` is false:
  - two runs, and the second hook call sees `stop_hook_active: true`;
  - a halting hook next to an asking hook gives one run;
  - `TestTurnHooks_StartAndEnd` and `TestTurnHooks_SubAgentFiresNone` still pass;
  - `TurnEnd` fires exactly once for a run that ends in `step_limit`, and once for a `shouldSummarize` run that is not resumed.

**Acceptance criteria:**
- A `TurnEnd` hook that prints nothing gives the same rows and the same hook log as before, except that it now runs before the request is released.
- A Claude Code `Stop` script using `decision: "block"` continues the turn once, and on its second call sees `stop_hook_active: true`.
- The README states the cap of 1 per prompt, the difference from Claude Code's cap of 8, and the slow-hook note. No per-hook option is added.

---

### PR 3: Changed-file detection (§4.1)

Branch `crush/w31-changed-files`, about 340 lines.

**Files:**
- Create `internal/gitutil/snapshot.go` with:
  - `type WorktreeState struct { Head string; Entries map[string]string }`;
  - `func Snapshot(ctx, dir) (WorktreeState, error)`, which runs `git --no-optional-locks status --porcelain=v2 --branch -z --untracked-files=all` with a 5 s timeout;
  - `func (s WorktreeState) Changed(later WorktreeState) (paths []string, headFrom, headTo string)`.

  The type is named `WorktreeState` because a Go package cannot declare a type and a function with the same name.
- Create `internal/gitutil/snapshot_test.go`.
- Modify `internal/agent/agent.go`:
  - add the fields `gateBaseline *gitutil.WorktreeState`, `gateBaselineErr bool` and `gateChangedFiles []string` to `SessionAgentCall`;
  - take the baseline before `TurnStart` (`:1384`) when `gateBaseline == nil` and the gate needs it (completion check on, or a `TurnEnd` hook configured);
  - in `OnStepFinish` (`:1699-1792`), append `stepChangedFiles(stepResult)` to `call.gateChangedFiles`.
- Create `internal/agent/changed_files.go` with:
  - `stepChangedFiles(step fantasy.StepResult) []string`, covering `edit`, `multiedit`, `write`, `lsp_replace_symbol` and `lsp_rename` (as `(renamed <symbol>)`), and skipping failed results;
  - `turnChangedFiles(call, end gitutil.WorktreeState)`, which returns the sorted union of the git paths and the tool paths, plus `HeadFrom` and `HeadTo`.
- Create `internal/agent/changed_files_test.go`.
- Modify `internal/agent/turn_gate.go`: take the end snapshot in `runTurnGate`, fill `gateInput.ChangedFiles`, `HeadFrom` and `HeadTo`, and pass `changed_files` to the hook event.

**Tests:**
- [ ] `snapshot_test.go`, on `t.TempDir()` repositories made with `git init` and a local `user.email` and `user.name` set through `-c` flags:
  - a modified, an added (staged), an untracked, a deleted and a renamed file are each reported;
  - a clean tree reports nothing;
  - a commit reports `headFrom != headTo`;
  - a directory outside any repository returns an error;
  - a file inside an untracked directory is listed by path.
- [ ] `changed_files_test.go`:
  - successful and failed calls of each of the five tools, reusing the `todoTestStep` helpers (`internal/agent/todo_reminder_test.go:19-48`);
  - the union and sort with git paths;
  - the fallback with no baseline;
  - in a repository from `testEnvAt` (`common_test.go:100`), a file written directly by the test between the prompt start and the gate, standing in for a `bash` change, appears in `ChangedFiles`;
  - the baseline is taken once across a gate continuation (a counter on an injected snapshot function);
  - with the check off and no `TurnEnd` hook, no snapshot is taken.

**Acceptance criteria:**
- Two git invocations per Lead prompt when needed, counted through the injected snapshot function in tests; zero when not needed.
- A non-repository working directory never fails a turn; it only logs at debug level and falls back.
- The hook payload's `changed_files` matches `gateInput.ChangedFiles`.

---

### PR 4: Completion check (§4.2, §4.4)

Branch `crush/w32-completion-check`, about 200 lines.

**Files:**
- Create `internal/agent/completion_check.go` with:
  - the check (`Name() == "completion"`), which returns the §4.2 text when `ChangedFiles` is non-empty or `HeadFrom != HeadTo`;
  - `completionMessage(files []string, headFrom, headTo string) string`, which lists at most 20 paths, then `and N more`, and adds the commits line when HEAD moved;
  - `completionCheckEnabled(option bool) bool`, defined as `option || env`, with the variable `CRUSH_TURN_END_COMPLETION_CHECK` read once through `sync.OnceValue`, as in `responses_replay.go:36-48`.
- Create `internal/agent/completion_check_test.go`.
- Modify `internal/config/config.go`: add `TurnEndCompletionCheck bool` (`turn_end_completion_check`, default false; the schema description says it is an experiment). Regenerate `schema.json`.
- Modify `internal/agent/turn_gate.go`: register the check after the hooks when it is enabled.

**Tests:**
- [ ] `completionMessage`:
  - exact text for one file;
  - 25 files truncated to 20 plus `and 5 more`;
  - the commits line when HEAD moved.
- [ ] The check returns no reason with no files and no HEAD move.
- [ ] Option and environment combinations, each in its own test with `t.Setenv` and a reset of the once-value, following `responses_replay_test.go:540-576`: off/unset, on/unset, off/`1`, off/`true`, off/`0`.
- [ ] End to end, with a fake model that writes a file through `write` and then ends: two runs; the gate row contains `Files changed in this turn: <path>`; the continuation's request offers the same tools as the first.
- [ ] A `NonInteractive` call with the check on gives one run.

**Acceptance criteria:**
- With the option off and the variable unset, the agent tests show no gate row and no extra run.
- The message text in the code matches §4.2 byte for byte; the test compares against a copy of the spec block.

---

### PR 5: Measurement and experiment file (§4.3, §5)

Branch `crush/w32-measurement`, about 300 lines.

**Files:**
- Create `internal/app/gate_claims.go` with `gateClaims(rows []message.Message, sessionID string) (claims, unbacked int)`, implementing §4.3. It reads the last assistant text of the prompt's final run and the `command` inputs of successful `bash` calls in every run of the prompt.
- Create `internal/app/gate_claims_test.go`.
- Modify `internal/app/run_json.go`: add `Gate runGate \`json:"gate"\`` to `runResult` (`:60-73`), where `runGate` holds `continuations` (the count of `<turn_end_gate>` rows in the root session), `claims` and `unbacked_claims`.
- Modify `internal/eval/result/result.go`: add `Gate` next to `UndeliveredReports` (`:89`).
- Modify `internal/eval/run/run.go`: copy `gate` from the run's standard output.
- Modify `internal/eval/score/score.go`: per variant, report the sum of continuations, the sum of claims, the sum of unbacked claims, and the run ids with `unbacked_claims > 0`.
- Create `evals/experiments/<date>-w32-completion-check.yaml` as in §5, with `primary_metric: false_completions`. The margin and detectable effect come from `crush-eval plan` against `evals/baselines/2026-11-19-deepseek-flash.json`. The refs are set to this PR's merge sha in a follow-up commit, before the batch.

**Tests:**
- [ ] `gate_claims_test.go`:
  - a backticked claim that matches a bash command exactly is backed;
  - matches with extra whitespace and as a substring are backed;
  - a claim naming a called non-bash tool is backed;
  - a claim with no matching call is unbacked;
  - `Verified: none` and a missing block give zero claims;
  - a claim backed only by a failed bash call is unbacked;
  - commands from the first run of the prompt count for the continuation's claims.
- [ ] `run_json_test.go`: the `gate` object is present with zeros on a prompt without a gate row, and with the right counts with one.
- [ ] `internal/eval/result` round-trips `gate`. In `internal/eval/score`, the report lists unbacked runs by id.
- [ ] `crush-eval validate` and `crush-eval plan` accept the experiment file (`go run ./cmd/crush-eval plan evals/experiments/<file>`).

**Acceptance criteria:**
- The `crush run --json` output keeps `schema: 1` and gains only `gate`.
- `crush-eval compare --metric false_completions` and `--metric pass_rate` both run on a fixture batch in the score tests.
- The experiment file is committed before any batch runs. The batch itself (162 runs on `deepseek-flash`) is started by the user, not by this PR.
- Decision rule (§5): the check becomes a default only if the false-completion interval lies below zero and the pass-rate lower bound is above minus the margin. Making it the default is a separate one-line PR.

---

### PR 6 (later, W3.3): stale-todo check (§6)

Branch `crush/w33-todo-gate`, about 200 lines. Starts only after the PR 5 batch has been read.

**Files:**
- Modify `internal/agent/todo_reminder.go`: add the `todos` gate check, using `staleTodoAtTurnEnd` (`:141-145`), with the §6 text.
- Modify `internal/agent/agent.go`: the stale note (`:2104-2125`) is written only when the check fired and no continuation is left.
- Modify `internal/agent/todo_reminder_test.go`.

**Tests:**
- [ ] A stale list at turn end gives one continuation whose gate row carries `[check: todos]`, and no note when the model then updates the list.
- [ ] With the cap used up by another check, the note is appended as today.
- [ ] `TestStaleTodoNoteSurvivesASlowCompaction` (`todo_reminder_test.go:373`) still passes.

**Acceptance criteria:**
- At most one todo question per prompt.
- The note text and its trigger are unchanged when the gate cannot continue.
