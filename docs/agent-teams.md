# Agent Teams

Crush ships an experimental Agent Teams surface ported from the DeepSeek
Harness bundle (`@deepseek-ai/dsh-experimental-agent-team-profile`). The
feature has three components, matching the three DSH packages it replaces:

| DSH package | Crush component |
| --- | --- |
| `@deepseek-ai/dsh-experimental-agent-team` | `internal/agentteam` — durable roster, inboxes, and shared task board |
| `@deepseek-ai/dsh-experimental-tool-agent-team` | `internal/agent/team_tools.go` — the nine model-facing tools |
| `@deepseek-ai/dsh-experimental-client-ui-agent-team` | `crush team` — roster and task-board viewer |

Agent Teams is on by default. The Lead decides whether to work alone or
dispatch teammates; nothing is spawned unless the model calls
`spawn_teammate`. Toggle it from the command palette (ctrl+p, "Agent Teams")
or with `/team on` and `/team off`; the choice is persisted as
`options.agent_teams` in the global config. You can also set it directly:
`option agent-teams false` in `crushrc`, or `"agent_teams": false` in
`crush.json`.

## Model surface

Every session gets the same nine tools and the shared coordination policy:

- `spawn_teammate` — create one named, durable teammate (Lead only).
- `send_message` — steer a running member or wake an inactive one.
- `list_agents` — roster with `target`, role, and availability.
- `wait_agent` — wait for the next roster, message, or task change.
- `interrupt_agent` — stop a teammate's current turn (Lead only).
- `team_task_create`, `team_task_list`, `team_task_get`,
  `team_task_update` — the shared task board.

Teammates run as real Crush sessions with the full coder toolset minus
nested delegation. Their session IDs appear in `crush team status`, and
`crush team session <name>` prints the command that opens one.

## Durable state

State lives at `<data-directory>/agent-teams/team.json` (by default
`.crush/agent-teams/team.json` under the project). It holds the roster,
task board, and mailboxes. Writes are atomic, and a restarted process
normalizes any stale `running` status to `inactive`.

Task updates are compare-and-set: every row carries a `revision`, and an
update based on an outdated copy is rejected with
`TEAM_TASK_STALE_REVISION`. Readiness follows `blocked_by`, and write
scopes produce advisory overlap warnings for in-progress tasks.

## Client UI

The chat sidebar shows an **Agent Team** section as soon as a teammate
exists: one row per member with its availability glyph, followed by up to
three shared tasks with their status. It refreshes every two seconds and
hides itself when Agent Teams is disabled.

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
