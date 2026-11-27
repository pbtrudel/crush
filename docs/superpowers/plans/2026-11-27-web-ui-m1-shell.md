# Web UI M1 (Shell) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the M0 placeholder with a React shell that boots from the
gateway, shows DSH's three-column frame and theme, lists sessions with live
running/queued/pending/unread markers in the left sidebar, and renders the
selected session's conversation read-only.

**Architecture:** Seven gateway PRs land first, so `--web` keeps serving the
placeholder page the whole time: per-session status, message paging, the
bootstrap/session-snapshot split with `GET /api/bootstrap`, the `crush.v2`
stream with bootstrap and subscribe frames, per-session delivery and replay
filtering, a golden frame contract the frontend validates, and the webfake
M1 seed. Ten frontend PRs follow: the React/Vite scaffold with the restored
CI and Taskfile (this is where `--web` switches from the placeholder to the
new UI), the copied DSH theme, the HTTP client with the zod contract, the
stream client, the observable stores, the three-column frame, the boot and
auth wiring, the sidebar, the read-only chat, and the Playwright suite.

**Tech Stack:** Go 1.27.4, `net/http`, `github.com/coder/websocket`,
testify, gofumpt. Frontend: React 18.3, Vite 6, TypeScript strict, CSS
Modules, `clsx`, `zod`, Vitest 3 with `@testing-library/react` and `jsdom`,
Playwright with `@axe-core/playwright`, Node 26 (`web/.nvmrc`).

**Spec:** `docs/superpowers/specs/2026-11-27-web-ui-dsh-design.md`. Read
"Milestones" (the M1 row), "Gateway API and stream" (all of it), "Frontend
structure", "Porting rule", "Bundle budget", "Error handling", "Testing" and
"Cleanup in M0" before Task 1. The M0 plan
(`docs/superpowers/plans/2026-11-27-web-ui-m0-removal.md`) is the state this
plan starts from, and shows the conventions it follows.

Every Go symbol, route, helper and test name below was checked against the
merged tree of 2026-11-27 (`main` after the four M0 merges). The plan names
code by symbol, never by line number, because line anchors go stale on the
first rebase.

Date: 2026-11-27.

### Execution order and PR grouping

Each task is one PR of roughly 400 changed lines or less, excluding
`web/package-lock.json`, the committed `internal/web/dist` build output and
the copied DSH CSS. The PRs merge in order; every branch starts from
`origin/main` after `git fetch origin`, once its predecessor has merged.
Tasks 4 and 5 both rewrite parts of `internal/web/stream.go`, and Tasks 10
and 11 share `web/src/api/`, so out-of-order or parallel work guarantees a
conflict.

| PR | Task | Branch | Scope |
|---|---|---|---|
| 1 | Task 1 | `crush/web-ui-m1-1-status` | Per-session status and the `session_status` frame |
| 2 | Task 2 | `crush/web-ui-m1-2-paging` | `?before` / `?limit` message paging |
| 3 | Task 3 | `crush/web-ui-m1-3-bootstrap` | `GET /api/bootstrap`, bootstrap and session-snapshot split |
| 4 | Task 4 | `crush/web-ui-m1-4-stream-v2` | `crush.v2`, bootstrap frame, subscribe and unsubscribe |
| 5 | Task 5 | `crush/web-ui-m1-5-routing` | Per-session frame delivery and replay filtering |
| 6 | Task 6 | `crush/web-ui-m1-6-golden` | `internal/web/testdata/frames.golden.json` |
| 7 | Task 7 | `crush/web-ui-m1-7-webfake` | webfake M1 scenario with several sessions |
| 8 | Task 8 | `crush/web-ui-m1-8-scaffold` | React/Vite scaffold, budget, Taskfile and CI |
| 9 | Task 9 | `crush/web-ui-m1-9-theme` | DSH tokens, light/dark/system |
| 10 | Task 10 | `crush/web-ui-m1-10-api` | HTTP client, auth, zod wire contract |
| 11 | Task 11 | `crush/web-ui-m1-11-stream` | Stream client with reconnect and resubscribe |
| 12 | Task 12 | `crush/web-ui-m1-12-stores` | Observable stores and unread markers |
| 13 | Task 13 | `crush/web-ui-m1-13-layout` | Three-column frame and preview banner |
| 14 | Task 14 | `crush/web-ui-m1-14-boot` | Boot, auth and the unauthorized screen |
| 15 | Task 15 | `crush/web-ui-m1-15-sidebar` | Sidebar and session actions |
| 16 | Task 16 | `crush/web-ui-m1-16-chat` | Read-only chat with paging |
| 17 | Task 17 | `crush/web-ui-m1-17-e2e` | Playwright and axe |
| 18 | Task 18 | (no branch) | Whole-branch verification |

## Global Constraints

- Go floor `go 1.27.4`; `CGO_ENABLED=0` and `GOEXPERIMENT=greenteagc` (the
  `env` block of `Taskfile.yaml`).
- The security model does not change: loopback only, per-run launch token,
  `POST /api/auth`, bearer header, Host allow-list, Origin checks, the CSP
  (`default-src 'self'; style-src 'self'; connect-src 'self'`) and the
  launch file. `internal/web/auth_test.go` and `launch_test.go` stay.
- The only stream protocol change is the subprotocol value: `crush.v1`
  becomes `crush.v2`. The `crush.auth.<token>` entry is unchanged, and no
  frame carries a token.
- Frame shape: `{"seq":<uint64>,"type":"<type>","session_id":"<sid>","payload":<json>}`.
  `session_id` is `omitempty`, so a global frame is byte-identical to M0's.
  The payload envelope inside event-derived frames stays
  `{"type":"created|updated|deleted","payload":{…}}` (the `pubsub.Event`
  wrapper); gateway-originated frames carry their struct directly.
- Session status vocabulary is exactly `idle`, `running`, `queued`,
  `pending_interaction`. Precedence, highest first:
  `pending_interaction` (an open permission or question for the session),
  `running` (`AgentIsSessionBusy`), `queued` (a non-empty
  `AgentQueuedPromptsList`), `idle`.
- The gateway publishes `session_status` only when a session's status value
  changes; an unchanged value publishes nothing. There is no periodic status
  poll: the status is recomputed for the affected session on the events that
  can change it.
- Message paging: `GET /api/sessions/{sid}/messages?before=<id>&limit=<n>`
  answers `{"messages":[…],"has_more":bool}`, oldest first, default 100,
  maximum 500. `before` is exclusive.
- Frontend: React 18, react-dom 18, Vite 6, TypeScript strict with
  `noUnusedLocals` and `noUnusedParameters`, CSS Modules plus `clsx`, no
  component library, `zod` at the wire boundary. No `zustand`, no `redux`.
- Node comes from `web/.nvmrc` and is `26`; `web/package.json` keeps
  `"engines": {"node": ">=22"}`.
- Vite builds into `internal/web/dist` with `emptyOutDir: true`,
  `assetsInlineLimit: 0`, no source maps. The build output is committed and
  CI fails when it is stale.
- `web/index.html` carries no inline `<script>` and no `style="…"`: the CSP
  allows `self` only. Vite's injected module tag is external and fine.
- First-paint JavaScript stays under 600 KiB gzipped, enforced by
  `web/scripts/budget.mjs` in CI.
- DSH porting rule: every lifted file starts with a comment naming its DSH
  source path and the pinned commit
  `4b399c39a6d1479cd84ca591b4d7c88e24d38a96`. `web/THIRD_PARTY_NOTICES.md`
  carries the DSH MIT notice. Montserrat fonts are **not** copied: the font
  stack is the system stack from `base.css`.
- The pinned DSH clone for the copies is `/tmp/dsh-research/repo`; it is not
  part of the repository and every copied byte must land in `web/src/theme/`
  or `web/THIRD_PARTY_NOTICES.md`.
- Theme preference lives in `localStorage` under `crush.theme`
  (`light`, `dark`, `system`), and the resolved theme is the
  `data-ds-theme` attribute on `<html>`. Unread markers compare a session
  row's `updated_at` with a per-session last-seen `updated_at` kept in
  `localStorage` under `crush.seen`. A session is marked seen when it is
  selected or receives a frame while selected.
- The tab subscribes the selected session plus every session whose status is
  not `idle`, at most 32, and unsubscribes a background session when it
  returns to `idle` and is not the selected one.
- Auth: the token arrives in `location.hash` as `#t=<token>`, is exchanged
  with `POST /api/auth`, kept in `sessionStorage` under `crush.token`,
  stripped from the URL, sent as `Authorization: Bearer` on every fetch, and
  offered to the WebSocket as the `crush.auth.<token>` subprotocol beside
  `crush.v2`.
- The preview banner reads exactly `Preview: the new web UI is incomplete`.
- Comments: own line, capital letter, period, wrapped at 78 columns
  (`AGENTS.md`); log messages start with a capital letter. Format Go with
  `gofumpt -w .`. Commits are one line, imperative, at most 72 characters,
  semantic, with no attribution and no `Co-authored-by:` trailer.
- `gofumpt` is installed on this host; `task` and `golangci-lint` are not.
  Where a step says `task lint`, run it only if `command -v task` succeeds,
  and otherwise run the fallback the step names.

## Review Focus

1. A tab that subscribes while a turn is streaming: the `session_snapshot`
   must arrive before any live frame for that session, or a message is lost
   between the snapshot query and the subscription. Pinned by
   `TestSubscribeBuffersFramesPublishedDuringTheSnapshot` and
   `TestSubscribeSendsSnapshotBeforeLiveFrames` (Task 4).
2. Two tabs on different sessions at once: a `message` frame for session A
   must never reach the tab subscribed only to B, and a global
   `session_status` must reach both. Pinned by
   `TestPerSessionFramesReachOnlySubscribers` and
   `TestSessionStatusReachesEveryConnection` (Task 5).
3. A session whose run ends while the tab is in the background: the marker
   must clear without a reload. Pinned by
   `TestSessionStatusPublishesOnChangeOnly` and
   `TestStatusClearsWhenARunEnds` (Task 1).
4. A reconnect with `?since=` after the ring wrapped, or after the client
   held subscriptions: the tab must get a fresh bootstrap and fresh session
   snapshots, never a stale per-session frame from the ring. Pinned by
   `TestReplayDropsPerSessionFrames` (Task 5) and the 4001 case of
   `web/src/api/stream.test.ts` (Task 11).
5. A gateway frame type the frontend does not know: the zod boundary must
   reject it in CI rather than render an empty row at runtime. Pinned by
   `web/src/api/wire.golden.test.ts` over every entry of the golden file
   (Task 10), and by `TestFrameGolden` covering every type the gateway emits
   (Task 6).

---

## Task 1: Per-session status and the session_status frame

**Files:**
- Create: `internal/web/status.go`
- Create: `internal/web/status_test.go`
- Modify: `internal/web/stream.go` (frame constants)
- Modify: `internal/web/gateway.go` (`Gateway` struct, `Start`, `pump`)
- Modify: `internal/web/api.go` (`handleListSessions`, `handleClearQueue`)
- Modify: `internal/web/api_sessions.go` (`handleDeleteSession`)
- Modify: `internal/web/pending.go` (add `interacts`)
- Modify: `internal/web/webtest/fake.go` (add `SetSessionQueued`)

**Interfaces:**
- Consumes: `workspace.Workspace` methods `ListSessions`,
  `AgentIsSessionBusy(id string) bool`, `AgentQueuedPromptsList(id string)
  []string`; `pending` (`internal/web/pending.go`).
- Produces:
  - `const statusIdle = "idle"`, `statusRunning`, `statusQueued`,
    `statusPendingInteraction` (all `string`).
  - `func (g *Gateway) sessionStatus(sessionID string) string`
  - `func (g *Gateway) refreshSessionStatus(sessionID string)`
  - `func (g *Gateway) forgetSessionStatus(sessionID string)`
  - `func (g *Gateway) seedSessionStatuses(ctx context.Context)`
  - `func statusSessionID(ev any) string`
  - `type sessionRow struct { proto.Session; Status string }`
  - `func sortSessionsNewestFirst(list []session.Session)` (extracted from
    `handleListSessions`, which keeps its tie-break comment)
  - `func (g *Gateway) sessionRows(list []session.Session) []sessionRow`
  - `func (p *pending) interacts(sessionID string) bool`
  - `func (f *Fake) SetSessionQueued(id string, prompts []string)` in
    `internal/web/webtest`.
  - `frameSessionStatus = "session_status"` (Task 4 keeps it global).
  Task 5's tests and Task 8's e2e consume the status values; Task 12 and
  Task 15 consume the same vocabulary for the sidebar markers.

- [ ] **Step 1: Write the failing status unit test**

Create `internal/web/status_test.go`:

```go
package web

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/stretchr/testify/require"
)

// TestSessionStatusRanksPendingOverBusy pins the precedence the sidebar
// relies on: a session waiting for the person outranks one that is just
// working, which outranks one with a queued prompt.
func TestSessionStatusRanksPendingOverBusy(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	require.Equal(t, statusIdle, g.sessionStatus("s1"))

	fake.SetSessionBusy("s1", true)
	require.Equal(t, statusRunning, g.sessionStatus("s1"))

	fake.SetSessionQueued("s1", []string{"second prompt"})
	require.Equal(t, statusRunning, g.sessionStatus("s1"), "a busy session outranks its queue")

	g.pending.observe(permissionEvent("p1", "s1", "tc-1"))
	require.Equal(t, statusPendingInteraction, g.sessionStatus("s1"))

	g.pending.observe(permissionResolvedEvent("s1", "tc-1"))
	require.Equal(t, statusRunning, g.sessionStatus("s1"))

	fake.SetSessionBusy("s1", false)
	require.Equal(t, statusQueued, g.sessionStatus("s1"))

	fake.SetSessionQueued("s1", nil)
	require.Equal(t, statusIdle, g.sessionStatus("s1"))
}

// TestSessionStatusCountsAQuestion pins the other interaction source.
func TestSessionStatusCountsAQuestion(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	g.pending.observe(questionEvent("b1", "s2"))
	require.Equal(t, statusPendingInteraction, g.sessionStatus("s2"))
	require.Equal(t, statusIdle, g.sessionStatus("s1"))
}

// TestSessionStatusPublishesOnChangeOnly pins the dedupe: recomputing an
// unchanged status publishes nothing, so a reconnect does not replay a burst
// of unchanged values.
func TestSessionStatusPublishesOnChangeOnly(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	g.seedSessionStatuses(t.Context())

	g.refreshSessionStatus("s1")
	require.Empty(t, statusFrames(g), "the seeded status did not change")

	fake.SetSessionBusy("s1", true)
	g.refreshSessionStatus("s1")
	require.Equal(t, []string{`{"session_id":"s1","status":"running"}`}, statusFrames(g))

	g.refreshSessionStatus("s1")
	require.Len(t, statusFrames(g), 1, "an unchanged status publishes nothing")

	fake.SetSessionBusy("s1", false)
	g.refreshSessionStatus("s1")
	require.Equal(t, []string{
		`{"session_id":"s1","status":"running"}`,
		`{"session_id":"s1","status":"idle"}`,
	}, statusFrames(g))
}

// TestStatusSessionID pins which events can change a status, and for which
// session: the pump recomputes only that session, with no periodic poll.
func TestStatusSessionID(t *testing.T) {
	t.Parallel()
	require.Equal(t, "s2", statusSessionID(permissionEvent("p1", "s2", "tc-1")))
	require.Equal(t, "s2", statusSessionID(permissionResolvedEvent("s2", "tc-1")))
	require.Equal(t, "s3", statusSessionID(questionEvent("b1", "s3")))
	require.Equal(t, "s4", statusSessionID(pubsub.Event[question.Notification]{
		Type: pubsub.UpdatedEvent, Payload: question.Notification{BatchID: "b1", SessionID: "s4"},
	}))
	require.Equal(t, "s1", statusSessionID(runCompleteEvent("s1")))
	require.Equal(t, "s1", statusSessionID(pubsub.Event[message.Message]{
		Type: pubsub.CreatedEvent, Payload: message.Message{SessionID: "s1", Role: message.User},
	}))
	require.Empty(t, statusSessionID(pubsub.Event[message.Message]{
		Type: pubsub.UpdatedEvent, Payload: message.Message{SessionID: "s1", Role: message.Assistant},
	}), "a streamed assistant chunk changes no status")
	require.Empty(t, statusSessionID(pubsub.Event[history.File]{Type: pubsub.CreatedEvent}))
}

// TestStatusClearsWhenARunEnds pins the event path: the run_complete event
// the workspace emits clears the marker, with no manual refresh and no
// periodic poll anywhere in the gateway.
func TestStatusClearsWhenARunEnds(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.SetSessionBusy("s1", true)
	g.refreshSessionStatus("s1")
	require.Equal(t, []string{`{"session_id":"s1","status":"running"}`}, statusFrames(g))

	fake.SetSessionBusy("s1", false)
	fake.Emit(runCompleteEvent("s1"))
	require.Eventually(t, func() bool {
		return len(statusFrames(g)) == 2
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, `{"session_id":"s1","status":"idle"}`, statusFrames(g)[1])
}

// statusFrames returns every session_status payload in the replay ring,
// oldest first, as JSON text.
func statusFrames(g *Gateway) []string {
	var out []string
	for _, f := range g.hub.frames() {
		if f.Type == frameSessionStatus {
			out = append(out, string(f.Payload))
		}
	}
	return out
}
```

Add the three event builders to the same file, below `statusFrames`:

```go
func permissionEvent(id, sessionID, toolCallID string) any {
	return pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
		ID: id, SessionID: sessionID, ToolCallID: toolCallID, ToolName: "bash", Description: "Run tests",
	}}
}

func permissionResolvedEvent(sessionID, toolCallID string) any {
	return pubsub.Event[permission.PermissionNotification]{Type: pubsub.UpdatedEvent, Payload: permission.PermissionNotification{
		SessionID: sessionID, ToolCallID: toolCallID, Granted: true,
	}}
}

func questionEvent(batchID, sessionID string) any {
	return pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: question.Request{
		ID: batchID, SessionID: sessionID,
		Questions: []question.Question{{ID: "q1", Type: question.TypeSingleChoice, Text: "Which?"}},
	}}
}

func runCompleteEvent(sessionID string) any {
	return pubsub.Event[notify.RunComplete]{Type: pubsub.UpdatedEvent, Payload: notify.RunComplete{
		SessionID: sessionID, RunID: "r1", MessageID: "a1", Text: "done",
	}}
}
```

The import block at the top of the file already carries `notify`, `history`,
`message`, `pubsub` and `question`; if `goimports` drops one, it is unused.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/web/ -run 'TestSessionStatus|TestStatusClears' -count=1`
Expected: FAIL to build, with `undefined: statusIdle`, `undefined:
permissionEvent` and `g.sessionStatus undefined`. The event builders are
new test helpers, so the build failure is the RED for the whole task.

- [ ] **Step 3: Write internal/web/status.go**

```go
package web

import (
	"cmp"
	"context"
	"log/slog"
	"slices"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/wire"
)

// Session status values the sidebar renders. A session waiting for the
// person outranks one that is merely running: the first needs an answer,
// the second only needs time.
const (
	statusIdle               = "idle"
	statusRunning            = "running"
	statusQueued             = "queued"
	statusPendingInteraction = "pending_interaction"
)

// sessionStatus computes the live status of one session from the same
// sources the terminal reads, so a sidebar marker cannot disagree with what
// the TUI shows.
func (g *Gateway) sessionStatus(sessionID string) string {
	switch {
	case g.pending.interacts(sessionID):
		return statusPendingInteraction
	case g.ws.AgentIsSessionBusy(sessionID):
		return statusRunning
	case len(g.ws.AgentQueuedPromptsList(sessionID)) > 0:
		return statusQueued
	default:
		return statusIdle
	}
}

// sessionRow is one session as the sidebar lists it: the wire session
// entity with the live status beside it.
type sessionRow struct {
	proto.Session
	Status string `json:"status"`
}

// sortSessionsNewestFirst orders a session list the way the sidebar and the
// session switcher show it: newest updated first, the ID breaking ties so two
// sessions saved in the same second keep a stable order.
func sortSessionsNewestFirst(list []session.Session) {
	slices.SortFunc(list, func(a, b session.Session) int {
		return cmp.Or(cmp.Compare(b.UpdatedAt, a.UpdatedAt), cmp.Compare(a.ID, b.ID))
	})
}

// sessionRows turns a session list into the rows the sidebar renders,
// keeping the caller's order.
func (g *Gateway) sessionRows(list []session.Session) []sessionRow {
	rows := make([]sessionRow, 0, len(list))
	for _, s := range list {
		rows = append(rows, sessionRow{
			Session: wire.SessionToProto(s),
			Status:  g.sessionStatus(s.ID),
		})
	}
	return rows
}

// seedSessionStatuses records every session's status without publishing, so
// the first event announces only what really changed. It is the gateway's
// one session list read for status; everything after it is per-session.
func (g *Gateway) seedSessionStatuses(ctx context.Context) {
	sessions, err := g.ws.ListSessions(ctx)
	if err != nil {
		slog.Debug("Web session status seed could not list sessions", "error", err)
		return
	}
	g.statusMu.Lock()
	defer g.statusMu.Unlock()
	for _, s := range sessions {
		g.statuses[s.ID] = g.sessionStatus(s.ID)
	}
}

// refreshSessionStatus recomputes one session's status and publishes a frame
// only when the value changed. The read and the publish happen under
// statusMu, so two refreshes cannot publish an older status after a newer
// one. The pump calls it for the session an event names, which is what keeps
// the gateway off a periodic poll.
func (g *Gateway) refreshSessionStatus(sessionID string) {
	g.statusMu.Lock()
	defer g.statusMu.Unlock()
	status := g.sessionStatus(sessionID)
	if last, ok := g.statuses[sessionID]; ok && last == status {
		return
	}
	g.statuses[sessionID] = status
	g.hub.publishJSON(frameSessionStatus, map[string]string{
		"session_id": sessionID,
		"status":     status,
	})
}

// forgetSessionStatus drops a deleted session from the map, so a later
// session that reuses the id starts from a clean slate.
func (g *Gateway) forgetSessionStatus(sessionID string) {
	g.statusMu.Lock()
	defer g.statusMu.Unlock()
	delete(g.statuses, sessionID)
}

// statusSessionID returns the session whose status ev can change: a
// permission or question arriving or resolving, a run ending, or a user
// prompt starting a run or joining the queue. Every other event answers "",
// including the streamed assistant chunks that arrive many times a second.
func statusSessionID(ev any) string {
	switch e := ev.(type) {
	case pubsub.Event[permission.PermissionRequest]:
		return e.Payload.SessionID
	case pubsub.Event[permission.PermissionNotification]:
		return e.Payload.SessionID
	case pubsub.Event[question.Request]:
		return e.Payload.SessionID
	case pubsub.Event[question.Notification]:
		return e.Payload.SessionID
	case pubsub.Event[notify.RunComplete]:
		return e.Payload.SessionID
	case pubsub.Event[message.Message]:
		if e.Payload.Role == message.User {
			return e.Payload.SessionID
		}
	}
	return ""
}
```

- [ ] **Step 4: Add the frame constant**

In `internal/web/stream.go`, add to the `const` block, after
`frameSessionSnapshot` exists in Task 4 (until then, after `frameSignIn`):

```go
	frameSessionStatus  = "session_status"
```

- [ ] **Step 5: Add the gateway state and the event hook**

In `internal/web/gateway.go`, add to the `Gateway` struct next to `modelMu`:

```go
	// statusMu guards statuses, the last status published per session, so a
	// refresh announces a value only when it really changed.
	statusMu sync.Mutex
	statuses map[string]string
```

In `Start`, initialize the map in the struct literal (beside
`apiRoutes:`):

```go
		statuses: make(map[string]string),
```

and after `g.srv = &http.Server{…}` and before the team watcher seed, add:

```go
	g.seedSessionStatuses(ctx)
```

In `pump`, leave the follow ticker alone and add one line after the
`if g.pending.observe(ev) { g.publishSecurePrompts() }` block:

```go
			if sessionID := statusSessionID(ev); sessionID != "" {
				g.refreshSessionStatus(sessionID)
			}
```

That is the whole status path: no `ListSessions` call and no periodic sweep,
so the gateway costs nothing while a session sits idle. The ticker case stays
exactly as it is today:

```go
		case <-ticker.C:
			if cur := g.ws.CurrentSessionID(); cur != last {
				last = cur
				g.hub.publishJSON(frameFollow, map[string]string{"session_id": cur})
			}
```

`statusSessionID` lives in `status.go` (Step 3), so `gateway.go` needs no
new import for it. `permission`, `question`, `pubsub`, `notify` and
`message` are imported by `status.go`, not here.

- [ ] **Step 6: Add pending.interacts**

Append to `internal/web/pending.go`:

```go
// interacts reports whether a permission or a question for sessionID still
// waits for an answer.
func (p *pending) interacts(sessionID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, req := range p.perms {
		if req.SessionID == sessionID {
			return true
		}
	}
	for _, batch := range p.qs {
		if batch.SessionID == sessionID {
			return true
		}
	}
	return false
}
```

- [ ] **Step 7: Serve the status and refresh it where no event fires**

In `handleListSessions` (`internal/web/api.go`), replace the inline
`slices.SortFunc` block with the shared helper and answer the rows:

```go
	sortSessionsNewestFirst(list)
	writeJSON(w, http.StatusOK, g.sessionRows(list))
```

Delete the now-unused `slices` and `cmp` imports from `api.go` if nothing
else in the file uses them (`go vet` names the leftover).

Two routes change a session's inputs without an event the pump sees, so they
refresh it themselves:

1. `handleClearQueue` in `internal/web/api.go`: after
   `g.ws.AgentClearQueue(sid)` succeeds and before it answers, add

```go
	g.refreshSessionStatus(sid)
```

2. `handleDeleteSession` in `internal/web/api_sessions.go`: after
   `g.ws.DeleteSession` succeeds and before `w.WriteHeader(http.StatusNoContent)`,
   add

```go
	g.forgetSessionStatus(sid)
```

A deleted session publishes no status frame: the `session` deleted event
already removes the row.

- [ ] **Step 8: Add the fake setter**

Append to `internal/web/webtest/fake.go`, next to `SetSessionBusy`:

```go
// SetSessionQueued makes AgentQueuedPromptsList report prompts for id, as a
// tab that queued a second prompt behind a running turn would. An empty
// slice clears the queue.
func (f *Fake) SetSessionQueued(id string, prompts []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(prompts) == 0 {
		delete(f.queued, id)
		return
	}
	f.queued[id] = append([]string(nil), prompts...)
}
```

- [ ] **Step 9: Add the route-level test for the status field**

Append to `internal/web/status_test.go`:

```go
// TestSessionsListCarriesStatus pins the row the sidebar renders.
func TestSessionsListCarriesStatus(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.SetSessionBusy("s1", true)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/sessions", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var rows []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(body, &rows))
	require.Len(t, rows, 1)
	require.Equal(t, "s1", rows[0].ID)
	require.Equal(t, statusRunning, rows[0].Status)
}
```

Add the imports `"encoding/json"` and `"net/http"` to the test file.

- [ ] **Step 10: Run the task's tests, then the package**

Run: `go test ./internal/web/ -run 'TestSessionStatus|TestStatusClears|TestSessionsListCarriesStatus' -count=1`
Expected: PASS.

Run: `go test ./internal/web/... ./internal/wire/... -count=1`
Expected: PASS. `TestListSessions` in `api_test.go` still passes: it reads
the row as `map[string]any` and checks `id`.

- [ ] **Step 11: Format and commit**

```bash
gofumpt -w internal/web
go vet ./internal/web/...
git add internal/web
git commit -m "feat(web): publish per-session status frames"
```

---

## Task 2: Message paging with ?before and ?limit

**Files:**
- Modify: `internal/web/api.go` (`handleMessages`, new helpers and
  `messagesResponse`)
- Create: `internal/web/messages_test.go`
- Modify: `internal/web/api_test.go` (`TestSessionsAndMessages`)

**Interfaces:**
- Consumes: `workspace.Workspace.ListMessages(ctx, sid) ([]message.Message,
  error)`, which is oldest first (`ORDER BY created_at ASC, rowid ASC` in
  `internal/db/sql/messages.sql`); `entities[T]` and `writeError` in
  `internal/web/encode.go` and `errors.go`.
- Produces:
  - `const messagePageSize = 100`, `const maxMessagePageSize = 500`.
  - `func pageMessages(msgs []message.Message, before string, limit int) ([]message.Message, bool, error)`
  - `func messageLimit(w http.ResponseWriter, r *http.Request) (int, bool)`
  - `type messagesResponse struct { Messages []json.RawMessage; HasMore bool }`
    with JSON `{"messages":…,"has_more":…}`.
  - `var errUnknownMessage = errors.New("web: unknown message id")`.
  Task 3 consumes `pageMessages` and `messagePageSize`.

- [ ] **Step 1: Write the failing paging tests**

Create `internal/web/messages_test.go`:

```go
package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// TestPageMessagesWalksBackwards pins the page boundaries: the newest page
// first, then older pages ending before the id the client already has.
func TestPageMessagesWalksBackwards(t *testing.T) {
	t.Parallel()
	msgs := messagesWithIDs("m1", "m2", "m3", "m4", "m5")

	page, hasMore, err := pageMessages(msgs, "", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m4", "m5"}, messageIDs(page))
	require.True(t, hasMore)

	page, hasMore, err = pageMessages(msgs, "m4", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m2", "m3"}, messageIDs(page))
	require.True(t, hasMore)

	page, hasMore, err = pageMessages(msgs, "m2", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"m1"}, messageIDs(page))
	require.False(t, hasMore)

	page, hasMore, err = pageMessages(msgs, "m1", 2)
	require.NoError(t, err)
	require.Empty(t, page)
	require.False(t, hasMore)
}

// TestPageMessagesUnknownID pins the refusal: an id the session does not
// have is an error, never a silent jump to the newest page.
func TestPageMessagesUnknownID(t *testing.T) {
	t.Parallel()
	_, _, err := pageMessages(messagesWithIDs("m1", "m2"), "nope", 10)
	require.ErrorIs(t, err, errUnknownMessage)
}

// TestMessagesRouteAnswersAPage pins the envelope every client decodes.
func TestMessagesRouteAnswersAPage(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for _, id := range []string{"m1", "m2", "m3"} {
		fake.AddMessage(textMessage(id, "s1", "hello "+id))
	}
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages?limit=2", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var page messagesResponse
	require.NoError(t, json.Unmarshal(body, &page))
	require.Len(t, page.Messages, 2)
	require.True(t, page.HasMore)
	var first struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(page.Messages[0], &first))
	require.Equal(t, "m2", first.ID)

	rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages?before=m2&limit=2", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.NoError(t, json.Unmarshal(body, &page))
	require.Len(t, page.Messages, 1)
	require.False(t, page.HasMore)
	require.NoError(t, json.Unmarshal(page.Messages[0], &first))
	require.Equal(t, "m1", first.ID)
}

// TestMessagesRouteRefusesABadPage pins the two refusals.
func TestMessagesRouteRefusesABadPage(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddMessage(textMessage("m1", "s1", "hi"))
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages?before=nope", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_message")

	for _, query := range []string{"limit=0", "limit=-3", "limit=ten"} {
		rsp, body = doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages?"+query, "")
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, query)
		require.Contains(t, string(body), "bad_limit", query)
	}
}

// TestMessagesRouteClampsThePage pins the cap: an oversized limit is
// clamped, never a refusal, so a client cannot ask for unbounded history.
func TestMessagesRouteClampsThePage(t *testing.T) {
	t.Parallel()
	require.Equal(t, maxMessagePageSize, clampMessageLimit(10_000))
	require.Equal(t, 7, clampMessageLimit(7))
}

func messagesWithIDs(ids ...string) []message.Message {
	out := make([]message.Message, 0, len(ids))
	for _, id := range ids {
		out = append(out, textMessage(id, "s1", id))
	}
	return out
}

func textMessage(id, sessionID, text string) message.Message {
	return message.Message{
		ID: id, SessionID: sessionID, Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}
}

func messageIDs(msgs []message.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/web/ -run 'TestPageMessages|TestMessagesRoute' -count=1`
Expected: FAIL to build with `undefined: pageMessages`, `undefined:
messagesResponse` and `undefined: clampMessageLimit`.

- [ ] **Step 3: Add the paging helpers**

In `internal/web/api.go`, add above `handleMessages`:

```go
// messagePageSize is the page a tab loads first, and maxMessagePageSize the
// most a caller may ask for.
const (
	messagePageSize    = 100
	maxMessagePageSize = 500
)

// errUnknownMessage reports a ?before id the session does not have.
var errUnknownMessage = errors.New("web: unknown message id")

// pageMessages returns the page of msgs that ends just before before, oldest
// first, and whether older messages remain. msgs is oldest first. An empty
// before asks for the newest page.
func pageMessages(msgs []message.Message, before string, limit int) ([]message.Message, bool, error) {
	end := len(msgs)
	if before != "" {
		index := -1
		for i, m := range msgs {
			if m.ID == before {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, false, errUnknownMessage
		}
		end = index
	}
	start := max(0, end-limit)
	return msgs[start:end], start > 0, nil
}

// clampMessageLimit keeps a request inside the page window.
func clampMessageLimit(limit int) int {
	return min(limit, maxMessagePageSize)
}

// messageLimit reads the limit query parameter: absent means the default
// page, a value below one is refused, and anything above the cap clamps.
func messageLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return messagePageSize, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		writeError(w, http.StatusBadRequest, "bad_limit", "limit must be a positive integer")
		return 0, false
	}
	return clampMessageLimit(n), true
}

// messagesResponse is one page of a session's messages.
type messagesResponse struct {
	Messages []json.RawMessage `json:"messages"`
	HasMore  bool              `json:"has_more"`
}
```

Add `"strconv"` to the import block of `api.go`.

- [ ] **Step 4: Page the messages route**

Replace the tail of `handleMessages` in `internal/web/api.go` (after the
`GetSession` check) with:

```go
	limit, ok := messageLimit(w, r)
	if !ok {
		return
	}
	msgs, err := g.ws.ListMessages(r.Context(), sid)
	if err != nil {
		writeInternal(w, "list_messages", err)
		return
	}
	page, hasMore, err := pageMessages(msgs, r.URL.Query().Get("before"), limit)
	if errors.Is(err, errUnknownMessage) {
		writeError(w, http.StatusNotFound, "unknown_message", "no message with that id in this session")
		return
	}
	writeJSON(w, http.StatusOK, messagesResponse{Messages: entities(page), HasMore: hasMore})
```

- [ ] **Step 5: Run the paging tests**

Run: `go test ./internal/web/ -run 'TestPageMessages|TestMessagesRoute' -count=1`
Expected: PASS.

- [ ] **Step 6: Adapt the existing messages test**

In `internal/web/api_test.go`, replace the body of `TestSessionsAndMessages`
between the request and the create-session half with:

```go
	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/messages", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var page messagesResponse
	require.NoError(t, json.Unmarshal(body, &page))
	require.Len(t, page.Messages, 1)
	var first struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(page.Messages[0], &first))
	require.Equal(t, "m1", first.ID)
	require.False(t, page.HasMore)
```

- [ ] **Step 7: Run the package**

Run: `go test ./internal/web/... -count=1`
Expected: PASS.

- [ ] **Step 8: Format and commit**

```bash
gofumpt -w internal/web
go vet ./internal/web/...
git add internal/web
git commit -m "feat(web): page session messages with before and limit"
```

---

## Task 3: GET /api/bootstrap and the session-snapshot split

**Files:**
- Rename: `internal/web/snapshot.go` → `internal/web/model.go`
- Create: `internal/web/bootstrap.go`
- Create: `internal/web/bootstrap_test.go`
- Modify: `internal/web/api.go` (`registerAPI`, new `handleBootstrap`)
- Modify: `internal/web/stream.go` (`frameSnapshot` → `frameBootstrap`,
  first frame built by `buildBootstrap`)
- Test: `internal/web/api_test.go`, `internal/web/controller_test.go`,
  `internal/web/stream_test.go`, `internal/web/api_models_test.go`,
  `internal/web/api_prompts_test.go`, `internal/web/api_signin_test.go`,
  `internal/web/team_test.go`, `internal/web/seed_m3_test.go`

**Interfaces:**
- Consumes: `pageMessages`, `messagePageSize` (Task 2); `g.sessionRows` and
  `sortSessionsNewestFirst` (Task 1); `g.team.current()`, `g.signInState()`,
  `g.pending.*`,
  `modelInfo(g, ready bool)`, `g.fileSummaries(ctx, sid)`,
  `innerPayload`, `entities`.
- Produces:
  - `type Bootstrap struct { … }` with the JSON fields listed in the step.
  - `type SessionSnapshot struct { SessionID, Session, Messages, HasMore,
    Files, Busy, Queued }`.
  - `func (g *Gateway) buildBootstrap(ctx context.Context) Bootstrap`
  - `func (g *Gateway) buildSessionSnapshot(ctx context.Context, sessionID, before string, limit int) SessionSnapshot`
  - `GET /api/bootstrap` answering a `Bootstrap`.
  - `frameBootstrap = "bootstrap"`: the stream's first frame.
  - Test helpers `bootstrapOf`, `bootstrapJSON`, `sessionSnapshotOf` in
    `internal/web/api_test.go`. Task 10's frontend schemas mirror these two
    structs.

- [ ] **Step 1: Write the failing bootstrap tests**

Create `internal/web/bootstrap_test.go`:

```go
package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// TestBootstrapCarriesGlobalState pins the payload a shell renders before it
// has subscribed to anything.
func TestBootstrapCarriesGlobalState(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddSession(session.Session{ID: "s2", Title: "Second", UpdatedAt: 10})
	fake.SetSessionBusy("s2", true)
	fake.LSP = map[string]workspace.LSPClientInfo{
		"gopls": {Name: "gopls", State: lsp.StateReady, DiagnosticCount: 2},
	}
	fake.MCP = map[string]mcp.ClientInfo{
		"fs": {Name: "fs", State: mcp.StateConnected, Counts: mcp.Counts{Tools: 3}},
	}
	// The pending store is filled directly: the pump goroutine owns the
	// event path and this test must not race it.
	g.pending.observe(questionEvent("b1", "s2"))

	b := g.buildBootstrap(t.Context())
	require.NotEmpty(t, b.Version)
	require.Equal(t, "/tmp/project", b.WorkingDir)
	require.Equal(t, "main", b.Branch)
	require.Equal(t, "s1", b.CurrentSessionID)
	require.Equal(t, "code", b.InputMode)
	require.True(t, b.AgentReady)
	require.Equal(t, ServerInfo{State: strconv.Itoa(int(lsp.StateReady)), DiagnosticCount: 2}, b.LSP["gopls"])
	require.Equal(t, ServerInfo{State: "connected", ToolCount: 3}, b.MCP["fs"])
	require.Len(t, b.Sessions, 2)
	statuses := map[string]string{}
	for _, row := range b.Sessions {
		statuses[row.ID] = row.Status
	}
	require.Equal(t, map[string]string{"s1": statusIdle, "s2": statusPendingInteraction}, statuses,
		"the open question outranks the busy run it waits on")
}

// TestBootstrapRouteServesIt pins the endpoint the shell loads with.
func TestBootstrapRouteServesIt(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/bootstrap", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var b Bootstrap
	require.NoError(t, json.Unmarshal(body, &b))
	require.Equal(t, "s1", b.CurrentSessionID)
	require.Len(t, b.Sessions, 1)
	require.Equal(t, "s1", b.Sessions[0].ID)
	require.Equal(t, statusIdle, b.Sessions[0].Status)
}

// TestPayloadsHaveNoNullArrays pins the contract the frontend's zod schema
// reads: a workspace with no runs, no roster and no interactions must still
// answer arrays, not nulls.
func TestPayloadsHaveNoNullArrays(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	for name, v := range map[string]any{
		"bootstrap":       g.buildBootstrap(t.Context()),
		"sessionSnapshot": g.buildSessionSnapshot(t.Context(), "s1", "", messagePageSize),
	} {
		raw, err := json.Marshal(v)
		require.NoError(t, err, name)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields), name)
		for key, value := range fields {
			require.NotEqual(t, "null", string(value), "%s.%s is null", name, key)
		}
	}
}

// TestSessionSnapshotCarriesOneSession pins the per-session payload.
func TestSessionSnapshotCarriesOneSession(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for _, id := range []string{"m1", "m2"} {
		fake.AddMessage(textMessage(id, "s1", "hello "+id))
	}
	fake.SetSessionBusy("s1", true)
	fake.SetSessionQueued("s1", []string{"later"})
	fake.AddHistory(historyFile("h1", "s1", "/tmp/project/main.go", 1))

	s := g.buildSessionSnapshot(t.Context(), "s1", "", messagePageSize)
	require.Equal(t, "s1", s.SessionID)
	require.NotEmpty(t, s.Session)
	require.Len(t, s.Messages, 2)
	require.False(t, s.HasMore)
	require.Len(t, s.Files, 1)
	require.Equal(t, "/tmp/project/main.go", s.Files[0].Path)
	require.True(t, s.Busy)
	require.Equal(t, []string{"later"}, s.Queued)
}

// TestSessionSnapshotPagesAndFlagsMore pins the paging the load-older button
// reads.
func TestSessionSnapshotPagesAndFlagsMore(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for i := range 5 {
		fake.AddMessage(textMessage("m"+strconv.Itoa(i), "s1", "hello"))
	}
	s := g.buildSessionSnapshot(t.Context(), "s1", "", 2)
	require.Len(t, s.Messages, 2)
	require.True(t, s.HasMore)
}

// TestSessionSnapshotForAnUnknownSession pins the degraded answer: the
// shell shows an empty session instead of failing the subscription.
func TestSessionSnapshotForAnUnknownSession(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	s := g.buildSessionSnapshot(t.Context(), "nope", "", messagePageSize)
	require.Empty(t, s.Session)
	require.Empty(t, s.Messages)
	require.False(t, s.HasMore)
}
```

Add the one helper this file needs to the same file (`textMessage` comes
from Task 2 and `questionEvent` from Task 1):

```go
func historyFile(id, sessionID, path string, version int64) history.File {
	return history.File{ID: id, SessionID: sessionID, Path: path, Content: "package main", Version: version}
}
```

with the imports `"github.com/charmbracelet/crush/internal/history"` and
`"github.com/charmbracelet/crush/internal/workspace"`. `textMessage` and
`questionEvent` come from Task 2 and Task 1.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/web/ -run 'TestBootstrap|TestSessionSnapshot' -count=1`
Expected: FAIL to build with `undefined: Bootstrap`, `undefined:
SessionSnapshot`, `g.buildBootstrap undefined` and
`g.buildSessionSnapshot undefined`.

- [ ] **Step 3: Split the old snapshot file**

```bash
git mv internal/web/snapshot.go internal/web/model.go
```

In `internal/web/model.go`, delete the `Snapshot` type and the
`buildSnapshot` function. Keep `ModelRef`, `ModelInfo`, `ServerInfo`,
`errText`, `modelInfo`, `seedModel`, `watchModelLoop` and `watchModel`
unchanged, and fix the file's package comment if any. Remove imports the
deletion leaves unused (`agentrun`, `config` if unused, `proto`, `pubsub`,
`question`, `session`, `version`, `workspace`): the compiler names each one;
`go vet ./internal/web/` must end clean.

- [ ] **Step 4: Create internal/web/bootstrap.go**

```go
package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/version"
)

// Bootstrap is the global state a shell renders before it subscribes to any
// session: what the gateway is, what the agent runs, and the interactions
// that wait for the person.
type Bootstrap struct {
	Version            string                `json:"version"`
	WorkingDir         string                `json:"working_dir"`
	Branch             string                `json:"branch,omitempty"`
	CurrentSessionID   string                `json:"current_session_id"`
	Sessions           []sessionRow          `json:"sessions"`
	Model              ModelInfo             `json:"model"`
	AgentReady         bool                  `json:"agent_ready"`
	PermissionMode     string                `json:"permission_mode"`
	InputMode          string                `json:"input_mode"`
	PlanReadySessionID string                `json:"plan_ready_session_id,omitempty"`
	LSP                map[string]ServerInfo `json:"lsp"`
	MCP                map[string]ServerInfo `json:"mcp"`
	Team               TeamState             `json:"team"`
	AgentRuns          []agentrun.Run        `json:"agent_runs"`
	Permissions        []json.RawMessage     `json:"permissions"`
	Questions          []json.RawMessage     `json:"questions,omitempty"`
	SecurePrompts      []SecurePrompt        `json:"secure_prompts,omitempty"`
	SignIn             *SignInState          `json:"signin,omitempty"`
	AWSSSO             *AWSSSO               `json:"aws_sso,omitempty"`
}

// SessionSnapshot is the state of one subscribed session: the session, its
// latest page of messages, the edited files, and whether a run is in flight
// or queued.
type SessionSnapshot struct {
	SessionID string            `json:"session_id"`
	Session   json.RawMessage   `json:"session,omitempty"`
	Messages  []json.RawMessage `json:"messages"`
	HasMore   bool              `json:"has_more"`
	Files     []FileSummary     `json:"files"`
	Busy      bool              `json:"busy"`
	Queued    []string          `json:"queued"`
}

// buildBootstrap reads the global half of the old snapshot, plus the session
// list with its live statuses.
func (g *Gateway) buildBootstrap(ctx context.Context) Bootstrap {
	b := Bootstrap{
		Version:            version.Version,
		WorkingDir:         g.ws.WorkingDir(),
		CurrentSessionID:   g.ws.CurrentSessionID(),
		Sessions:           []sessionRow{},
		Permissions:        []json.RawMessage{},
		LSP:                map[string]ServerInfo{},
		MCP:                map[string]ServerInfo{},
		PermissionMode:     string(proto.PermissionModeToProto(g.ws.PermissionMode())),
		InputMode:          g.InputMode(),
		PlanReadySessionID: g.PlanReadySessionID(),
	}
	// One readiness call feeds both fields, so a tab cannot read the agent
	// as ready while the model says otherwise.
	readyErr := g.ws.AgentReadyErr()
	b.Model = modelInfo(g, readyErr == nil)
	b.AgentReady = b.Model.AgentReady
	// Every array in the payload is non-nil: the frontend's zod schema reads
	// a null array as a contract break, and the workspace returns nil slices
	// when it has no runs, no roster or no questions.
	b.Team, b.AgentRuns = g.team.current()
	if b.AgentRuns == nil {
		b.AgentRuns = []agentrun.Run{}
	}
	if b.Team.Members == nil {
		b.Team.Members = []agentteam.Member{}
	}
	if b.Team.Tasks == nil {
		b.Team.Tasks = []TeamTask{}
	}
	if branch, err := g.ws.GitBranch(ctx); err == nil {
		b.Branch = branch
	}
	if list, err := g.ws.ListSessions(ctx); err == nil {
		sortSessionsNewestFirst(list)
		b.Sessions = g.sessionRows(list)
	} else {
		slog.Debug("Web bootstrap could not list sessions", "error", err)
	}
	for name, info := range g.ws.LSPGetStates() {
		b.LSP[name] = ServerInfo{
			State:           strconv.Itoa(int(info.State)),
			DiagnosticCount: info.DiagnosticCount,
			Error:           errText(info.Error),
		}
	}
	for name, info := range g.ws.MCPGetStates() {
		b.MCP[name] = ServerInfo{
			State:     info.State.String(),
			ToolCount: info.Counts.Tools,
			Error:     errText(info.Error),
		}
	}
	b.Permissions = entities(g.pending.permissions())
	b.Questions = []json.RawMessage{}
	if prompts := g.pending.securePrompts(); len(prompts) > 0 {
		b.SecurePrompts = prompts
	}
	b.SignIn = g.signInState()
	b.AWSSSO = g.pending.awsSSO()
	for _, q := range g.pending.questions() {
		b.Questions = append(b.Questions, innerPayload(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: q}))
	}
	return b
}

// buildSessionSnapshot reads one session's page of state. A session the
// workspace no longer has answers an empty snapshot rather than an error:
// the tab can then forget it instead of reconnecting.
func (g *Gateway) buildSessionSnapshot(ctx context.Context, sessionID, before string, limit int) SessionSnapshot {
	s := SessionSnapshot{
		SessionID: sessionID,
		Messages:  []json.RawMessage{},
		Files:     []FileSummary{},
		Queued:    []string{},
	}
	if sess, err := g.ws.GetSession(ctx, sessionID); err == nil {
		s.Session = innerPayload(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: sess})
	} else {
		slog.Debug("Web session snapshot could not load session", "session_id", sessionID, "error", err)
	}
	if msgs, err := g.ws.ListMessages(ctx, sessionID); err == nil {
		if page, hasMore, err := pageMessages(msgs, before, limit); err == nil {
			s.Messages = entities(page)
			s.HasMore = hasMore
		}
	} else {
		slog.Debug("Web session snapshot could not list messages", "session_id", sessionID, "error", err)
	}
	if files, err := g.fileSummaries(ctx, sessionID); err == nil {
		s.Files = files
	} else {
		slog.Debug("Web session snapshot could not list edited files", "session_id", sessionID, "error", err)
	}
	s.Busy = g.ws.AgentIsSessionBusy(sessionID)
	if q := g.ws.AgentQueuedPromptsList(sessionID); q != nil {
		s.Queued = q
	}
	return s
}
```

- [ ] **Step 5: Add the route**

In `internal/web/api.go`'s `registerAPI`, add beside the health route:

```go
	g.route(api, "GET /api/bootstrap", g.handleBootstrap)
```

and in `internal/web/bootstrap.go`:

```go
// handleBootstrap answers the global state a shell renders before it
// subscribes to a session.
func (g *Gateway) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.buildBootstrap(r.Context()))
}
```

The `route` call registers the pattern, so
`TestEveryRouteRequiresTheToken` covers the new endpoint with no list
change.

- [ ] **Step 6: Send the bootstrap as the stream's first frame**

In `internal/web/stream.go`:

1. Rename the constant `frameSnapshot` to `frameBootstrap` and set it to
   `"bootstrap"`.
2. In `handleStream`, replace the snapshot block's marshal with:

```go
	if needBootstrap {
		raw, err := json.Marshal(g.buildBootstrap(ctx))
		if err != nil {
			return
		}
		if !writeFrame(ctx, ws, frame{Seq: head, Type: frameBootstrap, Payload: raw}) {
			return
		}
	}
```

(`needSnapshot` is renamed `needBootstrap`; `hub.attach`'s third return
value keeps its meaning. Rename the local in `handleStream` and in the
`attach` doc comment, and leave the hub's internal flag name alone.)

3. Update every reference: `grep -rn 'frameSnapshot' internal/web` must
   print nothing.

- [ ] **Step 7: Rewrite the test helpers**

In `internal/web/api_test.go`, replace `snapshotOf` and `snapshotJSON` with:

```go
// bootstrapOf builds the payload the gateway sends as the first frame of a
// stream.
func bootstrapOf(t *testing.T, g *Gateway) Bootstrap {
	t.Helper()
	return g.buildBootstrap(t.Context())
}

// bootstrapJSON is bootstrapOf as JSON, for the tests that assert a secret
// never reaches a tab.
func bootstrapJSON(t *testing.T, g *Gateway) string {
	t.Helper()
	raw, err := json.Marshal(bootstrapOf(t, g))
	require.NoError(t, err)
	return string(raw)
}

// sessionSnapshotOf builds the payload a subscription answers with.
func sessionSnapshotOf(t *testing.T, g *Gateway, sessionID string) SessionSnapshot {
	t.Helper()
	return g.buildSessionSnapshot(t.Context(), sessionID, "", messagePageSize)
}
```

- [ ] **Step 8: Adapt the existing tests**

Apply these replacements; each keeps its assertions.

In `internal/web/controller_test.go`:
- `snapshotOf(t, c.gw, "").InputMode` → `bootstrapOf(t, c.gw).InputMode`
- `g.buildSnapshot(t.Context(), "s1").PlanReadySessionID` → `g.buildBootstrap(t.Context()).PlanReadySessionID` (two sites)
- `snapshotOf(t, c.gw, "").PlanReadySessionID` → `bootstrapOf(t, c.gw).PlanReadySessionID`

In `internal/web/stream_test.go`:
- `TestSnapshotIncludesLSPAndMCP`: `s := g.buildSnapshot(t.Context(), "s1")` → `s := g.buildBootstrap(t.Context())`
- `TestSnapshotShowsReportedInputMode`: `g.buildSnapshot(t.Context(), "s1").InputMode` → `g.buildBootstrap(t.Context()).InputMode`
- `TestStreamStartsWithSnapshotThenLive`: unmarshal into `Bootstrap`, assert `s.CurrentSessionID == "s1"`, `s.WorkingDir == "/tmp/project"`, `s.InputMode == "code"`, and expect `frameBootstrap`. Rename it to `TestStreamStartsWithBootstrapThenLive`.
- `TestStreamSinceTooOldGetsSnapshot`: expect `frameBootstrap`; rename it to `TestStreamSinceTooOldGetsBootstrap`.
- `TestSinceAheadOfHeadOrMalformedGetsSnapshot`: expect `frameBootstrap`; rename to `TestSinceAheadOfHeadOrMalformedGetsBootstrap`.
- The `"snapshot"` literals become `"bootstrap"`.

In `internal/web/api_test.go`:
- `TestHealthAndSnapshot` → rename to `TestHealth` and delete the whole
  snapshot section after the health assertions; the new bootstrap tests
  cover `CurrentSessionID` and `Branch`.
- `TestSnapshotExposesEveryPendingBatch`: `snapshotOf(t, g, "")` →
  `bootstrapOf(t, g)`; rename it to `TestBootstrapExposesEveryPendingBatch`.
- `TestSnapshotReportsAgentReady`: `snapshotOf(t, g, "").AgentReady` →
  `bootstrapOf(t, g).AgentReady`; rename it to
  `TestBootstrapReportsAgentReady`.
- `TestSnapshotRouteIsGone` keeps its name and body.

In `internal/web/api_models_test.go`: `snapshotOf(t, g, "")` →
`bootstrapOf(t, g)` (two sites).

In `internal/web/api_prompts_test.go`:
- line with `snap := g.buildSnapshot(t.Context(), "")` →
  `snap := g.buildBootstrap(t.Context())`
- `snapshotJSON(t, g, "")` → `bootstrapJSON(t, g)` (two sites)
- `frameSnapshot` → `frameBootstrap` (three sites)

In `internal/web/api_signin_test.go`: `snapshotJSON(t, g, "")` →
`bootstrapJSON(t, g)` (three sites).

In `internal/web/team_test.go`: `snapshotOf(t, g, "")` →
`bootstrapOf(t, g)`.

In `internal/web/seed_m3_test.go`: `snap := snapshotOf(t, g, "")` →
`snap := bootstrapOf(t, g)`.

In `internal/web/api_sessions_test.go`: `frameSnapshot` → `frameBootstrap`.

- [ ] **Step 9: Run the affected packages**

```bash
go build ./...
go test ./internal/web/... -count=1
```

Expected: PASS, including
`TestEveryRouteRequiresTheToken` (it walks the registry, so
`GET /api/bootstrap` is covered automatically).

- [ ] **Step 10: Prove the old name is gone**

Run: `grep -rn 'buildSnapshot\|snapshotOf\|snapshotJSON\|frameSnapshot' --include=*.go internal/`
Expected: no output.

- [ ] **Step 11: Format and commit**

```bash
gofumpt -w internal/web
go vet ./internal/web/...
git add internal/web
git commit -m "feat(web): add GET /api/bootstrap and session snapshots"
```

---

## Task 4: crush.v2, the bootstrap frame, subscribe and unsubscribe

**Files:**
- Modify: `internal/web/auth.go` (`streamProtocol`)
- Modify: `internal/web/stream.go` (frame struct, `conn`, hub, `readLoop`,
  `handleStream`, new `handleSubscribe`, `enqueueError`,
  `sessionExistsQuiet`)
- Modify: `internal/web/webtest/fake.go` (`SetListMessagesGate`, test
  support for the snapshot-race test)
- Test: `internal/web/stream_test.go`

**Interfaces:**
- Consumes: `g.buildSessionSnapshot` and `messagePageSize` (Task 3),
  `g.hub`, `webErrorPayload`, `g.ws.GetSession`, `isSessionNotFound`.
- Produces:
  - `streamProtocol = "crush.v2"` (`internal/web/auth.go`).
  - `frameSessionSnapshot = "session_snapshot"`,
    `clientSubscribe = "subscribe"`, `clientUnsubscribe = "unsubscribe"`,
    `maxSubscriptions = 32`.
  - `frame.Session string` (JSON `session_id,omitempty`).
  - `type clientMessage struct { Type, SessionID string }`.
  - `func (g *Gateway) handleSubscribe(ctx context.Context, c *conn, sessionID string)`
  - `func (g *Gateway) enqueueError(c *conn, sessionID, code, message string)`
  - `func (g *Gateway) sessionExistsQuiet(ctx context.Context, sessionID string) bool`
  - `func (h *hub) beginSubscription(c *conn, sessionID string) bool`,
    `func (h *hub) flushSubscription(c *conn, sessionID string, snap frame)`,
    `func (h *hub) abortSubscription(c *conn, sessionID string)`,
    `func (h *hub) unsubscribe(c *conn, sessionID string)`,
    `func (h *hub) enqueue(c *conn, f frame)`,
    `func (h *hub) sendLocked(c *conn, f frame)`,
    `func (h *hub) deliverLocked(c *conn, f frame)` (Task 5 calls it from
    `publishSession`).
  - `func (f *Fake) SetListMessagesGate(entered chan<- struct{}, release <-chan struct{})`
    in `internal/web/webtest`, a one-shot gate on the next `ListMessages`
    call.
  - Test helper `subscribeStream(t *testing.T, c *websocket.Conn, sessionID string) testFrame`
    and `writeClient(t *testing.T, c *websocket.Conn, msg string)`.
  Task 5 consumes `frame.Session`, `enqueue` and `sendLocked`.

- [ ] **Step 1: Write the failing subscribe tests**

Append to `internal/web/stream_test.go`:

```go
// TestSubscribeAnswersASessionSnapshot pins the message a tab sends to open
// a session and the frame it gets back.
func TestSubscribeAnswersASessionSnapshot(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddMessage(textMessage("m1", "s1", "hello"))
	c := dialStream(t, g, "")
	require.Equal(t, frameBootstrap, readFrame(t, c).Type)

	f := subscribeStream(t, c, "s1")
	var s SessionSnapshot
	require.NoError(t, json.Unmarshal(f.Payload, &s))
	require.Equal(t, "s1", s.SessionID)
	require.Len(t, s.Messages, 1)
}

// TestSubscribeThenLiveFrames pins the order the stream promises: the
// snapshot first, then the session's live frames.
func TestSubscribeThenLiveFrames(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	subscribeStream(t, c, "s1")

	fake.Emit(msgEvent("m1", "s1", "hello"))
	f := nextFrameOfType(t.Context(), t, c, "message")
	require.Contains(t, string(f.Payload), "hello")
}

// TestResubscribeAnswersAFreshSnapshot pins the resync path: subscribing to
// a session twice answers a new snapshot both times.
func TestResubscribeAnswersAFreshSnapshot(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	subscribeStream(t, c, "s1")
	fake.AddMessage(textMessage("m2", "s1", "later"))

	f := subscribeStream(t, c, "s1")
	var s SessionSnapshot
	require.NoError(t, json.Unmarshal(f.Payload, &s))
	require.Len(t, s.Messages, 1, "the second snapshot carries the new message")
}

// TestSubscribeRefusesAnUnknownSession pins the error frame a stale tab
// gets instead of a subscription.
func TestSubscribeRefusesAnUnknownSession(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	writeClient(t, c, `{"type":"subscribe","session_id":"nope"}`)
	f := nextFrameOfType(t.Context(), t, c, frameError)
	require.Contains(t, string(f.Payload), "unknown_session")
}

// TestSubscribeRefusesTooManySessions pins the per-connection cap.
func TestSubscribeRefusesTooManySessions(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for i := range maxSubscriptions {
		fake.AddSession(session.Session{ID: "x" + strconv.Itoa(i), Title: "x"})
	}
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	for i := range maxSubscriptions {
		subscribeStream(t, c, "x"+strconv.Itoa(i))
	}
	writeClient(t, c, `{"type":"subscribe","session_id":"s1"}`)
	f := nextFrameOfType(t.Context(), t, c, frameError)
	require.Contains(t, string(f.Payload), "too_many_subscriptions")
}

// subscribeStream asks for a session and returns its snapshot, skipping the
// global frames that may arrive first.
func subscribeStream(t *testing.T, c *websocket.Conn, sessionID string) testFrame {
	t.Helper()
	writeClient(t, c, `{"type":"subscribe","session_id":"`+sessionID+`"}`)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	f := nextFrameOfType(ctx, t, c, frameSessionSnapshot)
	require.Equal(t, sessionID, f.Session)
	return f
}

// TestSubscribeBuffersFramesPublishedDuringTheSnapshot pins the reservation
// at the hub, with no timing: a frame published while the snapshot is being
// built queues behind it instead of overtaking it.
func TestSubscribeBuffersFramesPublishedDuringTheSnapshot(t *testing.T) {
	t.Parallel()
	h := newHub()
	c, _, _, _ := h.attach(-1)
	require.True(t, h.beginSubscription(c, "s1"))
	h.publishSession("s1", "message", json.RawMessage(`{"id":"m2"}`))
	h.flushSubscription(c, "s1", frame{Type: frameSessionSnapshot, Payload: json.RawMessage(`{"session_id":"s1"}`)})

	first := <-c.ch
	require.Equal(t, frameSessionSnapshot, first.Type)
	second := <-c.ch
	require.Equal(t, "message", second.Type)
	require.Equal(t, "s1", second.Session)
}

// TestSubscribeSendsSnapshotBeforeLiveFrames pins the same ordering through
// the real gateway: the snapshot read is held open while a message is
// published, and the tab still reads the snapshot first.
func TestSubscribeSendsSnapshotBeforeLiveFrames(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddMessage(textMessage("m1", "s1", "from the page"))
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	fake.SetListMessagesGate(entered, release)

	c := dialStream(t, g, "")
	require.Equal(t, frameBootstrap, readFrame(t, c).Type)
	writeClient(t, c, `{"type":"subscribe","session_id":"s1"}`)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the snapshot never read the messages")
	}
	fake.Emit(msgEvent("m2", "s1", "mid-build"))
	close(release)

	f := readFrame(t, c)
	require.Equal(t, frameSessionSnapshot, f.Type)
	var s SessionSnapshot
	require.NoError(t, json.Unmarshal(f.Payload, &s))
	require.Len(t, s.Messages, 1, "the page holds what the snapshot read")

	live := nextFrameOfType(t.Context(), t, c, "message")
	require.Contains(t, string(live.Payload), "mid-build")
}

// writeClient sends one text message to the gateway.
func writeClient(t *testing.T, c *websocket.Conn, msg string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Write(ctx, websocket.MessageText, []byte(msg)))
}
```

Add the `session` and `strconv` imports to `stream_test.go` if it does not
already have them (`grep -n 'strconv\.\|session\.' internal/web/stream_test.go`),
and `time` if it is missing.

The snapshot-race test needs one hook in the fake. Append to
`internal/web/webtest/fake.go`, next to `SetAgentModelGate`:

```go
// SetListMessagesGate holds the next ListMessages call until release is
// closed, reporting on entered first. It is one-shot: later reads pass
// through, so a test only gates the snapshot it means to hold.
func (f *Fake) SetListMessagesGate(entered chan<- struct{}, release <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listMessagesGate = &agentModelGate{entered: entered, release: release}
}
```

with a `listMessagesGate *agentModelGate` field on `Fake`, and at the top of
`ListMessages`:

```go
	f.mu.Lock()
	gate := f.listMessagesGate
	f.listMessagesGate = nil
	f.mu.Unlock()
	if gate != nil {
		gate.entered <- struct{}{}
		select {
		case <-gate.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
```

`entered` must be buffered by at least one slot: the call reports and then
waits. Rename the parameter `_ context.Context` to `ctx` in `ListMessages`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/web/ -run 'TestSubscribe|TestResubscribe' -count=1`
Expected: FAIL to build with `undefined: subscribeStream`,
`undefined: writeClient`, `undefined: frameSessionSnapshot` and
`undefined: maxSubscriptions`.

- [ ] **Step 3: Bump the protocol and the frame shape**

In `internal/web/auth.go`, set `streamProtocol = "crush.v2"` and update the
comment to say the page offers it beside the token entry.

In `internal/web/stream.go`, extend `frame`:

```go
type frame struct {
	Seq     uint64          `json:"seq"`
	Type    string          `json:"type"`
	Session string          `json:"session_id,omitempty"`
	Payload json.RawMessage `json:"payload"`
}
```

and add to the `const` block:

```go
	frameSessionSnapshot = "session_snapshot"
	clientSubscribe      = "subscribe"
	clientUnsubscribe    = "unsubscribe"
	// maxSubscriptions is the most sessions one connection may hold at once:
	// a tab subscribes to the open session plus every background run.
	maxSubscriptions = 32
```

- [ ] **Step 4: Track subscriptions on the connection**

In `internal/web/stream.go`, extend `conn`:

```go
type conn struct {
	ch       chan frame
	overflow chan struct{}
	once     sync.Once
	// sessions holds the session ids this connection subscribed to. The hub
	// lock guards it, so a publish can read it without another mutex.
	sessions map[string]struct{}
	// pending holds, per session whose snapshot is being built, the frames
	// published in the meantime. It is what closes the snapshot race: the
	// session is reserved before the read starts, so nothing is lost.
	pending map[string][]frame
}
```

and add:

```go
// tracks reports whether the connection wants the session, either already
// subscribed or reserved while its snapshot is built. The caller holds the
// hub lock.
func (c *conn) tracks(sessionID string) bool {
	if _, ok := c.sessions[sessionID]; ok {
		return true
	}
	_, ok := c.pending[sessionID]
	return ok
}

// clientMessage is one text message from a tab.
type clientMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}
```

- [ ] **Step 5: Refactor the hub's send path**

Replace the body of `hub.publish`'s fan-out loop with a shared helper, and
add the three new methods:

```go
// sendLocked queues one frame for a connection, marking it for resync when
// the queue is full. The caller holds h.mu.
func (h *hub) sendLocked(c *conn, f frame) {
	select {
	case c.ch <- f:
	default:
		c.once.Do(func() { close(c.overflow) })
		delete(h.subs, c)
	}
}

// enqueue queues a frame for one connection without recording it in the
// ring. It is how a handler answers a client message: the writer loop owns
// the socket, so every frame, reply or not, travels this queue.
func (h *hub) enqueue(c *conn, f frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sendLocked(c, f)
}

// deliverLocked queues a per-session frame for one connection: into its
// buffer while the subscription's snapshot is still being built, onto its
// queue once the subscription is live. The caller holds h.mu.
func (h *hub) deliverLocked(c *conn, f frame) {
	if _, building := c.pending[f.Session]; building {
		c.pending[f.Session] = append(c.pending[f.Session], f)
		return
	}
	h.sendLocked(c, f)
}

// beginSubscription reserves the session on the connection before its
// snapshot is built, so a frame published during the read is buffered for it
// instead of arriving first. It reports false when the connection already
// holds maxSubscriptions.
func (h *hub) beginSubscription(c *conn, sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[c]; !ok {
		return true
	}
	if c.sessions == nil {
		c.sessions = make(map[string]struct{})
	}
	if c.pending == nil {
		c.pending = make(map[string][]frame)
	}
	if _, held := c.sessions[sessionID]; held {
		// A re-subscribe after a resync: buffer afresh and answer a new
		// snapshot, so the tab rebuilds the session from what exists now.
		c.pending[sessionID] = nil
		return true
	}
	if len(c.sessions)+len(c.pending) >= maxSubscriptions {
		return false
	}
	c.pending[sessionID] = nil
	return true
}

// flushSubscription sends the snapshot and then every frame buffered while
// it was built, under one lock, so the tab reads the snapshot first and the
// frames it missed after it. A buffered frame can repeat a message the
// snapshot already carried; the store upserts by message id, so the repeat
// costs nothing.
func (h *hub) flushSubscription(c *conn, sessionID string, snap frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	buffered := c.pending[sessionID]
	delete(c.pending, sessionID)
	if _, ok := h.subs[c]; !ok {
		return
	}
	c.sessions[sessionID] = struct{}{}
	snap.Seq = h.seq
	snap.Session = sessionID
	h.sendLocked(c, snap)
	for _, f := range buffered {
		h.sendLocked(c, f)
	}
}

// abortSubscription drops a reservation whose snapshot could not be built.
func (h *hub) abortSubscription(c *conn, sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(c.pending, sessionID)
}

// unsubscribe drops a session from the connection, buffered or live.
func (h *hub) unsubscribe(c *conn, sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(c.sessions, sessionID)
	delete(c.pending, sessionID)
}
```

`hub.publish`'s loop becomes:

```go
	for c := range h.subs {
		h.sendLocked(c, f)
	}
```

- [ ] **Step 6: Answer subscribe and unsubscribe**

In `internal/web/stream.go`, replace `readLoop`:

```go
// readLoop reads the client's messages and cancels the connection when it
// goes quiet for the idle timeout. A subscribe or unsubscribe is handled
// here; its reply travels the connection's queue, so the single writer loop
// stays the only thing writing to the socket.
func (g *Gateway) readLoop(ctx context.Context, cancel context.CancelFunc, c *conn, ws *websocket.Conn) {
	defer cancel()
	for {
		rctx, rcancel := context.WithTimeout(ctx, g.idle)
		typ, data, err := ws.Read(rctx)
		rcancel()
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg clientMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case clientSubscribe:
			g.handleSubscribe(ctx, c, msg.SessionID)
		case clientUnsubscribe:
			g.hub.unsubscribe(c, msg.SessionID)
		}
	}
}
```

and add:

```go
// handleSubscribe answers a subscribe message with the session's snapshot.
// The session is reserved on the connection before the snapshot is read, so
// every frame published while the read runs is buffered for it; the snapshot
// and the buffer are then flushed together, snapshot first. Without the
// reservation a frame could arrive before the snapshot that should have
// carried it.
func (g *Gateway) handleSubscribe(ctx context.Context, c *conn, sessionID string) {
	if sessionID == "" {
		g.enqueueError(c, "", "missing_session", "subscribe needs a session_id")
		return
	}
	if !g.sessionExistsQuiet(ctx, sessionID) {
		g.enqueueError(c, sessionID, "unknown_session", "no such session")
		return
	}
	if !g.hub.beginSubscription(c, sessionID) {
		g.enqueueError(c, sessionID, "too_many_subscriptions",
			"this connection holds the maximum number of session subscriptions")
		return
	}
	snap := g.buildSessionSnapshot(ctx, sessionID, "", messagePageSize)
	raw, err := json.Marshal(snap)
	if err != nil {
		g.hub.abortSubscription(c, sessionID)
		return
	}
	g.hub.flushSubscription(c, sessionID, frame{Type: frameSessionSnapshot, Payload: raw})
}

// enqueueError queues a web_error frame for one connection.
func (g *Gateway) enqueueError(c *conn, sessionID, code, message string) {
	raw, err := json.Marshal(webErrorPayload(sessionID, code, message))
	if err != nil {
		return
	}
	g.hub.enqueue(c, frame{Seq: g.hub.head(), Type: frameError, Payload: raw})
}

// sessionExistsQuiet reports whether the workspace still has the session,
// logging a failure instead of answering an HTTP caller.
func (g *Gateway) sessionExistsQuiet(ctx context.Context, sessionID string) bool {
	_, err := g.ws.GetSession(ctx, sessionID)
	switch {
	case err == nil:
		return true
	case isSessionNotFound(err):
		return false
	default:
		slog.Debug("Web subscribe could not read the session", "session_id", sessionID, "error", err)
		return false
	}
}
```

In `handleStream`, pass the connection to the reader:

```go
	go g.readLoop(ctx, cancel, c, ws)
```

- [ ] **Step 7: Teach the test frame the session tag**

In `internal/web/stream_test.go`, extend `testFrame` so a test can assert
which session a frame belongs to:

```go
type testFrame struct {
	Seq     uint64          `json:"seq"`
	Type    string          `json:"type"`
	Session string          `json:"session_id,omitempty"`
	Payload json.RawMessage `json:"payload"`
}
```

- [ ] **Step 8: Update the protocol literals in the tests**

In `internal/web/stream_test.go`:
- `TestOffersStreamProtocolReadsJoinedHeaders`: change the table rows that
  name `crush.v1` to `crush.v2` (`{"page pair", "crush.v2, " + …}` and
  `{"crowded value", "one, two ,crush.v2 , three", true}`); keep
  `{"prefix only", "crush.v10", false}`.
- `TestStreamRefusesAnUpgradeWithoutTheStreamProtocol` and
  `TestOffersStreamProtocolReadsJoinedHeaders` comments: replace `crush.v1`
  with `crush.v2`.
- `dialStream` needs no change: it already offers `streamProtocol`.

- [ ] **Step 9: Run the stream tests**

Run: `go test ./internal/web/ -run 'TestSubscribe|TestResubscribe|Protocol|StreamSince' -count=1`
Expected: PASS. Every other stream test still passes because Task 5 has not
made delivery per-session yet: a `message` frame still reaches every
connection.

- [ ] **Step 10: Run the package**

Run: `go test ./internal/web/... -count=1`
Expected: PASS.

- [ ] **Step 11: Format and commit**

```bash
gofumpt -w internal/web
go vet ./internal/web/...
git add internal/web
git commit -m "feat(web): speak crush.v2 with session subscriptions"
```

---

## Task 5: Per-session frame delivery and replay filtering

**Files:**
- Modify: `internal/web/stream.go` (`publish`, `publishSession`,
  `publishSessionJSON`, `appendLocked`, replay filter)
- Modify: `internal/web/gateway.go` (`pump`)
- Modify: `internal/web/encode.go` (`eventSessionID`)
- Modify: `internal/web/api_agent.go` (the two `shell_output` publishes)
- Test: `internal/web/stream_test.go`, `internal/web/gateway_test.go`,
  `internal/web/api_agent_test.go`

**Interfaces:**
- Consumes: `frame.Session`, `conn.tracks`, `hub.sendLocked`,
  `hub.deliverLocked` (Task 4); `encode(ev)`; `shellOutputPayload`;
  `textMessage` (Task 2); `msgEvent`, `dialStream`, `subscribeStream`,
  `writeClient`, `itoa` (`stream_test.go`).
- Produces:
  - `func (h *hub) publishSession(sessionID, typ string, raw json.RawMessage) frame`
  - `func (h *hub) publishSessionJSON(sessionID, typ string, v any)`
  - `func eventSessionID(ev any) string`
  The per-session frame set is exactly `message`, `file`, `shell_output` and
  `run_complete`; every other type stays global, delivery routes through
  `h.deliverLocked` so a frame published during a snapshot build is buffered
  (Task 4), and the replay ring never replays a per-session frame at all.

- [ ] **Step 1: Write the failing delivery tests**

Append to `internal/web/stream_test.go`:

```go
// TestPerSessionFramesReachOnlySubscribers pins the routing: a tab follows
// two sessions at once and never receives a third session's traffic.
func TestPerSessionFramesReachOnlySubscribers(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddSession(session.Session{ID: "s2", Title: "Other", UpdatedAt: 5})
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	subscribeStream(t, c, "s1")

	fake.Emit(msgEvent("m2", "s2", "not mine"))
	fake.Emit(msgEvent("m1", "s1", "mine"))
	f := nextFrameOfType(t.Context(), t, c, "message")
	require.Contains(t, string(f.Payload), "mine")
	require.Equal(t, "s1", f.Session, "the frame carries its session")
	require.NotContains(t, string(f.Payload), "not mine")
}

// TestSessionStatusReachesEveryConnection pins what stays global.
func TestSessionStatusReachesEveryConnection(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	fake.SetSessionBusy("s1", true)
	f := nextFrameOfType(t.Context(), t, c, frameSessionStatus)
	require.Equal(t, "", f.Session, "a status frame is global")
	require.Contains(t, string(f.Payload), `"status":"running"`)
}

// TestUnsubscribeStopsFrames pins the other half of the subscription by
// observing absence: the unsubscribed session's message is published before a
// global sentinel the pump emits later, so any message frame before the
// sentinel would be the leak.
func TestUnsubscribeStopsFrames(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	subscribeStream(t, c, "s1")
	writeClient(t, c, `{"type":"unsubscribe","session_id":"s1"}`)
	require.Eventually(t, func() bool { return !g.hub.tracksForTest(c, "s1") }, time.Second, 10*time.Millisecond)

	fake.SetSessionBusy("s1", true)
	fake.Emit(msgEvent("m1", "s1", "after"))
	// A user prompt makes the pump publish the session's status after the
	// message above, which is the sentinel this test reads.
	fake.Emit(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
		ID: "u1", SessionID: "s1", Role: message.User,
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		require.NoError(t, err)
		var f testFrame
		require.NoError(t, json.Unmarshal(data, &f))
		require.NotEqual(t, "message", f.Type, "an unsubscribed session still delivered")
		if f.Type == frameSessionStatus {
			return
		}
	}
}

// TestTwoSessionsOnOneConnection pins spec C4: one tab follows two sessions
// at once and receives both streams, each tagged with its session.
func TestTwoSessionsOnOneConnection(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddSession(session.Session{ID: "s2", Title: "Other", UpdatedAt: 5})
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	subscribeStream(t, c, "s1")
	subscribeStream(t, c, "s2")

	fake.Emit(msgEvent("m1", "s1", "one"))
	fake.Emit(msgEvent("m2", "s2", "two"))
	first := nextFrameOfType(t.Context(), t, c, "message")
	second := nextFrameOfType(t.Context(), t, c, "message")
	require.Equal(t, "s1", first.Session)
	require.Contains(t, string(first.Payload), "one")
	require.Equal(t, "s2", second.Session)
	require.Contains(t, string(second.Payload), "two")
}

// TestReplayDropsPerSessionFrames pins the resync contract: the ring's
// per-session frames never reach a reconnecting tab, which rebuilds every
// session from a fresh snapshot instead.
func TestReplayDropsPerSessionFrames(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddMessage(textMessage("m1", "s1", "kept"))
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	subscribeStream(t, c, "s1")
	fake.Emit(msgEvent("m2", "s1", "live only"))
	msg := nextFrameOfType(t.Context(), t, c, "message")
	c.Close(websocket.StatusNormalClosure, "")

	// Reconnect inside the ring: attach replays global frames only, so the
	// live message cannot come back, and the resubscribe answers a snapshot
	// carrying what the workspace stored.
	c2 := dialStream(t, g, "?since="+itoa(msg.Seq-1))
	writeClient(t, c2, `{"type":"subscribe","session_id":"s1"}`)
	snap := readSnapshotAfterReplay(t, c2)
	var s SessionSnapshot
	require.NoError(t, json.Unmarshal(snap.Payload, &s))
	require.Len(t, s.Messages, 1, "the snapshot carries the stored message")
	require.Contains(t, string(s.Payload), "kept")
}

// TestResyncRebuildsSessions pins spec C2: a tab that held a subscription,
// loses the ring and reconnects gets a bootstrap and then a fresh snapshot
// for the session it re-subscribes to.
func TestResyncRebuildsSessions(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddMessage(textMessage("m1", "s1", "kept"))
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	subscribeStream(t, c, "s1")
	for i := range ringSize + 1 {
		fake.Emit(msgEvent("m"+itoa(uint64(i)), "s1", "x"))
	}
	require.Eventually(t, func() bool { return g.hub.head() >= ringSize+1 }, 30*time.Second, 10*time.Millisecond)
	c.Close(websocket.StatusNormalClosure, "")

	c2 := dialStream(t, g, "?since=1")
	require.Equal(t, frameBootstrap, readFrame(t, c2).Type, "an expired ring answers a bootstrap")
	snap := subscribeStream(t, c2, "s1")
	var s SessionSnapshot
	require.NoError(t, json.Unmarshal(snap.Payload, &s))
	require.Len(t, s.Messages, 1)
}

// readSnapshotAfterReplay reads past global replayed frames to the session
// snapshot, failing if any per-session frame arrives first.
func readSnapshotAfterReplay(t *testing.T, c *websocket.Conn) testFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		require.NoError(t, err)
		var f testFrame
		require.NoError(t, json.Unmarshal(data, &f))
		require.Empty(t, f.Session, "a per-session frame reached the replay")
		if f.Type == frameSessionSnapshot {
			return f
		}
	}
}
```

`TestUnsubscribeStopsFrames` needs a way to observe the subscription set;
add the helper to `internal/web/stream_test.go`:

```go
// tracksForTest reports whether the connection still wants the session,
// subscribed or reserved.
func (h *hub) tracksForTest(c *conn, sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return c.tracks(sessionID)
}
```

Delete `TestStreamSinceReplays` outright: it expected a replayed `message`
frame, which is the behavior this task removes, and
`TestReplayDropsPerSessionFrames` replaces it with the surviving contract.
`TestStreamSinceTooOldGetsBootstrap` keeps its name and its assertions: an
expired ring still answers a bootstrap.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/web/ -run 'TestPerSessionFrames|TestSessionStatusReaches|TestUnsubscribeStops|TestTwoSessions|TestReplayDrops|TestResync' -count=1`
Expected: FAIL. `TestPerSessionFramesReachOnlySubscribers` gets the `"not
mine"` message first because delivery is still broadcast,
`TestUnsubscribeStopsFrames` reads a `message` before its sentinel, and
`TestReplayDropsPerSessionFrames` sees a replayed `message` frame before the
snapshot.

- [ ] **Step 3: Split the ring append out of publish**

In `internal/web/stream.go`:

```go
// appendLocked records a frame in the replay ring. The caller holds h.mu.
func (h *hub) appendLocked(f frame) {
	if len(h.ring) < ringSize {
		h.ring = append(h.ring, f)
		return
	}
	h.ring[h.start] = f
	h.start = (h.start + 1) % ringSize
}
```

`publish` keeps its signature and fans out to every connection:

```go
func (h *hub) publish(typ string, raw json.RawMessage) frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := frame{Seq: h.seq, Type: typ, Payload: raw}
	h.appendLocked(f)
	for c := range h.subs {
		h.sendLocked(c, f)
	}
	return f
}
```

Add the per-session pair:

```go
// publishSession tags a frame with its session, records it in the ring and
// delivers it to every connection that tracks the session, which includes one
// whose snapshot is still being built.
func (h *hub) publishSession(sessionID, typ string, raw json.RawMessage) frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := frame{Seq: h.seq, Type: typ, Session: sessionID, Payload: raw}
	h.appendLocked(f)
	for c := range h.subs {
		if !c.tracks(sessionID) {
			continue
		}
		h.deliverLocked(c, f)
	}
	return f
}

func (h *hub) publishSessionJSON(sessionID, typ string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		slog.Error("Failed to marshal web frame", "type", typ, "error", err)
		return
	}
	h.publishSession(sessionID, typ, raw)
}
```

- [ ] **Step 4: Filter the replay**

In `handleStream`'s replay loop:

```go
	for _, f := range replay {
		// Per-session frames are never replayed. A connecting tab holds no
		// subscriptions yet, and replaying another tab's session traffic
		// would be worse than the snapshot it re-subscribes for.
		if f.Session != "" {
			continue
		}
		if !writeFrame(ctx, ws, f) {
			return
		}
	}
```

- [ ] **Step 5: Tag the frames the pump forwards**

In `internal/web/encode.go`, add:

```go
// eventSessionID returns the session a per-session frame belongs to, or ""
// for a frame every tab receives.
func eventSessionID(ev any) string {
	switch e := ev.(type) {
	case pubsub.Event[message.Message]:
		return e.Payload.SessionID
	case pubsub.Event[history.File]:
		return e.Payload.SessionID
	case pubsub.Event[notify.RunComplete]:
		return e.Payload.SessionID
	}
	return ""
}
```

with the imports `"github.com/charmbracelet/crush/internal/agent/notify"`
and `"github.com/charmbracelet/crush/internal/history"`.

In `internal/web/gateway.go`'s `pump`, replace the encode-and-publish block:

```go
			if typ, raw, ok := encode(ev); ok {
				if sid := eventSessionID(ev); sid != "" {
					g.hub.publishSession(sid, typ, raw)
				} else {
					g.hub.publish(typ, raw)
				}
			}
```

- [ ] **Step 6: Tag shell output**

In `internal/web/api_agent.go`, both `shell_output` publishes become:

```go
			g.hub.publishSessionJSON(sessionID, frameShellOutput, shellOutputPayload{…})
```

and

```go
	g.hub.publishSessionJSON(sessionID, frameShellOutput, done)
```

Keep the payload fields exactly as they are.

- [ ] **Step 7: Subscribe in the tests that expect per-session frames**

The following tests read a per-session frame from a stream and must
subscribe after consuming the bootstrap:

- `internal/web/stream_test.go`: `TestStreamStartsWithBootstrapThenLive` —
  add `subscribeStream(t, c, "s1")` after `readFrame(t, c)`.
- `internal/web/gateway_test.go`: the test that emits `msgEvent("m1", …)`
  and reads a `message` frame (`grep -n 'nextFrameOfType(ctx, t, c,
  "message")' internal/web/gateway_test.go`) — add
  `subscribeStream(t, c, "s1")` after its `readFrame(t, c)`.
- `internal/web/api_agent_test.go`: every test that reads a `shell_output`
  frame (`grep -n frameShellOutput internal/web/api_agent_test.go`) — add
  `subscribeStream(t, c, "s1")` right after the `readFrame(t, c)` that
  consumes the bootstrap.

Replace the `// Snapshot.` comments on those bootstrap reads with
`// Bootstrap.` throughout `internal/web`.

- [ ] **Step 8: Run the stream and shell tests**

```bash
go test ./internal/web/ -run 'TestStream|TestPerSession|TestSessionStatusReaches|TestUnsubscribe|TestReplayDrops|TestSubscribe|TestShell|TestPrompt' -count=1
```

Expected: PASS.

- [ ] **Step 9: Run the package and vet**

```bash
go test ./internal/web/... -count=1
go vet ./internal/web/...
```

Expected: PASS, vet clean.

- [ ] **Step 10: Format and commit**

```bash
gofumpt -w internal/web
git add internal/web
git commit -m "feat(web): deliver per-session frames to subscribers"
```

---

## Task 6: The golden frame contract

**Files:**
- Create: `internal/web/frames_test.go`
- Create: `internal/web/testdata/frames.golden.json` (generated by the test)

**Interfaces:**
- Consumes: `encode`, `frame`, `Bootstrap`, `SessionSnapshot`,
  `sessionRow`, `ModelInfo`, `TeamState`, `SecurePrompt`, `SignInState`,
  `shellOutputPayload`, `webErrorPayload`, and the frame constants.
- Produces: `internal/web/testdata/frames.golden.json`, one entry per frame
  type, keyed by the type string, each an object
  `{"seq":1,"type":"…","session_id":"…","payload":…}`. Task 10's
  `web/src/api/wire.golden.test.ts` reads this file from
  `../../../internal/web/testdata/frames.golden.json`.

The golden's scope is every frame type the gateway constructs inside
`internal/web`. `skills_event` and `host_theme` are forwarded from the
client/server wire with constructors outside this package; the shell ignores
them in M1 and M6's systems page adds them to the golden.

- [ ] **Step 1: Write the golden test**

Create `internal/web/frames_test.go`:

```go
package web

import (
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// goldenPath is the wire contract the frontend validates with zod. Every
// frame type the gateway constructs has one example here.
const goldenPath = "testdata/frames.golden.json"

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// TestFrameGolden keeps the contract in step with the gateway. Regenerate
// with: go test ./internal/web -run TestFrameGolden -update
func TestFrameGolden(t *testing.T) {
	frames := goldenFrames(t)
	data, err := json.MarshalIndent(frames, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	if *updateGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(goldenPath, data, 0o644))
		return
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "run with -update to write the golden file")
	require.JSONEq(t, string(want), string(data))
}

// goldenFrames builds the contract: one example per event frame type, keyed
// by that type, plus the gateway-originated frames. The key of an event frame
// is its type and not the fixture name, because two fixtures for one type
// would make the file depend on map iteration order; the require below fails
// loudly if that ever happens again. A frame type with two shapes (a shell
// chunk and its end) is keyed "type" and "type_done".
func goldenFrames(t *testing.T) map[string]frame {
	t.Helper()
	out := make(map[string]frame)
	for name, ev := range fixtureEvents() {
		typ, raw, ok := encode(ev)
		require.True(t, ok, name)
		require.NotContains(t, out, typ, "two fixtures share the frame type %s", typ)
		out[typ] = frame{Seq: 1, Type: typ, Session: eventSessionID(ev), Payload: raw}
	}
	for name, f := range gatewayFrames(t) {
		out[name] = f
	}
	return out
}

// TestGoldenCoversEveryFrameType keeps the contract complete: a frame type the
// gateway can put on the wire without an example in the golden file fails
// here, which is what stops the frontend's zod boundary from meeting an
// unknown type at runtime.
func TestGoldenCoversEveryFrameType(t *testing.T) {
	t.Parallel()
	frames := goldenFrames(t)
	for _, typ := range frameTypes() {
		require.Contains(t, frames, typ)
	}
}

// frameTypes is every frame type this package can publish, gateway-originated
// or forwarded from the workspace wire.
func frameTypes() []string {
	return []string{
		frameBootstrap,
		frameSessionSnapshot,
		frameSessionStatus,
		frameFollow,
		frameInputMode,
		framePlanReady,
		frameTeamState,
		frameAgentRuns,
		frameModelChanged,
		frameClosing,
		frameError,
		frameSecurePrompt,
		frameSignIn,
		frameShellOutput,
		pubsub.PayloadTypeMessage,
		pubsub.PayloadTypeFile,
		pubsub.PayloadTypeSession,
		pubsub.PayloadTypePermissionRequest,
		pubsub.PayloadTypePermissionNotification,
		pubsub.PayloadTypePermissionModeChanged,
		pubsub.PayloadTypeQuestionRequest,
		pubsub.PayloadTypeQuestionNotification,
		pubsub.PayloadTypeRunComplete,
		pubsub.PayloadTypeAgentEvent,
		pubsub.PayloadTypeLSPEvent,
		pubsub.PayloadTypeMCPEvent,
		pubsub.PayloadTypeConfigStale,
		pubsub.PayloadTypeConfigChanged,
		pubsub.PayloadTypeUpdateAvailable,
	}
}

// fixtureEvents is one example of every workspace event the gateway
// forwards.
func fixtureEvents() map[string]any {
	return map[string]any{
		// One message fixture, the richest: the user shape is the same
		// message with one text part.
		"message": pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: message.Message{
			ID: "a-1", SessionID: "s1", Role: message.Assistant, CreatedAt: 101, UpdatedAt: 102,
			Model: "claude", Provider: "anthropic",
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "Look at parser.go first."},
				message.TextContent{Text: "Reading parser.go."},
				message.ToolCall{ID: "tc-1", Name: "view", Input: `{"file_path":"parser.go"}`, Finished: true},
				message.Finish{Reason: message.FinishReasonToolUse, Time: 102},
			},
		}},
		"session": pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: session.Session{
			ID: "s1", Title: "Parser refactor", MessageCount: 4, UpdatedAt: 104,
		}},
		"file": pubsub.Event[history.File]{Type: pubsub.CreatedEvent, Payload: history.File{
			ID: "f-1", SessionID: "s1", Path: "/repo/parser.go", Content: "package parser",
			Version: 1, CreatedAt: 104, UpdatedAt: 104,
		}},
		"permission_request": pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
			ID: "p-1", SessionID: "s1", ToolCallID: "tc-2", ToolName: "bash",
			Description: "Run tests", Action: "execute", Params: map[string]string{"command": "go test ./..."},
		}},
		"permission_notification": pubsub.Event[permission.PermissionNotification]{Type: pubsub.UpdatedEvent, Payload: permission.PermissionNotification{
			SessionID: "s1", ToolCallID: "tc-2", Granted: true,
		}},
		"permission_mode_changed": pubsub.Event[permission.ModeChangedEvent]{Type: pubsub.UpdatedEvent, Payload: permission.ModeChangedEvent{
			Mode: permission.PermissionModeYolo,
		}},
		"question_batch_request": pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: question.Request{
			ID: "q-1", SessionID: "s1", ToolCallID: "tc-3",
			Questions: []question.Question{{
				ID: "flavor", Type: question.TypeSingleChoice, Text: "Which theme?",
				Choices: []question.Choice{{ID: "cyan", Label: "Cyan"}},
			}},
		}},
		"question_batch_notification": pubsub.Event[question.Notification]{Type: pubsub.UpdatedEvent, Payload: question.Notification{
			BatchID: "q-1", SessionID: "s1",
		}},
		"run_complete": pubsub.Event[notify.RunComplete]{Type: pubsub.CreatedEvent, Payload: notify.RunComplete{
			SessionID: "s1", RunID: "r-1", MessageID: "a-1", Text: "done",
		}},
		"lsp_event": pubsub.Event[app.LSPEvent]{Type: pubsub.UpdatedEvent, Payload: app.LSPEvent{
			Type: app.LSPEventStateChanged, Name: "gopls", DiagnosticCount: 2,
		}},
		"mcp_event": pubsub.Event[mcp.Event]{Type: pubsub.UpdatedEvent, Payload: mcp.Event{
			Type: mcp.EventStateChanged, Name: "fs", State: mcp.StateConnected,
			Counts: mcp.Counts{Tools: 3},
		}},
		"agent_event": pubsub.Event[notify.Notification]{Type: pubsub.CreatedEvent, Payload: notify.Notification{
			SessionID: "s1", SessionTitle: "Parser refactor", Type: notify.TypeAgentError,
			ProviderID: "anthropic", Message: "The provider refused the request",
		}},
		"config_stale": app.ConfigStaleMsg{Changed: []string{"/repo/crushrc"}, Missing: []string{}},
		"update_available": app.UpdateAvailableMsg{CurrentVersion: "0.30.0", LatestVersion: "0.31.0"},
		"config_changed": pubsub.Event[proto.ConfigChanged]{Type: pubsub.UpdatedEvent, Payload: proto.ConfigChanged{WorkspaceID: "ws-1"}},
	}
}

// gatewayFrames builds one example of every frame the gateway originates
// itself, which never pass through encode.
func gatewayFrames(t *testing.T) map[string]frame {
	t.Helper()
	mk := func(typ string, v any) frame {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		return frame{Seq: 1, Type: typ, Payload: raw}
	}
	mkSession := func(typ, sessionID string, v any) frame {
		f := mk(typ, v)
		f.Session = sessionID
		return f
	}
	return map[string]frame{
		frameBootstrap: mk(frameBootstrap, fixtureBootstrap()),
		frameSessionSnapshot: mkSession(frameSessionSnapshot, "s1", SessionSnapshot{
			SessionID: "s1",
			Session:   fixtureSessionEntity(),
			Messages:  []json.RawMessage{fixtureUserMessage(t)},
			HasMore:   true,
			Files: []FileSummary{{
				Path: "/repo/parser.go", Versions: 1, VersionIDs: []int64{0},
				Additions: 3, Removals: 1, UpdatedAt: 104, SessionIDs: []string{"s1"},
			}},
			Busy:   true,
			Queued: []string{"and the tests"},
		}),
		frameSessionStatus: mk(frameSessionStatus, map[string]string{"session_id": "s1", "status": statusRunning}),
		frameFollow:        mk(frameFollow, map[string]string{"session_id": "s1"}),
		frameInputMode:     mk(frameInputMode, map[string]string{"mode": "plan"}),
		framePlanReady:     mk(framePlanReady, map[string]string{"session_id": "s1"}),
		frameTeamState:     mk(frameTeamState, fixtureTeam()),
		frameAgentRuns:     mk(frameAgentRuns, fixtureRuns()),
		frameModelChanged:  mk(frameModelChanged, fixtureModelInfo()),
		frameClosing:       mk(frameClosing, map[string]string{"reason": "crush exited"}),
		frameError:         mk(frameError, webErrorPayload("s1", "agent_starting", "Crush is starting")),
		frameSecurePrompt: mk(frameSecurePrompt, map[string][]SecurePrompt{"prompts": {{
			Source: sourcePinentry, ID: "p1", Kind: "passphrase", Title: "GPG passphrase",
			Message: "Unlock the signing key", Details: []string{"Key: ABCDEF0123456789"},
		}}}),
		frameSignIn: mk(frameSignIn, SignInState{
			FlowID: "f1", Provider: "copilot", ProviderName: "GitHub Copilot",
			State: signInWaiting, Kind: signInKindDevice,
			UserCode: "WDJB-MJHT", VerificationURL: "https://github.com/login/device",
		}),
		frameShellOutput: mkSession(frameShellOutput, "s1", shellOutputPayload{
			RunID: "run-1", SessionID: "s1", Chunk: "line 1\n",
		}),
		// The same frame type with the run's last payload: a client must
		// accept either shape.
		frameShellOutput + "_done": mkSession(frameShellOutput, "s1", shellDonePayload{
			RunID: "run-1", SessionID: "s1", Done: true, ExitCode: 0,
		}),
	}
}

func fixtureBootstrap() Bootstrap {
	return Bootstrap{
		Version:          "test",
		WorkingDir:       "/repo",
		Branch:           "main",
		CurrentSessionID: "s1",
		Sessions: []sessionRow{{
			Session: proto.Session{ID: "s1", Title: "Parser refactor", MessageCount: 4, UpdatedAt: 104},
			Status:  statusRunning,
		}},
		Model:              fixtureModelInfo(),
		AgentReady:         true,
		PermissionMode:     "normal",
		InputMode:          "code",
		PlanReadySessionID: "s1",
		LSP:                map[string]ServerInfo{"gopls": {State: "2", DiagnosticCount: 2}},
		MCP:                map[string]ServerInfo{"fs": {State: "connected", ToolCount: 3}},
		Team:               fixtureTeam(),
		AgentRuns:          fixtureRuns(),
		Permissions:        []json.RawMessage{},
	}
}

func fixtureModelInfo() ModelInfo {
	return ModelInfo{
		ID: "claude-sonnet", Name: "Claude Sonnet", Provider: "anthropic", ContextWindow: 200000,
		CanReason: true, EffortLevels: []string{"low", "medium", "high"},
		ReasoningEffort: "high", Think: true,
		Small:      &ModelRef{ID: "claude-haiku", Provider: "anthropic"},
		AgentReady: true,
	}
}

func fixtureSessionEntity() json.RawMessage {
	return innerPayload(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: session.Session{
		ID: "s1", Title: "Parser refactor", MessageCount: 4, UpdatedAt: 104,
	}})
}

func fixtureUserMessage(t *testing.T) json.RawMessage {
	t.Helper()
	raw := innerPayload(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
		ID: "u-1", SessionID: "s1", Role: message.User, CreatedAt: 100, UpdatedAt: 100,
		Parts: []message.ContentPart{message.TextContent{Text: "Refactor the parser"}},
	}})
	require.NotNil(t, raw)
	return raw
}

func fixtureTeam() TeamState {
	return TeamState{
		TeamID: "team-1",
		Members: []agentteam.Member{{
			ID: "m-1", Name: "alice", Role: agentteam.RoleTeammate, SessionID: "team-m-1",
			ParentID: "s1", Status: agentteam.StatusRunning, Model: "claude", ModelProvider: "anthropic",
		}},
		Tasks: []TeamTask{{
			ID: "task-1", Revision: 2, Subject: "Port the parser", Status: agentteam.TaskInProgress,
			OwnerID: "m-1", OwnerName: "alice", BlockedBy: []string{}, SessionID: "s1",
		}},
	}
}

func fixtureRuns() []agentrun.Run {
	started := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	return []agentrun.Run{{
		ID: "sub-1", Kind: agentrun.KindSubagent, Name: "Map the parser", SessionID: "sub-1",
		ParentSessionID: "s1", Status: agentrun.StatusRunning, StartedAt: started,
	}}
}
```

`lsp` is imported for `TestSnapshotIncludesLSPAndMCP` elsewhere in the
package; if this file does not use it, drop the import.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/web/ -run TestFrameGolden -count=1`
Expected: FAIL with `run with -update to write the golden file` (the file
does not exist yet).

- [ ] **Step 3: Generate the golden file**

Run: `go test ./internal/web/ -run 'TestFrameGolden|TestGoldenCoversEveryFrameType' -update -count=1`
Expected: PASS and `internal/web/testdata/frames.golden.json` appears.
Inspect it: the top-level keys are frame types, sorted, plus
`shell_output_done`; they include `bootstrap`, `session_snapshot`,
`session_status`, `message`, `file`, `run_complete`, `shell_output` and the
rest of `frameTypes()`. Every `session_snapshot`, `shell_output`,
`shell_output_done`, `message`, `file` and `run_complete` entry carries
`session_id`; `bootstrap` and every other global frame does not.

- [ ] **Step 4: Run the test without -update**

Run: `go test ./internal/web/ -run TestFrameGolden -count=1`
Expected: PASS.

- [ ] **Step 5: Run the package**

Run: `go test ./internal/web/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofumpt -w internal/web
git add internal/web/frames_test.go internal/web/testdata/frames.golden.json
git commit -m "test(web): pin the stream frame contract in a golden file"
```

---

## Task 7: The webfake M1 scenario

**Files:**
- Create: `internal/web/webtest/seed_m1.go`
- Create: `internal/web/webtest/seed_m1_test.go`
- Modify: `internal/web/cmd/webfake/main.go` (scenario `m1`)

**Interfaces:**
- Consumes: `Fake.AddSession`, `Fake.AddMessage`, `Fake.SetSessionBusy`
  (exists), `Fake.SetSessionQueued` (Task 1), `Fake.Emit`;
  `permission.PermissionRequest`.
- Produces:
  - `func SeedM1(f *Fake, clock time.Time) error`
  - `func SeedM1Pending(f *Fake)`
  - Sessions `s-m1-run` (running), `s-m1-queue` (queued), `s-m1-wait`
    (pending interaction) and `s-m1-idle`, which Task 17's browser specs
    drive. Two of them (`s-m1-run`, `s-m1-wait`) are non-idle at once, which
    is what the multi-session spec needs. There are no screenshot baselines in
    M1; they arrive once a surface settles.
  Task 17 reads `WEBFAKE_SCENARIO=m1`.

- [ ] **Step 1: Write the failing seed test**

Create `internal/web/webtest/seed_m1_test.go`:

```go
package webtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSeedM1GivesTheShellEveryMarker pins the fixture the browser specs
// drive: four sessions, each in a different state.
func TestSeedM1GivesTheShellEveryMarker(t *testing.T) {
	t.Parallel()
	f := NewFake()
	clock := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	require.NoError(t, SeedM1(f, clock))

	sessions, err := f.ListSessions(context.Background())
	require.NoError(t, err)
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	require.Contains(t, ids, "s-m1-run")
	require.Contains(t, ids, "s-m1-queue")
	require.Contains(t, ids, "s-m1-wait")
	require.Contains(t, ids, "s-m1-idle")

	require.True(t, f.AgentIsSessionBusy("s-m1-run"))
	require.Equal(t, []string{"and the golden files"}, f.AgentQueuedPromptsList("s-m1-queue"))
	require.False(t, f.AgentIsSessionBusy("s-m1-idle"))

	msgs, err := f.ListMessages(context.Background(), "s-m1-run")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
}

// TestSeedM1PendingOpensAPermission pins the fourth marker's source: a
// permission request the gateway's pending store keys to s-m1-wait.
func TestSeedM1PendingOpensAPermission(t *testing.T) {
	t.Parallel()
	f := NewFake()
	sub := f.SubscribeEvents(t.Context())
	SeedM1Pending(f)
	ev, ok := <-sub
	require.True(t, ok)
	req, isRequest := ev.(pubsub.Event[permission.PermissionRequest])
	require.True(t, isRequest, "the seed publishes a permission request, got %T", ev)
	require.Equal(t, "s-m1-wait", req.Payload.SessionID)
	require.Equal(t, "p-m1", req.Payload.ID)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/web/webtest/ -run TestSeedM1 -count=1`
Expected: FAIL to build with `undefined: SeedM1` and `undefined:
SeedM1Pending`.

- [ ] **Step 3: Write the seed**

Create `internal/web/webtest/seed_m1.go`:

```go
package webtest

import (
	"strconv"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

// SeedM1 gives the fake the sessions the M1 browser specs drive: one
// running, one with a queued prompt, one waiting for the person and one
// idle. Call it before the gateway starts; then call SeedM1Pending once the
// gateway has subscribed, so the open permission reaches its pending store.
func SeedM1(f *Fake, clock time.Time) error {
	seeded := []struct {
		id        string
		title     string
		updatedAt int64
		messages  []string
	}{
		{"s-m1-run", "Port the parser", clock.Unix(), []string{"Read the tokenizer", "Port the tokenizer"}},
		{"s-m1-queue", "Write parser tests", clock.Add(-time.Minute).Unix(), []string{"Draft the table tests"}},
		{"s-m1-wait", "Rename the grammar", clock.Add(-2 * time.Minute).Unix(), []string{"Rename Token to Lexeme"}},
		{"s-m1-idle", "Old scratch session", clock.Add(-time.Hour).Unix(), []string{"Hello"}},
	}
	for _, s := range seeded {
		f.AddSession(session.Session{ID: s.id, Title: s.title, UpdatedAt: s.updatedAt})
		for i, text := range s.messages {
			role := message.User
			if i%2 == 1 {
				role = message.Assistant
			}
			at := s.updatedAt + int64(i)
			f.AddMessage(message.Message{
				ID: s.id + "-m" + strconv.Itoa(i), SessionID: s.id, Role: role,
				CreatedAt: at, UpdatedAt: at, Model: "claude-haiku", Provider: "anthropic",
				Parts: []message.ContentPart{message.TextContent{Text: text}},
			})
		}
	}
	f.SetSessionBusy("s-m1-run", true)
	f.SetSessionQueued("s-m1-queue", []string{"and the golden files"})
	return nil
}

// SeedM1Pending publishes the permission request that leaves a session
// waiting for the person. It must run after the gateway started, or the
// event has no subscriber.
func SeedM1Pending(f *Fake) {
	f.Emit(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
		ID: "p-m1", SessionID: "s-m1-wait", ToolCallID: "tc-m1",
		ToolName: "edit", Description: "Edit internal/parser/parser.go",
		Path: "/tmp/project/internal/parser/parser.go",
	}})
}
```

- [ ] **Step 4: Run the seed test**

Run: `go test ./internal/web/webtest/ -run TestSeedM1 -count=1`
Expected: PASS.

- [ ] **Step 5: Wire the scenario into the webfake**

In `internal/web/cmd/webfake/main.go`, after the existing seeds and before
the scenario block, add:

```go
	m1 := os.Getenv("WEBFAKE_SCENARIO") == "m1"
	if m1 {
		if err := webtest.SeedM1(fake, clock); err != nil {
			fmt.Fprintln(os.Stderr, "webfake:", err)
			os.Exit(1)
		}
	}
```

and after `web.Start` returns (the gateway must be subscribed first):

```go
	if m1 {
		// The permission arrives a moment after the page loads, so the
		// waiting marker appears without a reload; that live change is what
		// the multi-session browser spec watches.
		time.AfterFunc(time.Second, func() { webtest.SeedM1Pending(fake) })
	}
```

`webfake` already imports `time`. Add
`"github.com/charmbracelet/crush/internal/permission"` and
`"github.com/charmbracelet/crush/internal/pubsub"` to
`internal/web/webtest/seed_m1_test.go` if they are not there.

- [ ] **Step 6: Verify the scenario by hand**

```bash
WEBFAKE_PROJECT_DIR=/tmp/crush-m1-check WEBFAKE_SCENARIO=m1 go run ./internal/web/cmd/webfake > /tmp/webfake-url.txt 2>/tmp/webfake-err.txt &
sleep 5
url=$(head -1 /tmp/webfake-url.txt)
origin=${url%%/#t=*}
token=${url##*#t=}
curl -sS "$origin/api/auth" -H "Origin: $origin" -H "Content-Type: application/json" -d "{\"token\":\"$token\"}" -o /dev/null -w '%{http_code}\n'
curl -sS "$origin/api/bootstrap" -H "Authorization: Bearer $token" | head -c 400; echo
kill %1
```

Expected: `204`, then a bootstrap whose `sessions` array lists `s-m1-run`
with `"status":"running"`, `s-m1-queue` with `"status":"queued"` and
`s-m1-wait` with `"status":"pending_interaction"`. The token must not be
printed anywhere in the output beyond the URL line this script reads.

- [ ] **Step 7: Format and commit**

```bash
gofumpt -w internal/web
go vet ./internal/web/...
git add internal/web
git commit -m "test(web): seed a multi-session webfake scenario"
```

---

## Task 8: The React/Vite scaffold, the restored CI and the real build

**Files:**
- Create: `web/package.json`, `web/package-lock.json` (generated),
  `web/tsconfig.json`, `web/vite.config.ts`, `web/index.html`,
  `web/.nvmrc`
- Create: `web/src/main.tsx`, `web/src/App.tsx`, `web/src/App.module.css`,
  `web/src/App.test.tsx`, `web/src/test/setup.ts`,
  `web/src/test/memoryStorage.ts`
- Create: `web/scripts/budget.mjs`
- Move: `internal/web/dist/favicon.svg` → `web/public/favicon.svg`
- Modify: `internal/web/assets_test.go` (the M0 placeholder assertions)
- Modify: `Taskfile.yaml` (`web:*`)
- Modify: `.github/workflows/web.yml` (frontend job)
- Modify: `internal/web/cmd/webfake/main.go` (the dev-server hint)
- Regenerate: `internal/web/dist/` (build output, committed)

**Interfaces:**
- Consumes: nothing at runtime; this task only proves the toolchain.
- Produces:
  - the toolchain every later frontend task uses: `npm run build`,
    `npm run typecheck`, `npm test`, `npm run budget`.
  - `web/src/App.tsx` exporting `default function App()`, which Task 13
    replaces with the frame.
  - `internal/web/dist/index.html` plus hashed `assets/`, served by
    `assetHandler` at `/`.
  Task 9 creates `web/THIRD_PARTY_NOTICES.md` with the DSH notice.

- [ ] **Step 1: Write the toolchain files**

`web/package.json`:

```json
{
  "name": "crush-web",
  "version": "2.0.0",
  "private": true,
  "type": "module",
  "engines": {
    "node": ">=22"
  },
  "scripts": {
    "dev": "vite",
    "build": "tsc --noEmit && vite build",
    "typecheck": "tsc --noEmit",
    "test": "vitest run",
    "budget": "node scripts/budget.mjs"
  },
  "dependencies": {
    "clsx": "^2.1.1",
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "zod": "^3.24.1"
  },
  "devDependencies": {
    "@testing-library/jest-dom": "^6.6.3",
    "@testing-library/react": "^16.2.0",
    "@testing-library/user-event": "^14.6.1",
    "@types/react": "^18.3.18",
    "@types/react-dom": "^18.3.5",
    "@vitejs/plugin-react": "^4.3.4",
    "jsdom": "^26.0.0",
    "typescript": "^5.7.3",
    "vite": "^6.0.11",
    "vitest": "^3.0.5"
  }
}
```

`web/.nvmrc`:

```
26
```

`web/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true,
    "noEmit": true,
    "isolatedModules": true,
    "skipLibCheck": true,
    "resolveJsonModule": true,
    "types": ["vite/client"]
  },
  "include": ["src", "vite.config.ts"]
}
```

`web/vite.config.ts`:

```ts
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    sourcemap: false,
  },
  server: {
    port: 5173,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
    css: true,
  },
})
```

`web/index.html` (no inline script, no inline style):

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="color-scheme" content="dark light" />
    <title>Crush</title>
    <link rel="icon" href="/favicon.svg" type="image/svg+xml" />
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

`web/src/test/setup.ts`:

```ts
import '@testing-library/jest-dom/vitest'
```

`web/src/test/memoryStorage.ts` (the one in-memory `Storage` every unit test
uses, instead of a copy per file):

```ts
// memoryStorage is the in-memory Storage the unit tests inject, so a test can
// assert what a module persisted without touching the jsdom localStorage.
export function memoryStorage(initial: Record<string, string> = {}): Storage {
  const data = new Map(Object.entries(initial))
  return {
    get length() {
      return data.size
    },
    clear: () => data.clear(),
    getItem: (key: string) => data.get(key) ?? null,
    key: (index: number) => [...data.keys()][index] ?? null,
    removeItem: (key: string) => data.delete(key),
    setItem: (key: string, value: string) => data.set(key, value),
  }
}
```

`web/scripts/budget.mjs`:

```js
// First-paint JavaScript budget: the gzipped size of every module script
// the built index.html loads must stay under 600 KiB.
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'

const budgetBytes = 600 * 1024
const here = dirname(fileURLToPath(import.meta.url))
const dist = join(here, '..', '..', 'internal', 'web', 'dist')

const html = readFileSync(join(dist, 'index.html'), 'utf8')
// A module preload is first-paint JavaScript too: the browser fetches and
// parses it before the first paint even though the tag is a link.
const links = [...html.matchAll(/<link[^>]+rel="modulepreload"[^>]*href="([^"]+)"/g)].map((match) => match[1])
const scripts = [...html.matchAll(/<script[^>]+src="([^"]+)"/g)].map((match) => match[1])
const sources = [...new Set([...scripts, ...links])]
if (scripts.length === 0) {
  console.error('budget: the built index.html loads no module script')
  process.exit(1)
}

let total = 0
for (const source of sources) {
  const file = join(dist, source.replace(/^\//, ''))
  const gzipped = gzipSync(readFileSync(file)).length
  total += gzipped
  console.log(`${source}: ${(gzipped / 1024).toFixed(1)} KiB gzip`)
}
console.log(`first paint: ${(total / 1024).toFixed(1)} KiB gzip of ${budgetBytes / 1024} KiB`)
if (total > budgetBytes) {
  console.error(`budget: over the ${budgetBytes / 1024} KiB cap`)
  process.exit(1)
}
```

- [ ] **Step 2: Install the toolchain**

```bash
cd web && npm install
```

Expected: `web/package-lock.json` appears and `node_modules/` is created
(already ignored by `.gitignore` lines 59 to 61). Commit the lockfile in
Step 9.

- [ ] **Step 3: Write the failing App test**

`web/src/App.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import App from './App'

describe('App', () => {
  it('shows the preview banner', () => {
    render(<App />)
    expect(screen.getByText('Preview: the new web UI is incomplete')).toBeInTheDocument()
  })
})
```

- [ ] **Step 4: Run it to verify it fails**

Run: `cd web && npm test`
Expected: FAIL with `Failed to resolve import "./App" from "src/App.test.tsx"`.

- [ ] **Step 5: Write the shell**

`web/src/App.tsx`:

```tsx
import styles from './App.module.css'

export default function App() {
  return (
    <div className={styles.shell}>
      <p className={styles.banner}>Preview: the new web UI is incomplete</p>
    </div>
  )
}
```

`web/src/App.module.css`:

```css
.shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.banner {
  margin: 0;
  padding: 6px 12px;
  font-size: 12px;
  text-align: center;
}
```

`web/src/main.tsx`:

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import App from './App'

const root = document.getElementById('root')
if (!root) {
  throw new Error('the page has no #root element')
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
```

- [ ] **Step 6: Run the test and the typecheck**

```bash
cd web && npm test && npm run typecheck
```

Expected: one passing test, and `tsc --noEmit` prints nothing.

- [ ] **Step 7: Move the favicon and build**

```bash
mkdir -p web/public
git mv internal/web/dist/favicon.svg web/public/favicon.svg
cd web && npm run build
```

Expected: `internal/web/dist` now holds `index.html`, `favicon.svg` and an
`assets/` directory with one hashed JS bundle (and a CSS file once Task 9
lands). The M0 `style.css` is gone, removed by `emptyOutDir`.

Run: `cd web && npm run budget`
Expected: one line per script plus `first paint: … KiB gzip of 600 KiB`,
under the cap.

- [ ] **Step 8: Rewrite the asset tests for the real build**

In `internal/web/assets_test.go`, replace the three M0 placeholder tests
and keep the stale-asset one:

```go
// TestRootServesTheShell pins the page a tab gets: the built index, with
// no inline script or style, because the CSP allows self only.
func TestRootServesTheShell(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	rsp := do(t, nil, http.MethodGet, g.URL()+"/", nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, rsp.Header.Get("Content-Type"), "text/html")
	require.Equal(t, "no-cache", rsp.Header.Get("Cache-Control"))
	body, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	page := string(body)
	require.Contains(t, page, `<div id="root">`)
	require.Contains(t, page, `/assets/`)
	require.NotContains(t, page, "<script>", "the CSP allows no inline script")
	require.NotContains(t, page, `style="`, "the CSP allows no inline style")
}

// TestShellAnswersAnyPath pins the deep-link case.
func TestShellAnswersAnyPath(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	for _, path := range []string{"/sessions/s1", "/settings/appearance"} {
		rsp := do(t, nil, http.MethodGet, g.URL()+path, nil)
		body, err := io.ReadAll(rsp.Body)
		rsp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, rsp.StatusCode, path)
		require.Contains(t, string(body), `<div id="root">`, path)
	}
}

// TestHashedAssetIsImmutable pins the cache header the entry bundle needs.
func TestHashedAssetIsImmutable(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	entry := builtEntry(t, g.URL())
	rsp := do(t, nil, http.MethodGet, g.URL()+entry, nil)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Equal(t, "public, max-age=31536000, immutable", rsp.Header.Get("Cache-Control"))
}

// builtEntry returns the first /assets/ script the served page loads.
func builtEntry(t *testing.T, origin string) string {
	t.Helper()
	rsp := do(t, nil, http.MethodGet, origin+"/", nil)
	defer rsp.Body.Close()
	body, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	m := regexp.MustCompile(`src="(/assets/[^"]+\.js)"`).FindStringSubmatch(string(body))
	require.Len(t, m, 2, "the built page loads no hashed script")
	return m[1]
}
```

Add `"regexp"` to the imports of `assets_test.go`; delete the old
`TestPlaceholderStylesheetIsServed` (the stylesheet now lives under
`/assets/` and `TestHashedAssetIsImmutable` covers that path).

- [ ] **Step 9: Run the gateway tests**

```bash
go test ./internal/web/ -run 'TestRootServes|TestShellAnswers|TestHashedAsset|TestStaleHashed' -count=1
go test ./internal/web/... -count=1
```

Expected: PASS.

- [ ] **Step 10: Drop the placeholder wording from the webfake hint**

`internal/web/cmd/webfake/main.go` still prints
`Dev server: the web UI is being rebuilt; this serves the placeholder page.`
Replace it with

```go
	fmt.Fprintln(os.Stderr, "Dev server: npm --prefix web run dev serves the UI from source on port 5173.")
```

- [ ] **Step 11: Restore the Taskfile tasks**

Replace the `web:*` block in `Taskfile.yaml` with the real commands, and add
`web:budget`. `web:e2e` comes back in Task 17, with the suite it runs:

```yaml
  web:install:
    desc: Install web mode frontend dependencies
    dir: web
    cmds:
      - npm ci

  web:build:
    desc: Build the web mode frontend into internal/web/dist
    dir: web
    cmds:
      - npm ci
      - npm run build

  web:test:
    desc: Typecheck and unit test the web mode frontend
    dir: web
    cmds:
      - npm run typecheck
      - npm test

  web:budget:
    desc: Fail when first-paint JavaScript exceeds the bundle budget
    dir: web
    cmds:
      - npm run budget

  web:check:
    desc: Fail when the committed web mode build is stale
    cmds:
      - task: web:build
      - task: web:budget
      - git diff --exit-code -- internal/web/dist
      - test -z "$(git status --porcelain --untracked-files=all -- internal/web/dist)"

  web:dev:
    desc: Serve the fake gateway; it prints how to start the Vite dev server
    cmds:
      - go run ./internal/web/cmd/webfake
```

- [ ] **Step 12: Restore the frontend CI job**

In `.github/workflows/web.yml`, replace the `frontend` job's steps with:

```yaml
  frontend:
    runs-on: ubuntu-26.04
    # Typecheck, unit tests, the bundle budget, the dist freshness check and
    # the gateway tests take about 7 minutes on a hosted runner.
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0
        with:
          node-version-file: web/.nvmrc
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
        working-directory: web
      - run: npm run typecheck && npm test
        working-directory: web
      - run: npm run budget
        working-directory: web
      - name: Committed build is fresh
        run: |
          npm --prefix web run build
          git diff --exit-code -- internal/web/dist
          stray=$(git status --porcelain --untracked-files=all -- internal/web/dist)
          if [ -n "$stray" ]; then
            echo "$stray"
            echo "internal/web/dist has files the commit does not carry"
            exit 1
          fi
      - run: go test ./internal/web/... ./internal/wire/...
```

Leave the `e2e` job as the M0 stub; Task 17 restores it.

- [ ] **Step 13: Verify the whole task**

```bash
cd web && npm run typecheck && npm test && npm run build && npm run budget
cd .. && go build ./... && go test ./internal/web/... -count=1
```

Expected: every command succeeds. `git status --short internal/web/dist`
lists only files you are about to commit.

- [ ] **Step 14: Commit**

```bash
git add web Taskfile.yaml .github/workflows/web.yml internal/web/assets_test.go internal/web/dist
git commit -m "feat(web): scaffold the React shell and restore its CI"
```

---

## Task 9: DSH theme tokens with light, dark and system

**Files:**
- Copy: `web/src/theme/base.css`, `design-platform.css`,
  `corner-shape.css`, `scrollbar.css`, `focus.css` (from
  `/tmp/dsh-research/repo/packages/client/ui-theme/src/styles/`)
- Create: `web/src/theme/app.css`, `web/src/theme/index.css`,
  `web/src/theme/theme.ts`, `web/src/theme/theme.test.ts`
- Modify: `web/src/main.tsx` (import the theme entry point)
- Create: `web/THIRD_PARTY_NOTICES.md` (the MIT notice)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type ThemePreference = 'light' | 'dark' | 'system'`
  - `type ResolvedTheme = 'light' | 'dark'`
  - `readPreference(storage?: Storage): ThemePreference`
  - `writePreference(pref: ThemePreference, storage?: Storage): void`
  - `resolveTheme(pref: ThemePreference, prefersDark: boolean): ResolvedTheme`
  - `applyTheme(root: HTMLElement, theme: ResolvedTheme): void`
  - `systemPrefersDark(): boolean`
  - `<html data-ds-theme="light|dark">` drives the copied DSH tokens.
  Task 13 consumes the `--crush-layout-*` custom properties declared here.

- [ ] **Step 1: Write the failing theme test**

`web/src/theme/theme.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { memoryStorage } from '../test/memoryStorage'
import { applyTheme, readPreference, resolveTheme, writePreference } from './theme'

describe('resolveTheme', () => {
  it('follows the system when the preference says so', () => {
    expect(resolveTheme('system', true)).toBe('dark')
    expect(resolveTheme('system', false)).toBe('light')
  })

  it('keeps an explicit preference whatever the system does', () => {
    expect(resolveTheme('light', true)).toBe('light')
    expect(resolveTheme('dark', false)).toBe('dark')
  })
})

describe('readPreference', () => {
  it('defaults to system', () => {
    expect(readPreference(memoryStorage())).toBe('system')
  })

  it('ignores a value it does not know', () => {
    expect(readPreference(memoryStorage({ 'crush.theme': 'neon' }))).toBe('system')
  })

  it('round-trips what writePreference wrote', () => {
    const storage = memoryStorage()
    writePreference('dark', storage)
    expect(readPreference(storage)).toBe('dark')
  })
})

describe('applyTheme', () => {
  it('writes the resolved theme on the document element', () => {
    const root = document.createElement('html')
    applyTheme(root, 'dark')
    expect(root.dataset.dsTheme).toBe('dark')
    applyTheme(root, 'light')
    expect(root.dataset.dsTheme).toBe('light')
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/theme/theme.test.ts`
Expected: FAIL with `Failed to resolve import "./theme"`.

- [ ] **Step 3: Write the theme module**

`web/src/theme/theme.ts`:

```ts
export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

const storageKey = 'crush.theme'

export function readPreference(storage: Storage = localStorage): ThemePreference {
  const raw = storage.getItem(storageKey)
  return raw === 'light' || raw === 'dark' || raw === 'system' ? raw : 'system'
}

export function writePreference(pref: ThemePreference, storage: Storage = localStorage): void {
  storage.setItem(storageKey, pref)
}

export function resolveTheme(pref: ThemePreference, prefersDark: boolean): ResolvedTheme {
  if (pref === 'system') {
    return prefersDark ? 'dark' : 'light'
  }
  return pref
}

export function applyTheme(root: HTMLElement, theme: ResolvedTheme): void {
  root.dataset.dsTheme = theme
}

export function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}
```

- [ ] **Step 4: Run the theme test**

Run: `cd web && npx vitest run src/theme/theme.test.ts`
Expected: PASS.

- [ ] **Step 5: Copy the DSH token sheets**

```bash
dsh=/tmp/dsh-research/repo/packages/client/ui-theme/src/styles
commit=4b399c39a6d1479cd84ca591b4d7c88e24d38a96
for f in base design-platform corner-shape scrollbar focus; do
  cp "$dsh/$f.css" "web/src/theme/$f.css"
done
```

Prepend a header to each copied file, naming its source and the commit:

```css
/* Copied from deepseek-harness packages/client/ui-theme/src/styles/base.css
 * at 4b399c39a6d1479cd84ca591b4d7c88e24d38a96. MIT license, see
 * web/THIRD_PARTY_NOTICES.md. Changes: none. */
```

For `design-platform.css`, the header's last line reads:

```css
 * web/THIRD_PARTY_NOTICES.md. Changes: the dark-theme selectors were
 * rewritten from `body[data-ds-dark-theme]` to `html[data-ds-theme='dark']
 * body` (and the darwin override to match), so the shell's html attribute
 * drives the theme. */
```

Adapt the two dark selectors:

```bash
sed -i "s/^body\[data-ds-dark-theme\] {/html[data-ds-theme='dark'] body {/" web/src/theme/design-platform.css
sed -i "s/^html\[data-platform='darwin'\] body\[data-ds-dark-theme\] {/html[data-platform='darwin'][data-ds-theme='dark'] body {/" web/src/theme/design-platform.css
grep -nE '^(body\[data-ds-dark-theme\]|html\[data-platform)' web/src/theme/design-platform.css
```

Expected: the last command prints only the adapted
`html[data-platform='darwin'][data-ds-theme='dark'] body {` lines, and no
`body[data-ds-dark-theme]` selector is left. It is anchored at the line start
on purpose: the file header names the old selector in prose.

Do not copy `brand-font.css`,
`montserrat-*.woff2` or `Montserrat-OFL.txt`: the shell uses the system
stack from `base.css`.

- [ ] **Step 6: Write the shell's own layer**

`web/src/theme/app.css`:

```css
/* The shell's layer over the DSH tokens: layout metrics, base type and the
 * colour-scheme switch that keeps form controls in step with the theme. */
:root {
  --crush-font-size-text: 14px;
  --crush-font-size-code: 11px;
  --crush-font-size-terminal: 13px;

  --crush-layout-sidebar-min: 264px;
  --crush-layout-sidebar-max: 420px;
  --crush-layout-sidebar-default: 280px;
  --crush-layout-sidebar-rail: 56px;
  --crush-layout-right-default: 45vw;
  --crush-layout-right-max: 70vw;
  --crush-layout-center-min: 400px;
  --crush-layout-banner-height: 30px;
}

html {
  color-scheme: light;
}

html[data-ds-theme='dark'] {
  color-scheme: dark;
}

/* The --dsw-alias-* tokens are declared on body by design-platform.css, so
 * the page background is painted here; a body background propagates to the
 * canvas, which is what fills the viewport. */
body {
  margin: 0;
  min-height: 100vh;
  font-family: var(--dsw-font-family);
  font-size: var(--crush-font-size-text);
  line-height: 1.5;
  color: var(--dsw-alias-label-primary);
  background: var(--dsw-alias-bg-base);
}

#root {
  min-height: 100vh;
}

code,
pre {
  font-family: var(--ds-font-family-code);
  font-size: var(--crush-font-size-code);
}
```

`web/src/theme/index.css`:

```css
@import './base.css';
@import './design-platform.css';
@import './corner-shape.css';
@import './scrollbar.css';
@import './focus.css';
@import './app.css';
```

In `web/src/main.tsx`, add above the `App` import:

```ts
import './theme/index.css'
```

- [ ] **Step 7: Apply the theme at boot**

In `web/src/main.tsx`, after the `#root` check, add:

```ts
applyTheme(document.documentElement, resolveTheme(readPreference(), systemPrefersDark()))
```

with `import { applyTheme, readPreference, resolveTheme, systemPrefersDark } from './theme/theme'`.

Task 12's preference store takes over the live switching; this line is what
makes a fresh boot honour the stored choice.

- [ ] **Step 8: Add the MIT notice**

Create `web/THIRD_PARTY_NOTICES.md`:

```markdown
# Third-party notices

## DeepSeek Harness (deepseek-harness)

The CSS token sheets under `web/src/theme/` are copied from
<https://github.com/deepseek-ai/deepseek-harness>, package
`packages/client/ui-theme`, at commit
`4b399c39a6d1479cd84ca591b4d7c88e24d38a96`. Each copied file names its
source path in its header comment.

MIT License

Copyright (c) 2026 DeepSeek

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

- [ ] **Step 9: Verify the theme renders**

```bash
cd web && npm run typecheck && npm test && npm run build && npm run budget
```

Expected: PASS, and the budget still under 600 KiB (the CSS is not counted;
it is not first-paint JavaScript).

Check the tokens resolve: `grep -c -- '--dsw-alias-bg-base' internal/web/dist/assets/*.css`
expects at least one hit, which proves the copied sheet reached the build.

- [ ] **Step 10: Commit**

```bash
git add web internal/web/dist
git commit -m "feat(web): adopt the DSH theme tokens"
```

---

## Task 10: HTTP client, token auth and the zod wire contract

**Files:**
- Create: `web/src/api/http.ts`
- Create: `web/src/api/wire.ts`
- Create: `web/src/api/http.test.ts`
- Create: `web/src/api/wire.golden.test.ts`

**Interfaces:**
- Consumes: the golden contract `internal/web/testdata/frames.golden.json`
  (Task 6); the bootstrap, session-snapshot and status payloads (Tasks 1 and
  3); `POST /api/auth`, `GET /api/bootstrap`.
- Produces:
  - `class ApiClient` with `authenticated`, `authenticate(token)`,
    `setToken`, `clearToken`, `get<T>(path)`, `send<T>(method, path, body?)`
    and `wsProtocols(): string[]`.
  - `class HttpError { status: number; code: string }`.
  - `readTokenFromHash(hash: string): string | null`,
    `stripTokenFromUrl(location, history)`.
  - `web/src/api/wire.ts`: `sessionStatusSchema`, `sessionRowSchema`,
    `bootstrapSchema`, `sessionSnapshotSchema`, `frameSchema`,
    `payloadSchemas: Record<string, ZodTypeAny>` and
    `parseFrame(raw: string): ParsedFrame`.
  Task 11 consumes `ApiClient.wsProtocols` and `parseFrame`; Task 12
  consumes `ParsedFrame`, `SessionRow` and `sessionStatusSchema`.

- [ ] **Step 1: Write the failing auth test**

`web/src/api/http.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'

import { memoryStorage } from '../test/memoryStorage'
import { ApiClient, HttpError, readTokenFromHash, stripTokenFromUrl } from './http'

describe('readTokenFromHash', () => {
  it('reads the launch token', () => {
    expect(readTokenFromHash('#t=abc123')).toBe('abc123')
  })

  it('reads it beside another fragment field', () => {
    expect(readTokenFromHash('#panel=files&t=abc123')).toBe('abc123')
  })

  it('answers null when there is no token', () => {
    expect(readTokenFromHash('')).toBeNull()
    expect(readTokenFromHash('#panel=files')).toBeNull()
  })
})

describe('stripTokenFromUrl', () => {
  it('keeps the path and the query', () => {
    const history = { replaceState: vi.fn() } as unknown as History
    stripTokenFromUrl({ pathname: '/sessions/s1', search: '?x=1' } as Location, history)
    expect(history.replaceState).toHaveBeenCalledWith(null, '', '/sessions/s1?x=1')
  })
})

describe('ApiClient', () => {
  it('exchanges a token and keeps it in the storage it was given', async () => {
    const storage = memoryStorage()
    const fetchImpl = vi.fn(async () => new Response(null, { status: 204 }))
    const client = new ApiClient({ storage, fetchImpl })
    await client.authenticate('abc123')
    expect(fetchImpl).toHaveBeenCalledWith('/api/auth', expect.objectContaining({ method: 'POST' }))
    expect(storage.getItem('crush.token')).toBe('abc123')
    expect(client.authenticated).toBe(true)
    expect(client.wsProtocols()).toEqual(['crush.v2', 'crush.auth.abc123'])
  })

  it('refuses a token the gateway rejects', async () => {
    const fetchImpl = vi.fn(async () =>
      new Response(JSON.stringify({ error: 'invalid token', code: 'unauthorized' }), { status: 401 }),
    )
    const client = new ApiClient({ storage: memoryStorage(), fetchImpl })
    await expect(client.authenticate('nope')).rejects.toBeInstanceOf(HttpError)
    expect(client.authenticated).toBe(false)
  })

  it('sends the bearer header and parses JSON', async () => {
    const storage = memoryStorage()
    const fetchImpl = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(JSON.stringify({ ok: true }), { status: 200 }),
    )
    const client = new ApiClient({ storage, fetchImpl })
    client.setToken('abc123')
    await expect(client.get<{ ok: boolean }>('/api/health')).resolves.toEqual({ ok: true })
    const [, init] = fetchImpl.mock.calls[0]
    expect(init?.headers).toMatchObject({ Authorization: 'Bearer abc123' })
  })

  it('answers 204 with undefined, not a parse error', async () => {
    const client = new ApiClient({
      storage: memoryStorage(),
      fetchImpl: vi.fn(async () => new Response(null, { status: 204 })),
    })
    client.setToken('abc123')
    await expect(client.send('DELETE', '/api/sessions/s1')).resolves.toBeUndefined()
  })

  it('answers 202 with an empty body too, which is what follow does', async () => {
    const client = new ApiClient({
      storage: memoryStorage(),
      fetchImpl: vi.fn(async () => new Response('', { status: 202 })),
    })
    client.setToken('abc123')
    await expect(client.send('POST', '/api/follow', { session_id: 's1' })).resolves.toBeUndefined()
  })

  it('turns an error envelope into an HttpError', async () => {
    const client = new ApiClient({
      storage: memoryStorage(),
      fetchImpl: vi.fn(async () =>
        new Response(JSON.stringify({ error: 'Crush is working in this session', code: 'session_busy' }), { status: 409 }),
      ),
    })
    client.setToken('abc123')
    await expect(client.send('DELETE', '/api/sessions/s1')).rejects.toMatchObject({
      status: 409,
      code: 'session_busy',
      message: 'Crush is working in this session',
    })
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/api/http.test.ts`
Expected: FAIL with `Failed to resolve import "./http"`.

- [ ] **Step 3: Write the client**

`web/src/api/http.ts`:

```ts
const tokenKey = 'crush.token'

export class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = 'HttpError'
  }
}

export interface ApiClientOptions {
  fetchImpl?: typeof fetch
  storage?: Storage
}

export function readTokenFromHash(hash: string): string | null {
  const match = /(?:^#|&)t=([^&]+)/.exec(hash)
  if (!match) {
    return null
  }
  try {
    return decodeURIComponent(match[1])
  } catch {
    // A malformed fragment is not a token: the tab shows the unauthorized
    // screen instead of throwing on boot.
    return null
  }
}

export function stripTokenFromUrl(location: Location, history: History): void {
  history.replaceState(null, '', location.pathname + location.search)
}

export class ApiClient {
  private token: string | null

  constructor(private readonly options: ApiClientOptions = {}) {
    this.token = this.storage.getItem(tokenKey)
  }

  private get storage(): Storage {
    return this.options.storage ?? sessionStorage
  }

  private get fetchImpl(): typeof fetch {
    return this.options.fetchImpl ?? fetch
  }

  get authenticated(): boolean {
    return this.token !== null
  }

  setToken(token: string): void {
    this.token = token
    this.storage.setItem(tokenKey, token)
  }

  clearToken(): void {
    this.token = null
    this.storage.removeItem(tokenKey)
  }

  // authenticate exchanges the launch token for the session's credential.
  // The gateway answers 204 and never sets a cookie: the tab keeps the token
  // and sends it as a bearer header.
  async authenticate(token: string): Promise<void> {
    const response = await this.fetchImpl('/api/auth', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token }),
    })
    if (!response.ok) {
      throw await toHttpError(response)
    }
    this.setToken(token)
  }

  async get<T>(path: string): Promise<T> {
    return this.send<T>('GET', path)
  }

  async send<T>(method: string, path: string, body?: unknown): Promise<T> {
    if (this.token === null) {
      throw new HttpError(401, 'unauthorized', 'the tab has no token')
    }
    const init: RequestInit = {
      method,
      headers: { Authorization: `Bearer ${this.token}` },
    }
    if (body !== undefined) {
      init.headers = { ...init.headers, 'Content-Type': 'application/json' }
      init.body = JSON.stringify(body)
    }
    const response = await this.fetchImpl(path, init)
    if (!response.ok) {
      throw await toHttpError(response)
    }
    // Several routes answer 202 or 204 with no body (follow, cancel, queue
    // clear, detach), so the body is read as text and parsed only when it is
    // there.
    const text = await response.text()
    if (text === '') {
      return undefined as T
    }
    return JSON.parse(text) as T
  }

  // wsProtocols are the entries the stream offers: the token entry beside the
  // protocol, because a browser cannot set an Authorization header there.
  wsProtocols(): string[] {
    return ['crush.v2', `crush.auth.${this.token ?? ''}`]
  }
}

async function toHttpError(response: Response): Promise<HttpError> {
  const fallback = `the gateway answered ${response.status}`
  try {
    const body = (await response.json()) as { error?: string; code?: string }
    return new HttpError(response.status, body.code ?? 'unknown', body.error ?? fallback)
  } catch {
    return new HttpError(response.status, 'unknown', fallback)
  }
}
```

- [ ] **Step 4: Run the auth test**

Run: `cd web && npx vitest run src/api/http.test.ts && npm run typecheck`
Expected: PASS. If `init.headers = { ...init.headers, … }` upsets the type
checker, build the headers as a `Record<string, string>` first.

- [ ] **Step 5: Write the failing wire contract test**

`web/src/api/wire.golden.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import golden from '../../../internal/web/testdata/frames.golden.json'
import { frameSchema, parseFrame, payloadSchemas } from './wire'

const frames = golden as Record<string, unknown>

describe('the wire contract', () => {
  it('carries at least the M1 frames', () => {
    for (const type of [
      'bootstrap',
      'session_snapshot',
      'session_status',
      'message',
      'file',
      'shell_output',
      'shell_output_done',
      'run_complete',
    ]) {
      expect(frames[type], type).toBeDefined()
    }
  })

  // The key of an entry is its frame type, or "type_variant" when one type
  // has two shapes (a shell chunk and its end), so the schema is looked up by
  // the frame's own type.
  for (const [name, frame] of Object.entries(frames)) {
    it(`parses the ${name} entry`, () => {
      const parsed = frameSchema.parse(frame)
      const schema = payloadSchemas[parsed.type]
      expect(schema, `no schema is registered for ${parsed.type}`).toBeDefined()
      expect(() => schema.parse(parsed.payload)).not.toThrow()
      expect(parseFrame(JSON.stringify(frame)).type).toBe(parsed.type)
    })
  }

  it('rejects a frame type it does not know', () => {
    expect(() => parseFrame(JSON.stringify({ seq: 1, type: 'not_a_frame', payload: {} }))).toThrow(
      /unknown frame type/,
    )
  })
})
```

- [ ] **Step 6: Run it to verify it fails**

Run: `cd web && npx vitest run src/api/wire.golden.test.ts`
Expected: FAIL with `Failed to resolve import "./wire"`.

- [ ] **Step 7: Write the schemas**

`web/src/api/wire.ts`:

```ts
import { z, type ZodTypeAny } from 'zod'

export const sessionStatusSchema = z.enum(['idle', 'running', 'queued', 'pending_interaction'])
export type SessionStatus = z.infer<typeof sessionStatusSchema>

const sessionSchema = z
  .object({
    id: z.string(),
    parent_session_id: z.string().optional().default(''),
    title: z.string(),
    message_count: z.number(),
    updated_at: z.number(),
  })
  .passthrough()

export const sessionRowSchema = sessionSchema.extend({ status: sessionStatusSchema })
export type SessionRow = z.infer<typeof sessionRowSchema>

export const modelInfoSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    provider: z.string(),
    context_window: z.number(),
    agent_ready: z.boolean(),
  })
  .passthrough()

export const serverInfoSchema = z
  .object({ state: z.string() })
  .passthrough()

export const bootstrapSchema = z
  .object({
    version: z.string(),
    working_dir: z.string(),
    branch: z.string().optional(),
    current_session_id: z.string(),
    sessions: z.array(sessionRowSchema),
    model: modelInfoSchema,
    agent_ready: z.boolean(),
    permission_mode: z.string(),
    input_mode: z.enum(['plan', 'code']),
    lsp: z.record(serverInfoSchema),
    mcp: z.record(serverInfoSchema),
    agent_runs: z.array(z.unknown()),
  })
  .passthrough()
export type Bootstrap = z.infer<typeof bootstrapSchema>

const messageSchema = z
  .object({
    id: z.string(),
    role: z.string(),
    session_id: z.string(),
    parts: z.array(z.object({ type: z.string(), data: z.unknown() })),
    created_at: z.number(),
    updated_at: z.number(),
  })
  .passthrough()

export const sessionSnapshotSchema = z
  .object({
    session_id: z.string(),
    session: sessionSchema.optional(),
    messages: z.array(messageSchema),
    has_more: z.boolean(),
    files: z.array(z.object({ path: z.string() }).passthrough()),
    busy: z.boolean(),
    queued: z.array(z.string()),
  })
  .passthrough()
export type SessionSnapshot = z.infer<typeof sessionSnapshotSchema>

// eventEnvelope wraps the pubsub envelope every event-derived payload
// carries. A session the workspace no longer has answers an empty entity,
// which is why the snapshot's session field is optional.
const eventEnvelope = (payload: ZodTypeAny) => z.object({ type: z.string(), payload }).passthrough()

export const payloadSchemas: Record<string, ZodTypeAny> = {
  bootstrap: bootstrapSchema,
  session_snapshot: sessionSnapshotSchema,
  session_status: z.object({ session_id: z.string(), status: sessionStatusSchema }),
  session_follow: z.object({ session_id: z.string() }),
  input_mode_changed: z.object({ mode: z.enum(['plan', 'code']) }),
  plan_ready: z.object({ session_id: z.string() }),
  team_state: z.object({ team_id: z.string() }).passthrough(),
  agent_runs: z.array(z.unknown()),
  model_changed: modelInfoSchema,
  gateway_closing: z.object({ reason: z.string() }),
  web_error: z.object({ session_id: z.string(), code: z.string(), error: z.string() }),
  secure_prompt: z.object({ prompts: z.array(z.unknown()) }),
  signin: z.object({ flow_id: z.string() }).passthrough(),
  // A shell run streams chunks and then one end frame of the same type, so
  // the schema accepts either shape.
  shell_output: z.union([
    z.object({ run_id: z.string(), session_id: z.string(), chunk: z.string() }).passthrough(),
    z
      .object({ run_id: z.string(), session_id: z.string(), done: z.boolean(), exit_code: z.number() })
      .passthrough(),
  ]),
  message: eventEnvelope(messageSchema),
  file: eventEnvelope(z.object({ session_id: z.string(), path: z.string() }).passthrough()),
  run_complete: eventEnvelope(z.object({ session_id: z.string(), message_id: z.string() }).passthrough()),
  session: eventEnvelope(sessionSchema),
  permission_request: eventEnvelope(z.object({ session_id: z.string() }).passthrough()),
  permission_notification: eventEnvelope(z.object({ session_id: z.string() }).passthrough()),
  permission_mode_changed: eventEnvelope(z.object({ mode: z.string() }).passthrough()),
  question_batch_request: eventEnvelope(z.object({ session_id: z.string() }).passthrough()),
  question_batch_notification: eventEnvelope(z.object({ session_id: z.string() }).passthrough()),
  agent_event: eventEnvelope(z.unknown()),
  lsp_event: eventEnvelope(z.unknown()),
  mcp_event: eventEnvelope(z.unknown()),
  config_stale: eventEnvelope(z.unknown()),
  config_changed: eventEnvelope(z.unknown()),
  update_available: eventEnvelope(z.unknown()),
  skills_event: eventEnvelope(z.unknown()),
  host_theme: eventEnvelope(z.unknown()),
  client_session: eventEnvelope(z.unknown()),
}

export const frameSchema = z
  .object({
    seq: z.number(),
    type: z.string(),
    session_id: z.string().optional(),
    payload: z.unknown(),
  })
  .passthrough()

export interface ParsedFrame {
  seq: number
  type: string
  sessionId?: string
  payload: unknown
}

// parseFrame validates one frame at the boundary. An unknown type is a
// contract break, so it throws instead of rendering nothing.
export function parseFrame(raw: string): ParsedFrame {
  const frame = frameSchema.parse(JSON.parse(raw))
  const schema = payloadSchemas[frame.type]
  if (!schema) {
    throw new Error(`unknown frame type ${frame.type}`)
  }
  const payload = schema.parse(frame.payload)
  return { seq: frame.seq, type: frame.type, sessionId: frame.session_id, payload }
}
```

- [ ] **Step 8: Run both tests and the typecheck**

```bash
cd web && npx vitest run src/api && npm run typecheck
```

Expected: PASS on every entry of the golden file. An entry that fails names
the frame type; fix the schema, not the golden.

- [ ] **Step 9: Commit**

```bash
git add web/src/api
git commit -m "feat(web): add the gateway client and the wire contract"
```

---

## Task 11: The stream client

**Files:**
- Create: `web/src/api/stream.ts`
- Create: `web/src/api/stream.test.ts`

**Interfaces:**
- Consumes: `ApiClient.wsProtocols()`, `parseFrame`, `ParsedFrame`,
  `bootstrapSchema` (Task 10).
- Produces:
  - `type ConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'`
  - `const backoffSteps: number[]` = `[500, 1000, 2000, 4000, 8000, 10000]`
  - `function backoffDelay(attempt: number, random?: () => number): number`
  - `class StreamClient` with `start()`, `stop()`, `subscribe(sessionID)`,
    `unsubscribe(sessionID)`, `state`, `lastSeq`, and constructor options
    `{ url, protocols, handlers, createSocket?, random?, pingIntervalMs? }`.
  Task 12 consumes the handlers; Task 15's reconciler calls
  `subscribe`/`unsubscribe`.

- [ ] **Step 1: Write the failing stream tests**

`web/src/api/stream.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { StreamClient, backoffDelay } from './stream'

class FakeSocket {
  static opened: FakeSocket[] = []
  readyState = 0
  sent: string[] = []
  onopen: (() => void) | null = null
  onclose: ((event: { code: number }) => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  constructor(
    readonly url: string,
    readonly protocols: string[],
  ) {
    FakeSocket.opened.push(this)
  }
  send(data: string): void {
    this.sent.push(data)
  }
  close(): void {
    this.readyState = 3
  }
  open(): void {
    this.readyState = 1
    this.onopen?.()
  }
  receive(frame: unknown): void {
    this.onmessage?.({ data: JSON.stringify(frame) })
  }
  drop(code = 1006): void {
    this.readyState = 3
    this.onclose?.({ code })
  }
}

function client(onFrame = vi.fn(), onState = vi.fn()) {
  return new StreamClient({
    url: () => 'ws://127.0.0.1:1234/api/stream',
    protocols: () => ['crush.v2', 'crush.auth.token'],
    handlers: { onFrame, onState, onBootstrap: vi.fn() },
    createSocket: (url, protocols) => new FakeSocket(url, protocols) as unknown as WebSocket,
    random: () => 1,
  })
}

describe('backoffDelay', () => {
  it('follows the step list and never exceeds it', () => {
    expect(backoffDelay(0, () => 1)).toBe(500)
    expect(backoffDelay(1, () => 1)).toBe(1000)
    expect(backoffDelay(5, () => 1)).toBe(10000)
    expect(backoffDelay(9, () => 1)).toBe(10000)
    expect(backoffDelay(0, () => 0)).toBe(250)
  })
})

describe('StreamClient', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeSocket.opened = []
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('connects with the token entry beside the protocol', () => {
    const s = client()
    s.start()
    expect(FakeSocket.opened).toHaveLength(1)
    expect(FakeSocket.opened[0].protocols).toEqual(['crush.v2', 'crush.auth.token'])
    s.stop()
  })

  it('pings every 15 seconds while open', () => {
    const s = client()
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    vi.advanceTimersByTime(15000)
    expect(socket.sent).toContain(JSON.stringify({ type: 'ping' }))
    s.stop()
  })

  it('sends subscribe and re-sends it after a reconnect', () => {
    const s = client()
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    s.subscribe('s1')
    expect(first.sent).toContain(JSON.stringify({ type: 'subscribe', session_id: 's1' }))

    first.drop()
    vi.advanceTimersByTime(500)
    const second = FakeSocket.opened[1]
    second.open()
    expect(second.sent).toContain(JSON.stringify({ type: 'subscribe', session_id: 's1' }))
    s.stop()
  })

  it('reconnects at once on 4001 and resubscribes on the new socket', () => {
    const s = client()
    s.start()
    const first = FakeSocket.opened[0]
    first.open()
    s.subscribe('s1')
    first.drop(4001)
    vi.advanceTimersByTime(0)
    expect(FakeSocket.opened).toHaveLength(2)
    const second = FakeSocket.opened[1]
    second.open()
    expect(second.sent).toContain(JSON.stringify({ type: 'subscribe', session_id: 's1' }))
    s.stop()
  })

  it('reports reconnecting, then open', () => {
    const onState = vi.fn()
    const s = client(vi.fn(), onState)
    s.start()
    FakeSocket.opened[0].open()
    FakeSocket.opened[0].drop()
    expect(onState).toHaveBeenLastCalledWith('reconnecting')
    vi.advanceTimersByTime(500)
    FakeSocket.opened[1].open()
    expect(onState).toHaveBeenLastCalledWith('open')
    s.stop()
  })

  it('passes the bootstrap payload to its handler and tracks the sequence', () => {
    const onBootstrap = vi.fn()
    const s = new StreamClient({
      url: () => 'ws://localhost/api/stream',
      protocols: () => ['crush.v2', 'crush.auth.token'],
      handlers: { onFrame: vi.fn(), onState: vi.fn(), onBootstrap },
      createSocket: (url, protocols) => new FakeSocket(url, protocols) as unknown as WebSocket,
      random: () => 1,
    })
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    socket.receive({ seq: 7, type: 'bootstrap', payload: { version: 'x' } })
    expect(onBootstrap).toHaveBeenCalledWith({ version: 'x' })
    expect(s.lastSeq).toBe(7)
    s.stop()
  })

  it('counts an unknown frame type as a contract break', () => {
    const onFrame = vi.fn()
    const s = client(onFrame)
    s.start()
    const socket = FakeSocket.opened[0]
    socket.open()
    socket.receive({ seq: 1, type: 'not_a_frame', payload: {} })
    expect(onFrame).not.toHaveBeenCalled()
    s.stop()
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/api/stream.test.ts`
Expected: FAIL with `Failed to resolve import "./stream"`.

- [ ] **Step 3: Write the client**

`web/src/api/stream.ts`:

```ts
import { parseFrame, type ParsedFrame } from './wire'

export type ConnectionState = 'connecting' | 'open' | 'reconnecting' | 'closed'

export const backoffSteps = [500, 1000, 2000, 4000, 8000, 10000]

const pingIntervalMs = 15000
const resyncCode = 4001

// backoffDelay is the wait before reconnect attempt number `attempt`,
// jittered inside its step so a gateway restart does not gather every tab
// at the same instant, and capped by the last step.
export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const step = backoffSteps[Math.min(attempt, backoffSteps.length - 1)]
  return Math.round(step / 2 + (random() * step) / 2)
}

export interface StreamHandlers {
  onBootstrap(payload: unknown): void
  onFrame(frame: ParsedFrame): void
  onState(state: ConnectionState): void
}

export interface StreamOptions {
  url: () => string
  protocols: () => string[]
  handlers: StreamHandlers
  createSocket?: (url: string, protocols: string[]) => WebSocket
  random?: () => number
  pingIntervalMs?: number
}

export class StreamClient {
  private socket: WebSocket | null = null
  private attempt = 0
  private reconnectTimer: number | null = null
  private pingTimer: number | null = null
  private stopped = true
  private readonly subscriptions = new Set<string>()
  private seq = 0
  private currentState: ConnectionState = 'closed'

  constructor(private readonly options: StreamOptions) {}

  get state(): ConnectionState {
    return this.currentState
  }

  get lastSeq(): number {
    return this.seq
  }

  start(): void {
    if (!this.stopped) {
      return
    }
    this.stopped = false
    this.connect()
  }

  stop(): void {
    this.stopped = true
    this.clearTimers()
    this.setState('closed')
    const socket = this.socket
    this.socket = null
    socket?.close()
  }

  subscribe(sessionID: string): void {
    if (this.subscriptions.has(sessionID)) {
      return
    }
    this.subscriptions.add(sessionID)
    this.send({ type: 'subscribe', session_id: sessionID })
  }

  unsubscribe(sessionID: string): void {
    if (!this.subscriptions.delete(sessionID)) {
      return
    }
    this.send({ type: 'unsubscribe', session_id: sessionID })
  }

  private connect(): void {
    this.setState(this.attempt === 0 ? 'connecting' : 'reconnecting')
    const socket = (this.options.createSocket ?? ((url, protocols) => new WebSocket(url, protocols)))(
      this.options.url(),
      this.options.protocols(),
    )
    this.socket = socket
    socket.onopen = () => {
      this.attempt = 0
      this.setState('open')
      this.startPing()
      for (const sessionID of this.subscriptions) {
        this.send({ type: 'subscribe', session_id: sessionID })
      }
    }
    socket.onmessage = (event) => this.handleMessage(event.data)
    socket.onclose = (event) => this.handleClose(event.code)
  }

  private handleClose(code: number): void {
    this.stopPing()
    this.socket = null
    if (this.stopped) {
      this.setState('closed')
      return
    }
    // A resync close means the ring could not answer: reconnect at once and
    // let the subscriptions rebuild the session state from fresh snapshots.
    const delay = code === resyncCode ? 0 : backoffDelay(this.attempt, this.options.random)
    this.attempt += 1
    this.setState('reconnecting')
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null
      this.connect()
    }, delay)
  }

  private handleMessage(data: unknown): void {
    if (typeof data !== 'string') {
      return
    }
    let frame: ParsedFrame
    try {
      frame = parseFrame(data)
    } catch (error) {
      console.warn('web: dropping a frame the client cannot read', error)
      return
    }
    if (frame.seq > this.seq) {
      this.seq = frame.seq
    }
    if (frame.type === 'bootstrap') {
      this.options.handlers.onBootstrap(frame.payload)
      return
    }
    this.options.handlers.onFrame(frame)
  }

  private send(message: unknown): void {
    if (this.socket === null || this.socket.readyState !== 1) {
      return
    }
    this.socket.send(JSON.stringify(message))
  }

  private startPing(): void {
    this.stopPing()
    const interval = this.options.pingIntervalMs ?? pingIntervalMs
    this.pingTimer = window.setInterval(() => this.send({ type: 'ping' }), interval)
  }

  private stopPing(): void {
    if (this.pingTimer !== null) {
      window.clearInterval(this.pingTimer)
      this.pingTimer = null
    }
  }

  private clearTimers(): void {
    this.stopPing()
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
  }

  private setState(state: ConnectionState): void {
    if (this.currentState === state) {
      return
    }
    this.currentState = state
    this.options.handlers.onState(state)
  }
}
```

- [ ] **Step 4: Run the stream tests**

Run: `cd web && npx vitest run src/api/stream.test.ts && npm run typecheck`
Expected: PASS. Note the subscribe-on-open loop also re-sends for a session
subscribed before the socket opened, which is what the resync path needs.

- [ ] **Step 5: Run the whole suite and the budget**

```bash
cd web && npm test && npm run build && npm run budget
```

Expected: PASS, budget under the cap.

- [ ] **Step 6: Commit**

```bash
git add web/src/api internal/web/dist
git commit -m "feat(web): add the reconnecting stream client"
```

---

## Task 12: Observable stores and unread markers

**Files:**
- Create: `web/src/store/store.ts`
- Create: `web/src/store/app.ts`
- Create: `web/src/store/seen.ts`
- Create: `web/src/store/app.test.ts`
- Create: `web/src/store/store.test.ts`

**Interfaces:**
- Consumes: `Bootstrap`, `SessionSnapshot`, `SessionRow`, `SessionStatus`
  and `ParsedFrame` from `web/src/api/wire.ts`; `ParsedFrame.type` and
  `payload`.
- Produces:
  - `interface Store<T> { get(): T; set(update): void; subscribe(listener): () => void }`,
    `createStore<T>(initial: T): Store<T>`, `useStore(store, selector)`.
  - `interface AppState { connection, bootstrap, sessions, selectedSessionId,
    sessionData, lastSeen }`, `createAppStore(): AppStore`,
    `reduceFrame(state, frame): AppState`, `reduceBootstrap(state, payload)`,
    `isUnread(state, sessionID): boolean`,
    `wantedSubscriptions(state): string[]`, `maxSubscriptions = 32`.
  - `readSeen(storage?)`, `writeSeen(seen, storage?)` under `crush.seen`,
    holding the last-seen `updated_at` per session.
  Task 14 consumes `AppStore.selectSession`; Task 15 consumes `isUnread`,
  `wantedSubscriptions` and `forgetSession`; Task 16 consumes
  `state.sessionData` and `emptySessionData`.

- [ ] **Step 1: Write the failing store tests**

`web/src/store/store.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'

import { createStore } from './store'

describe('createStore', () => {
  it('notifies subscribers on a partial update', () => {
    const store = createStore({ count: 0, label: 'a' })
    const listener = vi.fn()
    const stop = store.subscribe(listener)
    store.set({ count: 1 })
    expect(store.get()).toEqual({ count: 1, label: 'a' })
    expect(listener).toHaveBeenCalledTimes(1)
    stop()
    store.set({ count: 2 })
    expect(listener).toHaveBeenCalledTimes(1)
  })

  it('accepts an updater and publishes nothing for an equal value', () => {
    const store = createStore({ count: 0 })
    const listener = vi.fn()
    store.subscribe(listener)
    store.set(() => ({ count: 0 }))
    expect(listener).not.toHaveBeenCalled()
    store.set((state) => ({ count: state.count + 1 }))
    expect(listener).toHaveBeenCalledTimes(1)
  })
})
```

`web/src/store/app.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import type { Bootstrap, ParsedFrame } from '../api/wire'
import { memoryStorage } from '../test/memoryStorage'
import {
  createAppStore,
  isUnread,
  maxSubscriptions,
  reduceBootstrap,
  reduceFrame,
  wantedSubscriptions,
} from './app'

const bootstrap: Bootstrap = {
  version: 'test',
  working_dir: '/repo',
  current_session_id: 's1',
  sessions: [
    { id: 's1', parent_session_id: '', title: 'Parser refactor', message_count: 2, updated_at: 20, status: 'idle' },
    { id: 's2', parent_session_id: '', title: 'Tests', message_count: 0, updated_at: 10, status: 'running' },
  ],
  model: { id: 'm', name: 'M', provider: 'p', context_window: 1, agent_ready: true },
  agent_ready: true,
  permission_mode: 'normal',
  input_mode: 'code',
  lsp: {},
  mcp: {},
  agent_runs: [],
} as Bootstrap

function frame(type: string, payload: unknown, seq = 1, sessionId?: string): ParsedFrame {
  return { seq, type, payload, sessionId }
}

describe('reduceBootstrap', () => {
  it('fills the session list and the selected session', () => {
    const store = createAppStore()
    store.applyBootstrap(bootstrap)
    const state = store.get()
    expect(state.sessions.map((row) => row.id)).toEqual(['s1', 's2'])
    expect(state.selectedSessionId).toBe('s1')
    expect(state.bootstrap?.working_dir).toBe('/repo')
  })
})

describe('reduceFrame', () => {
  it('moves a session status without touching the list order', () => {
    const start = reduceBootstrap(createAppStore().get(), bootstrap)
    const next = reduceFrame(start, frame('session_status', { session_id: 's1', status: 'running' }, 5))
    expect(next.sessions[0].status).toBe('running')
    expect(next.sessions.map((row) => row.id)).toEqual(['s1', 's2'])
  })

  it('upserts a message by id', () => {
    const start = reduceBootstrap(createAppStore().get(), bootstrap)
    const first = reduceFrame(
      start,
      frame('message', { type: 'created', payload: { id: 'm1', role: 'user', session_id: 's1', parts: [], created_at: 1, updated_at: 1 } }, 2, 's1'),
    )
    expect(first.sessionData.s1.messages).toHaveLength(1)

    const second = reduceFrame(
      first,
      frame('message', { type: 'updated', payload: { id: 'm1', role: 'user', session_id: 's1', parts: [], created_at: 1, updated_at: 9 } }, 3, 's1'),
    )
    expect(second.sessionData.s1.messages).toHaveLength(1)
    expect(second.sessionData.s1.messages[0].updated_at).toBe(9)
  })

  it('upserts a session entity and keeps its status', () => {
    const start = reduceBootstrap(createAppStore().get(), bootstrap)
    const next = reduceFrame(
      start,
      frame('session', { type: 'updated', payload: { id: 's2', title: 'Renamed', message_count: 1, updated_at: 30 } }, 4),
    )
    expect(next.sessions[0].id).toBe('s2')
    expect(next.sessions[0].title).toBe('Renamed')
    expect(next.sessions[0].status).toBe('running')
  })

  it('drops a deleted session', () => {
    const start = reduceBootstrap(createAppStore().get(), bootstrap)
    const next = reduceFrame(start, frame('session', { type: 'deleted', payload: { id: 's2' } }, 5))
    expect(next.sessions.map((row) => row.id)).toEqual(['s1'])
  })

  it('replaces a session from its snapshot', () => {
    const start = reduceBootstrap(createAppStore().get(), bootstrap)
    const next = reduceFrame(
      start,
      frame('session_snapshot', {
        session_id: 's2',
        messages: [{ id: 'm2', role: 'user', session_id: 's2', parts: [], created_at: 1, updated_at: 1 }],
        has_more: true,
        files: [],
        busy: false,
        queued: ['later'],
      }, 6, 's2'),
    )
    expect(next.sessionData.s2.messages).toHaveLength(1)
    expect(next.sessionData.s2.hasMore).toBe(true)
    expect(next.sessionData.s2.queued).toEqual(['later'])
  })

  it('drops a deleted message', () => {
    const start = reduceBootstrap(createAppStore().get(), bootstrap)
    const withMessage = reduceFrame(
      start,
      frame('message', { type: 'created', payload: { id: 'm1', role: 'user', session_id: 's1', parts: [], created_at: 1, updated_at: 1 } }, 2, 's1'),
    )
    const next = reduceFrame(
      withMessage,
      frame('message', { type: 'deleted', payload: { id: 'm1', role: 'user', session_id: 's1', parts: [], created_at: 1, updated_at: 1 } }, 3, 's1'),
    )
    expect(next.sessionData.s1.messages).toHaveLength(0)
  })

  it('marks a session unread when its updated_at moves past what was seen', () => {
    const start = reduceBootstrap(createAppStore().get(), bootstrap)
    const state = reduceFrame(
      start,
      frame('session', { type: 'updated', payload: { id: 's2', title: 'Tests', message_count: 3, updated_at: 40 } }, 9),
    )
    expect(isUnread(state, 's2')).toBe(true)
    expect(isUnread(state, 's1')).toBe(false, "the open session is never unread")
  })

  it('marks the open session seen when its own update arrives', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s1')
    store.applyFrame(frame('session', { type: 'updated', payload: { id: 's1', title: 'Parser refactor', message_count: 3, updated_at: 55 } }, 10))
    expect(isUnread(store.get(), 's1')).toBe(false)
  })
})

describe('AppStore.selectSession', () => {
  it('marks the row seen at its updated_at and persists it', () => {
    const storage = memoryStorage()
    const store = createAppStore(storage)
    store.applyBootstrap(bootstrap)
    store.applyFrame(frame('session', { type: 'updated', payload: { id: 's2', title: 'Tests', message_count: 3, updated_at: 40 } }, 9))
    expect(isUnread(store.get(), 's2')).toBe(true)
    store.selectSession('s2')
    expect(isUnread(store.get(), 's2')).toBe(false)
    expect(storage.getItem('crush.seen')).toContain('"s2":40')
  })
})

describe('AppStore.forgetSession', () => {
  it('clears the selection and the messages of the deleted session', () => {
    const store = createAppStore(memoryStorage())
    store.applyBootstrap(bootstrap)
    store.selectSession('s2')
    store.forgetSession('s2')
    expect(store.get().sessions.map((row) => row.id)).toEqual(['s1'])
    expect(store.get().selectedSessionId).toBeNull()
    expect(store.get().sessionData.s2).toBeUndefined()
  })
})

describe('wantedSubscriptions', () => {
  it('follows the open session and every non-idle session', () => {
    const state = reduceBootstrap(createAppStore().get(), bootstrap)
    // bootstrap: s1 idle and current, s2 running.
    expect(wantedSubscriptions(state)).toEqual(['s1', 's2'])
    const other = { ...state, selectedSessionId: 's2' }
    expect(wantedSubscriptions(other)).toEqual(['s2'])
  })

  it('caps the set at the gateway limit', () => {
    const rows = Array.from({ length: maxSubscriptions + 5 }, (_, index) => ({
      id: `s${index}`,
      parent_session_id: '',
      title: `t${index}`,
      message_count: 0,
      updated_at: index,
      status: 'running' as const,
    }))
    const state = { ...reduceBootstrap(createAppStore().get(), bootstrap), sessions: rows, selectedSessionId: null }
    expect(wantedSubscriptions(state)).toHaveLength(maxSubscriptions)
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/store`
Expected: FAIL with `Failed to resolve import "./store"` and
`Failed to resolve import "./app"`.

- [ ] **Step 3: Write the generic store**

`web/src/store/store.ts`:

```ts
import { useRef, useSyncExternalStore } from 'react'

export interface Store<T> {
  get(): T
  set(update: Partial<T> | ((state: T) => Partial<T>)): void
  subscribe(listener: () => void): () => void
}

export function createStore<T extends object>(initial: T): Store<T> {
  let state = initial
  const listeners = new Set<() => void>()
  return {
    get: () => state,
    set: (update) => {
      const patch = isUpdater(update) ? update(state) : update
      let changed = false
      for (const key of Object.keys(patch) as (keyof T)[]) {
        if (!Object.is(state[key], patch[key])) {
          changed = true
          break
        }
      }
      if (!changed) {
        return
      }
      state = { ...state, ...patch }
      for (const listener of listeners) {
        listener()
      }
    },
    subscribe: (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
  }
}

// isUpdater narrows the union so the strict compiler sees the callable branch.
function isUpdater<T extends object>(update: Partial<T> | ((state: T) => Partial<T>)): update is (state: T) => Partial<T> {
  return typeof update === 'function'
}

// useStore reads a store through useSyncExternalStore. Selectors must return
// a primitive, or a value the caller has memoised: a fresh object on every
// read would loop.
export function useStore<T extends object, S>(store: Store<T>, selector: (state: T) => S): S {
  const cache = useRef<{ value: S } | null>(null)
  const getSnapshot = () => {
    const next = selector(store.get())
    if (cache.current === null || !Object.is(cache.current.value, next)) {
      cache.current = { value: next }
    }
    return cache.current.value
  }
  return useSyncExternalStore(store.subscribe, getSnapshot, getSnapshot)
}
```

- [ ] **Step 4: Write the seen preference**

`web/src/store/seen.ts`:

```ts
const seenKey = 'crush.seen'

// readSeen returns the last-seen updated_at per session. It survives a Crush
// restart because the timestamps come from the workspace, not from this run.
export function readSeen(storage: Storage = localStorage): Record<string, number> {
  const raw = storage.getItem(seenKey)
  if (!raw) {
    return {}
  }
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown>
    const out: Record<string, number> = {}
    for (const [sessionID, seq] of Object.entries(parsed)) {
      if (typeof seq === 'number') {
        out[sessionID] = seq
      }
    }
    return out
  } catch {
    return {}
  }
}

export function writeSeen(seen: Record<string, number>, storage: Storage = localStorage): void {
  storage.setItem(seenKey, JSON.stringify(seen))
}
```

`memoryStorage` comes from `web/src/test/memoryStorage.ts` (Task 8); it is
the seam every unit test injects.

- [ ] **Step 5: Write the app store**

`web/src/store/app.ts`:

```ts
import type { Bootstrap, ParsedFrame, SessionRow, SessionStatus } from '../api/wire'
import type { ConnectionState } from '../api/stream'
import { createStore, type Store } from './store'
import { readSeen, writeSeen } from './seen'

export interface WireMessage {
  id: string
  role: string
  session_id: string
  parts: { type: string; data: unknown }[]
  created_at: number
  updated_at: number
}

export interface SessionData {
  messages: WireMessage[]
  hasMore: boolean
  busy: boolean
  queued: string[]
  files: { path: string }[]
}

export interface AppState {
  connection: ConnectionState
  bootstrap: Bootstrap | null
  sessions: SessionRow[]
  selectedSessionId: string | null
  sessionData: Record<string, SessionData>
  lastSeen: Record<string, number>
}

export const emptySessionData: SessionData = {
  messages: [],
  hasMore: false,
  busy: false,
  queued: [],
  files: [],
}

export function createAppStore(storage: Storage = localStorage): Store<AppState> & AppStore {
  const store = createStore<AppState>({
    connection: 'connecting',
    bootstrap: null,
    sessions: [],
    selectedSessionId: null,
    sessionData: {},
    lastSeen: readSeen(storage),
  })
  return Object.assign(store, {
    applyBootstrap: (payload: Bootstrap) => store.set((state) => reduceBootstrap(state, payload)),
    applyFrame: (frame: ParsedFrame) => store.set((state) => reduceFrame(state, frame)),
    setConnection: (connection: ConnectionState) => store.set({ connection }),
    selectSession: (sessionID: string) => {
      store.set((state) => ({
        selectedSessionId: sessionID,
        lastSeen: markSeen(state.lastSeen, sessionID, updatedAtOf(state, sessionID)),
      }))
      writeSeen(store.get().lastSeen, storage)
    },
    // forgetSession drops a deleted session at once, without waiting for the
    // gateway's deleted frame: the row, its messages and a selection pointing
    // at it all go.
    forgetSession: (sessionID: string) => {
      store.set((state) => {
        const sessionData = { ...state.sessionData }
        delete sessionData[sessionID]
        return {
          sessions: state.sessions.filter((row) => row.id !== sessionID),
          sessionData,
          selectedSessionId: state.selectedSessionId === sessionID ? null : state.selectedSessionId,
        }
      })
    },
  })
}

export interface AppStore {
  applyBootstrap(payload: Bootstrap): void
  applyFrame(frame: ParsedFrame): void
  setConnection(connection: ConnectionState): void
  selectSession(sessionID: string): void
  forgetSession(sessionID: string): void
}

export function reduceBootstrap(state: AppState, payload: Bootstrap): AppState {
  return {
    ...state,
    bootstrap: payload,
    sessions: payload.sessions,
    // || and not ??: current_session_id is "" when no terminal is attached,
    // and an empty string must fall through to the first row.
    selectedSessionId: state.selectedSessionId || payload.current_session_id || payload.sessions[0]?.id || null,
  }
}

export function reduceFrame(state: AppState, frame: ParsedFrame): AppState {
  switch (frame.type) {
    case 'session_snapshot': {
      const snapshot = frame.payload as {
        session_id: string
        messages: WireMessage[]
        has_more: boolean
        busy: boolean
        queued: string[]
        files: { path: string }[]
      }
      const id = snapshot.session_id
      return {
        ...state,
        sessionData: {
          ...state.sessionData,
          [id]: {
            messages: snapshot.messages,
            hasMore: snapshot.has_more,
            busy: snapshot.busy,
            queued: snapshot.queued,
            files: snapshot.files,
          },
        },
      }
    }
    case 'session_status': {
      const payload = frame.payload as { session_id: string; status: SessionStatus }
      return {
        ...state,
        sessions: state.sessions.map((row) => (row.id === payload.session_id ? { ...row, status: payload.status } : row)),
      }
    }
    case 'session': {
      // A created or renamed session arrives as a full entity; a deleted one
      // leaves the list. The selected session is marked seen at the
      // updated_at the entity carries, which is how a tab that is open on a
      // session never shows it unread.
      const sessions = mergeSessionEvent(state.sessions, frame.payload)
      const id = sessionIDOf(frame.payload)
      const lastSeen =
        id !== null && id === state.selectedSessionId
          ? markSeen(state.lastSeen, id, updatedAtIn(sessions, id))
          : state.lastSeen
      return { ...state, sessions, lastSeen }
    }
    case 'message': {
      // A deleted message leaves the list; a created or updated one is
      // upserted by id, which is idempotent for a frame a snapshot echoed.
      const envelope = frame.payload as { type: string; payload: WireMessage }
      const id = envelope.payload.session_id
      const current = state.sessionData[id] ?? emptySessionData
      const messages =
        envelope.type === 'deleted'
          ? current.messages.filter((message) => message.id !== envelope.payload.id)
          : upsertMessage(current.messages, envelope.payload)
      return {
        ...state,
        sessionData: { ...state.sessionData, [id]: { ...current, messages } },
      }
    }
    case 'run_complete': {
      const payload = frame.payload as { type: string; payload: { session_id: string } }
      const id = payload.payload.session_id
      const current = state.sessionData[id] ?? emptySessionData
      return {
        ...state,
        sessionData: { ...state.sessionData, [id]: { ...current, busy: false } },
      }
    }
    default: {
      return state
    }
  }
}

function upsertMessage(messages: WireMessage[], next: WireMessage): WireMessage[] {
  const index = messages.findIndex((message) => message.id === next.id)
  if (index < 0) {
    return [...messages, next]
  }
  const copy = messages.slice()
  copy[index] = next
  return copy
}

function mergeSessionEvent(rows: SessionRow[], payload: unknown): SessionRow[] {
  const envelope = payload as { type: string; payload?: Record<string, unknown> }
  const entity = envelope.payload
  const id = typeof entity?.id === 'string' ? entity.id : ''
  if (!id) {
    return rows
  }
  if (envelope.type === 'deleted') {
    return rows.filter((row) => row.id !== id)
  }
  const existing = rows.find((row) => row.id === id)
  const fallback: SessionRow = existing ?? {
    id,
    parent_session_id: '',
    title: '',
    message_count: 0,
    updated_at: 0,
    status: 'idle',
  }
  const merged = { ...fallback, ...entity, status: existing?.status ?? 'idle' } as SessionRow
  const next = rows.filter((row) => row.id !== id).concat(merged)
  // Newest updated first, the ID breaking ties, the gateway's order.
  next.sort((a, b) => b.updated_at - a.updated_at || a.id.localeCompare(b.id))
  return next
}

// markSeen records the updated_at the person has seen for a session.
function markSeen(seen: Record<string, number>, sessionID: string, updatedAt: number): Record<string, number> {
  if ((seen[sessionID] ?? 0) >= updatedAt) {
    return seen
  }
  return { ...seen, [sessionID]: updatedAt }
}

function sessionIDOf(payload: unknown): string | null {
  const envelope = payload as { payload?: Record<string, unknown> }
  const id = envelope.payload?.id
  return typeof id === 'string' ? id : null
}

function updatedAtIn(rows: SessionRow[], sessionID: string): number {
  return rows.find((row) => row.id === sessionID)?.updated_at ?? 0
}

function updatedAtOf(state: AppState, sessionID: string): number {
  return updatedAtIn(state.sessions, sessionID)
}

// isUnread reports whether a session changed since the person last looked at
// it. The comparison is on the row's updated_at, which the workspace owns: it
// survives a Crush restart and needs no background subscription.
export function isUnread(state: AppState, sessionID: string): boolean {
  if (state.selectedSessionId === sessionID) {
    return false
  }
  return updatedAtIn(state.sessions, sessionID) > (state.lastSeen[sessionID] ?? 0)
}

// maxSubscriptions mirrors the gateway's per-connection cap.
export const maxSubscriptions = 32

// wantedSubscriptions is the set a tab follows: the selected session plus
// every session that is not idle, newest first, capped like the gateway does.
// The status comes from the global frames, so this needs no extra traffic to
// compute and only controls which session streams the tab receives.
export function wantedSubscriptions(state: AppState): string[] {
  const wanted = new Set<string>()
  if (state.selectedSessionId !== null) {
    wanted.add(state.selectedSessionId)
  }
  for (const row of state.sessions) {
    if (row.status !== 'idle') {
      wanted.add(row.id)
    }
  }
  return [...wanted].slice(0, maxSubscriptions)
}
```

- [ ] **Step 6: Run the store tests**

```bash
cd web && npx vitest run src/store && npm run typecheck
```

Expected: PASS.

- [ ] **Step 7: Run the suite and commit**

```bash
cd web && npm test && npm run build
cd .. && git add web/src/store internal/web/dist
git commit -m "feat(web): add the observable stores and unread markers"
```

---

## Task 13: The three-column frame and the preview banner

**Files:**
- Create: `web/src/layout/AppFrame.tsx`, `web/src/layout/AppFrame.module.css`
- Create: `web/src/layout/sidebarWidth.ts`
- Create: `web/src/layout/AppFrame.test.tsx`
- Create: `web/src/layout/sidebarWidth.test.ts`
- Modify: `web/src/App.tsx` (render the frame)
- Modify: `web/src/test/setup.ts` (a `matchMedia` stub for jsdom)

**Interfaces:**
- Consumes: nothing but React.
- Produces:
  - `function clampSidebarWidth(px: number): number` (264 to 420),
  - `const sidebarDefaultWidth = 280`, `sidebarRailWidth = 56`,
  - `<AppFrame sidebar={…} right={…}>{children}</AppFrame>`, rendering
    `data-collapsed` and `data-drawer` attributes the CSS keys on.
  Tasks 14 and 15 render into `sidebar` and the children slot.

- [ ] **Step 1: Write the failing layout tests**

`web/src/layout/sidebarWidth.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import { clampSidebarWidth, sidebarDefaultWidth } from './sidebarWidth'

describe('clampSidebarWidth', () => {
  it('keeps the default', () => {
    expect(sidebarDefaultWidth).toBe(280)
    expect(clampSidebarWidth(sidebarDefaultWidth)).toBe(280)
  })

  it('clamps to the 264 to 420 range', () => {
    expect(clampSidebarWidth(100)).toBe(264)
    expect(clampSidebarWidth(700)).toBe(420)
    expect(clampSidebarWidth(360)).toBe(360)
  })

  it('rounds a fractional drag', () => {
    expect(clampSidebarWidth(300.7)).toBe(301)
  })
})
```

`web/src/layout/AppFrame.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import AppFrame from './AppFrame'

describe('AppFrame', () => {
  it('shows the preview banner above everything', () => {
    render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    expect(screen.getByText('Preview: the new web UI is incomplete')).toBeInTheDocument()
    expect(screen.getByText('sidebar')).toBeInTheDocument()
    expect(screen.getByText('conversation')).toBeInTheDocument()
  })

  it('collapses the sidebar to the rail and back', async () => {
    const user = userEvent.setup()
    const { container } = render(
      <AppFrame sidebar={<p>sidebar</p>}>
        <p>conversation</p>
      </AppFrame>,
    )
    const frame = container.querySelector('[data-testid="app-frame"]')
    expect(frame?.getAttribute('data-collapsed')).toBe('false')
    await user.click(screen.getByRole('button', { name: 'Collapse sidebar' }))
    expect(frame?.getAttribute('data-collapsed')).toBe('true')
    await user.click(screen.getByRole('button', { name: 'Open sidebar' }))
    expect(frame?.getAttribute('data-collapsed')).toBe('false')
  })

  it('opens the sidebar as a modal drawer below the breakpoint', async () => {
    const user = userEvent.setup()
    const original = window.matchMedia
    window.matchMedia = ((query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia
    try {
      const { container } = render(
        <AppFrame sidebar={<p>sidebar</p>}>
          <p>conversation</p>
        </AppFrame>,
      )
      const frame = container.querySelector('[data-testid="app-frame"]')
      expect(frame?.getAttribute('data-drawer')).toBe('false')
      await user.click(screen.getByRole('button', { name: 'Open sidebar' }))
      expect(frame?.getAttribute('data-drawer')).toBe('true')
      expect(screen.getByRole('dialog', { name: 'Sessions' })).toBeInTheDocument()
      await user.keyboard('{Escape}')
      expect(frame?.getAttribute('data-drawer')).toBe('false')
    } finally {
      window.matchMedia = original
    }
  })
})
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/layout`
Expected: FAIL with `Failed to resolve import "./sidebarWidth"` and
`Failed to resolve import "./AppFrame"`.

- [ ] **Step 3: Stub matchMedia for jsdom**

`web/src/test/setup.ts` becomes:

```ts
import '@testing-library/jest-dom/vitest'

// jsdom has no matchMedia, and the frame asks it for the drawer breakpoint.
if (typeof window.matchMedia !== 'function') {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    }) as MediaQueryList
}
```

- [ ] **Step 4: Write the width helper**

`web/src/layout/sidebarWidth.ts`:

```ts
export const sidebarMinWidth = 264
export const sidebarMaxWidth = 420
export const sidebarDefaultWidth = 280
export const sidebarRailWidth = 56

// clampSidebarWidth keeps a drag inside the DSH range and whole pixels, so
// the grid never lands on a half-pixel column.
export function clampSidebarWidth(px: number): number {
  return Math.min(sidebarMaxWidth, Math.max(sidebarMinWidth, Math.round(px)))
}
```

- [ ] **Step 5: Write the frame**

`web/src/layout/AppFrame.tsx`:

```tsx
import { clsx } from 'clsx'
import { useEffect, useRef, useState, type ReactNode } from 'react'

import { clampSidebarWidth, sidebarDefaultWidth } from './sidebarWidth'
import styles from './AppFrame.module.css'

export interface AppFrameProps {
  sidebar: ReactNode
  right?: ReactNode
  children: ReactNode
}

export default function AppFrame({ sidebar, right, children }: AppFrameProps) {
  const [collapsed, setCollapsed] = useState(false)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [narrow, setNarrow] = useState(() => window.matchMedia('(max-width: 767px)').matches)
  const [width, setWidth] = useState(sidebarDefaultWidth)
  const frame = useRef<HTMLDivElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const toggle = useRef<HTMLButtonElement>(null)

  // The width rides a custom property on the frame element, so a drag updates
  // the layout without re-rendering the tree.
  useEffect(() => {
    frame.current?.style.setProperty('--crush-sidebar-width', `${width}px`)
  }, [width])

  // The breakpoint is a live query: a window narrowed past it opens the
  // sidebar as a drawer, and a window widened past it goes back to columns.
  useEffect(() => {
    const media = window.matchMedia('(max-width: 767px)')
    const onChange = () => setNarrow(media.matches)
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [])

  // A drag installs two window listeners; releasing or unmounting takes them
  // off, so a drag that ends outside the window still cleans up.
  const stopDrag = useRef<(() => void) | null>(null)
  useEffect(() => () => stopDrag.current?.(), [])

  const startResize = (event: React.PointerEvent<HTMLDivElement>) => {
    event.preventDefault()
    const originX = event.clientX
    const originWidth = width
    const move = (moveEvent: PointerEvent) => {
      setWidth(clampSidebarWidth(originWidth + (moveEvent.clientX - originX)))
    }
    const stop = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', stop)
      stopDrag.current = null
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', stop)
    stopDrag.current = stop
  }

  // The drawer is modal: focus moves into it, Tab cycles inside it, and
  // Escape closes it and returns focus to the toggle.
  useEffect(() => {
    if (!drawerOpen || panel.current === null) {
      return
    }
    const element = panel.current
    element.focus()
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setDrawerOpen(false)
        toggle.current?.focus()
        return
      }
      if (event.key !== 'Tab') {
        return
      }
      const focusable = element.querySelectorAll<HTMLElement>('button, [href], input, [tabindex]:not([tabindex="-1"])')
      if (focusable.length === 0) {
        return
      }
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [drawerOpen])

  const sidebarHidden = narrow ? !drawerOpen : collapsed

  return (
    <div className={styles.app}>
      <p className={styles.banner}>Preview: the new web UI is incomplete</p>
      <div
        ref={frame}
        className={styles.frame}
        data-testid="app-frame"
        data-collapsed={collapsed ? 'true' : 'false'}
        data-drawer={drawerOpen ? 'true' : 'false'}
      >
        <div
          ref={panel}
          className={clsx(styles.sidebar, collapsed && !narrow && styles.rail, narrow && drawerOpen && styles.drawer)}
          tabIndex={-1}
          role={narrow && drawerOpen ? 'dialog' : undefined}
          aria-modal={narrow && drawerOpen ? true : undefined}
          aria-label={narrow && drawerOpen ? 'Sessions' : undefined}
        >
          <button
            ref={toggle}
            type="button"
            className={styles.collapse}
            onClick={() => (narrow ? setDrawerOpen((value) => !value) : setCollapsed((value) => !value))}
            aria-label={sidebarHidden ? 'Open sidebar' : 'Collapse sidebar'}
            aria-expanded={!sidebarHidden}
          >
            {sidebarHidden ? '»' : '«'}
          </button>
          {sidebarHidden ? null : sidebar}
        </div>
        <div
          className={styles.resizer}
          role="separator"
          aria-orientation="vertical"
          onPointerDown={startResize}
        />
        <main className={styles.center}>{children}</main>
        {right ? <aside className={styles.right}>{right}</aside> : null}
      </div>
    </div>
  )
}
```
`web/src/layout/AppFrame.module.css`:

```css
.app {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.banner {
  margin: 0;
  height: var(--crush-layout-banner-height);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
  background: var(--dsw-alias-bg-layer-2);
  border-bottom: 1px solid var(--dsw-alias-border-l2);
}

.frame {
  flex: 1;
  display: grid;
  grid-template-columns: var(--crush-sidebar-width) 1px minmax(var(--crush-layout-center-min), 1fr);
  min-height: 0;
}

.frame[data-collapsed='true'] {
  grid-template-columns: var(--crush-layout-sidebar-rail) 0 minmax(0, 1fr);
}

.sidebar {
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
  background: var(--dsw-alias-bg-layer-1);
  border-right: 1px solid var(--dsw-alias-border-l2);
}

.rail {
  align-items: center;
}

.collapse {
  margin: 8px;
  padding: 4px 8px;
  border: 1px solid var(--dsw-alias-border-l2);
  border-radius: var(--dsw-radius-sm);
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.resizer {
  cursor: col-resize;
  background: transparent;
}

.center {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

.right {
  width: clamp(var(--crush-layout-center-min), var(--crush-layout-right-default), var(--crush-layout-right-max));
  border-left: 1px solid var(--dsw-alias-border-l2);
}

/* Below 768px the sidebar becomes a modal drawer over the conversation,
 * closed until the toggle opens it. */
@media (max-width: 767px) {
  .frame,
  .frame[data-collapsed='true'] {
    grid-template-columns: minmax(0, 1fr);
  }

  .sidebar {
    display: none;
  }

  .frame[data-drawer='true'] .sidebar {
    position: fixed;
    inset: var(--crush-layout-banner-height) auto 0 0;
    display: flex;
    width: min(var(--crush-layout-sidebar-max), 86vw);
    border-right: 1px solid var(--dsw-alias-border-l3);
    box-shadow: var(--dsw-alias-bg-mask-3) 0 0 0 100vmax;
    z-index: 10;
  }

  .drawer:focus {
    outline: none;
  }

  .resizer {
    display: none;
  }
}
```

The right column reserves nothing until M5: `AppFrame` renders it only when
a caller passes `right`, and the shell passes nothing.

- [ ] **Step 6: Render the frame from App**

`web/src/App.tsx` becomes:

```tsx
import AppFrame from './layout/AppFrame'

export default function App() {
  return (
    <AppFrame sidebar={<p>Workspace</p>}>
      <p>No session selected</p>
    </AppFrame>
  )
}
```

and delete `web/src/App.module.css` and its import; the banner now lives in
`AppFrame`.

Update `web/src/App.test.tsx` to the same banner assertion (it still passes,
since `AppFrame` renders the banner):

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import App from './App'

describe('App', () => {
  it('shows the preview banner', () => {
    render(<App />)
    expect(screen.getByText('Preview: the new web UI is incomplete')).toBeInTheDocument()
  })
})
```

- [ ] **Step 7: Run the layout tests**

```bash
cd web && npx vitest run src/layout src/App.test.tsx && npm run typecheck
```

Expected: PASS.

- [ ] **Step 8: Run the suite, build and commit**

```bash
cd web && npm test && npm run build && npm run budget
cd .. && git add web/src internal/web/dist
git commit -m "feat(web): add the three-column frame and preview banner"
```

---

## Task 14: Boot, auth and the unauthorized screen

**Files:**
- Create: `web/src/app/boot.ts`
- Create: `web/src/app/useGateway.ts`
- Create: `web/src/app/Unauthorized.tsx`, `web/src/app/Unauthorized.module.css`
- Create: `web/src/app/boot.test.ts`, `web/src/app/useGateway.test.tsx`
- Create: `web/src/app/Unauthorized.test.tsx`
- Modify: `web/src/App.tsx`

**Interfaces:**
- Consumes: `ApiClient`, `readTokenFromHash`, `stripTokenFromUrl`
  (Task 10); `StreamClient`, `ConnectionState` (Task 11);
  `createAppStore`, `AppState`, `AppStore` (Task 12); `bootstrapSchema`
  (Task 10); `AppFrame` (Task 13).
- Produces:
  - `type BootResult = { ok: true } | { ok: false; reason: 'unauthorized' | 'unreachable'; message: string }`
  - `async function boot(env: BootEnv): Promise<BootResult>`
  - `function useGateway(): { state: BootState; error: string | null;
    client: ApiClient; stream: StreamClient; store: Store<AppState> & AppStore }`
  - `<Unauthorized />`, the screen a tab without a token shows.
  Task 15 renders the sidebar into the frame this task wires, and Task 16
  renders the conversation beside it.

- [ ] **Step 1: Write the failing boot tests**

`web/src/app/boot.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'

import { boot } from './boot'

function env(overrides: Partial<Parameters<typeof boot>[0]> = {}) {
  return {
    client: {
      authenticated: false,
      authenticate: vi.fn(async () => undefined),
      // Typed as the generic it stands in for, so the mock is assignable.
      get: vi.fn(async (_path: string): Promise<unknown> => ({ ok: true })),
    },
    hash: '',
    stripHash: vi.fn(),
    ...overrides,
  }
}

describe('boot', () => {
  it('exchanges the hash token, strips it and checks the gateway', async () => {
    const e = env({ hash: '#t=abc123' })
    await expect(boot(e)).resolves.toEqual({ ok: true })
    expect(e.client.authenticate).toHaveBeenCalledWith('abc123')
    expect(e.stripHash).toHaveBeenCalled()
    expect(e.client.get).toHaveBeenCalledWith('/api/health')
  })

  it('keeps a stored token and skips the exchange', async () => {
    const e = env({ client: { ...env().client, authenticated: true } })
    await expect(boot(e)).resolves.toEqual({ ok: true })
    expect(e.client.authenticate).not.toHaveBeenCalled()
  })

  it('shows the unauthorized screen without a token', async () => {
    await expect(boot(env())).resolves.toMatchObject({ ok: false, reason: 'unauthorized' })
  })

  it('treats a refused token as unauthorized', async () => {
    const e = env({
      hash: '#t=bad',
      client: { ...env().client, authenticate: vi.fn(async () => Promise.reject(new Error('invalid token'))) },
    })
    await expect(boot(e)).resolves.toMatchObject({ ok: false, reason: 'unauthorized', message: 'invalid token' })
    expect(e.stripHash).not.toHaveBeenCalled()
  })

  it('reports an unreachable gateway without dropping the token', async () => {
    const e = env({
      client: { ...env().client, authenticated: true, get: vi.fn(async () => Promise.reject(new Error('offline'))) },
    })
    await expect(boot(e)).resolves.toMatchObject({ ok: false, reason: 'unreachable', message: 'offline' })
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/app/boot.test.ts`
Expected: FAIL with `Failed to resolve import "./boot"`.

- [ ] **Step 3: Write the boot sequence**

`web/src/app/boot.ts`:

```ts
import { readTokenFromHash } from '../api/http'

export interface BootEnv {
  client: {
    authenticated: boolean
    authenticate(token: string): Promise<void>
    get<T>(path: string): Promise<T>
  }
  hash: string
  stripHash(): void
}

export type BootResult =
  | { ok: true }
  | { ok: false; reason: 'unauthorized' | 'unreachable'; message: string }

// boot turns a launch link into an authenticated tab: exchange the token in
// the fragment, forget the fragment, then prove the gateway answers.
export async function boot(env: BootEnv): Promise<BootResult> {
  const fromHash = readTokenFromHash(env.hash)
  if (fromHash !== null) {
    try {
      await env.client.authenticate(fromHash)
    } catch (error) {
      return { ok: false, reason: 'unauthorized', message: messageOf(error) }
    }
    env.stripHash()
  } else if (!env.client.authenticated) {
    return { ok: false, reason: 'unauthorized', message: 'This tab has no token.' }
  }
  try {
    await env.client.get('/api/health')
  } catch (error) {
    return { ok: false, reason: 'unreachable', message: messageOf(error) }
  }
  return { ok: true }
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
```

- [ ] **Step 4: Run the boot test**

Run: `cd web && npx vitest run src/app/boot.test.ts`
Expected: PASS.

- [ ] **Step 5: Write the failing hook and screen tests**

`web/src/app/useGateway.test.tsx`:

```tsx
import { renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { useGateway } from './useGateway'

class FakeSocket {
  readyState = 1
  onopen: (() => void) | null = null
  onclose: ((event: { code: number }) => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  send(): void {}
  close(): void {}
  open(): void {
    this.onopen?.()
  }
}

function harness(hash: string, authenticated: boolean, health: () => Promise<unknown> = async () => ({ ok: true })) {
  const client = {
    authenticated,
    authenticate: vi.fn(async () => undefined),
    get: vi.fn(async (_path: string) => health()),
    wsProtocols: () => ['crush.v2', 'crush.auth.token'],
  }
  const socket = new FakeSocket()
  return {
    client,
    socket,
    options: {
      client,
      hash,
      stripHash: vi.fn(),
      createSocket: () => socket as unknown as WebSocket,
    },
  }
}

describe('useGateway', () => {
  it('becomes ready and starts the stream', async () => {
    const h = harness('#t=abc123', false)
    const { result } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('ready'))
    expect(result.current.stream.state).toBe('connecting')
  })

  it('stops on the unauthorized screen without a token', async () => {
    const h = harness('', false)
    const { result } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('unauthorized'))
  })

  it('reports an unreachable gateway as a network error, not a token one', async () => {
    const h = harness('#t=abc123', false, async () => Promise.reject(new Error('offline')))
    const { result } = renderHook(() => useGateway(h.options))
    await waitFor(() => expect(result.current.state).toBe('unreachable'))
    expect(result.current.error).toBe('offline')
  })
})
```

`web/src/app/Unauthorized.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import Unauthorized from './Unauthorized'

describe('Unauthorized', () => {
  it('points at the terminal command that makes a new link', () => {
    render(<Unauthorized />)
    expect(screen.getByText(/no valid token/i)).toBeInTheDocument()
    expect(screen.getByText('/web')).toBeInTheDocument()
  })
})
```

- [ ] **Step 6: Run them to verify they fail**

Run: `cd web && npx vitest run src/app`
Expected: `boot.test.ts` passes; the hook and screen tests fail with
`Failed to resolve import "./useGateway"` and `"./Unauthorized"`.

- [ ] **Step 7: Write the hook**

`web/src/app/useGateway.ts`:

```ts
import { useEffect, useMemo, useState } from 'react'

import { ApiClient, stripTokenFromUrl } from '../api/http'
import { StreamClient } from '../api/stream'
import { bootstrapSchema } from '../api/wire'
import { createAppStore } from '../store/app'
import type { AppState, AppStore } from '../store/app'
import type { Store } from '../store/store'
import { boot } from './boot'

export type BootState = 'authenticating' | 'ready' | 'unauthorized' | 'unreachable'

export interface GatewayOptions {
  client?: Pick<ApiClient, 'authenticated' | 'authenticate' | 'get' | 'wsProtocols'>
  hash?: string
  stripHash?: () => void
  createSocket?: (url: string, protocols: string[]) => WebSocket
  storage?: Storage
}

export interface Gateway {
  state: BootState
  error: string | null
  client: ApiClient
  stream: StreamClient
  store: Store<AppState> & AppStore
}

// useGateway owns the tab's connection: it authenticates once, then runs the
// stream for the life of the tab. The bootstrap frame is what fills the
// store, so the shell renders before any session is selected.
export function useGateway(options: GatewayOptions = {}): Gateway {
  const client = useMemo(() => (options.client as ApiClient) ?? new ApiClient(), [options.client])
  const store = useMemo(() => createAppStore(options.storage), [options.storage])
  const stream = useMemo(
    () =>
      new StreamClient({
        url: () => `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/api/stream`,
        protocols: () => client.wsProtocols(),
        handlers: {
          onBootstrap: (payload) => store.applyBootstrap(bootstrapSchema.parse(payload)),
          onFrame: (frame) => store.applyFrame(frame),
          onState: (state) => store.setConnection(state),
        },
        createSocket: options.createSocket,
      }),
    [client, store, options.createSocket],
  )
  const [state, setState] = useState<BootState>('authenticating')
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let mounted = true
    void boot({
      client,
      hash: options.hash ?? window.location.hash,
      stripHash: options.stripHash ?? (() => stripTokenFromUrl(window.location, window.history)),
    }).then((result) => {
      if (!mounted) {
        return
      }
      if (!result.ok) {
        setState(result.reason)
        setError(result.message)
        return
      }
      stream.start()
      setState('ready')
    })
    return () => {
      mounted = false
      stream.stop()
    }
  }, [client, stream, options.hash, options.stripHash])

  return { state, error, client, stream, store }
}
```

- [ ] **Step 8: Write the unauthorized screen**

`web/src/app/Unauthorized.tsx`:

```tsx
import styles from './Unauthorized.module.css'

export default function Unauthorized() {
  return (
    <main className={styles.screen}>
      <h1>Crush web mode</h1>
      <p>
        This tab has no valid token. Run <code>/web</code> in the terminal to open a new link.
      </p>
    </main>
  )
}
```

`web/src/app/Unreachable.tsx`, the screen a tab shows when the gateway does
not answer at all (the token may be perfectly good):

```tsx
import styles from './Unauthorized.module.css'

export interface UnreachableProps {
  message: string | null
}

export default function Unreachable({ message }: UnreachableProps) {
  return (
    <main className={styles.screen}>
      <h1>Crush is not answering</h1>
      <p>The tab could not reach its gateway. Check the terminal is still running, then reload.</p>
      {message ? <p className={styles.detail}>{message}</p> : null}
    </main>
  )
}
```

Add to `Unauthorized.module.css`:

```css
.detail {
  font-size: 12px;
  font-family: var(--ds-font-family-code);
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
}
```

`web/src/app/Unauthorized.module.css`:

```css
.screen {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 24px;
  text-align: center;
}

.screen h1 {
  margin: 0;
  font-size: 18px;
}

.screen p {
  margin: 0;
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
}
```

- [ ] **Step 9: Wire the shell**

`web/src/App.tsx` becomes:

```tsx
import AppFrame from './layout/AppFrame'
import Unauthorized from './app/Unauthorized'
import Unreachable from './app/Unreachable'
import { useGateway } from './app/useGateway'

export default function App() {
  const gateway = useGateway()

  if (gateway.state === 'authenticating') {
    return <p>Connecting to Crush…</p>
  }
  if (gateway.state === 'unreachable') {
    return <Unreachable message={gateway.error} />
  }
  if (gateway.state === 'unauthorized') {
    return <Unauthorized />
  }

  return (
    <AppFrame sidebar={<p>Workspace</p>}>
      <p>Select a session</p>
    </AppFrame>
  )
}
```

Task 15 replaces both placeholders with the real sidebar and conversation.

`web/src/App.test.tsx` must now inject a ready gateway, so replace it with:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import App from './App'

vi.mock('./app/useGateway', () => ({
  useGateway: () => ({
    state: 'ready',
    error: null,
    client: {},
    stream: { subscribe: vi.fn(), unsubscribe: vi.fn() },
    store: { get: () => ({ sessions: [], selectedSessionId: null }), subscribe: () => () => undefined, set: vi.fn() },
  }),
}))

describe('App', () => {
  it('shows the preview banner once the tab is ready', () => {
    render(<App />)
    expect(screen.getByText('Preview: the new web UI is incomplete')).toBeInTheDocument()
  })
})
```

- [ ] **Step 10: Run the app tests**

```bash
cd web && npx vitest run src/app src/App.test.tsx && npm run typecheck
```

Expected: PASS.

- [ ] **Step 11: Build and commit**

```bash
cd web && npm test && npm run build
cd .. && git add web/src internal/web/dist
git commit -m "feat(web): authenticate the tab and boot the shell"
```

---

## Task 15: The sidebar and the session actions

**Files:**
- Create: `web/src/sidebar/actions.ts`, `web/src/sidebar/actions.test.ts`
- Create: `web/src/sidebar/Sidebar.tsx`, `web/src/sidebar/Sidebar.module.css`
- Create: `web/src/sidebar/SessionRow.tsx`, `web/src/sidebar/SessionRow.module.css`
- Create: `web/src/sidebar/SessionRow.test.tsx`, `web/src/sidebar/Sidebar.test.tsx`
- Create: `web/src/sidebar/SidebarContainer.tsx`
- Modify: `web/src/App.tsx` (render the container in the frame)

**Interfaces:**
- Consumes: `ApiClient.send` (Task 10); `Store<AppState> & AppStore`,
  `isUnread` (Task 12); `StreamClient.subscribe`/`unsubscribe` (Task 11);
  `SessionRow`, `SessionStatus` (Task 10); `AppFrame` (Task 13).
- Produces:
  - `interface SessionActions { create(title?: string):
    Promise<SessionRow>; rename(id, title): Promise<void>; remove(id):
    Promise<void>; follow(id): Promise<void> }`
  - `function sessionActions(client: ApiClient): SessionActions`
  - `<Sidebar …>` (presentational) and `<SidebarContainer store stream
    actions>` (wired).
  Task 16 renders the conversation in the children slot next to this
  sidebar.

- [ ] **Step 1: Write the failing action tests**

`web/src/sidebar/actions.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest'

import type { ApiClient } from '../api/http'
import { sessionActions } from './actions'

function client() {
  return { send: vi.fn(async () => ({ id: 's2', title: 'New' })) } as unknown as ApiClient
}

describe('sessionActions', () => {
  it('creates a session with the default title', async () => {
    const c = client()
    const actions = sessionActions(c)
    await actions.create()
    expect(c.send).toHaveBeenCalledWith('POST', '/api/sessions', { title: 'New Session' })
  })

  it('renames only the title', async () => {
    const c = client()
    await sessionActions(c).rename('s2', 'Renamed')
    expect(c.send).toHaveBeenCalledWith('PUT', '/api/sessions/s2', { title: 'Renamed' })
  })

  it('deletes a session', async () => {
    const c = client()
    await sessionActions(c).remove('s2')
    expect(c.send).toHaveBeenCalledWith('DELETE', '/api/sessions/s2')
  })

  it('asks the terminal to open a session', async () => {
    const c = client()
    await sessionActions(c).follow('s2')
    expect(c.send).toHaveBeenCalledWith('POST', '/api/follow', { session_id: 's2' })
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/sidebar/actions.test.ts`
Expected: FAIL with `Failed to resolve import "./actions"`.

- [ ] **Step 3: Write the actions**

`web/src/sidebar/actions.ts`:

```ts
import type { ApiClient } from '../api/http'
import type { SessionRow } from '../api/wire'

export interface SessionActions {
  create(title?: string): Promise<SessionRow>
  rename(id: string, title: string): Promise<void>
  remove(id: string): Promise<void>
  follow(id: string): Promise<void>
}

export function sessionActions(client: ApiClient): SessionActions {
  return {
    create: (title = 'New Session') => client.send<SessionRow>('POST', '/api/sessions', { title }),
    rename: async (id, title) => {
      await client.send('PUT', `/api/sessions/${encodeURIComponent(id)}`, { title })
    },
    remove: async (id) => {
      await client.send('DELETE', `/api/sessions/${encodeURIComponent(id)}`)
    },
    follow: async (id) => {
      await client.send('POST', '/api/follow', { session_id: id })
    },
  }
}
```

- [ ] **Step 4: Write the failing row and sidebar tests**

`web/src/sidebar/SessionRow.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import SessionRow, { type SessionRowProps } from './SessionRow'

const row = {
  id: 's-m1-run',
  parent_session_id: '',
  title: 'Port the parser',
  message_count: 2,
  updated_at: 20,
  status: 'running' as const,
}

function props(overrides: Partial<SessionRowProps> = {}): SessionRowProps {
  return {
    row,
    selected: false,
    unread: false,
    onSelect: vi.fn(),
    onRename: vi.fn(),
    onDelete: vi.fn(),
    onCopyId: vi.fn(),
    onOpenInTerminal: vi.fn(),
    ...overrides,
  }
}

describe('SessionRow', () => {
  it('shows the title and the running marker', () => {
    render(<SessionRow {...props()} />)
    expect(screen.getByText('Port the parser')).toBeInTheDocument()
    expect(screen.getByLabelText('Running')).toBeInTheDocument()
  })

  it('shows the queued, waiting and unread markers', () => {
    render(<SessionRow {...props({ row: { ...row, status: 'queued' }, unread: true })} />)
    expect(screen.getByLabelText('Queued')).toBeInTheDocument()
    expect(screen.getByLabelText('Unread')).toBeInTheDocument()

    render(<SessionRow {...props({ row: { ...row, status: 'pending_interaction' } })} />)
    expect(screen.getByLabelText('Waiting for you')).toBeInTheDocument()
  })

  it('selects on click', async () => {
    const onSelect = vi.fn()
    render(<SessionRow {...props({ onSelect })} />)
    await userEvent.click(screen.getByText('Port the parser'))
    expect(onSelect).toHaveBeenCalledWith('s-m1-run')
  })

  it('renames inline on Enter', async () => {
    const onRename = vi.fn()
    render(<SessionRow {...props({ onRename })} />)
    await userEvent.click(screen.getByLabelText('Rename session'))
    const input = screen.getByLabelText('Session title')
    await userEvent.clear(input)
    await userEvent.type(input, 'Renamed{Enter}')
    expect(onRename).toHaveBeenCalledWith('s-m1-run', 'Renamed')
  })

  it('offers delete, copy and open in terminal', async () => {
    const onDelete = vi.fn()
    const onCopyId = vi.fn()
    const onOpenInTerminal = vi.fn()
    render(<SessionRow {...props({ onDelete, onCopyId, onOpenInTerminal })} />)
    await userEvent.click(screen.getByLabelText('Delete session'))
    await userEvent.click(screen.getByLabelText('Copy session ID'))
    await userEvent.click(screen.getByLabelText('Open in terminal'))
    expect(onDelete).toHaveBeenCalledWith('s-m1-run')
    expect(onCopyId).toHaveBeenCalledWith('s-m1-run')
    expect(onOpenInTerminal).toHaveBeenCalledWith('s-m1-run')
  })
})
```

`web/src/sidebar/Sidebar.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import Sidebar, { type SidebarProps } from './Sidebar'

const sessions = [
  { id: 's2', parent_session_id: '', title: 'Write parser tests', message_count: 0, updated_at: 20, status: 'queued' as const },
  { id: 's1', parent_session_id: '', title: 'Port the parser', message_count: 2, updated_at: 10, status: 'idle' as const },
]

function props(overrides: Partial<SidebarProps> = {}): SidebarProps {
  return {
    workspaceName: 'crush',
    branch: 'main',
    sessions,
    selectedSessionId: 's1',
    unread: () => false,
    error: null,
    onSelect: vi.fn(),
    onCreate: vi.fn(),
    onRename: vi.fn(),
    onDelete: vi.fn(),
    onCopyId: vi.fn(),
    onOpenInTerminal: vi.fn(),
    ...overrides,
  }
}

describe('Sidebar', () => {
  it('shows the workspace name and branch', () => {
    render(<Sidebar {...props()} />)
    expect(screen.getByText('crush')).toBeInTheDocument()
    expect(screen.getByText('main')).toBeInTheDocument()
  })

  it('lists the sessions in the order it was given', () => {
    render(<Sidebar {...props()} />)
    const titles = screen.getAllByTestId('session-title').map((node) => node.textContent)
    expect(titles).toEqual(['Write parser tests', 'Port the parser'])
  })

  it('creates a session', async () => {
    const onCreate = vi.fn()
    render(<Sidebar {...props({ onCreate })} />)
    await userEvent.click(screen.getByRole('button', { name: 'New Session' }))
    expect(onCreate).toHaveBeenCalled()
  })

  it('shows the refusal the gateway sent', () => {
    render(<Sidebar {...props({ error: 'The terminal is showing this session; switch it first' })} />)
    expect(screen.getByRole('alert')).toHaveTextContent('switch it first')
  })
})
```

- [ ] **Step 5: Run them to verify they fail**

Run: `cd web && npx vitest run src/sidebar`
Expected: `actions.test.ts` passes; the two component tests fail with
`Failed to resolve import "./SessionRow"` and `"./Sidebar"`.

- [ ] **Step 6: Write the row**

`web/src/sidebar/SessionRow.tsx`:

```tsx
import { clsx } from 'clsx'
import { useState } from 'react'

import type { SessionRow as SessionRowData } from '../api/wire'
import styles from './SessionRow.module.css'

export interface SessionRowProps {
  row: SessionRowData
  selected: boolean
  unread: boolean
  onSelect(id: string): void
  onRename(id: string, title: string): void
  onDelete(id: string): void
  onCopyId(id: string): void
  onOpenInTerminal(id: string): void
}

export default function SessionRow({
  row,
  selected,
  unread,
  onSelect,
  onRename,
  onDelete,
  onCopyId,
  onOpenInTerminal,
}: SessionRowProps) {
  const [renaming, setRenaming] = useState(false)
  const [draft, setDraft] = useState(row.title)

  const commit = () => {
    setRenaming(false)
    const title = draft.trim()
    if (title !== '' && title !== row.title) {
      onRename(row.id, title)
    }
  }

  return (
    <li className={clsx(styles.row, selected && styles.selected)} data-status={row.status}>
      <div className={styles.main}>
        {renaming ? (
          <input
            aria-label="Session title"
            className={styles.input}
            value={draft}
            autoFocus
            onChange={(event) => setDraft(event.target.value)}
            onBlur={commit}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                commit()
              }
              if (event.key === 'Escape') {
                setRenaming(false)
                setDraft(row.title)
              }
            }}
          />
        ) : (
          <button type="button" className={styles.title} onClick={() => onSelect(row.id)}>
            <span data-testid="session-title">{row.title}</span>
          </button>
        )}
        <span className={styles.markers}>
          {row.status === 'running' ? <span role="img" className={styles.running} aria-label="Running" /> : null}
          {row.status === 'queued' ? <span role="img" className={styles.queued} aria-label="Queued" /> : null}
          {row.status === 'pending_interaction' ? (
            <span role="img" className={styles.waiting} aria-label="Waiting for you" />
          ) : null}
          {unread ? <span role="img" className={styles.unread} aria-label="Unread" /> : null}
        </span>
      </div>
      <div className={styles.actions}>
        <button type="button" aria-label="Rename session" onClick={() => setRenaming(true)}>
          Rename
        </button>
        <button type="button" aria-label="Copy session ID" onClick={() => onCopyId(row.id)}>
          Copy ID
        </button>
        <button type="button" aria-label="Open in terminal" onClick={() => onOpenInTerminal(row.id)}>
          Open in terminal
        </button>
        <button type="button" aria-label="Delete session" onClick={() => onDelete(row.id)}>
          Delete
        </button>
      </div>
    </li>
  )
}
```

`web/src/sidebar/SessionRow.module.css`:

```css
.row {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 6px 8px;
  border-radius: var(--dsw-radius-sm);
}

.row:hover {
  background: var(--dsw-specific-sidebar-nav-item-active, var(--dsw-alias-bg-layer-2));
}

.selected {
  background: var(--dsw-specific-sidebar-nav-item-active-accent, var(--dsw-alias-bg-layer-2));
}

.main {
  display: flex;
  align-items: center;
  gap: 6px;
}

.title {
  flex: 1;
  overflow: hidden;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
}

.input {
  flex: 1;
  min-width: 0;
  border: 1px solid var(--dsw-alias-border-l3);
  border-radius: var(--dsw-radius-xs);
  background: var(--dsw-alias-bg-layer-2);
  color: inherit;
  font: inherit;
}

.markers {
  display: flex;
  gap: 4px;
  align-items: center;
}

.running,
.queued,
.waiting,
.unread {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.running {
  background: var(--dsw-static-green-500);
}

.queued {
  background: var(--dsw-static-amber-500);
}

.waiting {
  background: var(--dsw-static-deepseek-450);
}

.unread {
  background: var(--dsw-alias-label-primary);
}

.actions {
  display: flex;
  gap: 8px;
  font-size: 11px;
  visibility: hidden;
}

.row:hover .actions,
.row:focus-within .actions {
  visibility: visible;
}

.actions button {
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
  font-size: inherit;
  cursor: pointer;
}
```

- [ ] **Step 7: Write the sidebar**

`web/src/sidebar/Sidebar.tsx`:

```tsx
import type { SessionRow as SessionRowData } from '../api/wire'
import SessionRow from './SessionRow'
import styles from './Sidebar.module.css'

export interface SidebarProps {
  workspaceName: string
  branch?: string
  sessions: SessionRowData[]
  selectedSessionId: string | null
  unread(id: string): boolean
  error: string | null
  onSelect(id: string): void
  onCreate(): void
  onRename(id: string, title: string): void
  onDelete(id: string): void
  onCopyId(id: string): void
  onOpenInTerminal(id: string): void
}

export default function Sidebar({
  workspaceName,
  branch,
  sessions,
  selectedSessionId,
  unread,
  error,
  onSelect,
  onCreate,
  onRename,
  onDelete,
  onCopyId,
  onOpenInTerminal,
}: SidebarProps) {
  return (
    <nav className={styles.sidebar} aria-label="Sessions">
      <header className={styles.header}>
        <span className={styles.workspace}>{workspaceName}</span>
        {branch ? <span className={styles.branch}>{branch}</span> : null}
      </header>
      <button type="button" className={styles.new} onClick={onCreate}>
        New Session
      </button>
      {error ? (
        <p role="alert" className={styles.error}>
          {error}
        </p>
      ) : null}
      <ul className={styles.list}>
        {sessions.map((row) => (
          <SessionRow
            key={row.id}
            row={row}
            selected={row.id === selectedSessionId}
            unread={unread(row.id)}
            onSelect={onSelect}
            onRename={onRename}
            onDelete={onDelete}
            onCopyId={onCopyId}
            onOpenInTerminal={onOpenInTerminal}
          />
        ))}
      </ul>
    </nav>
  )
}
```

`web/src/sidebar/Sidebar.module.css`:

```css
.sidebar {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 8px;
  min-height: 0;
  padding: 8px;
  overflow-y: auto;
}

.header {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 4px 8px;
}

.workspace {
  font-weight: 600;
}

.branch {
  font-size: 11px;
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
}

.new {
  margin: 0 4px;
  padding: 6px 8px;
  border: 1px solid var(--dsw-alias-border-l3);
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-alias-bg-layer-2);
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.error {
  margin: 0 8px;
  padding: 6px 8px;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-static-red-400-a12, var(--dsw-alias-bg-layer-2));
  font-size: 12px;
}

.list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}
```

- [ ] **Step 8: Write the container**

`web/src/sidebar/SidebarContainer.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import type { ApiClient } from '../api/http'
import type { StreamClient } from '../api/stream'
import { isUnread, wantedSubscriptions, type AppState, type AppStore } from '../store/app'
import { useStore, type Store } from '../store/store'
import Sidebar from './Sidebar'
import { sessionActions } from './actions'

export interface SidebarContainerProps {
  client: ApiClient
  stream: StreamClient
  store: Store<AppState> & AppStore
}

export default function SidebarContainer({ client, stream, store }: SidebarContainerProps) {
  const sessions = useStore(store, (state) => state.sessions)
  const selected = useStore(store, (state) => state.selectedSessionId)
  const workspace = useStore(store, (state) => state.bootstrap?.working_dir ?? '')
  const branch = useStore(store, (state) => state.bootstrap?.branch ?? '')
  const [error, setError] = useState<string | null>(null)
  const actions = useMemo(() => sessionActions(client), [client])

  // The unread markers come from a derived string, so the list re-renders
  // exactly when a marker changes and not on every frame.
  const unreadIDs = useStore(store, (state) =>
    state.sessions
      .filter((row) => isUnread(state, row.id))
      .map((row) => row.id)
      .join(','),
  )
  const unread = useMemo(() => new Set(unreadIDs === '' ? [] : unreadIDs.split(',')), [unreadIDs])

  // One effect owns the connection's subscriptions: the open session plus
  // every session that is not idle. A background session that returns to
  // idle and is not selected is dropped, which keeps a busy workspace under
  // the gateway's 32-subscription cap.
  const subscriptionKey = useStore(store, (state) => wantedSubscriptions(state).join(','))
  const tracked = useRef<Set<string>>(new Set())
  useEffect(() => {
    const next = new Set(subscriptionKey === '' ? [] : subscriptionKey.split(','))
    for (const id of tracked.current) {
      if (!next.has(id)) {
        stream.unsubscribe(id)
      }
    }
    for (const id of next) {
      if (!tracked.current.has(id)) {
        stream.subscribe(id)
      }
    }
    tracked.current = next
  }, [subscriptionKey, stream])

  const workspaceName = workspace === '' ? 'Crush' : workspace.split('/').filter(Boolean).pop() ?? workspace

  const select = useCallback(
    (id: string) => {
      // The reconciler above subscribes the new selection and drops the old
      // one when it is idle; selecting only records the choice.
      store.selectSession(id)
    },
    [store],
  )

  const create = useCallback(() => {
    void actions
      .create()
      .then((row) => select(row.id))
      .catch((cause: unknown) => setError(message(cause)))
  }, [actions, select])

  const rename = useCallback(
    (id: string, title: string) => {
      void actions.rename(id, title).catch((cause: unknown) => setError(message(cause)))
    },
    [actions],
  )

  const remove = useCallback(
    (id: string) => {
      void actions
        .remove(id)
        .then(() => {
          // The deleted row, its messages and a selection pointing at it go
          // at once; the gateway's deleted frame would arrive later.
          store.forgetSession(id)
        })
        .catch((cause: unknown) => setError(message(cause)))
    },
    [actions, store],
  )

  const copyId = useCallback((id: string) => {
    void navigator.clipboard?.writeText(id)
  }, [])

  const openInTerminal = useCallback(
    (id: string) => {
      void actions.follow(id).catch((cause: unknown) => setError(message(cause)))
    },
    [actions],
  )

  return (
    <Sidebar
      workspaceName={workspaceName}
      branch={branch}
      sessions={sessions}
      selectedSessionId={selected}
      unread={(id) => unread.has(id)}
      error={error}
      onSelect={select}
      onCreate={create}
      onRename={rename}
      onDelete={remove}
      onCopyId={copyId}
      onOpenInTerminal={openInTerminal}
    />
  )
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause)
}
```

The unread set is derived from the store and memoised, so a marker change
re-renders the list and nothing else does. The subscription effect is the one
place that talks to the stream about sessions; Task 16's conversation only
renders what the store holds.

- [ ] **Step 9: Render it from App**

`web/src/App.tsx`:

```tsx
import Unauthorized from './app/Unauthorized'
import Unreachable from './app/Unreachable'
import { useGateway } from './app/useGateway'
import AppFrame from './layout/AppFrame'
import SidebarContainer from './sidebar/SidebarContainer'

export default function App() {
  const gateway = useGateway()

  if (gateway.state === 'authenticating') {
    return <p>Connecting to Crush…</p>
  }
  if (gateway.state === 'unreachable') {
    return <Unreachable message={gateway.error} />
  }
  if (gateway.state === 'unauthorized') {
    return <Unauthorized />
  }

  return (
    <AppFrame sidebar={<SidebarContainer client={gateway.client} stream={gateway.stream} store={gateway.store} />}>
      <p>Select a session</p>
    </AppFrame>
  )
}
```

and update the `App.test.tsx` mock so `client`, `stream` and `store` satisfy
the container: add `client: { send: vi.fn() }` and a `store` whose `get`
returns `{ sessions: [], selectedSessionId: null, bootstrap: null }`.

- [ ] **Step 10: Run the sidebar tests**

```bash
cd web && npx vitest run src/sidebar src/App.test.tsx && npm run typecheck
```

Expected: PASS.

- [ ] **Step 11: Build and commit**

```bash
cd web && npm test && npm run build && npm run budget
cd .. && git add web/src internal/web/dist
git commit -m "feat(web): add the session sidebar and its actions"
```

---

## Task 16: The read-only conversation

**Files:**
- Create: `web/src/conversation/Conversation.tsx`,
  `web/src/conversation/Conversation.module.css`
- Create: `web/src/conversation/MessageView.tsx`,
  `web/src/conversation/MessageView.module.css`
- Create: `web/src/conversation/ToolCallRow.tsx`
- Create: `web/src/conversation/ConversationContainer.tsx`
- Create: `web/src/conversation/MessageView.test.tsx`,
  `web/src/conversation/Conversation.test.tsx`
- Modify: `web/src/App.tsx` (render the conversation beside the sidebar)

**Interfaces:**
- Consumes: `WireMessage` and `SessionData` (Task 12); `ApiClient.get`
  (Task 10); `messagesResponse` shape `{messages, has_more}` from
  `GET /api/sessions/{sid}/messages?before=&limit=` (Task 2); the
  `session_snapshot` messages (Task 3).
- Produces:
  - `<MessageView message={WireMessage} />`: plain text, a collapsed
    reasoning block and one row per tool call.
  - `<Conversation sessionId title messages hasMore loadingOlder
    onLoadOlder />` (presentational) and `<ConversationContainer client
    stream store />` (wired).
  Markdown, syntax highlighting and the tool views arrive in M2; this task
  renders plain text so nothing is lost and nothing is invented. Chat
  virtualization (`@tanstack/react-virtual`) is deferred to M2 with the
  markdown renderer: plain text with paging renders a long session slowly in
  M1 alone.

- [ ] **Step 1: Write the failing message tests**

`web/src/conversation/MessageView.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { WireMessage } from '../store/app'
import MessageView from './MessageView'

function message(parts: WireMessage['parts'], role = 'assistant'): WireMessage {
  return { id: 'm1', role, session_id: 's1', parts, created_at: 1, updated_at: 2 }
}

function text(value: string) {
  return { type: 'text', data: { text: value } }
}

describe('MessageView', () => {
  it('renders the text of a user message', () => {
    render(<MessageView message={message([text('Refactor the parser')], 'user')} />)
    expect(screen.getByText('Refactor the parser')).toBeInTheDocument()
  })

  it('collapses reasoning behind a summary', () => {
    render(
      <MessageView
        message={message([{ type: 'reasoning', data: { thinking: 'Look at parser.go first.' } }, text('Reading it.')])}
      />,
    )
    const details = screen.getByText('Reasoning').closest('details')
    expect(details).not.toBeNull()
    expect(details?.open).toBe(false)
    expect(screen.getByText('Look at parser.go first.')).toBeInTheDocument()
    expect(screen.getByText('Reading it.')).toBeInTheDocument()
  })

  it('renders one row per tool call with its status', () => {
    render(
      <MessageView
        message={message([
          { type: 'tool_call', data: { id: 'tc-1', name: 'bash', input: '{}', finished: true } },
          { type: 'tool_call', data: { id: 'tc-2', name: 'view', input: '{}' } },
        ])}
      />,
    )
    expect(screen.getByText('bash')).toBeInTheDocument()
    expect(screen.getByText('done')).toBeInTheDocument()
    expect(screen.getByText('view')).toBeInTheDocument()
    expect(screen.getByText('running')).toBeInTheDocument()
  })

  it('renders nothing it does not understand', () => {
    const { container } = render(<MessageView message={message([{ type: 'unknown_part', data: {} }])} />)
    expect(container.querySelectorAll('[data-testid="tool-call"]')).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/conversation/MessageView.test.tsx`
Expected: FAIL with `Failed to resolve import "./MessageView"`.

- [ ] **Step 3: Write the message view**

`web/src/conversation/MessageView.tsx`:

```tsx
import { clsx } from 'clsx'

import type { WireMessage } from '../store/app'
import ToolCallRow from './ToolCallRow'
import styles from './MessageView.module.css'

export interface MessageViewProps {
  message: WireMessage
}

interface TextPart {
  text: string
}

interface ReasoningPart {
  thinking: string
}

interface ToolCallPart {
  id: string
  name: string
  finished?: boolean
}

export default function MessageView({ message }: MessageViewProps) {
  const texts: string[] = []
  const reasonings: string[] = []
  const calls: ToolCallPart[] = []
  for (const part of message.parts) {
    if (part.type === 'text') {
      texts.push((part.data as TextPart).text)
    } else if (part.type === 'reasoning') {
      reasonings.push((part.data as ReasoningPart).thinking)
    } else if (part.type === 'tool_call') {
      calls.push(part.data as ToolCallPart)
    }
  }

  return (
    <article className={clsx(styles.message, message.role === 'user' ? styles.user : styles.assistant)}>
      {reasonings.map((thinking, index) => (
        <details key={`reasoning-${index}`} className={styles.reasoning}>
          <summary>Reasoning</summary>
          <pre>{thinking}</pre>
        </details>
      ))}
      {texts.map((value, index) => (
        <p key={`text-${index}`} className={styles.text}>
          {value}
        </p>
      ))}
      {calls.length > 0 ? (
        <ul className={styles.tools}>
          {calls.map((call) => (
            <ToolCallRow key={call.id} name={call.name} finished={call.finished === true} />
          ))}
        </ul>
      ) : null}
    </article>
  )
}
```

`web/src/conversation/ToolCallRow.tsx`:

```tsx
import styles from './MessageView.module.css'

export interface ToolCallRowProps {
  name: string
  finished: boolean
}

export default function ToolCallRow({ name, finished }: ToolCallRowProps) {
  return (
    <li className={styles.tool} data-testid="tool-call">
      <span className={styles.toolName}>{name}</span>
      <span className={styles.toolStatus}>{finished ? 'done' : 'running'}</span>
    </li>
  )
}
```

```tsx
  it('renders nothing it does not understand', () => {
    const { container } = render(<MessageView message={message([{ type: 'unknown_part', data: {} }])} />)
    expect(container.querySelectorAll('li')).toHaveLength(0)
  })
```
and the row: `<li className={styles.tool} data-testid="tool-call">`.

`web/src/conversation/MessageView.module.css`:

```css
.message {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px 14px;
  border-radius: var(--dsw-radius-md);
}

.user {
  align-self: flex-end;
  max-width: 80%;
  background: var(--dsw-alias-bg-layer-2);
}

.assistant {
  align-self: stretch;
}

.text {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.reasoning {
  font-size: 12px;
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
}

.reasoning pre {
  margin: 4px 0 0;
  white-space: pre-wrap;
}

.tools {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.tool {
  display: flex;
  gap: 8px;
  align-items: center;
  font-family: var(--ds-font-family-code);
  font-size: var(--crush-font-size-code);
}

.toolName {
  font-weight: 600;
}

.toolStatus {
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
}
```

- [ ] **Step 4: Run the message tests**

Run: `cd web && npx vitest run src/conversation/MessageView.test.tsx`
Expected: PASS.

- [ ] **Step 5: Write the failing conversation test**

`web/src/conversation/Conversation.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import type { WireMessage } from '../store/app'
import Conversation from './Conversation'

const messages: WireMessage[] = [
  { id: 'm1', role: 'user', session_id: 's1', parts: [{ type: 'text', data: { text: 'hello' } }], created_at: 1, updated_at: 1 },
  { id: 'm2', role: 'assistant', session_id: 's1', parts: [{ type: 'text', data: { text: 'hi there' } }], created_at: 2, updated_at: 2 },
]

describe('Conversation', () => {
  it('renders the session title and its messages', () => {
    render(
      <Conversation sessionId="s1" title="Port the parser" messages={messages} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    expect(screen.getByRole('heading', { name: 'Port the parser' })).toBeInTheDocument()
    expect(screen.getByText('hello')).toBeInTheDocument()
    expect(screen.getByText('hi there')).toBeInTheDocument()
  })

  it('offers the older page only when there is one', async () => {
    const onLoadOlder = vi.fn()
    render(
      <Conversation sessionId="s1" title="t" messages={messages} hasMore loadingOlder={false} onLoadOlder={onLoadOlder} />,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Load older messages' }))
    expect(onLoadOlder).toHaveBeenCalled()
  })

  it('hides the button once the first page is the whole history', () => {
    render(
      <Conversation sessionId="s1" title="t" messages={messages} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    expect(screen.queryByRole('button', { name: 'Load older messages' })).toBeNull()
  })

  it('shows the reason an older page failed', () => {
    render(
      <Conversation
        sessionId="s1"
        title="t"
        messages={messages}
        hasMore
        loadingOlder={false}
        error="unknown_message"
        onLoadOlder={vi.fn()}
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent('unknown_message')
  })

  it('asks for a session when none is open', () => {
    render(
      <Conversation sessionId={null} title="" messages={[]} hasMore={false} loadingOlder={false} onLoadOlder={vi.fn()} />,
    )
    expect(screen.getByText('Select a session')).toBeInTheDocument()
  })
})
```

- [ ] **Step 6: Run it to verify it fails**

Run: `cd web && npx vitest run src/conversation/Conversation.test.tsx`
Expected: FAIL with `Failed to resolve import "./Conversation"`.

- [ ] **Step 7: Write the conversation**

`web/src/conversation/Conversation.tsx`:

```tsx
import { useEffect, useRef } from 'react'

import type { WireMessage } from '../store/app'
import MessageView from './MessageView'
import styles from './Conversation.module.css'

export interface ConversationProps {
  sessionId: string | null
  title: string
  messages: WireMessage[]
  hasMore: boolean
  loadingOlder: boolean
  error?: string | null
  onLoadOlder(): void
}

export default function Conversation({
  sessionId,
  title,
  messages,
  hasMore,
  loadingOlder,
  error = null,
  onLoadOlder,
}: ConversationProps) {
  const bottom = useRef<HTMLDivElement>(null)

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: 'end' })
  }, [messages.length, sessionId])

  if (sessionId === null) {
    return <p className={styles.empty}>Select a session</p>
  }

  return (
    <section className={styles.conversation} aria-label="Conversation">
      <header className={styles.header}>
        <h1 className={styles.title}>{title}</h1>
      </header>
      <div className={styles.log}>
        {hasMore ? (
          <button type="button" className={styles.older} onClick={onLoadOlder} disabled={loadingOlder}>
            {loadingOlder ? 'Loading…' : 'Load older messages'}
          </button>
        ) : null}
        {error ? (
          <p role="alert" className={styles.error}>
            {error}
          </p>
        ) : null}
        {messages.map((message) => (
          <MessageView key={message.id} message={message} />
        ))}
        <div ref={bottom} />
      </div>
    </section>
  )
}
```

`web/src/conversation/Conversation.module.css`:

```css
.conversation {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}

.header {
  padding: 8px 14px;
  border-bottom: 1px solid var(--dsw-alias-border-l2);
}

.title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
}

.log {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  overflow-y: auto;
}

.error {
  margin: 0 12px;
  padding: 6px 8px;
  border-radius: var(--dsw-radius-sm);
  background: var(--dsw-static-red-400-a12, var(--dsw-alias-bg-layer-2));
  font-size: 12px;
}

.older {
  align-self: center;
  padding: 4px 10px;
  border: 1px solid var(--dsw-alias-border-l3);
  border-radius: var(--dsw-radius-sm);
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.empty {
  margin: auto;
  color: var(--dsw-alias-label-secondary, var(--dsw-alias-label-primary));
}
```

- [ ] **Step 8: Write the container**

`web/src/conversation/ConversationContainer.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useState } from 'react'

import type { ApiClient } from '../api/http'
import { emptySessionData, type AppState, type AppStore, type WireMessage } from '../store/app'
import { useStore, type Store } from '../store/store'
import Conversation from './Conversation'

export interface ConversationContainerProps {
  client: ApiClient
  store: Store<AppState> & AppStore
}

interface MessagesPage {
  messages: WireMessage[]
  has_more: boolean
}

export default function ConversationContainer({ client, store }: ConversationContainerProps) {
  const sessionId = useStore(store, (state) => state.selectedSessionId)
  const title = useStore(store, (state) => state.sessions.find((row) => row.id === state.selectedSessionId)?.title ?? '')
  const data = useStore(store, (state) => (state.selectedSessionId ? state.sessionData[state.selectedSessionId] : undefined))
  const [older, setOlder] = useState<WireMessage[]>([])
  // olderHasMore is the answer the last page gave; null means no page has been
  // loaded yet, and the snapshot's own has_more decides.
  const [olderHasMore, setOlderHasMore] = useState<boolean | null>(null)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // A new session starts from its snapshot, so the pages loaded for the
  // previous one are dropped.
  useEffect(() => {
    setOlder([])
    setOlderHasMore(null)
    setError(null)
  }, [sessionId])

  const loadOlder = useCallback(() => {
    if (!sessionId) {
      return
    }
    const current = store.get().sessionData[sessionId] ?? emptySessionData
    // The next page ends just before the oldest message already on screen,
    // whichever page it came from.
    const before = (older[0] ?? current.messages[0])?.id
    const query = before ? `?before=${encodeURIComponent(before)}` : ''
    setLoadingOlder(true)
    setError(null)
    void client
      .get<MessagesPage>(`/api/sessions/${encodeURIComponent(sessionId)}/messages${query}`)
      .then((page) => {
        setOlder((previous) => [...page.messages, ...previous])
        setOlderHasMore(page.has_more)
      })
      .catch((cause: unknown) => setError(cause instanceof Error ? cause.message : String(cause)))
      .finally(() => setLoadingOlder(false))
  }, [client, older, sessionId, store])

  // A resync replaces the snapshot's messages, so a message can be in both
  // lists; the first copy wins and the order stays oldest first.
  const messages = useMemo(() => {
    const seen = new Set<string>()
    const out: WireMessage[] = []
    for (const message of [...older, ...(data?.messages ?? [])]) {
      if (seen.has(message.id)) {
        continue
      }
      seen.add(message.id)
      out.push(message)
    }
    return out
  }, [older, data?.messages])

  const hasMore = olderHasMore === null ? (data?.hasMore ?? false) : olderHasMore

  return (
    <Conversation
      sessionId={sessionId}
      title={title}
      messages={messages}
      hasMore={hasMore}
      loadingOlder={loadingOlder}
      error={error}
      onLoadOlder={loadOlder}
    />
  )
}
```

- [ ] **Step 9: Render it from App**

In `web/src/App.tsx`, replace the frame's children placeholder with:

```tsx
      <ConversationContainer client={gateway.client} store={gateway.store} />
```

and import it. Update `App.test.tsx`'s mock so `store.get` also returns
`sessionData: {}` and `sessions: []`; the conversation then shows its
"Select a session" state.

- [ ] **Step 10: Run the conversation tests**

```bash
cd web && npx vitest run src/conversation src/App.test.tsx && npm run typecheck
```

Expected: PASS. Remove `web/src/App.module.css` if it still exists.

- [ ] **Step 11: Build and commit**

```bash
cd web && npm test && npm run build && npm run budget
cd .. && git add web/src internal/web/dist
git commit -m "feat(web): render the conversation read-only"
```

---

## Task 17: Playwright and the axe scan

**Files:**
- Create: `web/playwright.config.ts`
- Create: `web/e2e/support/webfake.ts`
- Create: `web/e2e/shell.spec.ts`
- Create: `web/e2e/accessibility.spec.ts`
- Modify: `web/package.json` (Playwright and axe devDependencies, `e2e` script)
- Modify: `web/tsconfig.json` (include `e2e` and `playwright.config.ts`)
- Modify: `.github/workflows/web.yml` (e2e job)

**Interfaces:**
- Consumes: `WEBFAKE_SCENARIO=m1` and its seeded sessions (Task 7); the shell
  (Tasks 13 to 16).
- Produces: the browser suite CI runs, in three shards, against
  `go run ./internal/web/cmd/webfake`.

- [ ] **Step 1: Add the dependencies**

In `web/package.json`, add to `devDependencies`:

```json
    "@axe-core/playwright": "^4.10.1",
    "@playwright/test": "^1.50.0",
    "@types/node": "^22.10.7",
```

and to `scripts`:

```json
    "e2e": "playwright test",
```

Then:

```bash
cd web && npm install && npx playwright install chromium
```

- [ ] **Step 2: Include the e2e tree in the typecheck**

`web/tsconfig.json`: add `"e2e", "playwright.config.ts"` to `include` and
`"node"` to `types`.

- [ ] **Step 3: Write the config and the fixture**

`web/playwright.config.ts`:

```ts
import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
})
```

`web/e2e/support/webfake.ts`:

```ts
import { test as base, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

export interface Webfake {
  origin: string
  launchUrl: string
}

const startTimeoutMs = 120_000

// startWebfake builds the fake gateway once, then runs the binary for this
// worker. It has to be a binary and not `go run`: a signal to the go tool
// would leave the server running, and the port would leak between workers.
// The gateway binds a random loopback port and prints its launch URL on
// stdout; the URL carries the token in the fragment and is never logged.
async function startWebfake(): Promise<{ app: Webfake; stop: () => void }> {
  const root = fileURLToPath(new URL('../../..', import.meta.url))
  const binary = join(tmpdir(), `webfake-e2e-${process.pid}`)
  await new Promise<void>((resolve, reject) => {
    const build = spawn('go', ['build', '-o', binary, './internal/web/cmd/webfake'], { cwd: root })
    build.on('exit', (code) => (code === 0 ? resolve() : reject(new Error(`webfake build exited with ${code}`))))
  })
  const child = spawn(binary, [], {
    cwd: root,
    env: { ...process.env, WEBFAKE_SCENARIO: 'm1', WEBFAKE_PROJECT_DIR: '/tmp/crush-e2e-project' },
  })

  const launchUrl = await new Promise<string>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('webfake did not print a launch URL')), startTimeoutMs)
    let buffered = ''
    child.stdout.on('data', (chunk: Buffer) => {
      buffered += chunk.toString()
      const line = buffered.split('\n')[0]?.trim()
      if (line && line.startsWith('http://127.0.0.1:')) {
        clearTimeout(timer)
        resolve(line)
      }
    })
    child.on('exit', (code) => {
      clearTimeout(timer)
      reject(new Error(`webfake exited with ${code}`))
    })
  })

  const origin = launchUrl.split('/#t=')[0]
  return {
    app: { origin, launchUrl },
    stop: () => {
      child.kill('SIGTERM')
      rmSync(binary, { force: true })
    },
  }
}

export const test = base.extend<Record<string, never>, { app: Webfake }>({
  app: [
    async ({}, use) => {
      const { app, stop } = await startWebfake()
      await use(app)
      stop()
    },
    { scope: 'worker' },
  ],
})

export { expect }
```

- [ ] **Step 4: Write the shell spec**

`web/e2e/shell.spec.ts`:

```ts
import { expect, test } from './support/webfake'

test('boots from the launch link and drops the token from the URL', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await expect(page.getByText('Preview: the new web UI is incomplete')).toBeVisible()
  await expect(page.getByTestId('app-frame')).toBeVisible()
  expect(page.url()).not.toContain('#t=')
})

test('lists the seeded sessions with their markers', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await expect(page.getByText('Port the parser')).toBeVisible()
  await expect(page.getByText('Write parser tests')).toBeVisible()
  await expect(page.getByText('Rename the grammar')).toBeVisible()
  await expect(page.locator('[aria-label="Running"]').first()).toBeVisible()
  await expect(page.locator('[aria-label="Queued"]').first()).toBeVisible()
  await expect(page.locator('[aria-label="Waiting for you"]').first()).toBeVisible()
})

test('opens one session after another', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await page.getByText('Port the parser').click()
  await expect(page.getByText('Port the tokenizer')).toBeVisible()
  await page.getByText('Write parser tests').click()
  await expect(page.getByText('Draft the table tests')).toBeVisible()
})

test('shows two live sessions and a marker that appears without a reload', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  // s-m1-run is running from the seed.
  await expect(page.locator('li', { hasText: 'Port the parser' }).locator('[aria-label="Running"]')).toBeVisible()
  // webfake publishes s-m1-wait's permission a moment after the gateway
  // starts, so its marker appears on a page that is already open.
  await expect(
    page.locator('li', { hasText: 'Rename the grammar' }).locator('[aria-label="Waiting for you"]'),
  ).toBeVisible({ timeout: 15_000 })
})

test('keeps a background session live while another is open', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await page.getByText('Write parser tests').click()
  await expect(page.getByText('Draft the table tests')).toBeVisible()
  // The running session is not the open one, and its marker stays: the tab
  // keeps a subscription per running session.
  const running = page.locator('li', { hasText: 'Port the parser' })
  await expect(running.locator('[aria-label="Running"]')).toBeVisible()
})

test('shows the reason the gateway refused a delete', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  // s-m1-run is the busy session SeedM1 leaves a run in, so the gateway
  // answers 409 session_busy and the sidebar shows its sentence.
  const row = page.locator('li', { hasText: 'Port the parser' })
  await row.hover()
  await row.getByLabel('Delete session').click()
  await expect(page.getByRole('alert')).toContainText('working in this session')
})
```

- [ ] **Step 5: Write the accessibility spec**

`web/e2e/accessibility.spec.ts`:

```ts
import AxeBuilder from '@axe-core/playwright'

import { expect, test } from './support/webfake'

test('the shell has no serious accessibility violations', async ({ page, app }) => {
  await page.goto(app.launchUrl)
  await expect(page.getByText('Preview: the new web UI is incomplete')).toBeVisible()
  await page.getByText('Port the parser').click()
  await expect(page.getByText('Port the tokenizer')).toBeVisible()

  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  const serious = results.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical')
  expect(serious).toEqual([])
})
```

- [ ] **Step 6: Run the suite**

```bash
cd web && npm run typecheck && npm run e2e
```

Expected: every spec passes. The first run compiles the webfake, which
takes a minute; the worker fixture allows two.

- [ ] **Step 7: Put the Taskfile task back**

`web:e2e` was removed with the suite in Task 8; restore it beside the other
`web:*` tasks in `Taskfile.yaml`:

```yaml
  web:e2e:
    desc: Run Playwright smoke tests against the fake gateway
    dir: web
    cmds:
      - npm run e2e
```

- [ ] **Step 8: Restore the e2e CI job**

In `.github/workflows/web.yml`, replace the `e2e` job's steps with:

```yaml
  e2e:
    runs-on: ubuntu-26.04
    # The whole suite runs on one worker; a shard is a third of the spec
    # files. 25 minutes covers setup, the webfake build, the browser install
    # and a slow test with margin.
    timeout-minutes: 25
    strategy:
      fail-fast: false
      matrix:
        shard: [1, 2, 3]
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0
        with:
          node-version-file: web/.nvmrc
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
        working-directory: web
      - run: npx playwright install --with-deps chromium
        working-directory: web
      # A shard that owns no spec file must not fail the matrix.
      - run: npx playwright test --shard=${{ matrix.shard }}/3 --pass-with-no-tests
        working-directory: web
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        if: failure()
        with:
          name: playwright-results-${{ matrix.shard }}
          path: |
            web/test-results
            web/playwright-report
          if-no-files-found: ignore
```

Also add `'web/e2e/**'` to neither trigger list: `'web/**'` already covers it.

- [ ] **Step 9: Commit**

```bash
git add web Taskfile.yaml .github/workflows/web.yml
git commit -m "test(web): run the shell in Playwright with an axe scan"
```

---

## Task 18: Whole-branch verification

No commit: this task either passes or sends the work back to the task that
broke it. Run it from a clean checkout of `main` after PRs 1 to 17 merged.

**Files:** none.

**Interfaces:**
- Consumes: every task above.
- Produces: a verified M1 on `main`, and the baseline M2 builds on.

- [ ] **Step 1: Run the Go side**

```bash
go build ./...
go test ./internal/web/... ./internal/wire/... ./internal/ui/... ./internal/config/... -count=1
go vet ./...
gofumpt -l . | tee /tmp/gofumpt-m1.txt
test ! -s /tmp/gofumpt-m1.txt
./scripts/check_log_capitalization.sh
```

Expected: the build prints nothing, every package reports `ok`, `go vet`
prints nothing, `gofumpt -l` finds nothing and the log check exits 0.

- [ ] **Step 2: Run the frontend side**

```bash
cd web
npm ci
npm run typecheck
npm test
npm run build
npm run budget
cd ..
git diff --exit-code -- internal/web/dist
test -z "$(git status --porcelain --untracked-files=all -- internal/web/dist)"
```

Expected: typecheck quiet, every test passing, and the committed build
matching a fresh one byte for byte.

- [ ] **Step 3: Run the browser suite**

```bash
cd web && npm run e2e && cd ..
```

Expected: the shell, session and accessibility specs pass.

- [ ] **Step 4: Prove the milestone's contract end to end**

```bash
WEBFAKE_PROJECT_DIR=/tmp/crush-m1-check WEBFAKE_SCENARIO=m1 go run ./internal/web/cmd/webfake > /tmp/webfake-url.txt 2>/tmp/webfake-err.txt &
sleep 5
url=$(head -1 /tmp/webfake-url.txt)
origin=${url%%/#t=*}
token=${url##*#t=}
curl -sS "$origin/" | grep -c 'id="root"'
curl -sS "$origin/api/bootstrap" -H "Authorization: Bearer $token" | grep -c '"status":"running"'
curl -sS "$origin/api/bootstrap" -H "Authorization: Bearer $token" | grep -c '"status":"pending_interaction"'
curl -sS -o /dev/null -w '%{http_code}\n' "$origin/api/snapshot" -H "Authorization: Bearer $token"
kill %1
```

Expected: `1`, then at least `1`, then at least `1`, then `404` (the M0
removal still holds).

- [ ] **Step 5: Check the milestone's scope by hand**

Run `go run . --web` in a real session and confirm, in the tab:

1. The preview banner reads `Preview: the new web UI is incomplete`.
2. The sidebar shows the workspace directory name and the branch.
3. A running TUI turn turns the session's marker green within a second, and
   clears when the turn ends: the status frames are event-driven, so the
   change appears as the event lands and not on a poll.
4. A background session whose `updated_at` moves (it ran, or its title
   changed) gains an unread dot; selecting it clears the dot, and it stays
   cleared across a page reload.
5. `/web` in the session copies a link that opens a second working tab.
6. Reloading the tab keeps it signed in (the token is in `sessionStorage`).
7. Collapsing the sidebar leaves the 56 px rail, and a window narrower than
   768 px opens the sidebar as a drawer.

- [ ] **Step 6: Check nothing from the spec regressed**

```bash
grep -rn '/api/snapshot' --include=*.go --include=*.ts . | grep -v docs/ | grep -v node_modules || echo "snapshot route still gone"
grep -rnE 'crush\.v1\b' --include=*.go --include=*.ts . | grep -v docs/ | grep -v node_modules || echo "no v1 client"
grep -rnE '@font-face|\.woff2?' web/src web/index.html || echo "no bundled fonts"
grep -rn 'token' web/e2e/*.ts web/e2e/support/*.ts | grep -v launchUrl || echo "the specs never touch the token"
```

Expected: four echoes. `crush.v2` and the token entry stay in the client; the
specs reach the token only through `launchUrl`, never logging it, and no font
file is bundled (the theme uses the system stack, no Montserrat).

---

### Decisions this plan made that the brief did not

- Status precedence: `pending_interaction` outranks `running`, which
  outranks `queued`.
- Unread markers compare a session row's `updated_at` with a per-session
  last-seen `updated_at` (ruling): they survive a Crush restart and need no
  background subscription. A title-only update can fire a marker, which the
  ruling accepts.
- The tab subscribes the selected session plus every non-idle session, capped
  at 32 (ruling); a background session that returns to idle and is not
  selected is dropped.
- `subscribe` reserves the session before reading its snapshot and buffers
  the frames published meanwhile (ruling), so the snapshot is always first.
- Chat virtualization is deferred to M2 with markdown (ruling).
- There is no periodic status poll (ruling). The pump recomputes one
  session's status on the events that can change it (permission and question
  created or resolved, run complete, a user prompt starting a run or joining
  the queue), and the queue-clear and delete routes call the same refresh or
  forget directly. A recompute that finds no change publishes nothing. A
  status change with no event at all stays stale until the next event; that
  trade buys zero SQLite calls per second while a gateway idles.
- `?before` with an id the session does not have answers `404
  unknown_message`; a `limit` below 1 answers `400 bad_limit`; a `limit`
  above 500 clamps to 500.
- `GET /api/sessions` stays a bare JSON array of rows, now with `status`;
  making it an envelope would break the shape M0 left behind for no gain.
- The `session_snapshot`'s `seq` is the hub head at subscribe time, and
  `web_error` replies carry the head too; neither is recorded in the ring.
- The copied DSH dark selectors are rewritten from `body[data-ds-dark-theme]`
  to `html[data-ds-theme='dark'] body`, because the brief puts the theme on
  `<html>`. The file header records the change.
- The golden contract covers every frame type `internal/web` constructs;
  `skills_event` and `host_theme` are forwarded from the client/server wire
  with constructors outside this package and join the golden in M6.
- The e2e fixture spawns `go run ./internal/web/cmd/webfake` per worker and
  reads the launch URL from stdout, because the gateway binds a random port
  and Playwright's `webServer` cannot learn it.

### Notes for whoever executes this

- The Go tests must keep working at every intermediate commit: the frontend
  does not exist until Task 8, and Task 8 replaces the placeholder page the
  gateway tests assert.
- `internal/ui/model/*web*_test.go` keeps covering the TUI's own web
  plumbing; nothing in this plan touches it.
- `docs/web-mode.md` still describes the old UI with a "being rebuilt" note
  from M0. Leave it: M6 rewrites it.
- If a reviewer wants the preview banner to be dismissible, that is M6's
  concern, not M1's: the banner is how a person knows the shell is
  incomplete.
