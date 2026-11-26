# Agent Teams

Crush ships an experimental Agent Teams surface ported from the DeepSeek
Harness bundle (`@deepseek-ai/dsh-experimental-agent-team-profile`). The
feature has three components, matching the three DSH packages it replaces:

| DSH package | Crush component |
| --- | --- |
| `@deepseek-ai/dsh-experimental-agent-team` | `internal/agentteam`: durable roster, inboxes, and shared task board |
| `@deepseek-ai/dsh-experimental-tool-agent-team` | `internal/agent/team_tools.go`: the nine model-facing tools |
| `@deepseek-ai/dsh-experimental-client-ui-agent-team` | `crush team`: roster and task-board viewer |

Agent Teams is on by default. The Lead decides whether to work alone or
dispatch teammates; nothing is spawned unless the model calls
`spawn_teammate`. Toggle it from the command palette (ctrl+p, "Agent Teams")
or with `/team on` and `/team off`; the choice is persisted as
`options.agent_teams` in the global config. You can also set it directly:
`option agent-teams false` in `crushrc`, or `"agent_teams": false` in
`crush.json`.

## Model surface

Every session gets the same nine tools and the shared coordination policy:

- `spawn_teammate`: create one named, durable teammate (Lead only).
- `send_message`: steer a running member or wake an inactive one.
- `list_agents`: roster with `target`, role, and availability.
- `wait_agent`: wait for the next roster, message, or task change.
- `interrupt_agent`: stop a teammate's current turn (Lead only).
- `team_task_create`, `team_task_list`, `team_task_get`,
  `team_task_update`: the shared task board.

Teammates run as real Crush sessions with the full coder toolset minus
nested delegation. Their session IDs appear in `crush team status`, and
`crush team session <name>` prints the command that opens one.

`spawn_teammate` takes optional `model`, `provider`, and `reasoning`
arguments, with the same rules as `agent_dispatch`: a model-only override
inherits the Lead's provider, and the teammate runs every turn on the
resolved model. An override the config can no longer serve fails the turn
instead of falling back to the default model.

## Roles

A role is a named definition of how an agent works: a prompt, a tool
allow-list, and an optional model. The Lead gives a teammate or subagent a role
with the `role` argument, and the role adds its prompt to the message the agent
starts from, restricts the tools it may actually call, and may set the model it
runs on. Roles are optional: an agent without one behaves exactly as it did
before roles existed.

### Built-in roles

Crush ships five roles, embedded in the binary:

| Role | Job |
| --- | --- |
| `architect` | Designs a change from the real code, ending in options, a decision, and a task breakdown with acceptance criteria and write scopes. Read-only. |
| `researcher` | Investigates technologies, libraries, APIs, and alternatives, and answers one question with cited sources. Read-only. |
| `reviewer` | Challenges a design or a diff for bugs, risks, missed edge cases, and simpler options, with a verdict of `approve` or `changes-required`. Read-only. |
| `implementer` | Carries out one scoped task end to end, test-first, and verifies it with real command output. |
| `verifier` | Checks finished work against its acceptance criteria by running every check itself, with a verdict of `PASS` or `FAIL`. Read-only. |

`architect`, `researcher`, `reviewer`, and `verifier` allow no file-editing
tool. They keep `bash` for `git log`, tests, and builds, and permission prompts
still apply to every call.

### Where role files are read from

Role files are markdown files named `<role>.md`. They load from these
directories, lowest priority first:

1. Built-in roles embedded in the binary.
2. `$CRUSH_AGENTS_DIR` if set, otherwise `~/.config/crush/agents`.
3. `<git root>/.crush/agents`.
4. `<working dir>/.crush/agents`.

In a monorepo the working directory's file therefore wins over the repository
root's. Files are read at startup and on every config reload; there is no file
watcher and no `crushrc` builtin for roles.

### File format

```markdown
---
name: reviewer
description: Challenges assumptions and finds risks in a design or diff.
tools: view, grep, glob, ls, bash
model: deepseek-v4.1-flash
provider: neuralwatt
reasoning: high
---
Role prompt body.
```

| Field | Required | Rule |
| --- | --- | --- |
| `name` | yes | lower-kebab-case, equal to the file name without `.md`; `coder`, `task`, and `plan` are reserved |
| `description` | yes, unless overlay | one line, at most 200 bytes; shown in the role catalog |
| `tools` | no | comma-separated string or YAML list; exact tool names, or a prefix ending in `*` (`mcp_*`); omitted means every tool |
| `model`, `provider`, `reasoning` | no | same rules as the `agent_dispatch` overrides: a model without a provider uses the parent's provider, and a model the config cannot serve fails the call that uses it |
| body | no | at most 8 KiB; a file with no body is an overlay |

`tools` cannot be empty: an empty list is a load error, so omit the field to
allow every tool. An exact tool name that matches no known tool is dropped with
a warning. A `*` pattern is kept even when it matches nothing, because an MCP
server can connect later.

### Replacement and overlays

A file with a body **replaces** the role of the same name below it whole:
nothing is inherited, so a field it leaves out takes its default. A file with
no body is an **overlay**: it inherits every field it leaves out, including the
body, from the next lower role of the same name. Overlays stack, so a project
overlay can sit on a global overlay on a built-in.

An overlay is how you pin a model on a built-in role without copying its
prompt, and the built-in keeps improving underneath. This is the whole of
`~/.config/crush/agents/reviewer.md`:

```markdown
---
name: reviewer
model: claude-opus-5-5
provider: claude-agent
---
```

`model`, `provider`, and `reasoning` are one group: an overlay that sets any of
them replaces all three, so a provider meant for the old model never sticks to
a new one. An overlay must still repeat `name`, and the name must match the
file. An overlay with no role of that name below it is skipped with a
diagnostic.

### Passing a role

`role` is an optional argument on three tools:

- `spawn_teammate` gives a teammate the role for its whole life. The role text
  goes into the teammate's first message, after any inherited context and
  before its name; later turns and `send_message` calls do not repeat it.
  `list_agents`, the sidebar, and `crush team status --json` show the role as
  `role_name`.
- `agent_dispatch` gives a detached subagent the role. The role text goes in
  front of the prompt it was dispatched with.
- `agent` gives a synchronous subagent the role. Its existing `agent` preset
  parameter also accepts a role name, so `agent: "reviewer"` works as well as
  `role: "reviewer"`.

An unknown role is a tool error that lists the roles that exist. A role that is
removed while a teammate exists fails that teammate's next turn with
`role "x" no longer exists`. The role catalog in the delegation tools'
descriptions shows each role's source and whether it is `read-only` or `can
write`, and a project role that pins a model is marked `pins model`, so a
cloned repository cannot quietly move spend to another model.

**Model precedence:** an explicit `model`, `provider`, or `reasoning` on the
spawn or dispatch, then the role's, then the shared subagent model. The spawn or
dispatch `model`, `provider`, and `reasoning` are one group: setting any of them
replaces the role's group whole. The `agent`
tool takes no model override, so a role there runs on its model or the shared
subagent model. A role's model that the config cannot serve fails the call,
naming the model; nothing falls back.

**`access` only narrows.** A role that allows no file-editing tool is read-only
and runs on the read-only subagent variant even when `access` is omitted.
`access: "read"` narrows a write role to that variant. `access: "write"` on a
read-only role is a tool error, and a plan-mode parent, which may only dispatch
read-only subagents, refuses a write role unless you pass `access: "read"`.

### Caching

A role never changes an agent's tools block or system prompt: it runs on one of
the existing tool lists and system prompts, the same ones an agent without a
role uses, and the allow-list is checked when a tool is called instead. A
refused call comes back as a tool error that names the role and its allowed
tools, before any hook or permission prompt. Team coordination tools
(`send_message`, `list_agents`, `wait_agent`, and the four `team_task_*` tools)
always pass.

The role text depends only on the role, so it lives in the first message of the
agent (and at the top of the state message a compaction re-attaches), never in
the shared prefix. Teammates with the same role send the same role bytes, so
siblings share them in the provider's cache and diverge only at the name.

### Diagnostics

`crush_info` lists every loaded role and any problem found while loading under
`[roles]`:

```
[roles]
implementer = builtin, model inherits
reviewer = global, model claude-agent/claude-opus-5-5
! /home/me/.config/crush/agents/security.md: unknown tool "grep2" dropped
```

Each role line is `name = source, model ...`, where the source is `builtin`,
`global`, or `project`, and `model inherits` means the role pins none. A
diagnostic starts with `!` and names the file and the reason. A broken file is
skipped and the rest still load.

## Pipeline

A shared task can name a role. `team_task_create` and the `edit` action of
`team_task_update` take `role` (any role from the `spawn_teammate` catalog)
and `verify`. A task without a role stays manual: nothing starts it. Only the
Lead can set a task's `verify`, or change the `role` or `verify` of an
existing task; a teammate's request is refused. Only the Lead can reopen a
task in review, or complete it to accept it as is.

### Auto-start

When a task that names a role becomes ready (pending, unowned, every blocker
completed), the team service starts a teammate on it. It reuses the
earliest-created idle teammate of that role spawned by the same Lead session,
or spawns one named `<role>-<n>` with the lowest free number, within the
live-member limit. At the limit the task waits for a later pass. The teammate
gets a short brief with the task, its write scopes and how to complete it.

A start that fails, or a turn that fails, puts the task back to pending and
counts an attempt. After three attempts the task is escalated: the
dispatcher leaves it alone and the Lead gets a message. An interrupted turn
escalates at once. A task whose role no longer exists is skipped and
reported to the Lead once. The Lead takes a task back from escalation with
`edit`, `reassign` or `reopen`, which also reset its attempts.

Auto-start is on by default. Turn it off with `option agent-team-autostart
false` in `crushrc`, or `"agent_team_autostart": false` in `crush.json`; the
setting is read on every pass, so a reload applies at once. With auto-start
off nothing starts on its own: a role task waits for the Lead to spawn a
teammate and start the work, and finished verified work still moves to
`in_review` with its check `pending` until the Lead starts or claims a
verifier.

The escalation notice names only the actions that work at the task's current
status: a pending task can be edited or reassigned with no owner, an
in-progress task can be released or reassigned with no owner, and an
`in_review` task can be reopened, completed to accept it, or deleted.

### Verification

`verify` defaults to true for role `implementer` and false otherwise. When a
verified task completes (by `complete`, or by its owner's turn ending), it
moves to `in_review` and the service creates a verification task, role
`verifier`, that names the original. Dependents wait until the original is
`completed`. The verifier records its verdict with `team_task_update`:
`pass` completes both tasks; `fail` (with `notes`) completes the
verification task and hands the original back to its owner, with the notes,
as `in_progress`. If the owner's turn is still running, the failure waits in
its inbox and that turn settles with the task left in progress, so the owner
reads the notes before anyone reviews the work again. If the start that would
resume the owner is refused, the task stays in progress with nobody running
it and the Lead gets a notice naming the task and the error. The third failed
verification escalates the original, which stays `in_review` until the Lead
reopens it. The Lead can also complete an `in_review` task to accept it as
is: that finishes the task, readies its dependents, deletes its open check,
and a late verdict on that check is refused. Only the Lead can complete work
in review. A verifier that ends its turn without a verdict releases the
verification task and counts an attempt; nothing passes by default.

A member cannot claim the verification task of work it owns, and cannot pass
or fail that check: the service refuses it, so the verifier stays
independent of the implementer.

The verifier runs every check itself, but it shares the working directory
with everyone else, so it sees whatever is on disk when it runs.

### Write scopes

A teammate's `edit`, `multiedit`, `write`, `download`, `apply_patch` and
`lsp_replace_symbol` calls are checked against the write scopes of the tasks
it owns in progress; a path outside them is refused with a tool error, before
any hook or permission prompt. `lsp_rename` is refused while scopes apply,
because the files it changes are not known in advance. A teammate with no
scoped task is not restricted, and the Lead never is. `bash` is not covered:
a command can write anywhere. Symlinks are not resolved: the checked path is
the one in the call, made relative to the working directory and cleaned.
Per-teammate worktrees are the planned fix.

### Hooks

Teammates fire `PreToolUse`, `PostToolUse` and `PostToolUseFailure` with
their own session id, like the Lead. A `PreToolUse` hook that returns
`allow` pre-approves the teammate's permission prompt, exactly as it does
for the Lead. Dispatched subagents still fire none of them. Teammates fire
no turn hooks (`TurnStart`, `TurnEnd`, `Interrupt`, `StopFailure`), because
their calls are non-interactive.

None of this changes a tool definition or a system prompt per agent: the
brief, the verification task and every notice travel as messages.

## Durable state

State lives at `<data-directory>/agent-teams/team.json` (by default
`.crush/agent-teams/team.json` under the project). It holds the roster,
task board, and mailboxes. Writes are atomic, and a restarted process
normalizes any stale `running` status to `inactive`.

Task updates are compare-and-set: every row carries a `revision`, and an
update based on an outdated copy is rejected with
`TEAM_TASK_STALE_REVISION`. Readiness follows `blocked_by`: a blocker counts
only once it is `completed`, so an `in_review` blocker keeps its dependents
waiting. Write scopes produce overlap warnings for in-progress tasks and are
enforced for teammates' file tools (see [Pipeline](#pipeline)).

## Client UI

The chat sidebar shows an **Agent Team** section for the current session as
soon as it has a teammate or a shared task: a row for the Lead, one row per
teammate with its availability glyph, then every live shared task in
priority order (in progress, in review, ready, blocked), followed by a count
of completed tasks. It refreshes every second and hides itself when Agent
Teams is disabled.

The command line offers the same view:

```sh
crush team                  # roster and task board
crush team status --json    # machine-readable snapshot
crush team watch            # live refresh until interrupted
crush team session reviewer # print the command to open a teammate session
```

## Viewing agent transcripts

Open the command palette and choose **View Agents** to inspect a running or
recently completed subagent or teammate. The viewer shows the agent's task,
reasoning, responses, and tool activity as they stream. It is read-only: agents
continue running and the main session keeps updating underneath.

Use the normal chat keys to scroll, expand, and copy content. Press **Esc** to
return to the main session without cancelling either agent.

## Differences from DSH

- The `agent` tool stays available alongside the Team tools; DSH's bundle
  disables its legacy subagent controls because they share tool names with
  the Team surface.
- `fork` teammates inherit the Lead's completed turns as a bounded
  transcript prefix rather than a provider-level fork.
- Inbox messages reach a running member through its next tool result,
  which is the closest equivalent to a step-boundary steer.
- Teammates share the working directory; there is no worktree isolation.
- A shared task can name a role, and a ready one starts a teammate of that
  role on its own; DSH task readiness never starts an owner. Tasks without a
  role behave as in DSH.
- Implementer work goes through a verification task before its dependents
  start.
