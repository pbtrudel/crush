# Plugin and Event API Design

Design for item 7 of `~/crush-opencode-improvements.md` ("Plugin and SDK surface"). No code has been changed. Code references are to commit `1f3af66e` (branch `main`, 2026-11-21).

## Goal

Let three programs that today must live inside the core process or read its database run outside it, on one documented event API: the Discord bridge (`internal/discord`), the session log report (`internal/logreport`) and the eval harness (`cmd/crush-eval`, `internal/eval`).

The API is Crush's own. It is not opencode's plugin runtime: no JavaScript, no `tool.execute.before` compatibility layer, no in-process extension loading. The three consumers are the requirement, and each of them needs to *observe* and (for the bridge) to *act*, not to define tools.

Nothing here changes what a model is sent.

## Terms

- **Core**: the process that owns sessions, runs and tool calls. Today that is `app.App` inside either the TUI process (local mode) or `crush server` (`internal/cmd/server.go:28-46`).
- **Consumer**: a program outside the core that watches it. Discord bridge, logreport, eval.
- **Record**: one JSON object a consumer receives. It is the unit of the API.
- **Stream**: records delivered live, in order, over one connection.
- **Export**: records written to a file, for a consumer that reads a finished run.
- **Hook**: a synchronous shell command that *decides* something in the tool path. Existing feature, unchanged by this design.
- **Gate subscriber**: a control hook that lives outside the core and answers over the API instead of over stdin. Proposed here, not built in phase 1.

## Evidence

### 1. What exists today

| Piece | What it gives a consumer | Location |
| --- | --- | --- |
| Hook events | 13 named events, shell commands, JSON on stdin, JSON on stdout, `allow`/`deny`/`halt`/`rewrite input`/`rewrite output`/`context` | `internal/hooks/events.go:6-37`, `internal/hooks/hooks.go:67-76`, `docs/hooks/README.md:175-188` |
| Hook payload | One documented object per invocation, with its own version (`SupportedOutputVersion = 1`) | `internal/hooks/input.go:17`, `:25-43` |
| Hook call site | A wrapper around every top-level tool: `PreToolUse` before, `PostToolUse` or `PostToolUseFailure` after | `internal/agent/hooked_tool.go:54-118` |
| HTTP API | v1 REST, generated OpenAPI 3.1 at `/v1/docs/openapi.json`, `/v1/version` with build identity | `internal/server/endpoints.go:13-31`, `internal/proto/version.go:5-10` |
| Event stream | `GET /v1/workspaces/{id}/events`, SSE-like, `data:`-only frames | `internal/server/endpoints.go:154-168`, `internal/server/proto.go:203-286` |
| Client | ~60 typed methods plus `SubscribeEvents` returning `<-chan any` | `internal/client/proto.go:114-315` |
| Wire types | `proto.Message` with typed parts, including `ToolCall` and `ToolResult` | `internal/proto/message.go:149-170` |
| In-process broker | Lossy `Publish`, bounded `PublishMustDeliver`, per-subscriber queues | `internal/pubsub/broker.go:20-54` |
| Fan-out | One sink per workspace, encoded once, per-client coalescing, backlog 256 | `internal/server/eventsink.go:20-22`, `:167-205` |
| Stamps | Every step can carry `build` and `turn_end`; every tool result can carry `error_kind`; every compaction its stats | `internal/message/content.go:194-238`, `:260-270`, `internal/agent/tools/error_kind.go:30-32` |

The transport is already the right shape: a workspace-scoped stream, one connection per reader, a versioned client, an OpenAPI document, and a build identity to pin against. The design extends it; it does not replace it.

### 2. The Discord bridge

`internal/discord` is 2,311 lines in `bridge.go` alone. It is in-process only: `Start` refuses anything that is not a local workspace (`internal/discord/bridge.go:206-213`, "remote control requires local mode"), because `start` takes `*app.App` (`:219`), subscribes to `app.Events` (`:321`) and pushes UI messages back with `app.SendEvent` (`:307`, `:372`, `:421`, `:430`, `:444`, `:547`, `:1915`).

Its control surface is already an interface, `commandEnv` (`internal/discord/commands.go:24-45`): run, ask, cancel, queued prompts, summarize, session create/get, permission grant/deny/skip, question answer, config, working dir. Most of those are already v1 endpoints (`internal/server/endpoints.go:382`, `:482`, `:436`, `:446`, `:419`, `:322`, `:313`).

What it consumes, and where:

| It needs | Code |
| --- | --- |
| A prompt typed in the terminal, to echo it | `handleUserMessage`, `bridge.go:689-700` |
| Assistant text streaming, for the live transcript | `handleAssistantMessage`, `bridge.go:702-730`; `syncLiveTranscript`, `:752-803` |
| Tool calls inside an assistant snapshot: name, count, the one running, its main parameter | `applyTurnLocked`, `bridge.go:2116-2153`; `deriveTurnState` |
| A failed tool result, counted once per result | `handleToolResult`, `bridge.go:815-838` |
| The end of a run: message id, text, error, cancelled | `handleRunComplete`, `bridge.go:840-884` |
| A re-authentication notice | `handleAgentNotification`, `bridge.go:886-900` |
| Session title and usage totals: prompt, completion, cache, context, cost | `handleSessionEvent`, `bridge.go:901-924`; `renderMetricsLine` |
| Model and its context window | `modelContextWindow`, `bridge.go:2092-2115` |
| Permission requests and their resolution, plus the pending set at attach time | `bridge.go:926-985`, `:1043-1062` |
| Question batches, answers, cancellations | `bridge.go:986-1042` |
| A compaction, with before/after tokens and messages kept | `handleCompactionMessage`, `bridge.go:655-687` |
| Session rebind after `/clear` | `rebindSession`, `bridge.go:1846-1920` |
| Its mode (plan/yolo) and model changes reflected in the TUI | `:396-450` |

Two things it gets for free today and would lose outside the core: `notify.RunComplete` is published must-deliver (`internal/app/app.go:333-336`), and the pending permission set is readable synchronously from the same process.

### 3. `logreport`

`crush stats report` opens `crush.db` read-only and walks it (`internal/cmd/stats_report.go:113-141`). Package docs state the report never prints message text, a title, a path or a session id (`internal/logreport/report.go:1-3`); the JSON output has its own version, and within one version fields are only added (`:12-14`).

One query feeds the whole report (`internal/logreport/report.go:100-107`): `session_id, parent_session_id, role, parts, model, provider, created_at, is_summary_message`, ordered by session and time. Everything else it knows is inside `parts` and inside tool-result metadata read with `gjson`: `error_kind`, `exit_code`, `interrupted`, `edits_failed` (`internal/logreport/report.go:218-222`, `edits.go:130-138`, `groups.go:91-112`, `shell.go:38-52`, `reads.go:52-67`).

So logreport's real dependency is not the database, it is this record and the stamps inside it. It is also a bulk reader: 22,295 tool calls over a 27-day window in the measurement work, no streaming requirement at all.

Note the reverse edge: three test files in `internal/agent`, `internal/agent/prompt` and `internal/automation` import `logreport` to assert injection tags. The only non-test importer is the command itself.

### 4. The eval harness

The harness runs the binary as a child process (`internal/eval/run/run.go:297-304`), reads the one JSON object it prints (`:374-408`), and then scans the child's database directly: `sql.Open("sqlite", …)` and `SELECT parts FROM messages` (`internal/eval/run/contamination.go:145-172`). It deletes `CRUSH_CLIENT_SERVER` from the child environment on purpose, so the child always runs standalone (`internal/eval/route/env.go:26-47`).

Two dependencies, both on internals: the `crush run --json` schema (`internal/cmd/run.go:198`, refused in client/server mode at `:206-215`), and the raw `messages.parts` JSON.

What the harness needs is small and entirely in the past tense of a run: every tool call's name and input text, so contamination markers can be matched (`internal/eval/run/contamination.go:57-100`); the run's terminal reason, steps, tool calls, turn ends, tokens, cost and gate counters (`internal/eval/run/run.go:374-408`).

### 5. Where each consumer stops

| Consumer | Blocked by |
| --- | --- |
| Discord bridge | Needs `*app.App`, `tea.Msg` and `SendEvent`; no out-of-process path |
| logreport | Needs the file path of `crush.db` and the `messages` table |
| eval | Needs the same table, plus a local-mode binary |
| All three | Each re-derives semantics the core already knows: the bridge has `deriveTurnState`, logreport has injection and error-kind classifiers, eval has its own output parser |

Three separate derivations of the same facts is the actual cost of not having an event API.

## Design

### 1. One record, one schema

Every record is one JSON object with a fixed header and a payload:

```go
type Event struct {
	Schema    int             `json:"schema"`        // 1
	Seq       uint64          `json:"seq"`           // per workspace, monotonic, gapless
	Workspace string          `json:"workspace_id"`
	Time      time.Time       `json:"time"`
	Type      string          `json:"type"`          // "tool.after"
	Session   string          `json:"session_id,omitempty"`
	Run       string          `json:"run_id,omitempty"`
	Step      string          `json:"step_message_id,omitempty"` // assistant message of the step
	Build     string          `json:"build"`         // version.BuildID, as /v1/version
	Payload   json.RawMessage `json:"payload"`
}
```

`Seq` is the cursor. It is assigned by the workspace's event sink (`internal/server/eventsink.go`), not by the broker, so a consumer can say "everything after 41,203" and get it.

A consumer switches on `Type`. Unknown types are skipped, not errors: that is what makes additions safe.

### 2. The records

Sixteen record types, grouped by what they describe. Fields listed are the payload only.

**Session and run**

| Type | Fields | Emitted when |
| --- | --- | --- |
| `session.updated` | `title`, `parent_session_id`, `message_count`, `prompt_tokens`, `completion_tokens`, `cache_read_tokens`, `context_tokens`, `estimated`, `cost`, `todos` | a session row changes (`internal/session/session.go:65-82`) |
| `session.ended` | `reason` | the core stops using the session |
| `run.started` | `prompt` (visible user text), `model`, `provider`, `source` | the agent begins a prompt |
| `run.ended` | `message_id`, `text`, `turn_end`, `error`, `cancelled`, `duration_ms`, `gate` | the run stops; `turn_end` is the existing stamp (`internal/message/content.go:194-238`) |
| `step.usage` | `prompt`, `completion`, `cache_read`, `context`, `cost`, `model`, `provider`, `build`, `decode_ms` | one model request finishes; mirrors the finish part |

**Conversation**

| Type | Fields | Emitted when |
| --- | --- | --- |
| `message.created` | `message_id`, `role`, `hidden`, `summary`, `parts` | a message row is written |
| `message.finished` | `message_id`, `parts` | the message stops changing |
| `message.delta` | `message_id`, `part_index`, `text` | streamed assistant text, lossy, coalescable |

**Tools**

| Type | Fields | Emitted when |
| --- | --- | --- |
| `tool.before` | `tool_call_id`, `name`, `input` (raw JSON), `step_message_id`, `session_id` | `internal/agent/hooked_tool.go:54-61`, before the tool body |
| `tool.after` | `tool_call_id`, `name`, `is_error`, `error_kind`, `duration_ms`, `output` (see §6), `metadata` (raw JSON) | after the tool body, `:86-118` |

**Waiting and maintenance**

| Type | Fields | Emitted when |
| --- | --- | --- |
| `permission.requested` / `permission.resolved` | the existing proto shapes (`internal/proto/permission.go`) | a prompt is shown or answered |
| `question.requested` / `question.resolved` | the existing proto shapes | a question batch is shown or answered |
| `compaction.ended` | `tokens_before`, `summary_tokens`, `tail_tokens`, `saved_tokens`, `kept_messages`, `trigger` | `internal/message/content.go:260-270` |
| `notice` | `kind` (`reauthenticate`), `message` | `internal/agent/notify` today |

Two rules keep the set honest. A record is emitted only where the core already computes the value; no record requires new bookkeeping beyond what the measurement design's stamps already write. And a record never carries something the core has not resolved: `run.ended` carries the `turn_end` stamp rather than a guess, `tool.after` carries `error_kind` rather than message wording, `compaction.ended` carries the measured stats rather than before/after sizes recomputed.

### 3. What each consumer subscribes to

| Consumer | Records | History it also needs |
| --- | --- | --- |
| Discord bridge | `session.*`, `run.*`, `message.created`, `message.finished`, `message.delta`, `tool.before`, `tool.after`, `permission.*`, `question.*`, `compaction.ended`, `notice` | pending permissions and questions at attach, session list, message history |
| logreport | none live | `message.created` records for a window, i.e. the export |
| eval | `tool.before` (inputs), `run.ended` (terminal reason, gate), `step.usage`, `compaction.ended` | only its own run, written to a file |

That split is the point of the design: the bridge is a live reader of a small set; logreport is a bulk reader of persisted records; the eval is a per-run file reader. One shape serves all three, and none of them needs a second schema.

### 4. Versioning and stability

- **`schema` is one integer, version 1.** Within 1, fields are only added and record types are only added. A field is never removed, renamed or retyped. This is the rule `logreport` already states for its own output (`internal/logreport/report.go:12-14`) and the rule `crush run --json` already follows (`internal/eval/run/run.go:403-418`).
- **What is stable in 1**: the header, the sixteen types, the meaning of `Seq`, and the JSON names inside each payload. Everything else (the HTTP path, the socket, the file layout of an export) is also frozen in 1 but may grow alternatives in a later major.
- **What is not stable**: text inside payloads (model output, tool output, error strings). A consumer must not match on wording. This is the same ruling the measurement design made for error classes, and it is why `error_kind` exists.
- **How a consumer pins**: `/v1/version` already returns `Version`, `Commit`, `BuildID` (`internal/proto/version.go:5-10`). A consumer records the `build` from the header of the first record it sees, so a report or a result line always names the build that wrote it, even when the build string is `+dirty`.
- **Deprecation**: a field or type is marked in the OpenAPI document and in `docs/events/README.md` for one minor release before it stops being emitted. Consumers must tolerate absence; they may not assume presence.
- **Refuse, don't guess**: a consumer that receives `schema: 2` stops with an explicit error. A core that receives a request for `schema: 2` answers 400 rather than downgrading.

### 5. Transport

The v1 HTTP API is the transport, and SSE at `/v1/workspaces/{id}/events` is the stream. Three additions:

1. **Frames gain `id:` and `event:`.** Today the server writes `data:` only (`internal/server/proto.go:270-278`) and the client parses `data:` only (`internal/client/proto.go:145-170`). With `id: <seq>` the standard `Last-Event-ID` resume works, and a plain SSE client in any language can read the stream.
2. **A replay window.** The sink keeps the last N records per workspace (N bounded by bytes, not count, because a tool result can be large), and serves `?since=<seq>` or `Last-Event-ID`. A consumer that reconnects inside the window resumes; outside it, it gets `event: gap` with the oldest seq it can serve, then the tail. That is honest about the loss instead of silent.
3. **Delivery classes stay two.** High-frequency records (`message.delta`) keep today's lossy, coalescing behavior (`internal/server/eventsink.go:167-205`). Terminal records (`run.ended`, `tool.after`, `permission.resolved`, `question.resolved`, `compaction.ended`) are marked non-droppable and are never coalesced away: they go into the replay window before fan-out, so a slow reader falls behind on `Seq` rather than losing a terminal fact.

**Export.** `crush log export --since … --until … --format ndjson` writes the same records, in `Seq` order, to a file or stdout. It reads the database in the process that already knows the schema (`internal/db`) and writes records; a consumer never opens `crush.db`. `crush run --events <path>` does the same for the records of one run, from the core that ran it (`internal/app` `RunNonInteractive`).

**Who a consumer is.** Today an SSE subscriber must present a `client_id` and is attached as a client with a stream claim (`internal/server/proto.go:98-111`, `internal/backend/backend.go:584-605`). An observer is not a client: it holds no current session, must not keep a workspace alive, and must not count as a stream for presence. The stream therefore accepts `client_id` (interactive, today's behavior) or nothing (observer, no presence, still inside the same auth boundary as the socket).

**File sink for `crush run`.** The eval's child runs standalone by design (`internal/eval/route/env.go:26-47`). The observer path there is not HTTP at all: it is `--events <path>`, one NDJSON file, written by the child. Same records, no server, no socket.

### 6. Tool before and after

The tool wrapper already has exactly the two points a plugin system wants (`internal/agent/hooked_tool.go:54-118`). The design puts records at those two points and keeps decisions where they are.

**Observation** (new, async, never blocks): `tool.before` is emitted after the input is final, which today means after a `PreToolUse` hook may have rewritten it (`:76-79`). `tool.after` carries `is_error`, the `error_kind` the tool set (`internal/agent/tools/error_kind.go`), `duration_ms`, and the same metadata the result carries. `output` is the text the model saw, capped and truncated with an explicit `truncated` flag, and omitted entirely unless the stream was opened with `?include=output`. logreport's whole privacy contract depends on that default staying closed.

**Control** (existing, sync, in the path): shell hooks stay as they are, with all their semantics (`docs/hooks/README.md:214-244`). They are the only thing that can deny a call, rewrite its input, replace its output or halt a turn, and they will remain so through phase 4.

**The gap this design does not paper over**: hooks fire only for the top-level agent (`internal/agent/hooked_tool.go:29-34`, `docs/hooks/README.md:194`). Records do not inherit that limit. `tool.before` and `tool.after` are emitted for every session, child sessions included, because logreport counts those calls and the eval's contamination scan reads them. Phase 1 fixes the observation scope; widening the *control* scope is a separate change with its own cost (a hook per subagent call is a process spawn per subagent call).

**Gate subscriber** (phase 5, not in phase 1): the same stream, with a second connection opened as `role=gate`, on which a consumer may answer `tool.before` with `allow`, `deny`, `updated_input` or `updated_output` inside a deadline. It is the hook protocol with the transport swapped, not a new policy language. It is deliberately last: it needs the observation path proven and the timeout behavior understood, and the three consumers do not need it.

### 7. What a consumer must do without

| Gone | Replacement |
| --- | --- |
| `*app.App`, `tea.Msg`, `SendEvent` | the stream, plus a narrow `Notify` port the bridge keeps locally for TUI-side effects (mode, yolo, model, status note) |
| `env.Config()` in-process | `GET /v1/workspaces/{id}/config` and `/providers`, already present |
| the pending permission set read synchronously | a `permission.requested` record replayed on attach, from the state the sink holds for open prompts |
| the `messages` table | `message.created` records, streamed or exported |

## Failure behavior

- **Core dies.** Consumers see the stream end. `Seq` tells them what they have; on restart the replay window resumes them or reports a gap. The Discord bridge posts the same farewell it posts today (`bridge.go:346-376`).
- **Consumer too slow.** `message.delta` coalesces; terminal records accumulate against the replay window and then drop the *connection*, with an explicit gap on reconnect. A consumer that would rather be disconnected than lossy says so with `?delivery=strict`.
- **Consumer crashes mid-run.** Nothing on the core side changes state. No record is a request; there is no half-applied operation.
- **Record too large.** A payload over a fixed cap (tool output, message parts with images) is replaced by `{"truncated": true, "bytes": N}` and the full value is reachable through the existing history endpoints. A record is never dropped for size.
- **Gate timeout** (phase 5): the call proceeds as if the gate said nothing, and a `gate.timeout` record is emitted so the turn-end report shows it, mirroring hook timeouts today (`docs/hooks/README.md:122-127`).

## Validation

Before phase 1 ships, these tests exist:

- A golden-record test: a scripted session (text, one tool call, one failure, one permission, one compaction, one child session) produces a byte-stable set of records apart from ids and timestamps, recorded from the sink, not from the broker.
- A resume test: kill a reader mid-stream, reconnect with `Last-Event-ID`, and prove no terminal record is missing and `Seq` is contiguous or explicitly gapped.
- A strict-delivery test: a reader that stops reading gets disconnected before a terminal record is dropped.
- A schema test: a record of every type round-trips through the OpenAPI document, and the document lists `schema: 1`.
- A consumer-parity test: `crush stats report` over an export equals the same report over the database, field by field (this is the acceptance test for phase 2).
- An eval-parity test: the contamination findings from a run's NDJSON equal the findings from scanning that run's database (phase 3).

## Scope

**In scope**

- The record schema, its version rule, and the OpenAPI documentation of both.
- `id:`/`event:` frames, `Last-Event-ID`/`?since`, the replay window, two delivery classes.
- An observer role that holds no presence.
- `crush log export`, `crush run --events`.
- Moving logreport, the eval harness and the Discord bridge onto it.

**Out of scope, explicitly**

- A JavaScript or TypeScript plugin runtime, and compatibility with opencode's `tool.execute.before`/`after` plugin hooks.
- Plugins that *define* tools, models or commands. Every consumer here observes; none adds a tool.
- In-process Go plugins (`plugin.Open`), a `.so` ABI, or any interface that requires a consumer to be built against the core.
- Custom TUI components, themes or keybindings from a consumer. `host_theme` and `host_context` remain their own narrowly-scoped endpoints.
- A durable, cross-machine log. The stream is per workspace, per core process; the exports are files.
- MCP as the plugin transport. MCP stays the tool-provider protocol.
- Widening control hooks to child sessions.
- Auth changes. A remote observer lives inside whatever boundary the socket or HTTP endpoint already has; this design adds no new authentication.

## Alternatives considered

**Copy opencode's plugin runtime.** Rejected: it would mean embedding a JS engine and a second tool-execution path, for three consumers that need none of it. The item brief says the same.

**Call the hooks system the plugin API and stop.** Rejected: hooks fire on the top-level agent only, are synchronous process spawns, cannot stream, cannot read history and cannot observe subagents. logreport cannot be written as a hook, and neither can the eval's contamination scan.

**Make the stream the only transport and require a server.** Rejected: the eval deliberately denies `CRUSH_CLIENT_SERVER` to its children, and logreport must work over a log written weeks ago by a process that has exited.

**Let logreport and eval keep reading the database, and freeze `messages.parts` as a public schema.** Rejected: `parts` is a Go struct serialized ad hoc (`internal/message/content.go`), it grows fields per feature, and freezing it makes every later message change a compatibility decision. A record schema is cheaper to keep additive than an internal one is to freeze.

**One record type per notification, reusing the pubsub payload types as they are.** Rejected: the broker types are `pubsub.Event[T]`, layering `created`/`updated`/`deleted` over full snapshots (`internal/pubsub/events.go:54-78`). Consumers then re-derive what changed, which is exactly the cost this design removes. The record set is semantic and smaller.

## Delivery

| Phase | Content | Consumer unlocked |
| --- | --- | --- |
| 0 | Header, `Seq`, the sixteen types, `id:`/`event:` frames, replay window, observer role, `docs/events/README.md`, OpenAPI entries | none, this is the foundation |
| 1 | Emit `tool.before`/`tool.after` for every session, with `error_kind` and durations | logreport's tool sections become record-driven |
| 2 | `crush log export`; logreport reads records instead of `crush.db`; the five `injection_tag` tests move their classifier into the core | logreport |
| 3 | `crush run --events`; the eval scans NDJSON instead of `select parts from messages` | eval |
| 4 | The `commandEnv` client implementation (the API already has the endpoints), `Notify` port replacing `*app.App` in the bridge | Discord bridge outside the core |
| 5 | Gate subscriber: `role=gate` connections that answer `tool.before` | none of the three; keep until a fourth consumer needs it |

Each phase is one PR. Phases 2, 3 and 4 each need phases 0 and 1, and are independent of one another.

## Open questions

1. **Replay window size.** Tool results can be large. Bytes per workspace, or bytes per core? A default of 8 MiB per workspace is a guess until the record sizes are measured on a real session.
2. **Does `message.delta` belong in the API at all?** The bridge uses streamed text for its live transcript, and it can reconstruct that from `message.created`/`message.finished` snapshots at the cost of latency. Dropping it would remove the one lossy, high-frequency record from the set.
3. **Export retention and growth.** `message.created` records carry full `parts`, so an export of a month is roughly as large as the database slice. Is a `--redact` mode (parts replaced by their kinds and sizes) enough for logreport, which never prints text anyway?
4. **Whether the observer should be authenticated.** Today the socket or the HTTP endpoint is the boundary, and `client_id` is not a credential. A bridge running on another host changes that, and nothing here addresses it.
5. **The scope of `run_id`.** It exists today on `proto.AgentEvent` and on `RunComplete` (`internal/proto/proto.go:123-130`) but is caller-supplied and empty for most runs. The stream needs a core-assigned run identity, which is a small core change this design assumes.
6. **Whether the sink or the emitter assigns `Seq`.** The sink is where fan-out lives (`internal/server/eventsink.go`), but the file sink for `crush run` has no sink. Whatever answers this must answer it for both, or a record's cursor means two different things.
