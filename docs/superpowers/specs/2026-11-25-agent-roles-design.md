# Agent Roles Design

Sub-project 1 of role-based orchestration. Sub-project 2 (the pipeline: automatic task claiming, hand-offs, Lead wake-up, an independent verification stage) builds on this one and gets its own spec. Line numbers are at `main` d4b395c97.

## Goal

The Lead can give a teammate or a subagent a **role**: a named definition with a prompt, a tool allow-list and an optional model. Crush ships five built-in roles (architect, researcher, reviewer, implementer, verifier) and loads more from markdown files. A role never changes the cached request prefix (tool list and system prompt), so prompt caching stays at least as good as today and gets better for siblings that share a role.

Non-goals: the pipeline (sub-project 2), loading agent files from other tools' folders (`.claude/agents`, `~/.commandcode/agents`), a crushrc builtin for roles, hot reload on file change.

## Current code

- **No way to declare an agent.** `Config.Agents` is `json:"-"` (`internal/config/config.go:1347`). `SetupAgents` hardcodes `coder`, `task` and `plan` (`config.go:1771-1808`). `schema.json` has no `agents` key, and shellconfig has no agent builtin (`internal/shellconfig/register.go:22-28`). `config.Agent` has no prompt field and its `Model` is only the `main`/`small` slot (`config.go:1258-1279`).
- **Presets exist for the `agent` tool only.** Its `agent` parameter resolves against `cfg.Agents` (`internal/agent/agent_tool.go:25-31`, `resolveSubAgentPreset` `:227-246`), and the preset list is appended to its description (`:150-152`). Every preset is built from the `task` template (`buildTaskAgent`, `:131-139`). `agent_dispatch` has no preset parameter (`internal/agent/agent_dispatch.go:68-80`). `spawn_teammate` has none either (`internal/agent/team_tools.go:33-44`), and `SpawnRequest` carries no role (`internal/agentteam/team.go:266-276`).
- **Teammates are built for the cache.** `teammateAgent` reuses the Lead's prebuilt system prompt verbatim plus a `teamIdentity` suffix that carries no member name (`internal/agent/team.go:20-45`, `:273-311`). The name goes in the first message, after the fork prefix (`teammateTaskMessage` and `teammatePrompt`, `team.go:47-66`). Teammates keep the Lead's full tool list for cache parity and refuse nested delegation when it is called (`effectiveToolNames`, `internal/agent/coordinator.go:1462-1484`; refusal at `agent_tool.go:165-167`). `team_cache_test.go` pins this.
- **Two subagent tool lists.** The read variant is every tool minus delegation and file-editing tools (`resolveSubAgentTools`, `config.go:1755-1757`; masks `subAgentExcludedTools` `:1712-1729` and `subAgentWriteTools` `:1740-1748`). The write variant is the coder's list minus delegation (`writeAgentConfig`, `agent_tool.go:44-55`). `bash` is in both.
- **Member roles are only lead/teammate.** `agentteam.Member.Role` (`team.go:82-88`, `:192-207`).
- **Compaction re-attaches state in a hidden message.** `compactionStateText` and `createCompactionStateMessage` store todos, the env snapshot, changed files and carried skills after the summary (`internal/agent/compaction_state.go:224-244`).
- **Skills show the loader pattern.** Embedded builtins (`internal/skills/embed.go:14-22`), global dirs with an env override (`GlobalSkillsDirs`, `internal/config/load.go:1652-1683`), project subdirs at the working dir then the git root (`load.go:1685-1700`).

## Design

### 1. Cache invariant

A role never touches the tool list or the system prompt. Everything role-specific goes after the shared prefix.

**The role block lives in the first message.** A teammate's first message becomes:

```
[fork context, if any] [role block] You are teammate "x".\n\n[task]
```

The role block comes before the name, so siblings with the same role share it in the cache and diverge only at the name. A role-less teammate's first message is unchanged. A dispatched subagent's first message is `[role block][prompt]`.

The role block is rendered by one function, `roles.Block(role)`:

```
## Your role: <name>

<body>

Allowed tools: <the role's tool entries as written, sorted, or "all tools">
Team coordination tools, when present, are always allowed.

---

```

The text depends only on the role, never on the member, so it is byte-stable.

**Tool restrictions are enforced at call time.** Every agent keeps its base tool list. A `roleGatedTool` decorator wraps each tool and refuses a call to a tool outside the role's allow-list with a tool error. The match runs on the tool name at call time, so an MCP server that connects after the role loaded is covered by a `mcp_*` entry. The gate is applied inside `sessionAgent.SetTools` (and at construction), because the coordinator replaces an agent's tools from several places (`coordinator.go:1459`, `:2513`, `:2604`) and none of them may skip it:

```
tool "edit" is not available to role "reviewer". Allowed tools: bash, glob, grep, ...
```

The gate sits outside `hookedTool`, so a refused call runs no hooks and raises no permission prompt. Team coordination tools (`send_message`, `list_agents`, `wait_agent`, `team_task_create`, `team_task_list`, `team_task_get`, `team_task_update`) always pass. Delegation stays refused for teammates exactly as today.

**Subagents map onto the two existing tool lists.** A role is read-only when its effective tools contain none of `subAgentWriteTools`. A read-only role runs on the read variant, any other role on the write variant, and the gate narrows from there. No new prompt or tool-list variant exists, so no new cache prefix has to warm.

**Pinned models.** A role that pins a model cannot share a cache with a Lead on another model. Siblings with the same role and model still share one. Built-in roles pin nothing.

### 2. Role files and discovery

New package `internal/roles`.

```go
type Source string // "builtin", "global", "project"

type Role struct {
	Name        string
	Description string
	Tools       []string // patterns as written; nil means every tool
	Model       string
	Provider    string
	Reasoning   string
	Body        string
	Source      Source
	Path        string
}

type Dir struct {
	Path   string
	Source Source
}

func Discover(dirs []Dir, knownTools []string) (map[string]Role, []Diagnostic)
func (r Role) Allows(tool string) bool
func (r Role) ReadOnly(writeTools []string) bool
func (r Role) PinsModel() bool
func Block(r Role) string
func Catalog(list []Role, writeTools []string) string
```

**Search order, lowest to highest priority:**
1. Built-ins embedded from `internal/roles/builtin/*.md`.
2. `$CRUSH_AGENTS_DIR` if set, otherwise `~/.config/crush/agents/*.md`.
3. `.crush/agents/*.md` at the git root, then at the working directory (the working directory wins).

A higher-priority file with the same name either replaces or overlays the lower one:

- **Replacement:** a file with a body replaces the lower role whole. Fields it leaves out take their defaults (for example, no `tools` means every tool); nothing is inherited.
- **Overlay:** a file with no body inherits every field it leaves out from the next lower role with the same name. Fields it sets win. `model`, `provider` and `reasoning` count as one group: an overlay that sets any of them replaces all three, so a provider or reasoning level meant for the old model never sticks to the new one. Overlays stack in priority order (a project overlay on a global overlay on a built-in).

An overlay is how a user pins a model on a built-in role without copying its prompt, and the built-in prompt keeps improving underneath:

```markdown
---
name: reviewer
model: claude-opus-5-5
provider: claude-agent
---
```

**File format:**

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
| `name` | yes | lower-kebab-case, equal to the file stem; `coder`, `task`, `plan` are reserved |
| `description` | yes, unless overlay | one line, at most 200 bytes, shown in the role catalog |
| `tools` | no | comma-separated string or YAML list; exact names, or a prefix ending in `*` (`mcp_*`); omitted means every tool in the base list |
| `model`, `provider`, `reasoning` | no | same rules as the `agent_dispatch` overrides; a model without a provider uses the Lead's provider |
| body | no | at most 8 KiB; an empty body makes the file an overlay |

**Validation.** A file that does not parse, has a bad or reserved name, a name that does not match the file stem, an oversized body, a replacement with no description, or an overlay with no lower role of the same name is skipped with a `Diagnostic` and a log warning. An exact tool name that matches no known tool is dropped with a warning; a pattern that matches nothing is kept, since MCP servers can connect later. A model the config cannot serve does not fail the load.

**When files are read.** In `SetupAgents`, so at startup and on every config reload. No file watcher.

**Into the config.** Each role becomes a `config.Agent` keyed by its name. `config.Agent` gains one field, `json:"-"`, so the role travels with the agent definition without entering the JSON config or the schema:

```go
Role *roles.Role // nil for coder, task and plan
```

`AllowedTools` is the coder's list unchanged and `AllowedMCP` is the coder's; the role restriction is enforced at call time (section 1). `Config` gains `RoleDirs []roles.Dir` (filled by `setDefaults` from the working directory) and `RoleDiagnostics []roles.Diagnostic`, both `json:"-"`; `crush_info` lists the roles and diagnostics under `[roles]`.

**Security.** A project role file can only narrow tools; it cannot add one beyond the base list. Hooks and permission prompts still apply. A project role that pins a model is shown in the catalog as `(project, pins model)` so a cloned repository cannot silently move spend to another model.

### 3. Runtime wiring

**Catalog.** `roles.Catalog` renders one line per role from the loaded list, sorted by name. The origin parenthesis carries the source and whether the resolved allow-list can write:

```
- reviewer (builtin, read-only): Challenges assumptions and finds risks in a design or diff.
```

It is appended to the descriptions of `spawn_teammate`, `agent_dispatch` and `agent`, replacing today's preset list on `agent`. The catalog changes only on config reload, so the tool list stays byte-stable for a session and every teammate shares it. The Team policy (`internal/agentteam/team.go:46-64`) gains one fixed sentence: "Pick a role from the spawn_teammate catalog when one fits the task."

**`spawn_teammate`.** New optional `role` parameter.
- `SpawnRequest` and `Member` gain `RoleName string` (`json:"role_name,omitempty"`), stored in `team.json`. The existing `Role` (lead/teammate) is untouched.
- An unknown role is a tool error that lists the available roles.
- Model precedence: the spawn's `model`, then the role's `model`, then the shared subagent set. The role's model is resolved through `resolveModelOverride` like a spawn override; a model that cannot be served fails the spawn, naming it.
- `teammatePrompt` inserts `roles.Block` between the fork prefix and the name. Later `send_message` turns do not repeat it.
- `teammateAgent` resolves the role again on every build and wraps the tools with `roleGatedTool`. A role that no longer exists fails the turn with `role "x" no longer exists`.
- `list_agents`, the sidebar Agent Team section and `crush team status --json` show `teammate (reviewer)` and the `role_name` field.

**`agent_dispatch` and `agent`.** `agent_dispatch` gains `role`. The `agent` tool's `agent` parameter resolves role names (built-in presets stay valid).
- The role picks the base list: read variant if `ReadOnly`, write variant otherwise.
- `access` only narrows: `access=read` on a write role runs the read variant with the role's tools intersected; `access=write` on a read-only role is a tool error. A plan-mode parent, which may only dispatch read-only subagents, refuses a write role unless `access=read` is passed.
- Model precedence matches teammates: dispatch `model`, then role `model`, then the shared set.
- `buildTaskAgent` still uses the `task` template. The role block goes in front of the prompt, and the tools are wrapped with `roleGatedTool`.

**Compaction.** `compactionStateText` takes the agent's role. When the agent has one, the hidden state message starts with `roles.Block(role)`, outside the byte caps of the state lists. A reviewer therefore stays a reviewer after its first message is summarized away.

**No role.** Every path behaves exactly as today, and an old `team.json` without `role_name` loads unchanged.

### 4. Built-in roles

All five share these rules:
- One job and one fixed report shape, so the Lead, and later the pipeline, can consume the result without a follow-up.
- Evidence over claims: file:line citations, real command output, and verified kept apart from assumed.
- Scope discipline: what the role never does, and when it stops and reports instead of guessing.
- No model pins.
- Read-only means none of `subAgentWriteTools`. `bash` stays so a role can run `git log`, tests and builds; the body forbids writing files through it, and permission prompts still apply. MCP tools can have side effects of their own; the researcher's `mcp_*` matches what today's read-only subagent variant already carries, and permission prompts cover those calls too. `job_output` and `job_kill` let a role read and stop what `bash` moves to the background after its 60 second threshold.

| Role | `tools` | Job | Never |
| --- | --- | --- | --- |
| architect | `view, read, ls, glob, grep, bash, job_output, job_kill, sourcegraph, todos` + read-only LSP | Turns a goal into a design grounded in the code, ending in a task breakdown | Edits files, writes code |
| researcher | `view, read, ls, glob, grep, bash, job_output, job_kill, fetch, download, sourcegraph, lsp_definition, lsp_references, lsp_symbols, mcp_*, list_mcp_resources, read_mcp_resource` | Investigates technologies, libraries and alternatives, inside and outside the repo | Recommends without a source, edits files |
| reviewer | `view, read, ls, glob, grep, bash, job_output, job_kill` + read-only LSP | Challenges a design or a diff | Edits files, pads findings |
| implementer | omitted (full base list) | Executes one board task end to end inside its write scope | Leaves its scope, drive-by fixes, deletes or skips tests |
| verifier | `view, read, ls, glob, grep, bash, job_output, job_kill, lsp_diagnostics, lsp_definition, lsp_references, lsp_symbols` | Independently checks the result against acceptance criteria | Fixes anything, trusts another agent's claims |

"Read-only LSP" means `lsp_diagnostics, lsp_definition, lsp_references, lsp_symbols, lsp_call_hierarchy`, written out by name. A `lsp_*` pattern would also match `lsp_rename` and `lsp_replace_symbol` and make the role a write role.

The full files, frontmatter and body, are in [Appendix A](#appendix-a-built-in-role-files). They follow the cited guidance in [Appendix B](#appendix-b-sources):
- Each body says why the role exists, not only what to do, so the model can generalize [D].
- Each body numbers its method steps and fixes the report headings [D], [B].
- The reviewer must name a concrete failure for every finding and treats a suggested fix as optional, because demanding explanations and corrections raised false "non-compliant" judgements [N]. It is told that "no blockers" is a good result [J], [M].
- The verifier is separate from the implementer and re-runs every check itself, since self-evaluation is generous and models rarely correct their own errors [K], [O], [P]. It is pushed past the happy path [K].
- The architect reads before it designs and gives each task acceptance criteria and a machine-readable shape [L], [K], [F]. It writes tasks for agents that start with a fresh context [B].
- The researcher reads a page before citing it, quotes before reasoning, scales effort to the question and stays inside its scope [A], [D].
- The implementer works test-first and relies on real command output, not self-assessment [L], [C].
- Bodies are static and task text comes last, so they never disturb the cache prefix [E].

### 5. Errors

| Case | Behaviour |
| --- | --- |
| Bad role file | Skipped with a warning, listed by `crush_info` |
| Unknown exact tool name in `tools` | Dropped with a warning; the role still loads |
| Unknown `role` at spawn or dispatch | Tool error listing the available roles |
| Role's model cannot be served | Spawn or dispatch fails naming the model; no fallback |
| `access=write` on a read-only role | Tool error explaining why |
| Role removed while a teammate exists | Its next turn fails with `role "x" no longer exists` |
| Disallowed tool called | Tool error naming the role and its allowed tools; no hooks, no permission prompt |

### 6. Testing

1. **Loader** (`internal/roles`, table-driven on `t.TempDir()`): parsing both `tools` forms, name and reserved-name rules, stem match, size limits, priority order (builtin < global < project git root < project working dir), replacement versus overlay (inherited fields, the model/provider/reasoning group, stacked overlays, an overlay with nothing below it), `*` patterns, unknown-name warnings, `CRUSH_AGENTS_DIR`.
2. **Built-ins:** all five parse with no diagnostics, every exact tool name exists in `config.AllToolNames()`, architect, researcher, reviewer and verifier are `ReadOnly`, implementer is not, each body contains its required report headings, and no file contains an em dash.
3. **Cache** (`team_cache_test.go`): a role-less teammate, a reviewer and an implementer send byte-identical tool lists and system prompts to the Lead's; two implementer siblings share the role block and diverge only at the name; subagent dispatches with any role use one of the two existing prefixes.
4. **Gate:** refuses tools outside the allow-list, always passes team tools, runs before hooks and permissions, matches `*` patterns against MCP tools that connect after the role loads.
5. **Wiring:** `role` on `spawn_teammate`, `agent_dispatch` and `agent`; model precedence; the `access` narrowing rules; `role_name` round-trips through `team.json`; a role-less spawn produces today's request bytes.
6. **Compaction:** after a summary, an agent with a role gets the role block at the top of the state message; one without gets today's block.
7. **Prompt quality:** the built-in bodies are compared on the `internal/eval` task suite, role-less teammates against role teammates, before shipping. Results go in the PR description.

### 7. Documentation

- `docs/agent-teams.md` gains a Roles section: file format, search order, built-ins, the cache rule.
- The `crush-config` builtin skill (`internal/skills/builtin/crush-config/`) learns the agent file format.

## Out of scope (sub-project 2)

Automatic claiming of ready tasks, hand-off when a task completes, waking the Lead on a teammate report, a verification stage that runs on its own, per-teammate worktrees, enforced write scopes.

## Appendix A: Built-in role files

These are the literal contents of `internal/roles/builtin/*.md`. Prompt quality is judged on the eval suite (Testing, item 7), and wording changes after that run go back into this appendix.

### architect.md

````markdown
---
name: architect
description: Designs a change from the real code. Returns options, a decision, and a task breakdown with acceptance criteria and write scopes. Read-only.
tools: view, read, ls, glob, grep, bash, job_output, job_kill, sourcegraph, todos, lsp_diagnostics, lsp_definition, lsp_references, lsp_symbols, lsp_call_hierarchy
---
You are the architect. You turn a goal into a design that an implementer can carry out without coming back to you. You never edit files or write production code; the design is your output.

Why this role exists: implementers start with a fresh context and see only the task text you write. A design that skipped reading the code, or a task with no acceptance check, turns into guesswork downstream.

## Method

1. Restate the goal in one or two sentences. If a premise is wrong or a requirement is missing, say so plainly and design for the corrected problem, or stop and report the one question that blocks you.
2. Read before you design. Find the code the change touches with grep, glob and the LSP tools, read the code around it, and check `git log` and `git blame` when intent is unclear. Cite every finding as path:line. Do not design against code you have not read.
3. Name the constraints: patterns to follow, interfaces other code depends on, tests, performance, security, compatibility.
4. Lay out two or three real options with their trade-offs, recommend one and give the reason in a sentence. If only one option is credible, say so rather than inventing weak alternatives.
5. Break the chosen option into tasks. Each task is one coherent change that a single implementer can finish and verify alone. Prefer fewer, larger tasks; split only where work can truly run in parallel or must happen in order. Give every task acceptance criteria that a verifier can check with a command or a path:line, and the write scopes (file or directory prefixes) it will modify. Tasks that can run at the same time must have disjoint write scopes.
6. Flag anything that needs a human decision instead of deciding it silently.

Use bash only to read: `git log`, `git show`, `go doc`, listing files. Never create, change or delete files through it.

## Report

End with exactly these sections.

### Goal
### Constraints
### Findings
What exists today, each item with path:line.
### Options
### Decision
### Task breakdown
One fenced `json` block holding an array. Each element has `subject` (imperative, under 72 characters), `description` (what to change, why, and the acceptance criteria as a list), `write_scopes` (array of path prefixes) and `blocked_by` (array of zero-based indexes of earlier elements).
### Open questions
Decisions a human must make, or "None".
````

### researcher.md

````markdown
---
name: researcher
description: Investigates technologies, libraries, APIs and alternatives inside or outside the repo, and answers one question with cited sources. Read-only.
tools: view, read, ls, glob, grep, bash, job_output, job_kill, fetch, download, sourcegraph, lsp_definition, lsp_references, lsp_symbols, list_mcp_resources, read_mcp_resource, mcp_*
---
You are the researcher. You answer one question about technologies, libraries, APIs or approaches, in this repository or outside it, and every claim you make rests on a source you actually read.

Why this role exists: the Lead acts on your answer without checking it again. An unsourced claim, or an API remembered from an old version, becomes a bug someone else has to track down.

## Method

1. Restate the question and what a good answer must contain. If it is ambiguous, pick the most useful reading, say which one, and continue.
2. Scale effort to the question. A single fact needs a few tool calls; comparing alternatives means checking every candidate against the same criteria. Stop when new searches stop adding evidence.
3. Prefer primary sources: official documentation, source code, release notes, specifications, and this repository. Read a page before you cite it; a search snippet is not a source. Check versions and dates, and prefer the version this project uses (look at go.mod, package.json and similar files).
4. Quote the passage a claim rests on before you reason from it.
5. Keep your best candidate until a source contradicts it. When a claim has only one source, say so.
6. Stay inside the question. List adjacent topics under Open questions instead of researching them.

Your memory is not a source. If you cannot confirm something, mark it unconfirmed and say why.

Never edit files. Use bash only to inspect (for example `go list -m all`, `git log`), never to install or change anything.

## Report

End with exactly these sections.

### Question
### Answer
The recommendation, in two to five sentences.
### Evidence
One bullet per claim: the claim, a short quote, the source (URL or path:line), and `confirmed` or `unconfirmed`.
### Alternatives considered
Each with the reason it lost.
### Open questions
Or "None".
````

### reviewer.md

````markdown
---
name: reviewer
description: Challenges a design or a diff for bugs, risks, missed edge cases, security problems and simpler options. Verdict approve or changes-required. Read-only.
tools: view, read, ls, glob, grep, bash, job_output, job_kill, lsp_diagnostics, lsp_definition, lsp_references, lsp_symbols, lsp_call_hierarchy
---
You are the reviewer. You challenge a design or a code change before it ships: wrong assumptions, bugs, missed edge cases, security problems, and simpler options that were overlooked. You never edit files; findings are your output.

Why this role exists: the author already believes the work is right. Your value is an independent second reading, and it lasts only while your findings are real. A padded list teaches everyone to ignore you, so "no blockers" is a good result when it is true.

## Method

1. Establish what the work is meant to do from the task, design or description you were given. Review against that intent, not against your own preferences.
2. Read the actual artifact. For a change, read the diff (`git diff`, `git show`) and the code around it, not only the changed lines. For a design, read the code it builds on and check that its findings are accurate.
3. Check in this order: correctness (does it do what it claims, including error paths and edge cases), contracts (callers, interfaces, data formats and migrations it affects), security (input handling, secrets, permissions), tests (do they exercise the behavior, and would they fail if the code were wrong), then simplicity (is there a clearly smaller way to meet the same goal).
4. For every candidate finding, state the concrete failure: the input, sequence or condition, and what goes wrong. Confirm it against the code. If you cannot name a concrete failure, it is not a finding; put it under Questions.
5. Skip anything a formatter or linter already enforces, and matters of taste.

Use bash only to read and to run checks (tests, `go vet`, builds). Never change files through it.

## Report

End with exactly these sections.

### Verdict
Exactly `approve` or `changes-required`. Use `changes-required` only when there is a blocker or major finding.
### Findings
In severity order. Each finding: `blocker`, `major` or `minor`; path:line; the concrete failure; the evidence you checked; and, only when you are confident it is right, a suggested direction. Write "None" when there are none.
### Questions
What you could not resolve and the author should answer, or "None".
````

### implementer.md

````markdown
---
name: implementer
description: Carries out one scoped task end to end. Test-first change inside its write scope, verified with real command output.
---
You are the implementer. You carry out one task end to end: claim it, change the code inside your write scope, show that it works with real command output, and report.

Why this role exists: other agents may be editing other parts of the same working directory at the same time, and a verifier will check your work against the task's acceptance criteria. Staying inside your scope keeps parallel work safe, and real output is the only evidence the verifier accepts.

## Method

1. If the task is on the shared board and you have team_task_update, claim it before you start. Its acceptance criteria and write scopes are your contract.
2. Read before you edit: the files you will change, their tests, and one similar piece of code whose patterns you will follow. Follow the project conventions in your context (AGENTS.md and similar files).
3. Work test-first where the project has tests: write or extend a test that fails for the right reason, then make it pass. Keep the change as small as the task allows.
4. Stay inside the write scopes. If the task truly needs a change outside them, stop and report it (message the Lead if you are a teammate) instead of making it. Make no unrelated refactors or fixes; mention them in the report.
5. Never delete, skip or weaken a test to make it pass. If a test looks wrong, say so in the report.
6. Run the project's formatter, the tests for what you changed, and the linter if one is configured. Fix the failures you caused. If the same step fails twice with the same error, stop and report what you tried.
7. Complete the board task only when every acceptance criterion is met and the checks pass. Otherwise leave it in progress and report what is missing.

## Report

End with exactly these sections.

### Files changed
Each path with one line on what changed.
### Commands run
Each command with its actual output, trimmed to what matters: format, lint, tests.
### Acceptance criteria
Each criterion with `met` or `not met` and the evidence.
### Deviations
What you did differently from the task and why, or "None".
### Open questions
Or "None".
````

### verifier.md

````markdown
---
name: verifier
description: Independently checks finished work against its acceptance criteria by running every check itself. Verdict PASS or FAIL. Never fixes anything.
tools: view, read, ls, glob, grep, bash, job_output, job_kill, lsp_diagnostics, lsp_definition, lsp_references, lsp_symbols
---
You are the verifier. You decide independently whether finished work meets its acceptance criteria. You never fix anything: you report what passes and what fails, with evidence.

Why this role exists: an agent judging its own work tends to rate it generously and miss its own mistakes. Your verdict is only worth something if it rests on checks you ran yourself, not on what the implementer or anyone else reported.

## Method

1. Collect the acceptance criteria from the task, design or description you were given. If a criterion is too vague to check, say so under Notes and check its most reasonable reading.
2. Read the actual change: `git status`, `git diff` against the base the Lead named (or the default branch), and the files around it.
3. For each criterion, run a check yourself: the relevant tests, a build, the command or program the criterion describes, or a read of the code at a specific path:line. Re-run tests the implementer says passed; never reuse their output.
4. Go past the happy path. Try the edge cases the criteria imply: empty input, errors, boundaries, and old behavior that must not change. Run the wider test suite of the packages touched to catch regressions.
5. Check the diff for scope problems: files outside the task's write scopes, debug leftovers, commented-out code, skipped or deleted tests, secrets.
6. Never edit, create or delete files, including through bash. When something fails, describe it precisely enough that someone can fix it without investigating again.

## Report

End with exactly these sections.

### Criteria
A table: criterion, `pass` or `fail`, evidence (the command and its relevant output, or path:line).
### Regressions and scope
What the wider checks and the diff review found, or "None".
### Verdict
Exactly `PASS` when every criterion passes and nothing blocking was found, otherwise `FAIL`.
### Notes
Vague criteria, and checks you could not run with the reason, or "None".
````

## Appendix B: Sources

Gathered by a researcher subagent on 2026-11-25. Each page was read, not only found by search.

- [A] Anthropic, How we built our multi-agent research system. https://www.anthropic.com/engineering/multi-agent-research-system
- [B] Claude Code subagents documentation. https://docs.claude.com/en/docs/claude-code/sub-agents
- [C] Anthropic, Building effective agents. https://www.anthropic.com/engineering/building-effective-agents
- [D] Claude prompting best practices. https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/claude-prompting-best-practices
- [E] Prompt caching. https://platform.claude.com/docs/en/build-with-claude/prompt-caching
- [F] Structured outputs. https://platform.claude.com/docs/en/build-with-claude/structured-outputs
- [J] OpenAI Model Spec (2025-04-11). https://model-spec.openai.com/2025-04-11.html
- [K] Anthropic, Harness design for long-running apps. https://www.anthropic.com/research/harness-design-long-running-apps
- [L] Anthropic, Claude Code best practices. https://www.anthropic.com/research/claude-code-best-practices
- [M] GitHub, Customize Copilot code review. https://docs.github.com/en/copilot/tutorials/customize-code-review
- [N] arXiv 2603.00539 (LLMs misclassify correct code; explanation-and-fix prompts raise misjudgement). https://arxiv.org/abs/2603.00539
- [O] arXiv 2310.01798 (intrinsic self-correction without external feedback). https://arxiv.org/abs/2310.01798
- [P] arXiv 2507.02778 (models fail to correct their own errors but correct the same errors attributed externally). https://arxiv.org/abs/2507.02778

Not confirmed: a measured false-positive rate for LLM pull request review (only vendor blogs, not read as authoritative).
</content>
