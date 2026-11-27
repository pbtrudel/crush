# Agent Team Pipeline Design

Sub-project 2 of role-based orchestration. Sub-project 1 (agent roles, `docs/superpowers/specs/2026-11-25-agent-roles-design.md`) shipped in PR #186. This spec also covers four fixes found while scoping it, which ship in the same pull request at the owner's request. Line numbers are at `main` 318cc96e0.

## Goal

The Lead should not have to start every teammate by hand. A task on the shared board can name a role; when it becomes ready, the team service starts a teammate of that role on it. Finished implementer work is checked by a verifier before the tasks that depend on it start. A teammate cannot edit files outside the write scopes of the task it claimed. Everything is deterministic Go in the team service: coordination costs no model tokens, and it behaves the same in the TUI, web mode and `crush run`.

Prompt caching stays the first constraint: no tool definition, tool list or system prompt may differ between the Lead and any teammate. Pipeline text travels in messages only. Two static changes happen once, with the same bytes for every agent: the Policy sentence (section 1) and the new optional parameters `role` and `verify` on `team_task_create` and `role`, `verify`, `notes` on `team_task_update`.

Decisions made while planning (see the plan for detail):
- A dispatch pass only considers the Lead session tree of the write that triggered it, so one Lead's board never starts another Lead's teammates.
- Lead notices (escalation, removed role) come from a "Crush" sender scoped to that Lead's session, so they reach only that Lead's inbox and wake it.
- A failed teammate turn on a role task counts as an attempt; an interrupted turn escalates at once, so a teammate the Lead stopped is not restarted on its own.
- `reopen` also accepts an `in_review` task, so the Lead can recover a task escalated after three failed verifications.
- `Task.RoleReported` records that a removed role was reported. Task roles are at most 60 characters, so `<role>-<n>` fits the 64-character member name limit.
- Verification tasks do not count against `MaxTasks`.
- Skills are bounded in `Parse` and in `ReadContent` (`internal/skills/catalog.go`); there is no `ReadSkill`.

Non-goals: per-teammate worktrees, enforcing write scopes for `bash`, an LLM "manager" agent, running the eval suite.

## Current code

- **Tasks.** `Task` (`internal/agentteam/team.go:226-240`) has no role. Statuses are `pending`, `in_progress`, `completed`, `deleted` (`:112-120`). Readiness is computed on read only (`taskReady` `:2242-2250`, `projectTaskView` `:2270-2314`). Updates are compare-and-set on `Revision` (`:2029-2034`). Actions live in `applyTaskUpdateLocked` (`:2005`); `reassign` sets an owner but starts nobody (`:2115`).
- **Settling.** `settleTasksLocked` (`:1219-1242`) auto-completes a member's in-progress tasks after a successful turn and releases them after a failed one.
- **Starting members.** `SpawnTeammate` (`:929`, Lead-only, live cap `DefaultMaxMembers = 8` at `:29`, counted at `:963-971`), `startRun`/`launch` (`:1017`, `:1036`), `watch` (`:1108`) settles a run, writes the report, drains the inbox and resumes. `SendMessage` (`:1417`) wakes an inactive target (`:1470-1478`).
- **Hooks out of the service.** `SetTaskCompletedHook(sessionID, subject)` (`:494`) and `SetLeadNotifyHook(sessionID)` (`:503`). The Lead is already woken when a teammate reports (`internal/agent/coordinator.go:765-800`, wired at `internal/agent/team.go:124`).
- **Policy.** `agentteam.Policy` (`:46-66`) says "Task readiness never starts an owner" (`:64`).
- **Write scopes** are validated (`normalizeWriteScopes` `:2209-2231`) and produce overlap warnings only (`:2294-2313`). Edit tools read only the session id (`internal/agent/tools/edit.go:137`, `write.go:62`). The member name does reach tool calls through `agentteam.WithMember` (`internal/agent/team.go:199`).
- **Hooks for teammates.** `wrapToolsWithHooks` skips every non-Lead agent (`internal/agent/hooked_tool.go:31-40`; `isSubAgent` is true for teammates, `coordinator.go:1285`), so user hooks never fire for teammates.
- **Bug A.** `hookedTool` (`hooked_tool.go:42-54`) has no `MCP()`. The hook runner always exists (`internal/app/app.go:142`), so every Lead tool is a `hookedTool`, and `filterToolsForChannel` (`internal/agent/agent.go:259-264`) and `dropDisabledMCPTools` (`agent.go:4068-4071`) treat Lead MCP tools as built-ins. The Lead keeps tools from disabled servers and, on a channel turn, other channels' tools; teammates filter correctly, so the Lead's and teammates' tool blocks differ and stop sharing a cache.
- **Bug B.** Role files are read with `fs.ReadFile` over `os.DirFS` (`internal/roles/discover.go:102`), skills with `os.ReadFile` (`internal/skills/skills.go:150-152`, `internal/skills/catalog.go:99`). Neither checks for a regular file or caps the size, so a FIFO or a symlink to `/dev/zero` named like a role or skill hangs or exhausts memory at startup.
- **Bug C.** `teammatePrompt` prepends the fork prefix on every turn (`internal/agent/team.go:64-75`, fork branch `:71-73`), although `Start` computes a first-turn flag (`:231`). Each wake re-sends up to 16 KB of the Lead transcript already in the teammate's history.

## Design

### 1. Task fields and the dispatcher

New `Task` fields, all `omitempty` so old `team.json` files load unchanged:

| Field | JSON | Meaning |
| --- | --- | --- |
| `Role` | `role` | Role that should do the task. Empty: the task stays manual. |
| `Verify` | `verify` | `*bool`. Nil: effective value is true for role `implementer`, false otherwise. |
| `Verifies` | `verifies` | Set by the service on a verification task: the id of the task it checks. |
| `Attempts` | `attempts` | Failed auto-starts plus failed verifications. |
| `Escalated` | `escalated` | The dispatcher no longer touches the task; the Lead decides. |

`TaskView` exposes all five. `team_task_create` and the `edit` action accept `role` and `verify`. The tool layer validates `role` with `resolveRole` and refuses an unknown name with the same refusal `spawn_teammate` gives. `edit`, `reassign` and `reopen` by the Lead clear `Escalated` and reset `Attempts`.

**The dispatch pass** is a `Service` method run after every write that can make work possible: task create, edit, complete (and the new `pass`/`fail`), a member's turn settling, and a spawn. It plans under the state lock and starts members after releasing it.

1. Candidates: tasks that are `pending`, ready, unowned, have a `Role`, and are not `Escalated`, in creation order.
2. For each, the chosen member is the earliest-created member with `RoleName == task.Role`, status `inactive`, not `Failed`, owning no `in_progress` or `in_review` task, whose `ParentID` equals the task's `SessionID` (the Lead session that created it).
3. With no such member, spawn one named `<role>-<n>` (lowest unused `n`), with `RoleName` set, description `Auto-started for task <id>`, `ParentID` the task's session, provided the live-member count is below `MaxMembers`. At the cap the task waits for a later pass.
4. Claim the task for the member (owner set, `in_progress`, revision bumped) and start it with a brief message:

```
Task <id> on the shared board was claimed for you: <subject>

<description>

Write scopes: <comma-separated scopes, or "none">
Complete it with team_task_update (action "complete", expected_revision <rev>) when the acceptance criteria are met.
```

For a verification task the last line reads: `Record your verdict with team_task_update, action "pass", or action "fail" with notes describing what failed.`

The brief is per-task text in a message; the role block (sub-project 1) still precedes it on a member's first turn, so siblings with one role share their prefix.

A task is never started twice: claiming is a revision-checked write, and a pass that loses the race skips the task. A start that fails releases the task to `pending`, increments `Attempts`, and marks the member failed as today. At `Attempts >= 3` the task is `Escalated` and the Lead gets a message: `Task <id> (<subject>) could not be started: <reason>. It is escalated; edit, reassign or reopen it to continue.`

The pass does nothing when Agent Teams is off or `options.agent_team_autostart` is false (default true; crushrc `option agent-team-autostart false`; JSON `"agent_team_autostart": false`). The agent layer passes the setting to the service through a new `Config` field read on every pass, so a config reload applies without a restart.

A task whose `Role` names a role that no longer exists is skipped and the Lead is messaged once (the task records that it was reported).

**Policy.** The sentence "Task readiness never starts an owner." becomes "A ready task that names a role starts on its own; tasks without a role stay manual." This is the only system-prompt change, a one-time cache miss of the kind sub-project 1 accepted.

### 2. Verification

A new status `in_review` sits between `in_progress` and `completed`.

- `complete` on a task whose effective `Verify` is true moves it to `in_review` instead of `completed`. The same applies when `settleTasksLocked` auto-completes such a task.
- Entering `in_review` creates a verification task in the same write: subject `Verify: <subject>` (truncated to the subject limit), role `verifier`, `Verifies` = the original id, `Verify` false, no write scopes, description:

```
Verify task <id>: <subject>

Acceptance criteria and description of the original task:
<original description>

Implemented by: <owner name>
Write scopes: <scopes or "none">
The change is in the shared working directory; inspect it with git status and git diff.
```

- Readiness treats a blocker as satisfied only when it is `completed`; an `in_review` blocker keeps dependents waiting.
- New `team_task_update` actions, allowed only on a task with `Verifies` set and only for its owner:
  - `pass`: the verification task and the original become `completed`.
  - `fail` (requires `notes`): the verification task becomes `completed`; the original returns to `in_progress` with its owner, `Attempts` incremented, and the owner is messaged `Verification of task <id> failed:\n\n<notes>\n\nFix it and complete the task again.` (which wakes it). At `Attempts >= 3` the original is instead `Escalated`, its status `in_review` is kept, and the Lead is messaged with the notes.
- `settleTasksLocked` never auto-completes a verification task. One left `in_progress` after a successful turn is released to `pending` with `Attempts` incremented; at 3 it is `Escalated` and the Lead is messaged. A verifier that never decides cannot pass work by default.
- `in_review` and `escalated` appear in `team_task_list`, the sidebar Agent Team section and `crush team` output.

### 3. Enforced write scopes

A `scopeGatedTool` wraps these tools for teammates (role `roleTeammate`) only: `edit`, `multiedit`, `write`, `apply_patch`, `lsp_rename`, `lsp_replace_symbol`. Its `Info()`, provider options and `MCP()` forward to the inner tool, so the tools block is unchanged.

On each call it reads the member name from the context and asks the service for the union of `WriteScopes` of tasks the member owns in `in_progress`. With no such task, or none with scopes, the call passes. Otherwise:

| Tool | Paths checked |
| --- | --- |
| `edit`, `multiedit`, `write`, `lsp_replace_symbol` | `file_path` |
| `apply_patch` | every path in the patch's file headers; all must pass |
| `lsp_rename` | refused, because the files it changes are not known in advance; the refusal suggests `edit` |

A path is made relative to the working directory and cleaned (`..` resolved, absolute paths relativized; a path that escapes the working directory fails). It passes when it equals a scope or lies under a scope directory. A refusal is a tool error naming the path and the scopes:

```
path "internal/ui/x.go" is outside the write scopes of your claimed task: internal/agent/, docs/agent-teams.md
```

The scope gate sits outside the hook wrapper, so a refused call runs no hook and raises no permission prompt. `bash` is not covered; the docs say so and name per-teammate worktrees as the future fix.

### 4. Teammate hooks (fix D)

`wrapToolsWithHooks` wraps tools for teammates as well as the Lead; dispatched subagents still skip hooks. Teammates fire `PreToolUse`, `PostToolUse` and `PostToolUseFailure` with their own session id. A teammate tool is wrapped, outermost first: role gate, scope gate, hook wrapper, tool.

### 5. Bug fixes A, B, C

- **A.** `func (h *hookedTool) MCP() string` returns the inner tool's `MCP()`, or `""`. The Lead's MCP tools are filtered again, and the Lead's tools block matches its teammates' when a server is disabled or a channel is active.
- **B.** `fsext.ReadRegularFileLimit(path string, max int64) ([]byte, error)` and an `fs.FS` variant `fsext.ReadRegularFSFileLimit(fsys fs.FS, name string, max int64)`. Both stat first (following symlinks), refuse anything that is not a regular file before opening it, and read at most `max` bytes, failing above. Roles use `MaxBodyBytes + 4 KiB`; skills use a new `skills.MaxSkillFileBytes = 256 KiB` in `Parse` and `ReadContent`. A refused file is a diagnostic, like any other broken file.
- **C.** The fork prefix is prepended only on a member's first turn (`teammateFirstTurn`), like the role block.

### 6. Errors

| Case | Behavior |
| --- | --- |
| Unknown `role` on create or edit | Tool error listing available roles |
| `pass`/`fail` on a non-verification task, or by a non-owner | Tool error |
| `fail` without `notes` | Tool error |
| Auto-start fails | Task back to pending, `Attempts`+1; at 3 escalated, Lead messaged |
| Member cap reached | Task waits for a later pass |
| Task role no longer exists | Task skipped, Lead messaged once |
| Verifier gives no verdict | Verification task released, `Attempts`+1; at 3 escalated |
| Third failed verification | Original escalated in `in_review`, Lead messaged |
| Path outside claimed scopes | Tool error naming path and scopes; no hook runs |

### 7. Testing

1. Dispatcher (`internal/agentteam`, fake runner): idle member of the role chosen; `<role>-N` spawned when none; cap respected; role-less tasks untouched; concurrent passes start a task once; three failed starts escalate; option off disables; removed role reported once; members of another Lead session are never chosen.
2. Hand-off: A blocks B; completing an unverified A starts B; a verified A starts B only after `pass`.
3. Verification: `complete` moves to `in_review` and creates the verification task; `pass`; `fail` reopens with notes and wakes the owner; third `fail` escalates; no verdict releases and escalates at 3; Lead `edit`/`reassign`/`reopen` clears escalation.
4. Scope gate: each tool; `apply_patch` with several files; `..`, absolute, and escaping paths; `lsp_rename` refused under scopes; no scopes, no restriction; a refused call runs no hook.
5. Cache: teammates with any role, scopes and hooks send the Lead's exact tools block and system prompt (extend `team_cache_test.go`, with a non-nil hook runner so the test reflects production).
6. Fixes: A (hook-wrapped MCP tool dropped by both filters; Lead and teammate palettes equal), B (FIFO and `/dev/zero` symlink refused without hanging; oversize skill skipped), C (second-turn fork prompt has no inherited context), D (a blocking `PreToolUse` hook stops a teammate call).
7. Compatibility: an old `team.json` loads; a role-less task behaves as today.

### 8. Documentation

- `docs/agent-teams.md`: a Pipeline section (roles on tasks, auto-start and its option, verification and its limits, write scopes and the `bash` gap, teammates running hooks); update the "Differences" and Policy-related lines.
- `docs/hooks/README.md`: teammates fire tool hooks.
- `crush-config` builtin skill: `agent-team-autostart`.

## Out of scope

Per-teammate worktrees, `bash` scope enforcement, an LLM pipeline manager, batching or deduplicating Lead wake-ups, the eval suite.
