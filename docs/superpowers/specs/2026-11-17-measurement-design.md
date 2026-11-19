# Measurement Design

Design for items W0.1 to W0.3 of the roadmap (`docs/superpowers/plans/2026-11-17-model-intelligence-roadmap.md`). No code has been changed. Code references are to commit `c71dec5e`.

## Goal

Make every Crush run leave a record that says which build produced it, why each turn ended and what its tools returned. Make that record readable by a person (`crush stats report`) and by a script (`crush run --json`). Every later roadmap item is judged on those two outputs, and the task suite (W0.4) is built on the second.

Nothing in this design changes what a model is sent.

## Terms

- **Step**: one model request and the tool calls it produced. One assistant message row.
- **Run**: one pass of the agent loop over a session, from a prompt until the loop stops.
- **Turn**: everything one prompt causes. Usually one run; several when a run is cut by a compaction and continued.
- **Session tree**: a session and the sessions started from it: subagents, teammates, `agentic_fetch`.
- **Stamp**: a field Crush writes into the session log for later reading. It is never part of a provider request.

## Evidence

From the code, and from this workspace's session log over a fixed window (2026-10-21 00:00 to 2026-11-17 00:00 local time: 44,692 messages, 22,295 tool calls; counts only).

1. **Build.** No row records which build wrote it. A release is identified by its version alone, `0.100.0-fork.<CI run>` (`.github/workflows/publish-npm.yml:66-67`), and its binary carries no version-control data (`-buildvcs=false`, `:82`). A local `go build .` at this commit reports `v0.100.0-fork.100+dirty`: the Go toolchain derives the version from the nearest tag and marks a modified checkout, and `internal/version/version.go:25-38` adopts it. Two modified builds therefore share one version string.
2. **Turn end.** Seven ways a run can end are not recorded as such: the repeated-tool-call guard, the mid-turn context guard, a compaction that a hook blocked, an empty response, and a tool that halts the turn (a denied permission, a hook halt, a cancelled question). Their last step is stored as `tool_use` or `end_turn` like any other (`internal/agent/agent.go:1649-1656`, `:1734-1765`). An empty response can be inferred from a message without content, and a halting tool from the wording of its result. The first three leave only indirect traces: a turn that ends on a tool result, a summary followed by a requeued prompt, a line in the application log.
3. **Compaction.** Eight causes reach one function (`agent.go:2128`). Its `trigger` argument has two values, `auto` and `manual`, and reaches only the hooks (`internal/agent/turn_hooks.go:15-19`). The summary message already stores sizes (`CompactionStats`, `internal/message/content.go:203-214`), not the cause.
4. **Shell.** A command that exits non-zero is an ordinary text result with `Exit code N` appended to its output (`internal/agent/tools/bash.go:486-521`). The error flag stays false and the metadata holds no exit code (`bash.go:42-50`). In the window the flag marks 7 of 10,470 shell results as failed. Matching the text finds about 285, and two careful matchers disagree (284 and 289).
5. **Tool errors.** The class of an error exists only as its wording, at more than 160 places in the tools. W2 of the roadmap rewords several of them, so a report that classifies by wording would break when it is needed. One message is already wrong: `write` reports a file that was never read as "modified since it was last read" (`internal/agent/tools/write.go:75-80`).
6. **Prompts.** 5,695 of the 6,285 user rows in the window were written by the harness: reminders, background-job notices, subagent reports. The one predicate for "typed by the user" counts hidden rows as prompts (`internal/message/content.go:240-243`).
7. **`crush stats`** builds an HTML page and opens a browser (`internal/cmd/stats.go:232-242`). It has no text or JSON output, and of its nine queries only the tool-call count reads message parts (`internal/db/sql/stats.sql:75-83`).
8. **`crush run`** prints the assistant's text and nothing else: no session id, no usage, no reason. It has no step cap and no time limit. It exits 0, 1 or 130 (`internal/cmd/root.go:243-251`). A run cancelled by anything but a signal exits 0 (`internal/app/app.go:502-506`), and so does one stopped by the repeated-tool-call guard.
9. **Delegated work.** In non-interactive mode only the dispatch tools are removed (`internal/agent/coordinator.go:1224-1236`). The Team tools stay whenever Agent Teams is on, which is the default (`coordinator.go:1276-1278`, `internal/agent/team.go:50-64`), and a teammate runs on its own 30-minute context, detached from the turn that started it (`team.go:162-185`). `crush run` returns when the first turn of its session ends (`app.go:466`, `:500-509`). It can therefore exit 0 while teammates are still working, and they end with the process. A session whose turn has ended is not woken by a teammate's report: the report waits in its inbox until the session's next Team tool call (`internal/agentteam/team.go:753-809`, `:1042-1044`).
10. **Token totals.** The session row totals context tokens, cache reads and cost, but keeps only the last step's output tokens, and its cost is zero for flat-rate models and for estimated usage (`agent.go:3899-3929`, `internal/db/sql/sessions.sql:74-84`). Each step's own usage is on its finish part (`content.go:152-172`).

## Design

### 1. Stamps

Five fields. All are optional and omitted when empty, so existing rows, tests and exports are unaffected. None reaches a provider: finish parts, compaction statistics and tool-result metadata are not part of a request (`content.go:888-921` builds a tool result from its content alone).

#### 1.1 Build

`build`, on the finish part of every assistant message the agent writes, in every session.

The value is `version.Version`. When that version marks a modified checkout (`+dirty`) or is `devel`, the build fingerprint Crush already computes to detect a stale server (`version.BuildID`) is appended, because two such builds can differ. Examples: `0.100.0-fork.100` for a release, `v0.100.0-fork.100+dirty.<fingerprint>` for a local build.

The roadmap asked for version and commit. A release version is also a git tag (`publish-npm.yml:67`), which names the commit; a local build carries the fingerprint instead.

The agent adds the stamp, not the message type, so message tests do not depend on the binary that runs them. It is not a column and not on the session row: a session outlives builds (sessions here run for days), so only a per-message stamp is true, and the finish part is where per-request usage already lives. The stamp is left out when Crush repairs a message that a dead process left unfinished (`internal/agent/settle.go:160-166`), because the build doing the repair did not write the message.

#### 1.2 Turn end

`turn_end`, on the finish part of the last step of each run. A compaction summary is written inside a run and never carries it. The step's own `reason` field is unchanged.

| Value | The run ended because |
|---|---|
| `completed` | the model stopped calling tools |
| `empty` | the model's last step had neither text nor a tool call |
| `max_tokens` | the last step was cut at the output limit |
| `content_filter` | the provider refused to continue |
| `tool_stop` | a tool result halted the turn: a denied permission, a hook halt, a cancelled question |
| `loop_guard` | the same tool call kept repeating (`internal/agent/loop_detection.go:68`) |
| `repetition_guard` | the output kept repeating |
| `context_guard` | the context was nearly full; the run is compacted and continued as a new run |
| `context_overflow` | the provider rejected the request as too long; the run is compacted and continued when recovery succeeds |
| `compaction_blocked` | a hook blocked the compaction that a `context_guard` or a `context_overflow` required |
| `compaction_failed` | the compaction that a `context_guard` or a `context_overflow` required failed |
| `step_limit` | the caller's step budget was spent (section 3) |
| `time_limit` | the caller's deadline passed (section 3) |
| `canceled` | the user or a caller cancelled |
| `request_timeout` | Crush's own request or stream timeout fired |
| `provider_error` | the provider or the network failed after retries, including sign-in failures and responses the provider cut short |
| `error` | anything else |

When more than one applies, the first match wins:

1. The step loop returned an error. The cause decides, in this order: `time_limit`, `repetition_guard`, `canceled`, `request_timeout`, `context_overflow`, `provider_error`, `error`. `time_limit` applies when the deadline of the run's session tree has passed (3.4). It is read first, so a run stopped by the deadline is not mistaken for a user's cancel or for a provider failure. `repetition_guard` comes before `canceled` because the repetition detector stops a run by cancelling it (`agent.go:1858-1865`).
2. The last step called no tools. The model had already stopped, whatever limit was reached on the same step: `max_tokens`, `content_filter`, `empty`, `completed`.
3. The last step called tools, so something stopped the loop: `context_guard`, `tool_stop`, `loop_guard`, `step_limit`.

Two rules apply after that. A required compaction that a hook blocks turns `context_guard` or `context_overflow` into `compaction_blocked`, and one that fails turns it into `compaction_failed`. A compaction that runs after a completed turn and fails changes nothing: the model finished.

Runs in child sessions go through the same code and are stamped the same way.

A prompt continued after `context_guard` or `context_overflow` produces a second run. Its opening user row is written by the harness but is visible, so today it looks typed. The report recognises it as a continuation by the stamp on the last step before it, looking past the summary.

#### 1.3 Compaction trigger

`trigger`, on the statistics the summary message already carries: `threshold`, `ceiling`, `guard`, `overflow`, `model_switch`, `fusion_preflight`, `fusion_route` or `manual`.

Hooks keep receiving `auto` or `manual`. That payload is documented for users (`docs/hooks/README.md:856`), so the finer value travels beside it, not in its place.

The eight causes arrive through five call sites. Four of them (`threshold`, `ceiling`, `guard`, `overflow`) share one site with identical arguments (`agent.go:1941`), so that site passes which one applied.

#### 1.4 Shell exit code

`exit_code`, in the shell tool's result metadata, for every foreground command that finished, zero included, and `interrupted` when the command was aborted. The result text is unchanged. Commands moved to the background report through notices and are not covered.

#### 1.5 Error kind

`error_kind`, in tool-result metadata, next to the `hook` key that hooks already add there (`internal/agent/hooked_tool.go:143-175`).

| Kind | Set for |
|---|---|
| `not_read` | the edit guard when the file was not read (`edit.go:285`), and `write` when its file was never read |
| `changed_since_read` | the edit and write guards when the file changed after it was read (`edit.go:290-295`, `write.go:75-80`) |
| `text_not_found` | a match failure in `edit` or `multiedit` (`edit.go:229`) |
| `multiple_matches` | a match failure in `edit` or `multiedit` (`edit.go:205`) |
| `permission_denied` | a refused permission (`internal/agent/tools/tools.go:94-98`) |
| `hook_denied` | a hook that blocks or halts the call (`hooked_tool.go:61-73`) |
| `invalid_arguments` | arguments that are not valid JSON, fail validation or do not fit the tool's parameters |
| `unknown_tool` | a call to a tool that does not exist |
| `interrupted` | the result Crush writes for a call its run never answered (`internal/agent/settle.go:16-24`) |

The first six and the last are set by the code that produces the error. The two match failures are plain errors today, told apart only by their wording. They become distinguishable values, and `edit` attaches the kind where it turns the error into its response (`edit.go:386-389`). `multiedit` records each failed edit of a batch in its metadata (`internal/agent/tools/multiedit.go:130-145`), and each record gets the kind.

`invalid_arguments` and `unknown_tool` come from the provider library, so the agent sets them at the one place every result passes before it is stored (`agent.go:1618-1639`), from the library's message prefixes. These two still rest on the library's wording. A test produces each of those errors with the pinned library, so a reworded message fails a test instead of the report.

The list is the set the roadmap measures. It is closed: an error outside it carries no kind and is reported as `other`, and adding a kind is a code change with a test, not a new pattern in the report.

### 2. Report: `crush stats report`

A subcommand, so `crush stats` keeps producing its page. It reads one project's database, opened read-only (`internal/db/connect.go:221`). The page's default path opens the database for writing and runs migrations (`connect.go:83`, `:163`), which a report on a live session should not do.

Flags: `--since` and `--until` (a date such as `2026-11-09` or an age such as `7d`, in local time) and `--json`. The root flags `--cwd` and `--data-dir` select the project.

The output is counts, rates and the names of tools, models, providers, kinds and builds. It never prints text from a message, a title, a path or a session id.

| Section | Contents |
|---|---|
| Window | bounds, messages by role, sessions, builds seen with their first and last message date |
| Tools | per tool: calls, error results, rate, error kinds. `view` and `read` are one tool under two names and also get a combined line |
| Shell | foreground commands, non-zero exits, interrupted |
| Edits | `edit` and `multiedit`: calls and failed calls by kind; failed edits inside batches; what followed a refusal by the read guard; runs of consecutive failed edits |
| Reads | calls, share with an explicit limit, median lines requested, share that touch a file already read, share that page |
| Turns | top-level sessions: prompts, hidden turns, continuations; steps and tool calls per prompt, with and without child sessions (median, 90th percentile, maximum); runs by `turn_end` |
| Sessions | by kind, with each kind's share of steps |
| Injections | harness-written rows by kind: count, sessions, characters |
| Compactions | count by trigger, median tokens before |
| By model | per model and provider: prompts, median steps per prompt, tool error rate, edit failure rate, shell failure rate |
| By build | the same columns per build |

Definitions:

- **Injection**: a hidden user row that carries one of the tags the harness writes (`<system_reminder>`, `<background_job_notice>`, `<env_snapshot>` and the others), or a subagent report. It is named by its tag.
- **Hidden turn**: any other hidden user row. These start a run the user did not type: a goal, a loop iteration, a plan handoff, a wake-up.
- **Continuation**: the user row that reopens a turn after a compaction (1.2).
- **Prompt**: any other user row with a visible text part, in a top-level session, wherever it falls. A prompt typed while a run is in progress is folded into that run by the agent and still counts. The same kind of row in a child session is the brief the session was started with; it is counted with its session and is not a prompt.
- **Steps per prompt**: assistant messages, summaries excluded, between a prompt and the next prompt in the same session. Hidden turns and continuations stay inside the segment of the prompt they follow, so a teammate's report that wakes the session does not cut the user's request in two. The figure is given twice: for the session alone, and with each child session's steps added to the segment that was open when the step was written. A segment is attributed to the model of its first step.
- **Session kind**: from the session id and its parent. A `team-` prefix is a teammate, a `title-` prefix is title generation, `<message>$$<tool call>` is a subagent or `agentic_fetch` according to the tool call that created it, and a session without a parent is top-level. Kinds are resolved after the pass from a map of tool call ids to tool names, because a child can be read before its parent.
- **Failed shell command**: `exit_code` is non-zero, or `interrupted` is set.
- **Edit failure rate**: failed calls over calls, the roadmap's figure (185 of 1,845). Failed edits inside a `multiedit` batch that otherwise succeeded are listed on a separate line and do not enter the rate.
- **After a refusal by the read guard** (`not_read` or `changed_since_read`): the next `edit` or `multiedit` on the same file in the session is the retry, with the same text or a different one; an edit of another file first, or a `write` to the same file, means the model moved on. "Same text" compares a hash of the `old_string` values, so the text is never held.
- **Run of failed edits**: consecutive failed `edit` or `multiedit` results in a session with no successful one between them.
- **Re-read**: a `view` or `read` of a path already requested in the session. **Paging read**: one whose previous `view` or `read` touched the same path and that sets an offset or a limit.
- **Percentiles**: nearest rank.

Rows written before the stamps exist are reported without guessing. Shell exits, error kinds, turn endings, compaction triggers and builds read `unstamped`, and a continuation written before the stamps counts as a prompt. Nothing else in the report depends on a stamp. The roadmap's baseline starts after the stamps land, and the history before it is covered by the one-off scripts.

Implementation: a new package, `internal/logreport`, makes one pass over `messages` in `session_id, created_at, rowid` order, decodes parts with the existing `message` types and keeps state per session only. Its one query is written by hand and read row by row, because sqlc's generated queries return whole result sets and a single `parts` value can exceed half a megabyte. The report owns the list of injection tags; a test in each package that writes one asserts that its text carries the tag the report expects, so the producers themselves do not change.

### 3. `crush run`

One change of behavior, three flags and two new exit codes. All of it applies to the in-process command. With `CRUSH_CLIENT_SERVER=1` the three flags are refused with a message: nothing in the roadmap runs against a server, and that path would need the same work again on the wire.

#### 3.1 The command ends when its session tree is idle

Today the command returns when the first turn of its session ends, and teammates still running are lost with the process (Evidence 9). It will return when nothing is running or queued on the session and no run started from it is live.

Two sources say whether the tree is idle, and both are read. The registry of live runs that the sidebar reads records each subagent and teammate run with its parent session (`coordinator.go:218-220`, `:2427-2492`). Only runs started during the command count, so an entry left by an earlier process cannot hold the command. The Team service knows more about teammates: after the registry already shows a teammate's run as finished, the service marks the teammate inactive, files its report, and starts it again when messages are waiting for it (`internal/agentteam/team.go:753-809`). The tree is therefore idle only when the Team service also shows no teammate running or being started and no unread message addressed to one.

The wait changes what is on disk, not what the session said. A session whose turn has ended is not woken by a teammate's report (Evidence 9), so the result's `text` and `reason` are those of its last run, and the reports it never read are counted (3.2).

Background shell jobs are not waited for: a server started in the background never ends. A background job that finishes during the wait can still wake the session, as it does today (`coordinator.go:629-659`). That run is then part of the command, and whether it happens depends on timing.

This applies with and without `--json`.

#### 3.2 `--json`

Prints one JSON object when the command ends, and nothing else on standard output. The assistant's text, which the command streams today (`app.go:511-539`), is not printed.

```json
{
  "schema": 1,
  "session_id": "…",
  "reason": "completed",
  "text": "…",
  "duration_ms": 184000,
  "model": "…",
  "provider": "…",
  "build": "0.100.0-fork.100",
  "session": {"steps": 12, "tool_calls": 31, "tokens": {"input": 48000, "cache_read": 610000, "cache_write": 22000, "output": 9000}},
  "tree": {"sessions": 3, "steps": 57, "tool_calls": 140, "tokens": {"input": 190000, "cache_read": 2400000, "cache_write": 96000, "output": 41000}, "turn_ends": {"completed": 3}, "undelivered_reports": 0},
  "estimated": false,
  "cost": 0.42,
  "error": "…"
}
```

- The object is computed from the session log the command has just written, by the code the report uses, over rows created after the command started. `session` is the session the prompt was sent to. `tree` adds every session started from it.
- `reason` is the turn-end value of the session's last run (1.2), and `text` is that run's final text. Only runs that a visible user row opened count here (the prompt, or its continuation after a compaction); runs the harness opened with hidden rows, such as a background-job wake-up, and rows it records, such as subagent reports, count in the tree's totals but never decide the reason. One exception: when any run in the tree ended with `step_limit` or `time_limit` during the command, that value is the `reason`, so a turn that a limit cut short in a child session is not reported as completed.
- `tree.turn_ends` counts the runs in the tree by turn-end value. `tree.undelivered_reports` counts the teammate reports the session had not read when the command ended. It is the one figure taken from the Team service and not from the log.
- `tokens` sums the usage each step recorded on its finish part. `estimated` is true when any of those steps had no usage from its provider and Crush estimated it.
- `cost` is what the session row accumulated during the command, child sessions included. It is zero for flat-rate models and for estimated usage, so tokens, not cost, are the figure to compare across models.
- `error` is present when the last run returned an error. It can accompany `completed`, when a compaction after the turn failed; `reason` and the exit code stay those of the completion.

The report's `--json` output carries `schema` as well. Within one schema number fields are only added.

#### 3.3 `--max-steps N`

A budget of N steps for the session tree. Zero, the default, means none.

The budget belongs to the session for the duration of the command, and every run in its tree draws on it: a continuation, a run woken by a background job, a teammate, a subagent. A run finds the budget through its session's parent chain, not through its context: a teammate that is started again begins on a fresh context (`internal/agentteam/team.go:802`), and so does a run woken by a background job. Each run gets a third stop condition beside the two that exist (`agent.go:1734-1765`). Once the budget is spent, every run in the tree stops after its current step and ends with `step_limit`.

Today the step count restarts when a run is continued, because the continuation is a new run (`agent.go:2114`). A budget held on the session does not.

#### 3.4 `--timeout D`

A deadline for the session tree (for example `20m`; the default is none), held on the session like the budget. When it passes, the command cancels the session's run and the runs started from it. A teammate's context is detached from the turn that started it, so teammates are cancelled through the Team service (`internal/agentteam/team.go:1148-1165`). That cancel carries no cause, which is why `time_limit` is decided from the deadline itself (1.2): a run in the tree that is cancelled after the deadline ends with `time_limit`. After the deadline no run in the tree is started or started again. A signal still wins: Ctrl-C exits 130.

#### 3.5 Exit codes

With `--json`:

| Code | Meaning | Turn-end values |
|---|---|---|
| 0 | the model said it was done | `completed` |
| 1 | the command failed before or outside a run: usage, configuration, unknown session | unchanged |
| 2 | the work can be graded, but the model did not complete the turn | `empty`, `max_tokens`, `content_filter`, `tool_stop`, `loop_guard`, `repetition_guard`, `context_guard`, `context_overflow`, `compaction_blocked`, `step_limit`, `time_limit` |
| 3 | the outcome says nothing about the model; run it again | `canceled`, `request_timeout`, `provider_error`, `compaction_failed`, `error` |
| 130 | interrupted by a signal | unchanged |

`context_guard` and `context_overflow` reach this table only when no continuation followed. The Claude Agent provider reports a refusal as a stream error (`internal/claudeagent/stream.go:305-316`), so there a refusal arrives as `provider_error`.

Without `--json` the command keeps today's codes, so existing callers see no change.

`Execute` exits 1 for every error today (`root.go:243-251`). The command returns an error that carries its exit code, and `Execute` uses it.

## Failure behavior

- Writing a stamp never fails a turn or a tool call. A stamp that cannot be computed is omitted.
- The report lists a stamp value it does not know under that value's own name. It skips a row whose parts do not decode and counts it.
- A database with no stamped rows is still reported; the figures that need a stamp read `unstamped`.
- Once a run has started, `--json` prints its object whatever the outcome. When the command fails before a run starts it prints nothing on standard output and exits 1.
- The wait in 3.1 has no bound of its own: a teammate turn is limited to 30 minutes, but a teammate can be started again. `--timeout` is the bound a caller sets.

## Validation

- Unit tests for the build stamp (release, modified and unversioned builds, and a repaired message); for each turn-end value, from a run driven by the scripted test model the agent tests already have (`internal/agent/turn_injections_test.go:71`), including a continued run; for each pair the precedence rules decide (a final answer on the step that spends the budget, a halting tool on that step, the deadline against a cancel, a repetition stop against a cancel, a blocked and a failed required compaction, a failed compaction after a completed turn); for each compaction trigger from its call site; for the exit code; for each error kind where it is produced.
- A request-identity test: the provider request built from a stamped session equals the one built from the same session without stamps.
- Report tests on a database built inside the test: every section, unstamped rows, and a privacy test in which a marker string placed in prompts, answers, tool output, paths and titles must appear in neither output format.
- `crush run`: a run that starts a teammate and ends its own turn first (the command waits, the tree figures include the teammate, and the unread report is counted); a limit that stops a teammate after the session completed (the reason is the limit); the JSON object and the exit code for each row of 3.5; the step budget across a continuation and across a teammate; the deadline stopping a teammate; the refusal of the flags against a server.
- `go build ./...` and `go test ./...`.
- Live checks, reported in the pull requests: the report on the fixed window against the one-off scripts for the figures that need no stamp (44,692 messages, 22,295 tool calls, 628 error results, 590 prompts), and `crush run --json` on DeepSeek V4.1 Flash ending as `completed`, `step_limit` and `time_limit`.

## Scope

Included: the five stamps, the report, and for the in-process `crush run` the wait for the session tree, the three flags and the exit codes.

Left out, with the reason:

- The task suite (W0.4). It is the next design and consumes section 3. Two things are left to it: whether a run that ends with unread teammate reports counts as a completion, and clearing `CRUSH_CLIENT_SERVER` in its runner.
- The three flags against a server. See section 3.
- A reason on the end-of-run event. The command reads the stamp from the log, and nothing else needs it yet.
- A schema migration or a table of runs. See the alternatives.
- Rules that classify old rows from their text. See section 2.
- Other gaps the audit listed: which tools were offered at a step, whether a result was truncated, a turn id, retry attempts within a step. No roadmap metric needs them yet, and each is one more optional field later.
- Background shell jobs: their exit codes, and waiting for them.
- A report across several projects.
- Any change to `IsPrompt`, to the page `crush stats` generates, or to any prompt or tool text.

## Alternatives considered

1. **Classify everything from text when the report runs.** It needs no change to the agent, and it is what the one-off scripts did. Rejected: it misreads shell failures (7, 284 or 289), it cannot see three kinds of turn ending and has to infer four more from wording, and W2 will reword the errors it would match.
2. **Columns and a table of runs.** Clean SQL, at the cost of a migration in a fork that tracks an upstream with its own migrations, new write paths, and a second home for facts the finish part already holds. Rejected for now. If the report becomes slow the stamps can move without changing their values.
3. **End the command with the first turn and report delegated work as pending.** Rejected: in the interactive app teammates keep working after the turn ends, while the command would kill them, so under the default policy the suite would grade work cut off mid-edit, and W8 could not be measured.
4. **A step cap for the first session only.** Rejected: under the default policy the first session delegates, so the cap would bound almost nothing.
5. **The new exit codes for every caller.** Rejected: `crush run ... && next` and `set -e` scripts would change behavior on a guard stop and on a provider failure. A caller that passes `--json` has opted in.
6. **A stream of JSON events, one per step.** Rejected: the suite needs the result, not the stream.
7. **Exit codes from `sysexits.h`.** Rejected: few readers know them, and Crush already uses small integers.

## Delivery

Eight pull requests in this order, each within the roadmap's size target of about 400 changed lines:

1. Build stamp and compaction trigger.
2. Turn end, with its precedence rules.
3. Stamps the tools write: shell exit code, error kind.
4. `crush run` waits for its session tree.
5. `crush run --json` and the exit codes. Needs 2 and 4.
6. `--max-steps` and `--timeout`. Needs 4.
7. `crush stats report`: window, tools, shell, edits, reads.
8. `crush stats report`: turns, sessions, injections, compactions, by model and by build.

Three things differ from the roadmap's W0 list. W0.1 grows from three stamps to five; the two additions, the exit code and the error kind, are what evidence items 4 and 5 show the report cannot do without. W0.3 gains the wait for the session tree, which evidence item 9 shows the suite cannot do without. And the roadmap's `--output json` becomes `--json`, the flag the session commands already use (`internal/cmd/session.go:89-93`).

## Noticed, not changed here

- After the context guard, a compaction blocked by a hook falls through to the code that requeues the prompt (`agent.go:1941-1974`), although the comment beside it says the turn ends there.
- `IsPrompt` counts hidden rows as prompts (`content.go:240-243`). The report does not use it.
