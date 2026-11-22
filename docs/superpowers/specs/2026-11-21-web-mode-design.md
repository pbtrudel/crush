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
  `assets/2026-11-21-web-mode-reference.jpg` and, after the first build was
  rejected, the neon holographic dashboard references in section 3.1.
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
  - `input_mode_changed`: `{"mode":"plan"|"code"}`, sent when the terminal's
    input mode changes or the gateway is told a new one.
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
| input mode | `POST /input-mode` |
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

`POST /sessions` starts a session from the browser when the terminal has no
current one. It creates the session through the workspace with the same
`New Session` title the TUI uses for a first prompt, then asks the terminal to
open it; the tab follows the new current session through `session_follow`. An
empty body uses that title.

The terminal follows the most recently browser-created session even when it
was showing another session. In the TUI status bar, config-stale and update
notices wait behind a notice that is already visible instead of replacing it,
and appear when that notice clears.

`POST /input-mode` takes `{"mode":"plan"|"code"}` and returns 202. The gateway
validates the mode, keeps no agent state, and asks the terminal to switch
through a callback. Every valid request is forwarded, even for the mode that
is already active: the terminal applies its usual busy and mode-switching
checks, where a same-mode request is a no-op. When the mode settles, the
terminal reports it back to the gateway, which includes it in the snapshot and
publishes `input_mode_changed`. A refused switch publishes `web_error`, which
the browser shows as a toast.

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

Concept name: **Holodeck**. Dominant work patterns: operate (prompt, approve,
steer) and monitor (agents, files).

### 3.1 Direction and references

The first build used a muted navy theme with grey wireframes and flat boxes.
The user rejected it. This section replaces it with a neon holographic look
agreed in brainstorming on 2026-11-22.

References: the "holographic dashboard" image set on stockcake.com
(`https://stockcake.com/i/holografische-dashboard-oberfl%C3%A4che_4008192_1821188`
and its related images), shared as screenshots. The traits to reproduce:

- Saturated neon light (cyan, hot magenta, violet) with heavy glow and bloom.
- Atmosphere: violet fog, drifting particles, light beams.
- A glossy floor with a neon perspective grid running to the horizon.
- A holographic projector: glowing ring pedestal, light cone, content
  floating above it.
- Angled glass screens with glowing two-tone edges, corner brackets and scan
  lines.
- Instrument-style data: radial gauges, bar charts, sparklines, big glowing
  numerals.

Not reproduced: city skylines and isometric circuit landscapes. They are
decoration that competes with the agents for attention and costs GPU time.

Decisions taken (user choices in brackets):

| Topic | Decision |
|---|---|
| 3D depth | Readable 2D layout of glass panels over a full 3D world, plus a cinematic mode (C) |
| Palette | Cyan-led; violet for fog and depth; magenta only for "needs you" (A) |
| Scene | Grid floor, atmosphere, projector and core, agent nodes in orbit (1 to 4) |
| Panels | Glass for what you read, hologram for what you glance at (C) |
| Top bar | Instruments plus a live activity chart (B) |
| Motion | Calm while idle, lively while working (A) |
| Cinematic | Watch mode, toggled by a key and a button (A plus a shortcut) |
| Technique | HTML panels over a three.js scene; CSS 3D renderer for cinematic mode |

### 3.2 Tokens

| Role | Value | Use |
|---|---|---|
| void | `oklch(0.12 0.05 275)` | page background, scene clear color |
| haze | `oklch(0.30 0.16 300)` | horizon gradient and depth fog |
| glass | `oklch(0.16 0.05 265 / 0.70)` | glass panel body, behind `backdrop-filter: blur(14px)` |
| glass solid | `oklch(0.16 0.05 265 / 0.94)` | glass body where `backdrop-filter` is unsupported |
| holo cyan | `oklch(0.86 0.17 205)` | primary signal: edges, grid, core, focus, links |
| magenta | `oklch(0.70 0.29 340)` | attention only: permission requests, questions, errors, context near full |
| violet | `oklch(0.62 0.25 295)` | depth and structure: fog, subagents, reasoning, network tools |
| teal | `oklch(0.82 0.16 170)` | success, additions, write tools |
| amber | `oklch(0.83 0.17 75)` | warnings, reconnecting, run tools |
| text | `oklch(0.95 0.02 220)` / muted `oklch(0.74 0.05 240)` | body / secondary |

- Glow is a token too: `--glow-cyan` is a two-layer shadow (a tight 1 px
  edge at full strength plus a soft 18 px halo at 35 % alpha). The same
  exists for magenta and violet.
- Values past the sRGB gamut are clipped by the browser; P3 displays show the
  fuller saturation. Contrast is checked against the clipped sRGB value.
- Every state carried by color also carries an icon, label, or shape.

Type (self-hosted, bundled): Chakra Petch for labels and numerals, with a
cyan text glow on numerals; IBM Plex Sans for prose; JetBrains Mono for code.
Spacing on a 4 px base (4, 16, 36 rhythm). Focus ring: 2 px dashed holo cyan,
2 px offset, on every interactive element.

### 3.3 Layout

```
┌──────────────────── HUD strip (hologram) ──────────────────────┐
│ title · model · branch │ context gauge │ tokens · cost │ activity │
│ LSP/MCP hex tiles │ permissions · Plan/Code · cinematic · display · link │
├───────────────┬──────────────────────────────┬────────────────┤
│ Transmission  │        3D scene (§3.4)        │ File drawer    │
│ log (glass,   │  projector, core, agent nodes │ (glass, M2,    │
│ curved)       │  inspector on node click (M2) │  slides in)    │
├───────────────┴──────────────────────────────┴────────────────┤
│ Command deck (glass, flat): prompt · attachments · send/cancel │
└───────────────────────────────────────────────────────────────┘
```

- The 3D scene fills the whole viewport behind every panel; the center column
  is simply where no panel covers it.
- The transmission log keeps its slight inward curve toward the projector
  (CSS perspective with a small `rotateY`). The command deck stays flat.
- Below 1024 px the side panels become tabs and the activity chart and
  sparklines hide (numbers stay). Below 768 px everything stacks and the scene
  shrinks to a strip behind the HUD.

### 3.4 The scene

Rendered by three.js behind all panels.

**Camera.** Slightly above the floor, looking at the projector. In Full
motion a small parallax follows the pointer (at most 2 degrees); the camera
never drifts on its own.

**Floor.** Dark plane with a cyan perspective grid whose lines fade into the
haze at the horizon. The glossy look comes from a mirrored, dimmed copy of the
emissive objects (core, rings, nodes) under the floor; there are no real
reflections.

**Atmosphere.**
- Exponential fog in the haze color; the background shades from void at the
  top to haze at the horizon.
- About 1,500 dust particles drifting slowly, drawn as one `Points` batch.
- Three or four soft light beams angled down from above, fading in and out
  over 6 to 10 seconds.

**Projector and core.**
- A pedestal of three stacked cyan rings casting an upward cone of light.
- The core floats in the cone: a faceted crystal with a glowing rim and a
  bright inner heart. It replaces the grey wireframe sphere.
- A ring around the core fills with the context window share.

| Core state | Look |
|---|---|
| Idle | Slow breathing pulse (about 4 s period) |
| Working | Brighter, spinning faster |
| Needs you (permission or question pending) | Magenta, pulsing |
| Error | Magenta flicker, plus an icon and text in the panels |

**Agent nodes (M2, with the constellation).**
- Hexagonal prisms on a tilted orbit around the core: cyan for teammates,
  violet for subagents.
- Each node is joined to the core by a light filament; a pulse runs along it
  when messages flow.
- A tool call fires a short particle burst from the core to the node that ran
  it. In M1, with only the main agent, the burst is a ring expanding from the
  core.
- Burst color by tool category: read cyan, write teal, run amber, network
  violet. Magenta stays reserved for "needs you".
- Clicking a node opens its inspector. Node statuses: running (bright, slow
  spin), inactive (dim), provisioning (assembling wireframe), failed (magenta
  flicker plus icon), done (collapses into the core).

**Glow.** An `UnrealBloomPass` (three.js `examples/jsm/postprocessing`) makes
only emissive parts bleed light. Panels and text are HTML above the canvas and
are never bloomed.

**Fallbacks.**
- Low power mode: no bloom, no beams, a quarter of the particles, no
  cinematic mode.
- No WebGL: the existing 2D view, with low power mode forced on.

### 3.5 Panels

**Glass** (read often): transmission log, command deck, file drawer, agent
inspector, permission requests, questions.

- Body in the glass token over a 14 px backdrop blur; the solid token where
  blur is unsupported.
- Two-tone glowing edge: cyan at the top left fading to violet at the bottom
  right, with the cyan glow token outside.
- Brighter cyan L-shaped corner brackets on all four corners.
- A very faint scan-line texture in the panel background only, never over
  text.
- Code blocks sit in a darker inset well inside the glass.
- Permission requests and questions use the same glass with magenta edges and
  brackets. The scene dims behind them, and the edge pulse grows with the
  danger level. Focus moves into them and the page behind is `inert`.

**Hologram** (glanced at): the HUD strip, agent labels next to their nodes in
the scene, and toasts.

- No fill, or a whisper of void at 20 % alpha. Content is glowing cyan lines
  and text inside thin bracket frames, with a faint scan-line texture.
- Each element flickers once for about 120 ms when it appears; never
  continuously.
- Toasts appear top right, dismiss themselves after 6 s (errors stay until
  dismissed), and are announced by a polite live region.

**Controls.**
- Primary (Transmit): solid glowing cyan.
- Secondary: cyan outline, filling on hover.
- The permission `<select>` is restyled to match (it shows the browser's
  default white control today).
- The HUD gains the cinematic mode button.

### 3.6 Instruments (the HUD)

| Instrument | Look | Behavior |
|---|---|---|
| Session title, model, branch | Hologram text; the model has a small provider glyph | Static |
| Context | Radial gauge, about 56 px, percentage in the center | Cyan; amber from 75 %; magenta and pulsing from 90 % |
| Tokens, cost | Big glowing numerals, each with a sparkline of the last 10 minutes | Numerals roll to new values instead of jumping |
| Activity | One bar per minute for the last 15 minutes, stacked by tool category in the §3.4 colors | The current minute grows live; hover shows counts |
| LSP, MCP | One small hex tile per server | Lit cyan when running, dim when stopped, magenta plus icon on errors or down; hover shows name and status |
| Controls | Permissions, Plan/Code, cinematic (`Alt+C`), display settings, link status | As today, restyled |

- Instruments are SVG built in Preact.
- Sparklines and the activity chart are derived in the browser from stream
  frames already sent (usage updates and tool calls). History starts when the
  tab opens; a reconnect replays what the gateway ring still holds. No new
  server API is needed.
- Tool category is derived from the tool name with a fixed table in the
  frontend (for example `view`, `grep`, `glob`, `ls` are read; `edit`,
  `write`, `multiedit` are write; `bash` and job tools are run; `fetch`,
  `download`, web search and MCP tools are network). Unknown tools count as
  run.
- Each instrument has a text label for screen readers, for example "Context
  42 percent".

### 3.7 Cinematic mode

A watch mode for following agents work, not for long reading.

- **Toggle:** `Alt+C` (`Option+C` on macOS) from anywhere, including while
  typing in the prompt, matched on `event.code === "KeyC"` with `altKey` and
  `preventDefault` so macOS does not insert a character. Also the HUD button,
  whose tooltip shows the shortcut.
- **Exit:** `Alt+C`, the button, or Esc. Esc exits only when focus is outside
  the prompt (in the prompt it keeps cancelling a run) and no interrupt dialog
  is open (the dialog owns Esc).
- **Transition:** the camera pulls back and up over about 900 ms (expo-out).
  At the same time the transmission log (and in M2 the agent inspector) moves
  onto curved screens around the projector through three.js `CSS3DRenderer`.
  The panels stay real HTML: sharp, selectable, readable by screen readers.
  The renderer positions them through the CSSOM (`element.style.transform`),
  which the CSP allows; no `style` attributes are written into markup.
- **While in it:** drag to orbit (polar angle limited so the camera stays
  above the floor), scroll to zoom within bounds. Clicking an agent node turns
  its screen to face the camera.
- **Prompt:** a slim prompt bar stays at the bottom. Permission requests and
  questions still appear flat in front of everything, and focus jumps to
  them.
- **Reduced and Off motion:** a 150 ms cross-fade instead of the flight.
- **Low power mode:** cinematic mode is unavailable and the button hidden.
- DOM order never changes, so assistive technology reads cinematic mode the
  same as the normal layout.

### 3.8 Components and display features

Unchanged in purpose; they take the glass or hologram treatment above.

- **Inspector** (M2): opens on node click or keyboard selection. Live
  transcript of that agent's session, current tool with elapsed time, model
  and provider, tokens and cost, assigned task, status, and actions (cancel,
  message for teammates).
- **Transmission log**: markdown rendering, collapsible tool cards with input
  and output, streaming text, reasoning as a dimmed violet "thought layer"
  (collapsed by default, expandable), plan handoff cards, compaction markers,
  subagent report cards. Virtualized for long sessions.
- **File drawer** (M2): tree of edited files with +/- bars and diagnostic
  markers; selecting a file opens a CodeMirror 6 merge view (side-by-side or
  unified, read-only, syntax highlighted), a version stepper, and links to
  the tool calls that produced each version. CodeMirror is loaded lazily.
- **Chronoline** (M2): horizontal timeline of tool calls and edits above the
  command deck; hover previews, click scrolls the log and highlights the
  agent node.
- **Permission request**: magenta glass (§3.5), shows tool, path, params,
  and description. Keys: `A` allow, `S` allow for session, `D` deny.
- **Question form**: magenta glass, tabbed, supporting yes/no, single, multi,
  free text, and the confirmation step.
- **Task board** (M2): three lanes (pending, in progress, completed) for team
  tasks; owner chips link to agent nodes.
- **Todo orbit** (M2): session todos as a compact ring checklist beside the
  core.
- **Command palette** (M3, `Ctrl+K` / `Cmd+K`): commands, slash commands, MCP
  prompts, skills, sessions, models, toggles; argument form for commands that
  take arguments.
- **Systems panel** (M3): LSP and MCP servers as a hex status grid with
  start/stop/refresh/auth actions and diagnostics counts. The HUD hex tiles
  open it.
- **Boot sequence**: the scene powers up (grid draws outward, rings stack,
  core ignites) while the snapshot arrives; it doubles as the loading state.
- **Toasts**: notifications, refusals ("Can't switch while Crush is busy")
  and "answered in the other client" messages.

### 3.9 Motion

- Settings: Full, Reduced, Off. Default follows `prefers-reduced-motion`.
- **Idle:** the core breathes, particles drift, the grid scrolls gently,
  beams fade in and out. Nothing else moves.
- **Working:** tool-call bursts, filament pulses, faster core spin, live
  activity bar.
- **Entrances:** panels and messages power up with a short glitch and
  scan-line sweep under 300 ms (expo-out); exits at about 70 % of the entrance
  duration. Hologram elements flicker once on appearance.
- Only `transform`, `opacity` and `filter` animate in CSS.
- Reduced: no parallax, no flicker or glitch, entrances become fades, scene
  ambient motion at a quarter speed, bursts become a single brightness pulse.
  Off: the scene renders static frames on state change only.

### 3.10 Performance

- The scene renders on demand: up to 60 fps while anything event-driven is
  moving, 30 fps when only idle ambient motion remains, paused when
  `document.hidden`.
- Bloom renders at half resolution. Particles are one draw call. In-scene
  agent labels re-render only when their content changes.
- **Frame-time guard:** when the average frame time exceeds 25 ms over about
  3 s, effects step down one level at a time: bloom off, then beams off, then
  half the particles. A toast says "Reduced effects to keep things smooth"
  with a button to restore them. The guard does not step down again for 10 s
  after a change.
- Streaming deltas are coalesced once per animation frame.
- Initial load budget about 600 KB gzipped. three.js (scene and
  postprocessing), the cinematic mode code and CodeMirror are separate chunks
  loaded in parallel so the text panels render first.

### 3.11 Accessibility

- Every 3D element has a DOM equivalent: an agent list (roving tabindex) that
  drives the same selection as the nodes, and a file list.
- Text on glass at least 4.5:1, measured with the glass composited over the
  brightest pixel the scene renders (the core at full bloom). HUD numerals at
  least 3:1 against the haze.
- Native elements first; dialogs trap and restore focus; live regions
  announce new permission requests, run completion, errors and toasts.
- Works at 200 % zoom; the keyboard path covers every flow, including
  cinematic mode.

### 3.12 Visual verification

- Playwright screenshot tests against webfake with a fixed random seed and a
  paused clock (`page.clock`), in headless Chromium with SwiftShader WebGL:
  normal layout, cinematic mode, a permission request open, and Reduced
  motion. Baselines are committed under `web/e2e/` and compared with a small
  tolerance; they are regenerated deliberately with `--update-snapshots`.
- An axe scan (`@axe-core/playwright`) on each of those screens, failing on
  serious and critical violations.
- During the build, the same screenshots are reviewed by eye against the
  references after each visual task.

## 4. States and error handling

| Situation | Behavior |
|---|---|
| Loading | Boot sequence until snapshot applied |
| Empty session | Idle core on the projector, with a deck hint: what to type and what will appear |
| No edited files | Drawer explains edits will appear here as agents write |
| Socket dropped | HUD amber "Signal lost, reconnecting", exponential backoff to 10 s, resync via `since` or snapshot |
| Crush exited | `gateway_closing` or repeated failure shows "Session ended", log stays readable, input disabled with reason |
| Bad or missing token | 401 page: run `/web` in the terminal to get a fresh link |
| Browser cannot open | Link already copied and shown; the failure warning carries the URL |
| Permission or question answered elsewhere | Dialog closes, toast "Answered in terminal" |
| API error | Inline error at the control that triggered it, with the server message and a retry where safe |
| Oversized output or diff | Truncated with "show full"; diffs over 1 MB fall back to unified text |
| WebGL missing | Low power mode forced on, 2D view, no cinematic mode |

## 5. Parity map and milestones

| TUI feature | Web surface | Milestone |
|---|---|---|
| Chat stream, markdown, tool cards, reasoning | Transmission log, thought layer | M1 |
| Send, queue, cancel, attachments, file picker, `@` completion | Command deck | M1 |
| Start a session from the landing screen (first prompt) | Command deck, `POST /sessions` | M1 |
| Permission dialog, permission mode | Magenta glass request, HUD selector | M1 |
| Plan mode | HUD Plan/Code toggle, deck badge, `POST /input-mode` | M1 |
| Question forms | Magenta glass form | M1 |
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
| Theme, detach | Visual settings | M4 |

Each milestone is independently shippable as its own PR series; M1 includes the
gateway, auth, stream, build pipeline, and the full visual language of
section 3: scene (floor, atmosphere, projector, core, bloom), glass and
hologram panels, HUD instruments, cinematic mode, motion settings (Full,
Reduced, Off), low power mode, the frame-time guard, and the screenshot and
axe checks. Agent nodes, filaments, the inspector and the file drawer arrive
with their features in M2.

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
  keyboard and pointer, reduced motion and low power modes, `Alt+C`
  cinematic toggle.
- Screenshot and axe checks as in section 3.12.

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
