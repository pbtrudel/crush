# Web mode: holographic browser UI for a Crush session

Date: 2026-11-21
Status: design approved in conversation, awaiting written-spec review

## 1. Intent

A user runs several `crush` instances in different terminals. With web mode on,
each instance opens its own browser tab that mirrors that instance's session
with full TUI parity, rendered as a futuristic, 3D, animated holographic
interface. The browser makes the agents' work visible: who is running, what
each agent is doing, which files changed and how.

### What the user said

- Web mode, when enabled, opens a browser with a UI for Crush.
- Same functionality as the TUI, in a browser tab.
- Futuristic, 3D, animations, holograms; theme matches
  `assets/2026-11-21-web-mode-reference.jpg`.
- See agents working; click an agent to see what it is doing.
- A side section listing edited files and showing their contents as a git diff
  in an editor.
- Several terminals, each with web mode, give one tab per session.
- Free hand to add display ideas.

### Decisions taken in brainstorming

| Question | Decision |
|---|---|
| Terminal while web mode is on | TUI and browser are both live and synchronized |
| Scope | Full TUI parity, delivered in milestones M1 to M4 |
| Architecture | Per-instance gateway inside each crush process (approach A) |
| Frontend stack | Vite + TypeScript + Three.js, built `dist/` committed and embedded |

### Success criteria

1. `crush --web` opens a tab showing the live session; the TUI keeps working.
2. Two terminals with `--web` produce two independent tabs, in both in-process
   mode and `CRUSH_CLIENT_SERVER=1` mode.
3. Any action available in the TUI is available in the browser (parity map,
   section 5), and actions taken in either client are reflected in the other
   within one event round-trip.
4. Agents (main, subagents, teammates) are visible as live nodes; clicking one
   shows its transcript, current tool, model, tokens, task, and status.
5. Edited files are listed with diff stats; selecting one shows a syntax
   highlighted diff in an editor view.
6. `go build .` succeeds without Node installed.
7. The gateway is unreachable from other hosts and from other browser origins.

### Out of scope

- Remote access from another machine (gateway is loopback only).
- Editing files from the browser (diff view is read-only).
- A multi-session dashboard aggregating several crush processes in one tab.
- Replacing the TUI or changing the default launch path.

## 2. Architecture

```
terminal crush process
 ├─ TUI (Bubble Tea)  ── Subscribe(*tea.Program) ──┐
 │                                                  ├─ workspace.Workspace
 └─ internal/web gateway ── SubscribeEvents(ctx) ──┘   (AppWorkspace | ClientWorkspace)
        │  127.0.0.1:<random>, token cookie
        ├─ GET  /            embedded web/dist (index.html + hashed assets)
        ├─ /api/...          REST over workspace.Workspace
        └─ /api/stream       WebSocket event stream
browser tab (Vite/TS/Three.js app)
```

The gateway lives in the same process as the TUI and wraps the same
`workspace.Workspace` value. It does not touch `internal/server`; in
client/server mode it goes through `ClientWorkspace`, which already proxies to
the shared server.

### 2.1 Package layout

| Path | Responsibility |
|---|---|
| `internal/web/gateway.go` | Listener, lifecycle (`Start`, `URL`, `Shutdown`), route mux |
| `internal/web/auth.go` | Token generation, token-to-cookie exchange, Origin/Host checks |
| `internal/web/api_*.go` | REST handlers grouped by domain (session, agent, permission, question, files, models, mcp, lsp, commands, config, auth, prompts) |
| `internal/web/stream.go` | WebSocket hub: sequence numbers, ring buffer, resync, snapshot |
| `internal/web/team.go` | Watches `agent-teams/team.json` and `agents.json`, emits deduplicated `team_state` and `agent_runs` events |
| `internal/web/files.go` | Edited-file aggregation and unified diff generation via `internal/diff` |
| `internal/web/assets.go` | `//go:embed all:dist` handler with cache headers and CSP |
| `internal/web/dist/` | Committed Vite build output, copied from `web/dist` by `task web:build` |
| `web/` | Frontend source: `package.json`, `vite.config.ts`, `src/` |

The embed must live inside a Go package directory, so the build copies
`web/dist` into `internal/web/dist`.

### 2.2 Enabling web mode

- CLI flag `--web` on the root command (interactive mode only; rejected with
  a clear error for `crush run`).
- Config option `options.web = true` (crushrc `option web true`, JSON
  `"options": {"web": true}`).
- TUI command `/web` (also in the commands dialog): starts the gateway if not
  running, then shows the launch link, copies it and opens the tab in the
  background.
- As soon as the gateway is up, the launch link is shown in a TUI
  notification, then copied to the clipboard, and `internal/browser.OpenURL`
  runs off the render path so a slow clipboard or opener cannot stall the TUI
  or hide the link. On failure, the URL is shown in a TUI notification (token
  included only in the notification, never in logs). A failed copy also
  reports the URL.
- TUI status bar shows a `web` indicator while the gateway runs.

### 2.3 Security

- Listener binds `127.0.0.1:0` only. No configuration to change the address.
- Per-run token: 32 random bytes from `crypto/rand`, base64url encoded.
- Launch URL is `http://127.0.0.1:<port>/#t=<token>`. The fragment never
  reaches server logs. The page POSTs the token to `/api/auth`, which sets
  cookie `crush_web_<port>=<token>; HttpOnly; SameSite=Strict; Path=/`, then
  strips the fragment with `history.replaceState`. The name carries the port
  because cookies are not isolated by port: a fixed name would let a second
  gateway overwrite the first tab's cookie and log that tab out. The
  frontend never reads the cookie, so the name is free to vary.
- Every `/api/*` request and the WebSocket upgrade require:
  - a valid cookie (constant-time compare),
  - `Host` equal to `127.0.0.1:<port>` or `localhost:<port>` (DNS rebinding),
  - `Origin`, when present, equal to the same origin (CSRF, cross-site WS).
- Static assets are served without auth so the 401 page can render; they
  contain no session data.
- Headers: `Content-Security-Policy: default-src 'self'; img-src 'self' data:
  blob:; style-src 'self'; connect-src 'self'; object-src 'none';
  frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`.
- Secrets (API keys, pinentry passphrases, SSH answers) are accepted only in
  request bodies, never echoed in events, never logged; the frontend clears the
  input immediately after submit.

#### Threat model

Web mode assumes a single-user host. The token is visible in the browser
opener's process arguments to other local users, and the cookie is sent to
other `127.0.0.1` ports (the `Origin` and `Host` checks still protect
state-changing calls). The launch link is also copied to the clipboard, so
clipboard history tools (for example Windows clipboard history under WSL) may
keep it until the session ends; the token dies with the gateway. A strict
`0600` redirect file and header-based auth are deferred to M4.

### 2.4 Event stream

New method on `workspace.Workspace`:

```go
// SubscribeEvents returns a channel of workspace events for a non-TUI
// consumer. The channel closes when ctx is done or the workspace shuts down.
SubscribeEvents(ctx context.Context) <-chan any
```

- `AppWorkspace`: subscribes to the same `app.events` broker the TUI uses
  (brokers already support multiple subscribers).
- `ClientWorkspace`: fans out from its existing SSE subscription so the
  process keeps a single server connection.
- The gateway converts each payload to the JSON envelope already used by
  `internal/server` (`{"type": ..., "payload": {...}}`), reusing that
  encoder rather than writing a second one. If the encoder is unexported, it
  moves to a shared package (`internal/proto` or a small `internal/wire`).

WebSocket protocol (library: `github.com/coder/websocket`, already a direct
dependency in `go.mod`, pure Go):

- Server frame: `{"seq": n, "type": "...", "payload": {...}}`, `seq`
  monotonically increasing per gateway.
- Ring buffer of the last 2048 frames.
- Client connects with `?since=<seq>`. If `since` is in the buffer, the server
  replays from there; otherwise it sends a `snapshot` frame (current session,
  messages, agents, team state, files summary, pending permission and question
  requests, LSP/MCP states, config summary) followed by live frames.
- Client sends `{"type":"ping"}` every 15 s; server closes idle connections
  after 45 s.
- Slow client: if the per-connection send queue exceeds 512 frames, the
  connection is closed with code 4001 and the client resyncs.
- Gateway-originated event types:
  - `team_state`: `agentteam.State` (members, tasks), emitted on change.
  - `agent_runs`: `[]agentrun.Run`, emitted on change.
  - `session_follow`: the terminal's current session changed.
  - `gateway_closing`: sent on shutdown so the page shows "Session ended".

### 2.5 Session follow

The gateway tracks the TUI's current session (the same value the TUI reports
through `SetCurrentSession` in client mode; for in-process mode a small hook in
the UI model notifies the gateway). The tab follows it by default. The user can
unpin to browse another session in the same workspace; re-pin returns to the
terminal's session.

Permission and question requests: in-process mode delivers every request to
both clients. In client/server mode the shared server routes requests by the
reported current session; the gateway shares the TUI's client ID, so it
receives exactly what the TUI receives.

### 2.6 REST API

All routes under `/api`, JSON bodies, `proto` types where they exist. Each
handler is a thin call into `workspace.Workspace`; no business logic in the
gateway or browser.

The set of operations below is normative; exact path spelling is fixed in the
implementation plan.

| Group | Routes |
|---|---|
| auth | `POST /auth` |
| snapshot | `GET /snapshot` |
| sessions | `GET/POST /sessions`, `GET/PUT/DELETE /sessions/{sid}`, `GET /sessions/{sid}/messages`, `POST /follow` |
| agent | `POST /sessions/{sid}/prompt` (multipart for attachments), `POST /sessions/{sid}/cancel`, `GET/DELETE /sessions/{sid}/queue`, `POST /sessions/{sid}/summarize`, `POST /sessions/{sid}/ask`, `POST /sessions/{sid}/shell`, `POST /sessions/{sid}/notes` |
| permissions | `POST /permissions/grant`, `POST /permissions/mode` |
| questions | `POST /questions/answer`, `POST /questions/cancel` |
| secure prompts | `POST /pinentry/{answer,cancel}`, `POST /ssh/{answer,cancel}` |
| files | `GET /sessions/{sid}/files`, `GET /sessions/{sid}/files/diff?path=` |
| completion | `GET /complete/files?q=` (for `@` mentions) |
| team | `POST /team/members/{id}/message`, `POST /agents/{run}/cancel` (only where `Workspace` already exposes the operation; otherwise added to `Workspace` first) |
| models | `GET /providers`, `POST /models/select`, `POST /models/reasoning` |
| commands | `GET /commands`, `POST /commands/run`, `GET /mcp/prompts`, `POST /mcp/prompts/get` |
| skills | `GET /skills`, `POST /skills/reload` |
| mcp | `GET /mcp`, `POST /mcp/{name}/{start,refresh,disable,enable,auth}` |
| lsp | `GET /lsp`, `GET /lsp/{name}/diagnostics`, `POST /lsp/{start,stop}` |
| config | `GET /config`, `POST /config/{set,remove,reload}` |
| provider auth | `POST /auth/provider/key`, `POST /auth/provider/oauth/start`, `GET /auth/provider/oauth/status` |
| system | `GET /health`, `GET /git/branch`, `POST /detach` |

Errors use `{"error": "...", "code": "..."}` with proper HTTP status codes.
Any operation the TUI performs by reaching into internals instead of through
`Workspace` gets a `Workspace` method first, so both clients share one path.

### 2.7 Edited files and diffs

`GET /sessions/{sid}/files` returns, for every path with history in the
session (including child subagent sessions):

```json
{"path": "...", "versions": 3, "additions": 12, "removals": 4,
 "diagnostics": {"error": 1, "warning": 0}, "updated_at": 0}
```

`GET /sessions/{sid}/files/diff?path=...&from=<version>&to=<version>` returns
`{"old": "...", "new": "...", "unified": "...", "language": "go",
"truncated": false}`. Defaults: first to latest version. Payloads over 1 MB
return `truncated: true` and only the unified diff. Paths are validated
against the session's history set, never read from arbitrary disk locations.

## 3. Visual language

Reference: `assets/2026-11-21-web-mode-reference.jpg`. A user faces a
holographic projector; curved panels wrap around them; hexagonal node networks,
glowing rings, cyan light with magenta and violet accents on a deep navy void.

Concept name: **Holodeck**. Dominant work patterns: operate (prompt, approve,
steer) and monitor (agents, files).

### 3.1 Tokens

| Role | Value | Use |
|---|---|---|
| void | `oklch(0.14 0.04 265)` | background, with faint scan grid and depth fog |
| surface | `oklch(0.19 0.05 260 / 0.72)` | glass panels, inner shadow + edge highlight |
| holo cyan | `oklch(0.82 0.14 210)` | primary signal: active, focus, links |
| magenta | `oklch(0.68 0.24 330)` | attention only: permission requests, errors, hot path |
| violet | `oklch(0.60 0.20 290)` | secondary structure: subagents, reasoning |
| teal | `oklch(0.78 0.13 175)` | success, additions |
| amber | `oklch(0.80 0.15 75)` | warning, reconnecting |
| text | `oklch(0.93 0.02 230)` / muted `oklch(0.70 0.04 240)` | body / secondary |

Every state carried by color also carries an icon, label, or shape. Contrast:
body text at least 4.5:1 on surface; HUD numerals at least 3:1.

Type (self-hosted, bundled): Chakra Petch (HUD labels, numerals), IBM Plex Sans
(prose), JetBrains Mono (code). Spacing on a 4 px base (4, 16, 36 rhythm).
Focus ring: 2 px dashed holo cyan, 2 px offset, on every interactive element.

### 3.2 Layout

```
┌──────────────────────── HUD strip ─────────────────────────┐
│ title · model · tokens/cost · branch · LSP/MCP hex · mode · link │
├──────────────┬──────────────────────────────┬──────────────┤
│ Transmission │      Agent Constellation      │ File         │
│ log (curved, │   (Three.js, core on ring     │ hologram     │
│ rotateY +)   │    projector, orbiting hexes) │ drawer       │
│              │   todo orbit · context ring   │ (toggle)     │
│              │   inspector (on node click)   │              │
├──────────────┴──────────────────────────────┴──────────────┤
│ Chronoline (tool-call ticks)                                │
│ Command deck: prompt · attachments · queue chips · send/cancel │
└────────────────────────────────────────────────────────────┘
```

- Side panels use CSS 3D perspective (`perspective-origin: 50% 0%`) with a
  slight inward `rotateY`, reproducing the wraparound panels of the reference.
- Below 1024 px the side panels become tabs; the constellation stays central.
  Below 768 px everything stacks, constellation collapses to a compact strip.

### 3.3 Components and display features

- **Agent Constellation**: main agent is a glowing core over a ring projector.
  Subagents (violet) and teammates (cyan) are hexagonal nodes in orbit,
  connected by light filaments. A filament pulses when messages flow; a tool
  call fires a particle burst tinted by tool category (read, write, exec,
  network, agent). Statuses: running (bright, slow spin), inactive (dim),
  provisioning (assembling wireframe), failed (magenta flicker plus icon),
  done (collapses into the core). A ring around the core shows context window
  fill; a small cost and token readout sits under it.
- **Inspector**: opens on node click or keyboard selection. Live transcript of
  that agent's session, current tool with elapsed time, model and provider,
  tokens and cost, assigned task, status, and actions (cancel, message for
  teammates).
- **Transmission log**: markdown rendering, collapsible tool cards with input
  and output, streaming text, reasoning as a dimmed violet "thought layer"
  (collapsed by default, expandable), plan handoff cards, compaction markers,
  subagent report cards. Virtualized for long sessions.
- **File hologram drawer**: tree of edited files with +/- bars and diagnostic
  markers; selecting a file opens a CodeMirror 6 merge view (side-by-side or
  unified, read-only, syntax highlighted), a version stepper, and links to the
  tool calls that produced each version. CodeMirror is loaded lazily.
- **Chronoline**: horizontal timeline of tool calls and edits; hover previews,
  click scrolls the log and highlights the agent node.
- **Interrupt hologram**: permission requests emerge from the core, pulse
  magenta scaled to danger level, show tool, path, params, and description.
  Keys: `A` allow, `S` allow for session, `D` deny. Focus moves into it;
  background is `inert`.
- **Question form**: tabbed holographic form supporting yes/no, single, multi,
  free text, and the confirmation step.
- **Task board**: three lanes (pending, in progress, completed) for team
  tasks; owner chips link to constellation nodes.
- **Todo orbit**: session todos as a compact ring checklist beside the core.
- **Command palette** (`Ctrl+K` / `Cmd+K`): commands, slash commands, MCP
  prompts, skills, sessions, models, toggles; argument form for commands that
  take arguments.
- **Systems panel**: LSP and MCP servers as a hex status grid with
  start/stop/refresh/auth actions and diagnostics counts.
- **Boot sequence**: short power-up animation that doubles as the loading
  state while the snapshot arrives.
- **Toast holograms**: notifications and "answered in the other client"
  messages.

### 3.4 Motion and performance

- Motion settings: Full, Reduced, Off. Default follows
  `prefers-reduced-motion`. Only `transform`, `opacity`, `filter` animate.
  Easing: expo-out for entrances; exits at about 70 % of entrance duration.
- Low power mode swaps WebGL for a 2D SVG constellation; auto-enabled when
  WebGL is unavailable.
- The Three.js scene renders on demand (on change and during active
  animation), targets 60 fps, and pauses when `document.hidden`.
- Streaming deltas are coalesced once per animation frame.
- Initial load budget about 600 KB gzipped; Three.js and CodeMirror in
  separate chunks.

### 3.5 Accessibility

- Every 3D element has a DOM equivalent: an agent list (roving tabindex) that
  drives the same selection as the constellation, and a file list.
- Native elements first; dialogs trap and restore focus; live regions announce
  new permission requests, run completion, and errors.
- Works at 200 % zoom; keyboard path covers every flow.

## 4. States and error handling

| Situation | Behavior |
|---|---|
| Loading | Boot sequence until snapshot applied |
| Empty session | Empty constellation core with deck hint: what to type and what will appear |
| No edited files | Drawer explains edits will appear here as agents write |
| Socket dropped | HUD amber "Signal lost, reconnecting", exponential backoff to 10 s, resync via `since` or snapshot |
| Crush exited | `gateway_closing` or repeated failure shows "Session ended", log stays readable, input disabled with reason |
| Bad or missing token | 401 page: run `/web` in the terminal to get a fresh link |
| Browser cannot open | Link already copied and shown; the failure warning carries the URL |
| Permission or question answered elsewhere | Dialog closes, toast "Answered in terminal" |
| API error | Inline error at the control that triggered it, with the server message and a retry where safe |
| Oversized output or diff | Truncated with "show full"; diffs over 1 MB fall back to unified text |
| WebGL missing | Low power 2D constellation |

## 5. Parity map and milestones

| TUI feature | Web surface | Milestone |
|---|---|---|
| Chat stream, markdown, tool cards, reasoning | Transmission log, thought layer | M1 |
| Send, queue, cancel, attachments, file picker, `@` completion | Command deck | M1 |
| Permission dialog, permission mode | Interrupt hologram, HUD toggle | M1 |
| Question forms | Holographic form | M1 |
| Subagents, teammates, agents dialog | Constellation, inspector, agent list | M2 |
| Team tasks | Task board | M2 |
| Todos | Todo orbit | M2 |
| Edited files, diff view | File hologram drawer | M2 |
| Sessions dialog | Palette + HUD switcher | M3 |
| Commands dialog, slash commands, MCP prompts, skills, arguments | Command palette | M3 |
| Models dialog, reasoning level | Model selector | M3 |
| MCP toggles and auth, LSP status and diagnostics | Systems panel | M3 |
| Summarize, side answer, shell commands, notes | Palette actions, deck modes | M3 |
| Plan handoff, notifications | Log cards, toasts | M3 |
| API key, OAuth (Copilot, Hyper, OpenAI, Grok), AWS SSO | Auth dialogs reusing existing flows | M4 |
| Pinentry, SSH askpass | Secure prompt dialog | M4 |
| Theme, motion, low power, detach | Visual settings | M4 |

Each milestone is independently shippable as its own PR series; M1 includes the
gateway, auth, stream, build pipeline, and the visual shell.

Conflict rule: first answer wins for permissions and questions; the other
client closes its dialog on the resolution event.

## 6. Build pipeline

- `web/` holds the Vite + TypeScript app: Preact with `@preact/signals` for
  DOM UI and state (small, fits the budget), Three.js for the constellation,
  CodeMirror 6 (`@codemirror/merge`) for diffs, `marked` plus `DOMPurify` for
  sanitized markdown.
- `task web:build`: `npm ci && npm run build` in `web/`, then sync `web/dist`
  into `internal/web/dist`.
- `internal/web/dist` is committed so `go build` never needs Node.
- CI job: build the frontend and fail if `internal/web/dist` differs from the
  committed copy; run Vitest and Playwright.
- `task web:dev`: Vite dev server proxying `/api` to a running gateway
  (`CRUSH_WEB_DEV_PORT`), for frontend iteration.

## 7. Testing

Go (`internal/web`, testify `require`, `t.Parallel()`):

- Auth: token exchange sets the cookie; missing or wrong cookie returns 401;
  foreign `Origin` and foreign `Host` are rejected; static assets need no auth.
- REST: each route against a fake `Workspace` asserting the call and mapping.
- Stream: ordering, `since` replay, snapshot fallback, slow-client close,
  `gateway_closing` on shutdown.
- Team watcher: emits only on change; handles missing and malformed files.
- Files: aggregation across child sessions, diff correctness, path validation,
  truncation.
- `SubscribeEvents` for `AppWorkspace` and `ClientWorkspace` coexisting with
  the TUI subscription.

Frontend:

- Vitest: event reducer, snapshot application, reconnect logic, permission
  race resolution, delta coalescing.
- Playwright smoke against a gateway backed by a fake workspace: load, send a
  prompt, permission round-trip, open a diff, select an agent node via
  keyboard and pointer, reduced motion and low power modes.

Manual: two terminals with `--web` (one in-process, one
`CRUSH_CLIENT_SERVER=1`), confirm two independent tabs and two-way sync with
each TUI.

## 8. Risks and open points for the plan

- `ClientWorkspace` fan-out must not duplicate the server SSE connection; the
  plan verifies its current subscription shape before choosing the design.
- Teammate cancel and message operations may not exist on `Workspace`; the
  plan adds them there first, never in the gateway.
- In-process "current session" notification needs a small hook in
  `internal/ui/model`; the plan reads `internal/ui/AGENTS.md` before touching
  it.
- Committing `dist` adds binary-ish churn; mitigated by hashed filenames and
  the staleness check.
