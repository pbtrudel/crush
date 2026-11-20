# Turn-End Gate Design (W3.1)

Roadmap: `docs/superpowers/plans/2026-11-17-model-intelligence-roadmap.md`, W3 items 1 and 2, Decision 4. Line numbers are at `origin/main` 65ba78dc.

## Goal

When the Lead stops calling tools, Crush ends the turn and nothing checks the work. This design adds one continuation point at the end of the turn. Built-in checks and `TurnEnd` hooks both use it. A check can return a reason. Crush delivers the reason as a labelled harness message, and the model gets one more run, a bounded number of times per prompt. The first built-in check (W3.2) is off by default and is judged on the task suite. Moving the stale-todo note into the gate (W3.3) is a later step.

## Current code

- **The loop.** `Run` makes one `agent.Stream` call per run (`internal/agent/agent.go:1434`). fantasy ends that call as soon as a step has no tool calls (`charm.land/fantasy@v0.45.5/agent.go:1049-1051`, `ShouldContinue`), and Crush has no hook that can say "continue". So a continuation cannot happen inside `Stream`: it has to be a new run.
- **There is already one continuation by re-run.** The compaction path rewrites the call's prompt, puts the call back on the queue and sets `continued` (`agent.go:2079-2090`). The hand-off at the end of `Run` then starts it recursively (`agent.go:2165-2260`). Because the RunID is the same, the outer run does not publish its own `RunComplete` (`agent.go:2230-2238`).
- **The order at turn end today:**
  1. The ending is computed (`agent.go:1994-2002`).
  2. Compaction runs; for a threshold compaction it deletes `activeRequests` first (`agent.go:2023-2025`). Otherwise the ending is stamped (`agent.go:2101`).
  3. The stale-todo note is appended to the reply (`agent.go:2104-2125`).
  4. The channel reply is sent (`agent.go:2127-2130`).
  5. The request is released (`agent.go:2136-2137`).
  6. `TurnEnd` fires (`agent.go:2139-2141`).
  7. The `AgentFinished` notification goes out (`agent.go:2145-2151`).
  8. The queue is handed off (`agent.go:2165`).
- **The hook result is ignored.** `fireTurnHook` drops the `AggregateResult` (`internal/agent/turn_hooks.go:75-81`). It returns early for `call.NonInteractive` (`turn_hooks.go:76`), and `subAgentCall` sets that flag for every subagent and teammate call (`internal/agent/coordinator.go:2452`, `:2478`). Teammates reach it through `executeSubAgent` (`internal/agent/team.go:173`, `coordinator.go:2589-2590`). `turnEndEvent` sends only `last_assistant_message` (`turn_hooks.go:86-98`).
- **Hook engine.**
  - Hooks run in parallel and their results combine in config order (`internal/hooks/runner.go:67-114`, `internal/hooks/hooks.go:99-168`).
  - Exit 2 means deny with stderr as the reason. Exit 49 means deny plus halt (`runner.go:240-251`).
  - `decision: "block"` is parsed as deny (`internal/hooks/input.go:239-247`). `continue: false` is parsed as halt (`input.go:145-150`). `hookSpecificOutput.additionalContext` becomes `Context` (`input.go:208-210`).
  - The default timeout is 30 s per hook (`internal/hooks/config.go:31-36`). A timeout counts as "no opinion" (`runner.go:214-222`).
- **Documentation.** The hooks documentation lives in `docs/hooks/README.md`; the repository has no `HOOKS.md`. Its events table calls `TurnEnd` "observe" and maps it to Claude Code's `Stop` (`docs/hooks/README.md:184`). It promises that a `TurnEnd` hook speaks "once per prompt" (`:194-197`). It claims Claude Code's `decision: "block"`, `continue: false` and exit 2 are understood (`:701-707`).
- **Harness rows.** Reminders are stored as hidden user rows (`agent.go:1784-1789`; `Hidden` is defined at `internal/message/content.go:82-83`). The log report knows the tags they open with (`internal/logreport/injections.go:19-25`).
- **How `crush run --json` picks its reason.** It takes the stamp of the last run that a *visible* user row opened (`internal/app/run_json.go:76-84`, `:153-158`). Only `completed` exits 0 (`internal/app/run_exit.go:41-54`).
- **Claude Agent provider.** The provider sends a steered message (`Priority: "next"`) only while the CLI is parked on tool calls (`internal/claudeagent/session.go:431-438`, `internal/claudeagent/transport.go:566-575`). A prompt that brings new history after a finished turn goes out as a new user message (`session.go:440-444`, `:286`, `transport.go:555-563`).

## Design

### 1. The continuation point

**Where it sits.** A new step in `Run`, `runTurnGate`, sits after the ending is computed (`agent.go:1994-2002`) and before the compaction block (`agent.go:2023`). It therefore runs before the stale-todo note, the channel reply, the release of the request, the `AgentFinished` notification and the queue hand-off. It runs with `genCtx`, so a cancel by the user interrupts a slow check.

**When the gate is evaluated.** All of these must hold:
- `err == nil`, not `overflowRecovery`, not `shouldSummarize`. A run that compaction will resume is not ending.
- `currentAssistant != nil`.
- `!call.NonInteractive` (the Lead only; see section 3).
- `!call.Ephemeral`.

**When a continuation is allowed.** The gate is always evaluated when the conditions above hold. A continuation is allowed only if, in addition:
- `turnEnd == message.TurnEndCompleted`;
- `!call.limits.reached()`;
- `call.gateContinuations < max`;
- `genCtx.Err() == nil`.

Other endings (`step_limit`, `tool_stop`, `loop_guard`, `max_tokens`, `empty`, …) still fire `TurnEnd`, but a request to continue is logged and ignored. A limit, a halt or a guard must not be overridden.

**What a check is.**

```go
// internal/agent/turn_gate.go
type gateInput struct {
    SessionID     string
    TurnEnd       message.TurnEnd
    Reply         string   // currentAssistant.Content().String()
    ChangedFiles  []string // section 4.1: git comparison united with tool calls
    HeadFrom      string   // HEAD at prompt start; "" in fallback mode
    HeadTo        string   // HEAD at the gate; equal to HeadFrom if no commit
    Continuations int      // call.gateContinuations
}
type gateCheck interface {
    Name() string                                          // "hooks", "completion"
    Check(ctx context.Context, in gateInput) gateVerdict
}
type gateVerdict struct {
    Reason string // non-empty asks for a continuation
    Stop   bool   // a hook halt: no continuation this time, whatever others said
}
```

The checks run in a fixed order: the `TurnEnd` hooks first, then the built-in checks in registration order. If any check returns `Stop`, there is no continuation. Otherwise the non-empty reasons are joined into one message. The checks are evaluated before the per-prompt cap is applied, so a hook always runs once per candidate ending, even when the cap leaves its answer unused. Built-in checks that are disabled are not registered.

**The harness message.** It is one hidden user row whose text opens with a new tag. The tag is added to `injectionTags` (`internal/logreport/injections.go:19-25`), and the test convention there applies.

```
<turn_end_gate>
Crush checked your turn before ending it. This message comes from Crush, not from the user, and the user does not see it. Act on it, then finish your reply to the user.

[hook: <display name>]
<reason>

[check: completion]
<text of section 4.2>
</turn_end_gate>
```

On every provider the row reaches the model with the user role. The `Hidden` flag keeps it out of the chat view, as with the reminders. The text says where the message comes from.

**How the loop resumes.** If the gate decides to continue, `runTurnGate` builds `gateNext`, a copy of `call` with these changes:
- `Prompt` is the harness text and `HiddenUserMessage` is true;
- `Attachments`, `HostContext`, `Report` and `OnComplete` are cleared;
- `gateContinuations` is incremented. This is a new unexported field next to `overflowRetried` (`agent.go:218`), so the count survives a compaction continuation, which copies `call`;
- `gateContinued` is set, a new unexported flag.

The same RunID, `limits` and `channelMeta` are kept. The continuation's steps therefore count against the tree's step budget and deadline, and its reply goes back to the same channel. `gateNext` is a local value and never enters `messageQueue`, so `QueuedPromptsList` never shows it. With `gateNext` set, the rest of `Run` changes like this:

- The threshold compaction is put off. The block at `agent.go:2024` is skipped and `recordTurnEnd()` stamps the run `completed`. The continuation's own ending runs the threshold check. Compacting now would hand the model a summary in place of the work the question is about. Within the continuation, the context guard in `StopWhen` still applies (`agent.go:1794-1822`).
- The stale-todo note (`:2111`), the channel reply (`:2128`), the `TurnEnd` firing at its old place (`:2139`) and `AgentFinished` (`:2145`) are skipped. The continuation does all four when it really ends.
- The release (`:2136-2137`) still happens, as on every hand-off today.
- At the hand-off, under `sessionMu` (`agent.go:2165`):
  - `gateNext` is dropped if a cancel mark exists, if `call.limits.reached()`, or if the queue now holds a visible user prompt (see below). Its prompt's turn then ends as it is. Because the reply, the note and the notification were skipped earlier, the dropped case sends them at this point, after unlocking, through `finishWithoutGate`, which the normal path also calls, so the two paths cannot drift. The ordinary hand-off then runs the queued prompt next.
  - If none applies and `gateNext` survives, it becomes `firstQueuedMessage`. Queued reports and wake-ups stay behind it. `outerOwesRunComplete` is false because the RunID is the same, and `BeginAccepted` keeps the session visible to a cancel during the hand-off, as for queued prompts.
- In the recursive `Run`, a call with `gateContinued` set does not fire `TurnStart` (`agent.go:1384`). Claude Code does not fire `UserPromptSubmit` for a `Stop` continuation either.

**A queued user prompt takes precedence (decided).** A queued call counts as a user prompt if it carries a visible prompt: `!HiddenUserMessage && Report == nil && !Wakeup`. The precedence is checked twice, both times under `sessionMu`:
- *Before the gate runs.* If a user prompt is already queued, the built-in checks are skipped, the hooks run observe-only, and the turn ends as today.
- *At the hand-off.* A user prompt that arrived while the gate was running, for example during a slow hook, drops `gateNext` (previous paragraph). The turn ends with the reply as it stood, and the user's prompt runs next.

The user therefore never waits behind a harness run that had not started when they sent the prompt. The continuation is never folded into the user's run, because that would mix a user turn with a harness turn. Queued subagent reports and wake-ups do not stop a continuation. They are folded into its first step the way they are folded today (`agent.go:1468-1503`).

**The bound.** `options.turn_end_max_continuations`, an `*int`, defaults to 1. `0` turns off continuations from every source; hooks still fire and are observed. Unlike Claude Code's cap of 8 consecutive continuations, which resets on every tool call, the count here is per prompt and never resets: Crush's default checks cost a model run each.

**Reporting.**
- In `crush run --json`, a run opened by a hidden row that opens with `<turn_end_gate>` counts as reopening the prompt's run (`opensRun`, `run_json.go:153-158`). The reason is therefore the continuation's ending: a continuation that hits the step limit exits 2, not 0.
- `continues` in `internal/logreport/turns.go:120-127` gains the same rule.
- The run object gains an additive `gate` object: `{"continuations": n, "claims": n, "unbacked_claims": n}`. Section 4.3 defines the two claim counts. `schema` stays 1.

### 2. The `TurnEnd` hook protocol

**Input.** The payload gains fields; none is removed. Each new field is added to `hooks.Event` and to `Payload` (`internal/hooks/events.go:89-110`, `input.go:25-40`).

```jsonc
{
  "hook_event_name": "TurnEnd",
  "last_assistant_message": "…",        // unchanged
  "stop_hook_active": false,             // true when this run is a gate continuation (Claude Code's name)
  "turn_end": "completed",               // the TurnEnd stamp; continuation is honoured only for "completed"
  "changed_files": ["internal/x/y.go"],  // section 4.1; may be empty
  "continuations_left": 1                // max minus continuations already used for this prompt
}
```

**Output.** A `TurnEnd`-specific reading of the existing parse is added as `hooks.TurnEndVerdict(AggregateResult) (reason string, stop bool)`. `parseStdout` and `runOne` are not changed.

| Hook says | Parsed today as | Gate meaning |
|---|---|---|
| exit 2, stderr text | deny + reason (`runner.go:241-245`) | continue with stderr as the reason |
| `{"decision":"block","reason":…}` (Claude Code) or `"deny"` | deny + reason | continue with `reason` |
| `{"context":…}` or `hookSpecificOutput.additionalContext` | `Context` | continue with the context as the reason (Claude Code: "Stop hook feedback") |
| `{"continue":false,"stopReason":…}`, `{"halt":true}`, exit 49 | halt | no continuation from any check; reason logged |
| exit 0 with no JSON, `allow`, any other exit, timeout | none | no opinion |

A deny without a reason gets the text `A TurnEnd hook asked you to continue before finishing.`. The default `blocked by hook` from `runner.go:242-244` describes a tool and is replaced. When several hooks answer, the existing aggregation applies: reasons and contexts are concatenated in config order (`hooks.go:99-168`), and a halt anywhere wins. Each hook's reason is labelled with its display name in the harness message (`AggregateResult.Hooks`, `runner.go:93-105`).

**Timeouts.** These are unchanged: per hook, 30 s by default, and a timeout means no opinion. The hooks run in parallel, so the gate waits as long as the slowest matching hook. A cancel by the user cancels `genCtx`, and the runner reports a cancel by the parent as no opinion (`runner.go:216-217`). The turn then ends with no continuation and no `Interrupt` event, because the turn itself had already ended.

**Claude Code compatibility.** `Stop` scripts that block with exit 2, `decision: "block"` or `additionalContext`, or that check `stop_hook_active`, work unchanged. Two differences go into the "Where Crush differs" list (`docs/hooks/README.md:709-723`):
- the default cap is 1 per prompt, not 8 consecutive;
- `continue: false` and exit 49 also stop the built-in checks.

The events table row becomes `TurnEnd | … | continue with reason, halt | — | Stop`.

**Backward compatibility.**
- Hooks that print nothing and exit 0, such as the read-aloud example (`docs/hooks/README.md:296-303`) and notification hooks, behave as before, with two exceptions:
  1. They now run before the request is released. A slow synchronous hook keeps the session busy for its duration, up to its timeout.
  2. When a continuation happens they run once per candidate ending, so twice per prompt at the default cap. `stop_hook_active` lets a script skip the second call.

  Both are handled with a documentation note only; no per-hook option is added (decided). The note goes under the `TurnEnd` example and in the Timeouts section (`docs/hooks/README.md:296-303`, `:526-533`). It says that a slow `TurnEnd` hook keeps the session busy, and that a notification hook should background its work, as the read-aloud example already does. The "once per prompt" sentence (`:196`) is corrected.
- A hook that already exits 2 or prints `decision: "block"` on `TurnEnd` will now continue the turn. Today that output is discarded, so such a hook was written either for Claude Code's `Stop` semantics or by mistake. The release notes name the change.
- `fireTurnHook` keeps its signature for `TurnStart`, `Interrupt` and `StopFailure`. The `TurnEnd` firing moves into the gate on the normal path. It stays at `agent.go:2139` only for runs that skip the gate, for example `shouldSummarize` runs that did not resume, so `TurnEnd` still fires exactly once per run.

### 3. Scope

**Lead turns only (decided).** Teammates and subagents are revisited together with W8, the delegation policy.
- Hooks keep their documented scope, because the skip at `turn_hooks.go:76` stays and `docs/hooks/README.md:194-197` keeps its promise.
- The built-in checks also run only where `!call.NonInteractive`. No scope option is added.

The tradeoff, kept for W8:
- *For covering children.* With delegation mandatory, much of the editing happens in teammates, and their work reaches the user without a check of its own. The Lead's gate does see the files they changed, because the git comparison of section 4.1 counts every change in the working tree.
- *Against.* Every child turn would pay one extra run, and the child's final message is a report that the Lead reads, not an answer to the user. A continuation in a child also keeps the Lead's `wait_agent` waiting longer.

**Claude Agent provider.** The CLI owns the loop inside a run, but each Crush run still ends where `Stream` returns, so the gate runs in `Run` as on every provider. The continuation's run brings one new hidden row, and the provider sends it as a new user message on the CLI's transcript (`session.go:286`, `transport.go:555-563`). It is not a steer, because nothing is parked. That is the "one more steered message" the roadmap describes. The CLI shows the row as a user message, so the text's opening sentence carries the label. The CLI's own `Stop` hooks, from the user's Claude settings, run inside the CLI before Crush sees the end. They are independent of this gate; in the suite, `CLAUDE_CONFIG_DIR` isolates them. Results on this route are reported separately (roadmap W0.4).

### 4. First built-in check: completion (W3.2)

#### 4.1 Changed files

**Primary: compare the git state (decided).** A new exported function, `gitutil.Snapshot(ctx, dir) (WorktreeState, error)`, lives in `internal/gitutil/snapshot.go`. The existing `gitOutput` (`internal/gitutil/worktree.go:264`) is unexported.

- **One git call.** `Snapshot` runs `git --no-optional-locks status --porcelain=v2 --branch -z --untracked-files=all`. Its `# branch.oid` header carries the HEAD sha, so the porcelain lines and the HEAD sha come from one call. `--no-optional-locks` keeps the call from taking the index lock that the model's own git commands need.
- **The result.** It returns the HEAD sha and a map from path to its porcelain entry.
- **Timeout.** Each call is bounded to 5 s.

When the snapshots are taken:
- **At prompt start.** In `Run`, before `TurnStart` (`agent.go:1384`), when `call.gateBaseline` is nil. A gate continuation and a compaction continuation both copy `call`, so the baseline is taken once per prompt. It is stored on the call, as a new unexported field.
- **At turn end.** In `runTurnGate`.
- **Only when needed.** Both are taken only when the completion check is on or at least one `TurnEnd` hook is configured, because `changed_files` is in the hook payload.

The cost is two git calls per Lead prompt in that case, plus one more for each continuation, whose ending is checked again.

**Comparing the snapshots.**
- **Changed paths.** A path is changed if its entry was added, removed or differs between the two snapshots.
- **HEAD moved.** If HEAD moved, the turn counts as changed. The message names the old and new short shas, because files the model committed no longer appear in the porcelain output.
- **Coverage.** This catches edits made through `bash` (formatters, `sed`, `git apply`) and by teammates and subagents in the same working tree. It does not see work moved into another worktree.
- **The gap.** A file that was already modified at prompt start and is modified again by `bash` keeps the same porcelain entry, because v2 lists the HEAD and index object names, not the working-tree content. Tool edits of such a file are still caught by the union with the tool-call set below.

**Fallback: the tool calls.** If `Snapshot` fails at prompt start (not a repository, git missing, timeout), the prompt uses tool-call detection alone, and a debug log records why.

The tool-call set is always collected, because it is free. It is united with the git result when both exist. `OnStepFinish` already walks each step's tool calls and results (`agent.go:1699-1792`). A new helper, `stepChangedFiles(step fantasy.StepResult) []string`, follows the pattern of `stepMadeTaskProgress` (`internal/agent/todo_reminder.go:48-71`). It collects the `file_path` input of every successful call to:
- `edit` (`internal/agent/tools/edit.go:46`)
- `multiedit` (`internal/agent/tools/multiedit.go:55`)
- `write` (`internal/agent/tools/write.go:45`)
- `lsp_replace_symbol` (`internal/agent/tools/lsp_replace_symbol.go:26`)
- `lsp_rename` (`internal/agent/tools/lsp_rename.go:27`). It takes no file argument (`:22-24`) and records the files it changed only in file history (`:80-87`). A successful rename therefore marks the turn as changed and adds the entry `(renamed <symbol>)` to the list.

A call whose result is `ToolResultOutputContentError` is skipped. The set is carried on the call (`gateChangedFiles`), so it accumulates across the runs of one prompt.

In fallback mode, files changed by `bash` or by a teammate or subagent are not seen.

The check runs only when the union is non-empty or HEAD moved. Paths are relative to the working directory.

#### 4.2 The message

```
Before you end this turn, check your work and then answer in your reply to the user, in this form:

Asked: <the request in one sentence>
Verified:
- `<exact command you ran in this turn>`: <what it showed>
Unverified: <what you changed but did not check, and why; "nothing" if all is checked>

List under Verified only commands you ran in this turn, exactly as you ran them. If you ran none, write "Verified: none". If a check is missing that you can still run, run it now before answering. If a check shows the work is unfinished or broken, fix it first.

Files changed in this turn: <comma-separated paths, at most 20, then "and N more">
```

If HEAD moved, the message adds the line `Commits made in this turn: <old short sha>..<new short sha>`.

**The continuation gets the full tool set (decided).** The point is a verification pass, as in the published harness change (roadmap source [5]). A text-only answer would only ask the model to recall what it did.

#### 4.3 Regression signal

The question invites invented verification, so each run counts claims whose command was never run.
- **Claims.** Take the last assistant text of the prompt's final run. Find the `Verified:` block and take the backticked string of each `- ` line in it. A missing block, or `Verified: none`, gives zero claims.
- **Backed.** A claim is backed if, after collapsing whitespace, it equals the `command` input of some successful `bash` call in any run of this prompt, or is a substring of one. It is also backed if it names a non-bash tool that was called (for example `lsp_diagnostics`).
- **Unbacked** is the number of claims that are not backed.

The count is a pure function over the session rows, `gateClaims(rows) (claims, unbacked int)`, in `internal/app`, and it feeds the `gate` object of section 1. Gate-on runs where `unbacked_claims / claims` is above zero are listed in the batch report by run id.

#### 4.4 Switch

- Config option: `options.turn_end_completion_check`, a `bool`, default false. The JSON schema text says it is an experiment.
- Environment override: `CRUSH_TURN_END_COMPLETION_CHECK`, read once with `strconv.ParseBool` through `sync.OnceValue`. The switch is `option || env`, exactly as `responsesReasoningReplayEnabled` does (`internal/agent/responses_replay.go:36-48`).

### 5. How it is judged

The experiment file is `evals/experiments/<date>-w32-completion-check.yaml`, committed before the batch runs (task-suite design 4.3):

```yaml
date: <date the batch starts>
name: w32-completion-check
variants:
  - name: control
    ref: <merge sha of PR 5>
  - name: check
    ref: <merge sha of PR 5>
    overlay: {turn_end_completion_check: true}
routes: [deepseek-flash]
repetitions: 3
primary_metric: false_completions
margin: <pass-rate guard, from crush-eval plan, at least the detectable effect>
baseline: evals/baselines/2026-11-19-deepseek-flash.json
detectable_effect: <from crush-eval plan>
```

- **Same build for both variants.** The overlay is merged into the generated config's options (`internal/eval/score/plan.go:137-144`, `internal/eval/run/run.go:169`). The child's environment is built from an allow-list (`internal/eval/route/env.go:28-45`, `:120-140`), so the runner uses the config option and does not pass the environment variable. The variable exists for manual `crush run` trials and for a one-off run outside the suite.
- **Metrics (decided).** Following task-suite design 4.3:
  - The primary metric is false completions (`internal/eval/score/compare.go:19-20`; definition in task-suite design 4.1).
  - Pass rate is the guard, with the margin stated in the file. `crush-eval compare --metric pass_rate` reports it from the same batch.

  Also reported:
  - `tree.steps` and tokens, since a continuation costs at least one run;
  - stopped-short runs, since a continuation can reach the frozen step limit and turn a pass into exit 2;
  - `gate.unbacked_claims`.
- **Decision (Decision 4).** The check becomes a default only if false completions fall, meaning the paired interval for the difference lies below zero, and the lower bound of the pass-rate interval stays above minus the margin. Otherwise it stays off. The Claude route is judged separately if it is run.

### 6. Later: the stale-todo check (W3.3), not in these PRs

The turn-end part of the todo guard moves into the gate as a second built-in check, `todos`. If `staleTodoAtTurnEnd` (`todo_reminder.go:141-145`) holds when the gate runs, it returns one question:

> Your todo list still shows "<item>" as unfinished. Update it with the `todos` tool, or tell the user plainly that this item is not done.

This asks once, in place of appending to the model's reply. `staleTodoNote` (`todo_reminder.go:152-158`) stays as the fallback, appended as today (`agent.go:2104-2125`) when the check fires but no continuation is left. The narrower mid-turn trigger from roadmap W3.3 is a separate change. On the Claude Agent provider the question arrives as the new user message described in section 3, which also removes the wrapping as a steered message that W3.3 describes.

## Pull requests

Each PR is at most about 400 changed lines including tests. Until PR 4 nothing changes for a user without `TurnEnd` hooks, and the completion check stays off by default after it.

1. **Gate core** (~380 lines), in `internal/agent/turn_gate.go`, `agent.go`, `internal/config/config.go`, `internal/app/run_json.go`, `internal/logreport`.
   - Contents: the `gateCheck` interface, `runTurnGate`, `gateNext` hand-off and `finishWithoutGate`, the `gateContinuations` and `gateContinued` fields, `turn_end_max_continuations`, the `<turn_end_gate>` tag, the run reopening in `opensRun` and `continues`.
   - No check is registered in production, so behavior does not change.
   - Tests, with a fake check and the existing scripted-model agent tests:
     - a reason produces a second run whose first row is hidden and tagged;
     - the cap of 1 holds with a check that always asks;
     - `0` disables the gate;
     - a prompt queued before the gate wins and the check is not run;
     - a prompt queued while a blocking fake check runs drops the continuation, the reply is sent once, and the prompt runs next;
     - a queued subagent report does not drop the continuation;
     - a cancel during the check ends the turn with no continuation and sends the reply once;
     - a reached step limit drops the continuation;
     - `step_limit` and `tool_stop` endings never continue;
     - `TurnStart` is not fired for the continuation;
     - one `RunComplete` per RunID;
     - `summarizeRun` takes the continuation's stamp.
2. **`TurnEnd` hooks through the gate** (~320 lines).
   - Contents: the payload fields, `TurnEndVerdict`, the hooks check, moving the `TurnEnd` firing, `docs/hooks/README.md` (table, differences, payload reference, the "once per prompt" text), and `internal/skills/builtin/crush-hooks/SKILL.md`.
   - Tests:
     - `internal/hooks`: the verdict for exit 2, `block`, `deny` without reason, `additionalContext`, `context`, `continue:false`, exit 49, timeout, and two hooks combined;
     - `internal/agent`: a hook continues once; `stop_hook_active` is true on the second call; a halting hook overrides a hook that asks to continue; `TurnEnd` fires once per run on the paths that skip the gate.
3. **Changed-file detection** (~340 lines), in `internal/gitutil/snapshot.go`, `internal/agent/turn_gate.go`, `agent.go`, `internal/hooks`.
   - Contents: `gitutil.Snapshot` and its comparison, the `gateBaseline` field and the snapshot at prompt start, `stepChangedFiles` and `gateChangedFiles`, the union, the fallback, `HeadFrom`/`HeadTo`, and `changed_files` filled in the hook payload.
   - Tests:
     - `internal/gitutil`, on temporary repositories: a modified, an added, an untracked, a deleted and a renamed file; a commit (HEAD moved); a clean tree; a directory outside a repository returns an error;
     - `internal/agent`: changed files from successful and failed calls of each tool; a file changed only by a fake `bash` step is found through git; the fallback outside a repository; a baseline taken once across a gate continuation; no snapshot when the check is off and no `TurnEnd` hook is configured.
4. **Completion check** (~200 lines).
   - Contents: the check, the message, `turn_end_completion_check`, `CRUSH_TURN_END_COMPLETION_CHECK`.
   - Tests:
     - no check when nothing changed and HEAD did not move;
     - the message text, including the file-list truncation and the commits line;
     - option and environment combinations, following `internal/agent/responses_replay_test.go:540-576`;
     - the check is skipped for `NonInteractive` calls.
5. **Measurement** (~300 lines).
   - Contents: `gateClaims`, the `gate` object in `crush run --json`, copying it into result lines and the batch report in `internal/eval`, and the experiment file with `plan` output.
   - Tests: claim parsing (backticks, `none`, missing block); backed matches by whitespace and substring; unbacked claims; the JSON field.
6. **Later, W3.3** (~200 lines): the `todos` check and the note as fallback. Tests: the check asks once; the note is written only when no continuation is left.

## Decisions

Settled by the user on 2026-11-20.

1. **Primary metric:** false completions. Pass rate is the guard, following task-suite design 4.3 (section 5).
2. **Changed files:** compare the `git status` porcelain output and the HEAD sha at prompt start and at turn end, and fall back to tool-call detection outside a git repository. The cost is two git calls per Lead prompt (section 4.1).
3. **Tools during the continuation:** the full tool set (section 4.2).
4. **Scope:** Lead turns only. Teammates and subagents are revisited with W8 (section 3).
5. **A user prompt that arrives while the gate runs:** it drops the continuation and runs next (section 1).
6. **Slow `TurnEnd` hooks:** a documentation note only, with no per-hook option (section 2).
