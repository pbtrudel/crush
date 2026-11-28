# Web UI rebuild modeled on the DeepSeek Harness web UI

Date: 2026-11-27
Status: approved design, pending spec review

## Intent

The current web UI (Preact, three.js "holodeck" stage, transmission log, HUD)
is being scrapped. It is replaced by a web UI that copies the look, layout and
feature set of the DeepSeek Harness (DSH) web client, mapped onto what Crush
actually does, with several sessions live at once in one browser.

What the user said:

- Scrap the current web UI completely and copy the DSH web UI and features.
- Parity level: core parity plus selected extras. The extras are Office, PDF
  and spreadsheet previews, an embedded browser panel, and multi-session.
- "Multisession" means several sessions running and driven at once in one
  browser, independent of the session shown in the TUI.
- Frontend stack: React 18 + Vite + CSS Modules, same as DSH.
- Backend: scrap the frontend, rework the Go gateway, keep its security core.
- Cutover: delete the old UI first, build the new one on `main` in milestones.

Assumptions made in the design (approved section by section):

- Multi-session is part of this spec because it shapes the gateway API. The
  Office/PDF/spreadsheet previews and the embedded browser panel each get a
  separate follow-up spec built on the right-sidebar tab system defined here.
- DSH features with no Crush backend are out of scope (see Non-goals).

Success criteria:

- Every feature of the current web UI that still makes sense has a home in
  the new UI (the old cinematic, holodeck, chronoline and pin/browse features
  are intentionally dropped).
- The new UI is recognizably DSH: three-column frame, DSH design tokens,
  light/dark/system theme, DSH chat, composer, approval and right-sidebar
  patterns.
- Two or more sessions can run at the same time, each driven from the
  browser, with live status in the sidebar.
- `go build` still needs no Node; the security model is unchanged.

## Reference

- Source: https://github.com/deepseek-ai/deepseek-harness, MIT license,
  pinned at commit `4b399c39a6d1479cd84ca591b4d7c88e24d38a96`
  (2026-11-22). DSH is a developer preview with announced breaking changes;
  we port from the pin and do not track upstream.
- The DSH UI lives in `packages/client/*` (about 230k lines of source) and is
  composed at runtime from Cordis plugins talking to a Node host over
  `POST /api/<namespace>/<method>` plus a multiplexed WebSocket. It cannot be
  dropped in; it is re-implemented against Crush's Go gateway.
- Relevant DSH packages: `ui-theme`, `ui-layout`, `ui-sidebar`,
  `ui-workspace`, `ui-conversation`, `ui-chat`, `ui-tool`, `ui-primitives`,
  `ui-approval`, `ui-user-questions`, `ui-sidebar-right`, `ui-dockkit`,
  `ui-sidebar-files`, `ui-sidebar-documentpreview`, `ui-session-panel`,
  `ui-subagent`, `ui-agent-team`, `ui-jobs`, `ui-settings*`,
  `ui-model-selection`, `ui-permission-presets`, `ui-plan`, `ui-shortcuts`.

## Non-goals

- DSH plugin system, plugin manager and marketplace.
- Schedules / automation tasks page, built-in terminals (xterm.js),
  trajectory view, i18n, DeepSeek account and quota, QR pairing and remote
  devices, agent presets, message feedback, action and recommendation cards.
- Office/PDF/spreadsheet previews and the embedded browser panel (separate
  follow-up specs).
- Any change to the agent, coordinator or database schema.
- Any change to the gateway security model (loopback only).

## Milestones

Each milestone ships as a series of PRs of roughly 400 changed lines or
less, merged to `main`.

| # | Milestone | Contents |
|---|---|---|
| M0 | Removal | Delete `web/` (source, e2e specs, baselines), the old frontend build in `internal/web/dist`, and endpoints the new design does not need. `--web` and `/web` serve a placeholder page. The gateway security core and its tests stay. |
| M1 | Shell | React/Vite scaffold, DSH theme tokens (light/dark/system) with the MIT notice, three-column frame, auth and reconnect, left sidebar with workspace and sessions (running, pending, unread markers), read-only chat. `--web` now serves the new UI, with a "preview, incomplete" banner, instead of the placeholder. |
| M2 | Chat | Markdown (micromark + Shiki + KaTeX), streamed assistant replies, reasoning, nested tool-call trees with a view per tool type (bash, view, edit/diff, grep/glob, fetch, todos, agent/subagent, MCP), usage footer, and the virtualized message list (`@tanstack/react-virtual`) that M1 defers. |
| M3 | Composer | Lexical editor with a draft per session; `/` menu (commands, skills, MCP prompts, and Crush actions: compact, export, goal/loop, note, side question); `@` mentions; attachments; queue and stop; model and reasoning picker; permission picker; plan mode and plan handoff. |
| M4 | Interactions | Approval card, question card, GPG/SSH secure prompts, provider sign-in (API key, OAuth device code and loopback, AWS SSO). |
| M5 | Right sidebar | Dockable tabs (lightweight dock system), file tree with search, review and diff tabs, previews for Markdown, code and images. |
| M6 | Panels and settings | Session panel (subagents, Agent Team task board, background jobs), settings (appearance, models and providers, LSP/MCP servers, config reload), shortcuts reference. The preview banner goes away; `docs/web-mode.md` is rewritten. |

### Mapping DSH onto Crush

| DSH surface | Crush mapping |
|---|---|
| Workspaces grouping in sidebar | One workspace (the Crush working directory); header shows its name. |
| Permission presets (Read Only / Workspace Write / Full access) | Crush modes: ask and auto-approve (yolo). |
| `/plan`, `/goal`, `/compact`, `/export`, `/team` | Crush plan mode, goal and loop automation, summarize, Markdown export, Agent Teams toggle. |
| Plugin pages in Settings | Systems page: LSP and MCP server states, start/stop, enable/disable, auth, Docker MCP catalog. |
| No DSH equivalent | Notes, side question, `!` shell, skills enable/disable/attach, config reload, project init: `/` menu and Settings. |
| Session fork | New `POST /api/sessions/{sid}/fork` endpoint. |

## Gateway API and stream

### Kept unchanged

Auth (per-run launch token, `POST /api/auth`, bearer header, token in the
WebSocket subprotocol), Host allow-list and Origin checks, CSP and security
headers, the launch file, the embedded assets handler, the controller and
its TUI callbacks, the `--web` flag, `/web` and the palette items, the WEB
status badge. Their tests stay.

### Transport

REST/JSON for actions. One WebSocket at `GET /api/stream`, new subprotocol
`crush.v2` (the `crush.auth.<token>` entry is unchanged).

### Stream model

Global frames, sent to every connection:

- `session`: created, renamed and deleted, one entity per frame.
- `session_status`: `{"session_id","status"}`, published only when a
  session's status changes. The statuses are `idle`, `running`, `queued` and
  `pending_interaction`; a session waiting for the person outranks one that
  is running, which outranks one with a queued prompt.
- Permission and question requests and notifications:
  `permission_request`, `permission_notification`,
  `permission_mode_changed`, `question_batch_request` and
  `question_batch_notification`. These are global frames, and the session
  they belong to travels in the payload's `session_id` field, not in a frame
  header.
- `model_changed`, `config_changed`, `lsp_event`, `mcp_event`, `team_state`,
  `agent_runs`, `skills_event`, `update_available`, `gateway_closing`,
  `web_error`, `secure_prompt`, `signin`.

Per-session frames, sent only to connections subscribed to that session:

- Client sends `{"type":"subscribe","session_id":"…"}` and
  `{"type":"unsubscribe","session_id":"…"}`.
- On subscribe the server sends `session_snapshot`: the latest page of
  messages, edited files, queue, busy state.
- Then live updates `message`, `file`, `shell_output` and `run_complete`.
  These four, and only these four, carry `session_id` at the top level of
  the frame beside the global sequence number; every other frame is global.
- A connection may hold several subscriptions at once: the open session plus
  any session that is not idle, at most 32. A background session that
  returns to `idle` and is not the open one is dropped.
- The subscription is reserved before the session's snapshot is read, and
  the frames published while it is read are buffered and flushed after the
  snapshot, so the snapshot is always the first thing a tab reads for a
  session.

Replay: the global ring stays, with `?since=<seq>` replay and resync on
overflow (close code 4001). Only global frames are replayed: a connecting
tab holds no subscriptions yet, so per-session frames in the ring are never
sent, and the client re-sends its subscriptions and receives fresh snapshots
after a reconnect or a resync.

The browser does not follow the TUI. It has its own selected session.
"Open in terminal" calls `POST /api/follow` explicitly.

The agent already supports concurrent sessions (`activeRequests` is keyed
by session ID; `IsSessionBusy(sessionID)` in
`internal/agent/coordinator.go`), so no agent changes are needed.

### Endpoints

Kept or adapted:

- Sessions: list, create, get, rename, delete; messages with new
  `?before=<id>&limit=<n>` paging; files and file diff; prompt (multipart,
  32 MiB cap); cancel; queue get and clear; summarize; ask; shell and shell
  cancel; notes; automation; export.
- Interaction: permissions grant and mode, input mode, questions answer and
  cancel, pinentry and SSH answer and cancel, plan handoff, `@` completion,
  team message and interrupt.
- Commands, MCP prompts, skills (list, reload, enable, disable, global,
  attach).
- Models: providers, select, reasoning. Config: get, reload. Agent Teams
  toggle, Docker MCP enable/disable, project init.
- Systems: MCP list and actions, LSP list, start, stop, diagnostics.
- Sign-in: all current `/api/auth/provider/*` routes.
- `GET /api/health`, `POST /api/detach`, `POST /api/follow`.

New:

- `GET /api/bootstrap`: global state (session list with status, model,
  permission mode, input mode, LSP, MCP, team, version, branch, pending
  interactions).
- `POST /api/sessions/{sid}/fork`.
- `GET /api/sessions/search?q=`: title and content search with snippets,
  20 results.
- `GET /api/workspace/files?path=`: one directory level of the working
  directory tree.
- `GET /api/workspace/search?q=`: file name search.
- `GET /api/workspace/file?path=`: raw bytes for previews. The path is
  cleaned and must resolve (after symlinks) inside the working directory;
  otherwise 403.

Removed:

- `GET /api/snapshot` (replaced by bootstrap plus per-session snapshots).
- `POST /api/config/set` and `POST /api/config/remove` for
  `options.web_display.*`, and `options.web_display` itself.
- Cinematic, pin and browse plumbing and the `display_changed` frame.

### Browser-side state

Unread markers, theme, font sizes, right-sidebar layout per session, and
composer drafts live in `localStorage`. No database migration.

An unread marker compares the session row's `updated_at` (from
`GET /api/sessions`, the bootstrap and the global `session` frames) with a
per-session last-seen `updated_at` kept under `crush.seen`. The comparison
survives a Crush restart because the workspace owns the timestamps, and it
needs no subscription: a session is marked seen when it is selected or
receives an update while selected. A title-only update can fire a marker;
that is accepted.

## Frontend structure

Source in `web/` (new tree), React 18, Vite 6, TypeScript strict, CSS
Modules and `clsx`, no component library. Vite builds into
`internal/web/dist`, which stays committed and embedded; CI checks it is
fresh.

`web/src`:

- `api/`: HTTP client; WebSocket client with reconnect (jittered backoff
  capped at 500 ms, 1 s, 2 s, 4 s, 8 s, 10 s), subscribe and unsubscribe,
  resync. Wire types validated with `zod` at the boundary.
- `store/`: small observable stores read through `useSyncExternalStore`:
  global, session list, one store per subscribed session, UI preferences
  (persisted).
- `theme/`: DSH `--dsw-*` tokens, base CSS, radius scale, fonts. Light, dark
  and system. Text, code and terminal font sizes (DSH defaults 14, 11, 13).
- `layout/`: three-column frame with DSH sizing: left sidebar 264 to 420 px,
  default 280, 56 px rail when collapsed, modal drawer below 768 px; right
  panel opens at 45% of the viewport, capped at 70%, center keeps at least
  400 px.
- `sidebar/`: workspace header, New Session, search, session rows with
  running, pending and unread markers, and rename, fork, delete, copy ID and
  "Open in terminal" actions.
- `conversation/`: header with title (inline rename) and More menu (Copy as
  Markdown, Export); chat view virtualized with `@tanstack/react-virtual`;
  turn rail; back-to-bottom. M2 ships the virtualized view and
  back-to-bottom; the turn rail lands with M3.
- `chat/`: message bubbles with copy, markdown (micromark/mdast, Shiki,
  KaTeX), reasoning previews, usage footer, context ring, retry row, error
  rows, toasts. M2 ships everything up to the context ring; the retry row
  and toasts land with M3, because the retry row needs the composer.
- `tools/`: tool-call trees with nested subcalls and one view per tool type;
  ANSI-aware terminal card; exit-code capsule; diff rendering.
- `composer/`: Lexical editor; `/` and `@` menus with atomic chips;
  attachments with drag and drop; prompt history recall; queue display;
  Send/Stop; model and reasoning picker; permission picker; plan-mode chip;
  goal strip.
- `interactions/`: approval card and question card replacing the composer
  while active (Enter approves, Escape rejects); secure prompt and sign-in
  dialogs.
- `right/`: dock with at most two panes, layout saved per session. Tab kinds
  in this spec: guide (home), files, review/diff, document preview
  (Markdown, code via Shiki, images). The tab registry is open so the
  follow-up specs add Office/PDF/spreadsheet and browser tabs.
- `panel/`: session panel with Subagents, Shared tasks (Agent Team board,
  teammate message and interrupt) and Background jobs sections.
- `settings/`: General (appearance, fonts), Models and Providers (sign-in
  entry), Systems (LSP/MCP), Config (reload, project init), shortcuts
  reference (`Mod+/`).

### Porting rule

Lift DSH code only for leaf pieces: styles, tokens, markdown and tool
renderers, the Lexical composer setup. Each lifted file starts with a
comment naming its DSH source path and the pinned commit. The DSH MIT notice
goes in `web/THIRD_PARTY_NOTICES.md`. Anything tied to Cordis (`ctx`, slots,
`renderSlot`, plugin registries, remotes) is rewritten as plain React props
and context.

### Bundle budget

First-paint JavaScript stays under 600 KiB gzipped, enforced in CI. Shiki
languages, KaTeX and Lexical plugins load lazily.

## Error handling

- Stream: reconnect with backoff; a connection banner shows reconnecting and
  ended states; resync re-subscribes and replaces session stores with fresh
  snapshots.
- API errors use the existing JSON error envelope (`errors.go`); the UI shows
  them as toasts or inline rows next to the failing action.
- Deleting a running session, or one shown in the terminal, is refused
  server-side as today; the UI shows the reason.
- `GET /api/workspace/file` rejects paths outside the working directory and
  files above a size cap (32 MiB) with explicit errors.

## Testing

Go (`internal/web`):

- Security-core tests stay unchanged.
- testify tests for every new or adapted endpoint against the `webtest` fake
  workspace, which gains support for concurrent sessions.
- Stream tests: subscribe and unsubscribe, snapshot then updates, tagging
  with `session_id`, replay filtered by subscription, resync with active
  subscriptions, two sessions streaming at once.
- The route registry auth test keeps covering every `/api/*` route.
- Path-escape tests for `GET /api/workspace/file` (`..`, absolute paths,
  symlinks pointing outside).

Frontend:

- Vitest + Testing Library for stores, the stream client (reconnect,
  resync), and components.
- Playwright e2e against `internal/web/cmd/webfake`, rebuilt: one spec per
  milestone plus a multi-session spec (two sessions running, switching,
  markers updating).
- Visual baselines for light and dark at 1440 and 1280 wide, added only once
  their milestone settles. The chat screen settles with M6's polish pass, so
  its baselines land there; M2 pins light colours with contrast unit tests
  and the axe scan instead.
- axe scans on every main screen.

CI: `.github/workflows/web.yml` keeps its jobs (typecheck and unit tests,
committed-build freshness, Go tests, sharded e2e) and adds the bundle budget
check.

## Cleanup in M0

- TUI: keep `--web`, `/web`, Open/Stop Web Mode and the WEB badge. Update
  only tests that depend on old behavior (for example
  `plan_handoff_web_test.go`).
- Config: remove `WebDisplayOptions` and `Options.WebDisplay` from
  `internal/config/config.go`, regenerate `schema.json`. Keep `options.web`.
- Taskfile `web:*` tasks stay, pointed at the new tree.
- Docs: `docs/web-mode.md` gets a short "being rebuilt" note until M6; the
  README web mode section is trimmed to match. Old design and plan docs under
  `docs/superpowers/` stay as history.
- `github.com/coder/websocket` stays (still used by the stream).

## Risks

- DSH is a developer preview: port from the pinned commit only.
- Lexical, Shiki and KaTeX are large: lazy loading plus the CI budget.
- Web mode is a placeholder from M0 until M1 lands a usable shell, and lacks
  features (shown by the preview banner) until M6 (accepted).
- Replay filtering per subscription makes the hub more complex: covered by
  dedicated stream tests.
