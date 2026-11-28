package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/wire"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type testFrame struct {
	Seq     uint64          `json:"seq"`
	Type    string          `json:"type"`
	Session string          `json:"session_id,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

func dialStream(t *testing.T, g *Gateway, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// The 101 body is the hijacked connection, so the websocket owns it.
	c, rsp, err := websocket.Dial(ctx, strings.Replace(g.URL(), "http", "ws", 1)+"/api/stream"+query, //nolint:bodyclose // See above.
		&websocket.DialOptions{Subprotocols: []string{streamProtocol, authProtocolPrefix + g.token()}})
	require.NoError(t, err)
	require.Equal(t, streamProtocol, rsp.Header.Get("Sec-WebSocket-Protocol"))
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func readFrame(t *testing.T, c *websocket.Conn) testFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	require.NoError(t, err)
	var f testFrame
	require.NoError(t, json.Unmarshal(data, &f))
	return f
}

func msgEvent(id, sid, text string) pubsub.Event[message.Message] {
	return pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
		ID: id, SessionID: sid, Role: message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}}
}

func itoa(n uint64) string { return strconv.FormatUint(n, 10) }

func webtestFake() *webtest.Fake { return webtest.NewFake() }

func TestStreamStartsWithBootstrapThenLive(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	snap := readFrame(t, c)
	require.Equal(t, frameBootstrap, snap.Type)
	var s Bootstrap
	require.NoError(t, json.Unmarshal(snap.Payload, &s))
	require.Equal(t, "s1", s.CurrentSessionID)
	require.Equal(t, "/tmp/project", s.WorkingDir)
	require.Equal(t, "code", s.InputMode)

	// A message frame is per-session, so the tab asks for the session
	// after consuming the bootstrap.
	subscribeStream(t, c, "s1")
	fake.Emit(msgEvent("m1", "s1", "hello"))
	f := nextFrameOfType(t.Context(), t, c, "message")
	require.Equal(t, "message", f.Type)
	require.Equal(t, "s1", f.Session)
	require.Equal(t, snap.Seq+1, f.Seq)
	require.Contains(t, string(f.Payload), `"hello"`)
}

func TestStreamSinceTooOldGetsBootstrap(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for i := range ringSize + 10 {
		fake.Emit(msgEvent("m"+itoa(uint64(i)), "s1", "x"))
	}
	require.Eventually(t, func() bool { return g.hub.head() >= ringSize+10 }, 5*time.Second, 10*time.Millisecond)
	c := dialStream(t, g, "?since=1")
	require.Equal(t, frameBootstrap, readFrame(t, c).Type)
}

func TestSnapshotIncludesLSPAndMCP(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.LSP = map[string]workspace.LSPClientInfo{
		"gopls": {Name: "gopls", State: lsp.StateReady, DiagnosticCount: 2},
	}
	fake.MCP = map[string]mcp.ClientInfo{
		"fs": {Name: "fs", State: mcp.StateConnected, Counts: mcp.Counts{Tools: 3}},
	}
	s := g.buildBootstrap(t.Context())
	require.Equal(t, ServerInfo{State: strconv.Itoa(int(lsp.StateReady)), DiagnosticCount: 2}, s.LSP["gopls"])
	require.Equal(t, ServerInfo{State: "connected", ToolCount: 3}, s.MCP["fs"])
}

func TestEncodeWorkspaceLSPEvent(t *testing.T) {
	t.Parallel()
	typ, raw, ok := encode(pubsub.Event[workspace.LSPEvent]{Type: pubsub.UpdatedEvent, Payload: workspace.LSPEvent{
		Type: workspace.LSPEventStateChanged, Name: "gopls", State: lsp.StateReady, DiagnosticCount: 3,
	}})
	require.True(t, ok)
	require.Equal(t, "lsp_event", typ)
	require.JSONEq(t, `{"type":"updated","payload":{"type":"state_changed","name":"gopls","state":2,"diagnostic_count":3}}`, string(raw))
}

func TestEncodeDropsNonWireEvents(t *testing.T) {
	t.Parallel()
	_, _, ok := encode(workspace.HostThemeEvent{})
	require.False(t, ok)
	require.Nil(t, innerPayload(workspace.HostThemeEvent{}))
}

func TestEntitiesKeepOrder(t *testing.T) {
	t.Parallel()
	got := entities([]message.Message{msgEvent("m1", "s1", "x").Payload, msgEvent("m2", "s1", "y").Payload})
	require.Len(t, got, 2)
	require.Contains(t, string(got[0]), `"id":"m1"`)
	require.Contains(t, string(got[1]), `"id":"m2"`)
}

// TestEncodeStripsAttachmentData verifies the gateway's frames carry an
// attachment's path and type but not its base64 bytes. The ring keeps full
// frames and snapshots resend them, and the browser only renders the name.
func TestEncodeStripsAttachmentData(t *testing.T) {
	t.Parallel()
	ev := pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
		ID: "m1", SessionID: "s1", Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "see attached"},
			message.BinaryContent{Path: "/tmp/shot.png", MIMEType: "image/png", Data: []byte("\x89PNG\r\n\x1a\nrest")},
		},
	}}
	typ, raw, ok := encode(ev)
	require.True(t, ok)
	require.Equal(t, "message", typ)
	require.NotContains(t, string(raw), `"Data"`)
	require.Contains(t, string(raw), `"Path":"/tmp/shot.png"`)
	require.Contains(t, string(raw), `"MIMEType":"image/png"`)
	require.NotContains(t, string(innerPayload(ev)), `"Data"`)

	// The client/server path keeps the bytes it needs.
	p, ok := wire.Encode(ev)
	require.True(t, ok)
	require.Contains(t, string(p.Payload), `"Data"`)
}

// frames returns the ring oldest first. Only tests read it; the stream never
// replays from the ring as a whole, it computes one offset from attach.
func (h *hub) frames() []frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.orderedLocked()
}

// TestRingKeepsAStubForSessionFrames pins what the replay ring holds for a
// per-session frame: its sequence, type and session, never its payload. A
// per-session frame is never replayed, so keeping the payload would only cost
// memory for the life of the ring, while a live subscriber still needs all of
// it.
func TestRingKeepsAStubForSessionFrames(t *testing.T) {
	t.Parallel()
	h := newHub()
	c, _, _, _ := h.attach(-1)
	require.True(t, h.beginSubscription(c, "s1"))
	h.flushSubscription(c, "s1", frame{Type: frameSessionSnapshot, Payload: json.RawMessage(`{}`)})
	readConnFrame(t, c) // The snapshot.

	big := json.RawMessage(`{"id":"m1","text":"` + strings.Repeat("x", 64<<10) + `"}`)
	h.publishSession("s1", "message", big)
	h.publish("agent_runs", json.RawMessage(`[]`))

	ring := h.frames()
	last := ring[len(ring)-1]
	require.Equal(t, "agent_runs", last.Type)
	session := ring[len(ring)-2]
	require.Equal(t, "message", session.Type)
	require.Equal(t, "s1", session.Session)
	require.Empty(t, session.Payload, "a per-session frame keeps no payload in the ring")
	for i := 1; i < len(ring); i++ {
		require.Equal(t, ring[i-1].Seq+1, ring[i].Seq, "the ring stays contiguous in seq")
	}

	live := readConnFrame(t, c)
	require.Equal(t, "message", live.Type)
	require.Equal(t, "s1", live.Session, "a live frame keeps its session")
	require.Equal(t, big, live.Payload, "a live subscriber gets the full payload")
}

func TestRingIsBounded(t *testing.T) {
	t.Parallel()
	h := newHub()
	for range ringSize*3 + 5 {
		h.publish("message", json.RawMessage(`{}`))
	}
	frames := h.frames()
	require.Len(t, frames, ringSize)
	require.Equal(t, uint64(ringSize*2+6), frames[0].Seq)
	require.Equal(t, uint64(ringSize*3+5), frames[len(frames)-1].Seq)
	for i := 1; i < len(frames); i++ {
		require.Equal(t, frames[i-1].Seq+1, frames[i].Seq)
	}
}

func TestReplayAfterTheRingWraps(t *testing.T) {
	t.Parallel()
	h := newHub()
	for range ringSize + 10 {
		h.publish("message", json.RawMessage(`{}`))
	}
	_, replay, snap, head := h.attach(int64(ringSize + 5))
	require.False(t, snap)
	require.Equal(t, uint64(ringSize+10), head)
	require.Len(t, replay, 5)
	require.Equal(t, uint64(ringSize+6), replay[0].Seq)
}

// TestSinceAtHeadReplaysNothing pins that a tab resuming from the frame it
// already has gets no replay, wrapped ring or not: it asks for nothing new.
func TestSinceAtHeadReplaysNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		publishes int
	}{
		{"short ring", 3},
		{"wrapped ring", ringSize + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHub()
			for range tc.publishes {
				h.publish("message", json.RawMessage(`{}`))
			}
			head := h.head()
			c, replay, snap, attachedHead := h.attach(int64(head))
			h.detach(c)
			require.False(t, snap, "nothing is missing, so no snapshot is needed")
			require.Empty(t, replay)
			require.Equal(t, head, attachedHead)
		})
	}
}

// TestRingBoundaryReplay pins the exact ringSize boundary: while the ring
// is exactly full the first frame is still replayable, and one publish
// later it is not, so the tab gets a snapshot instead.
func TestRingBoundaryReplay(t *testing.T) {
	t.Parallel()
	h := newHub()
	for range ringSize {
		h.publish("message", json.RawMessage(`{}`))
	}
	require.Equal(t, uint64(ringSize), h.head())

	c, replay, snap, _ := h.attach(0)
	h.detach(c)
	require.False(t, snap, "the ring is exactly full, so frame 1 is still there")
	require.Len(t, replay, ringSize)
	require.Equal(t, uint64(1), replay[0].Seq)
	require.Equal(t, uint64(ringSize), replay[len(replay)-1].Seq)

	h.publish("message", json.RawMessage(`{}`))
	c, replay, snap, _ = h.attach(0)
	h.detach(c)
	require.True(t, snap, "frame 1 fell off the ring")
	require.Empty(t, replay)

	c, replay, snap, _ = h.attach(1)
	h.detach(c)
	require.False(t, snap)
	require.Len(t, replay, ringSize)
	require.Equal(t, uint64(2), replay[0].Seq)
	require.Equal(t, uint64(ringSize+1), replay[len(replay)-1].Seq)
}

func TestSinceAheadOfHeadOrMalformedGetsBootstrap(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	for _, q := range []string{"?since=999", "?since=abc", "?since=-4"} {
		c := dialStream(t, g, q)
		require.Equal(t, frameBootstrap, readFrame(t, c).Type, q)
	}
}

func TestIdleConnectionIsClosed(t *testing.T) {
	t.Parallel()
	g, err := Start(t.Context(), Options{Workspace: webtestFake(), idleTimeout: 150 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	c := dialStream(t, g, "")
	readFrame(t, c)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, _, err = c.Read(ctx)
	require.Error(t, err, "a client that never pings must be dropped")
	require.NoError(t, ctx.Err(), "the gateway closed it, not the test deadline")
}

func TestFollowContinuesAfterEventsClose(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c)
	fake.CloseSubscribers()
	fake.SetCurrent("s2")
	f := readFrame(t, c)
	require.Equal(t, "session_follow", f.Type)
}

func TestSlowClientClosed4001(t *testing.T) {
	t.Parallel()
	h := newHub()
	c, _, _, _ := h.attach(-1)
	for range sendQueue + 1 {
		h.publish("message", json.RawMessage(`{}`))
	}
	select {
	case <-c.overflow:
	default:
		t.Fatal("expected overflow signal")
	}
}

func TestSessionFollowPublished(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c)
	fake.SetCurrent("s2")
	f := readFrame(t, c)
	require.Equal(t, "session_follow", f.Type)
	require.JSONEq(t, `{"session_id":"s2"}`, string(f.Payload))
}

func TestInputModePublished(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c)
	g.SetInputMode(true)
	f := readFrame(t, c)
	require.Equal(t, "input_mode_changed", f.Type)
	require.JSONEq(t, `{"mode":"plan"}`, string(f.Payload))

	g.SetInputMode(true)
	g.SetInputMode(false)
	f = readFrame(t, c)
	require.Equal(t, "input_mode_changed", f.Type)
	require.JSONEq(t, `{"mode":"code"}`, string(f.Payload))
}

// TestInputModeReplaysAfterReconnect checks that a tab reconnecting from
// before the switch learns the mode from the replay ring.
func TestInputModeReplaysAfterReconnect(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := dialStream(t, g, "")
	snap := readFrame(t, c)
	g.SetInputMode(true)
	mode := readFrame(t, c)
	c.Close(websocket.StatusNormalClosure, "")

	c2 := dialStream(t, g, "?since="+itoa(snap.Seq))
	replay := readFrame(t, c2)
	require.Equal(t, "input_mode_changed", replay.Type)
	require.Equal(t, mode.Seq, replay.Seq)
	require.JSONEq(t, `{"mode":"plan"}`, string(replay.Payload))
}

// TestSnapshotShowsReportedInputMode checks a fresh snapshot after a mode
// switch, including the one the first tab receives.
func TestSnapshotShowsReportedInputMode(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	g.SetInputMode(true)
	require.Equal(t, "plan", g.buildBootstrap(t.Context()).InputMode)
}

func TestShutdownSendsClosing(t *testing.T) {
	t.Parallel()
	g, err := Start(t.Context(), Options{Workspace: webtestFake()})
	require.NoError(t, err)
	// A failed assertion before the gateway's own shutdown must not leak
	// the listener. Shutdown is safe to call twice.
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	c := dialStream(t, g, "")
	readFrame(t, c)
	done := make(chan error, 1)
	go func() { done <- g.Shutdown(context.Background()) }()
	require.Equal(t, "gateway_closing", readFrame(t, c).Type)
	require.NoError(t, <-done)
}

func TestStreamRequiresToken(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, rsp, err := websocket.Dial(ctx, strings.Replace(g.URL(), "http", "ws", 1)+"/api/stream",
		&websocket.DialOptions{Subprotocols: []string{streamProtocol, authProtocolPrefix + "nope"}})
	require.Error(t, err)
	require.NotNil(t, rsp)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)
}

func TestStreamRejectsForeignOrigin(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", "http://evil.example")
	_, rsp, err := websocket.Dial(ctx, strings.Replace(g.URL(), "http", "ws", 1)+"/api/stream",
		&websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{streamProtocol, authProtocolPrefix + g.token()}})
	require.Error(t, err)
	require.NotNil(t, rsp)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusForbidden, rsp.StatusCode, "a valid token from a foreign page is still refused")
}

// TestStreamRefusesAnUpgradeWithoutTheStreamProtocol pins that the
// subprotocol is checked before the handshake completes: a client that
// offers only the token entry is refused with a 400, so the upgrade never
// happens and no workspace call runs. The page offers both entries, and
// that pair still selects crush.v2.
func TestStreamRefusesAnUpgradeWithoutTheStreamProtocol(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	target := strings.Replace(g.URL(), "http", "ws", 1) + "/api/stream"

	c, rsp, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		Subprotocols: []string{authProtocolPrefix + g.token()},
	})
	if c != nil {
		t.Cleanup(func() { c.CloseNow() })
	}
	require.Error(t, err, "an upgrade without crush.v2 must be refused")
	require.NotNil(t, rsp)
	defer rsp.Body.Close()
	body, readErr := io.ReadAll(rsp.Body)
	require.NoError(t, readErr)
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "missing_protocol")
	require.Empty(t, rsp.Header.Get("Sec-WebSocket-Protocol"),
		"a refused upgrade selects no subprotocol")
	require.Empty(t, fake.CallLog(), "a refused upgrade reaches no workspace call")

	c = dialStream(t, g, "")
	require.Equal(t, streamProtocol, c.Subprotocol())
}

// TestOffersStreamProtocolReadsJoinedHeaders pins the header parse: the page
// offers crush.v2 in the same comma-separated value as the token entry, and
// spacing or extra entries must not hide it.
func TestOffersStreamProtocolReadsJoinedHeaders(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		header string
		want   bool
	}{
		{"page pair", "crush.v2, " + authProtocolPrefix + "token", true},
		{"token only", authProtocolPrefix + "token", false},
		{"crowded value", "one, two ,crush.v2 , three", true},
		{"prefix only", "crush.v10", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/stream", nil)
			if tc.header != "" {
				req.Header.Set("Sec-WebSocket-Protocol", tc.header)
			}
			require.Equal(t, tc.want, offersStreamProtocol(req))
		})
	}
}

func TestStreamSelectsThePlainProtocol(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := dialStream(t, g, "")
	require.Equal(t, streamProtocol, c.Subprotocol())
	require.NotContains(t, c.Subprotocol(), authProtocolPrefix)
}

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
	deliverTo(h, c, frame{Type: "message", Session: "s1", Payload: json.RawMessage(`{"id":"m2"}`)})
	h.flushSubscription(c, "s1", frame{Type: frameSessionSnapshot, Payload: json.RawMessage(`{"session_id":"s1"}`)})

	first := readConnFrame(t, c)
	require.Equal(t, frameSessionSnapshot, first.Type)
	second := readConnFrame(t, c)
	require.Equal(t, "message", second.Type)
	require.Equal(t, "s1", second.Session)
}

// readConnFrame reads one frame from a connection's queue. The timeout keeps
// a regression a failure instead of a hung test.
func readConnFrame(t *testing.T, c *conn) frame {
	t.Helper()
	select {
	case f := <-c.ch:
		return f
	case <-time.After(time.Second):
		t.Fatal("no frame reached the connection")
		return frame{}
	}
}

// deliverTo runs one per-session frame through the hub's delivery path for
// one connection. These hub-level tests hand the frame to the delivery path
// directly, with no timing.
func deliverTo(h *hub, c *conn, f frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.deliverLocked(c, f)
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
	// The subscription is reserved on the connection, so the frame the
	// gateway publishes during the read must queue behind the snapshot.
	// Waiting for the hub to sequence it is what pins it to the buffer:
	// releasing the read earlier could publish it after the snapshot was
	// built, and the test would pass for the wrong reason.
	before := g.hub.head()
	fake.Emit(msgEvent("m2", "s1", "mid-build"))
	require.Eventually(t, func() bool { return g.hub.head() > before }, 2*time.Second, 5*time.Millisecond)
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

// TestEnqueueAfterDetachIsDropped pins that a reply for a connection that is
// already gone is dropped: detach closes the queue, so a send would panic.
func TestEnqueueAfterDetachIsDropped(t *testing.T) {
	t.Parallel()
	h := newHub()
	c, _, _, _ := h.attach(-1)
	h.detach(c)
	require.NotPanics(t, func() {
		h.enqueue(c, frame{Type: frameError, Payload: json.RawMessage(`{}`)})
	}, "a detached connection must not be sent to")
}

// tracksForTest reports whether any attached connection still wants the
// session, subscribed or reserved. The test sees the socket, not the hub's
// connection, so it asks the hub about the session instead.
func (h *hub) tracksForTest(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		if c.tracks(sessionID) {
			return true
		}
	}
	return false
}

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

// TestSessionStatusReachesEveryConnection pins what stays global. The busy
// flag alone publishes nothing, so a message for the session gives the pump
// the event that recomputes its status.
func TestSessionStatusReachesEveryConnection(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	fake.SetSessionBusy("s1", true)
	fake.Emit(msgEvent("m1", "s1", "starts the turn"))
	f := nextFrameOfType(t.Context(), t, c, frameSessionStatus)
	require.Equal(t, "", f.Session, "a status frame is global")
	require.Contains(t, string(f.Payload), `"status":"running"`)
}

// TestTwoTabsOnDifferentSessions pins Review Focus 2 across two connections
// at once: each tab receives only its own session's message, and the global
// session_status reaches both. A later LSP event is the sentinel that proves
// absence: the pump handles workspace events in order, so any frame a tab
// should not have seen would arrive before it.
func TestTwoTabsOnDifferentSessions(t *testing.T) {
	t.Parallel()
	// s2 exists before the gateway starts, so its seeded status is idle
	// and the message below publishes no status frame for it: the one
	// status change in this test is s1's.
	fake := webtest.NewFake()
	fake.AddSession(session.Session{ID: "s2", Title: "Other", UpdatedAt: 5})
	g, err := Start(t.Context(), Options{
		Workspace:          fake,
		DockerMCPAvailable: func() bool { return false },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	a := dialStream(t, g, "")
	readFrame(t, a) // Bootstrap.
	subscribeStream(t, a, "s1")
	b := dialStream(t, g, "")
	readFrame(t, b) // Bootstrap.
	subscribeStream(t, b, "s2")

	fake.SetSessionBusy("s1", true)
	fake.Emit(msgEvent("m1", "s1", "for a"))
	fake.Emit(msgEvent("m2", "s2", "for b"))
	fake.Emit(pubsub.Event[workspace.LSPEvent]{Type: pubsub.UpdatedEvent, Payload: workspace.LSPEvent{
		Name: "gopls", State: lsp.StateReady,
	}})

	messages, statuses := readUntilSentinel(t, a)
	require.Len(t, messages, 1, "tab A gets only its session's message")
	require.Equal(t, "s1", messages[0].Session)
	require.Contains(t, string(messages[0].Payload), "for a")
	require.Len(t, statuses, 1, "the status frame reaches tab A")
	require.Equal(t, "", statuses[0].Session, "a status frame is global")
	require.Contains(t, string(statuses[0].Payload), `"status":"running"`)

	messages, statuses = readUntilSentinel(t, b)
	require.Len(t, messages, 1, "tab B gets only its session's message")
	require.Equal(t, "s2", messages[0].Session)
	require.Contains(t, string(messages[0].Payload), "for b")
	require.Len(t, statuses, 1, "the status frame reaches tab B")
	require.Equal(t, "", statuses[0].Session, "a status frame is global")
	require.Contains(t, string(statuses[0].Payload), `"status":"running"`)
}

// readUntilSentinel reads one tab's frames up to the global LSP sentinel,
// returning the message and session_status frames it saw on the way.
func readUntilSentinel(t *testing.T, c *websocket.Conn) (messages, statuses []testFrame) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		require.NoError(t, err)
		var f testFrame
		require.NoError(t, json.Unmarshal(data, &f))
		switch f.Type {
		case "message":
			messages = append(messages, f)
		case frameSessionStatus:
			statuses = append(statuses, f)
		case string(pubsub.PayloadTypeLSPEvent):
			return messages, statuses
		}
	}
}

// TestUnsubscribeStopsFrames pins the other half of the subscription by
// observing absence: the unsubscribed session's message is published before a
// global sentinel the pump emits later, so any message frame before the
// sentinel would be the leak. The sentinel is an LSP event, which the pump
// handles in order after the message and publishes to every tab.
func TestUnsubscribeStopsFrames(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	subscribeStream(t, c, "s1")
	writeClient(t, c, `{"type":"unsubscribe","session_id":"s1"}`)
	require.Eventually(t, func() bool { return !g.hub.tracksForTest("s1") }, time.Second, 10*time.Millisecond)

	fake.Emit(msgEvent("m1", "s1", "after"))
	// The LSP event that follows is published by the one loop that reads
	// workspace events, after the message, so it is the sentinel this test
	// reads.
	fake.Emit(pubsub.Event[workspace.LSPEvent]{Type: pubsub.UpdatedEvent, Payload: workspace.LSPEvent{
		Name: "gopls", State: lsp.StateReady,
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		require.NoError(t, err)
		var f testFrame
		require.NoError(t, json.Unmarshal(data, &f))
		require.NotEqual(t, "message", f.Type, "an unsubscribed session still delivered")
		if f.Type == string(pubsub.PayloadTypeLSPEvent) {
			return
		}
	}
}

// TestSubscribeWithoutASessionAnswersMissingSession pins the error reply a
// tab gets when it subscribes to nothing.
func TestSubscribeWithoutASessionAnswersMissingSession(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.
	writeClient(t, c, `{"type":"subscribe"}`)
	f := nextFrameOfType(t.Context(), t, c, frameError)
	require.Contains(t, string(f.Payload), "missing_session")
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
	require.Contains(t, string(snap.Payload), "kept")
}

// readSnapshotAfterReplay reads past global replayed frames to the session
// snapshot, failing if any per-session frame arrives before it. The snapshot
// is the one per-session frame expected here: the tab just asked for it.
func readSnapshotAfterReplay(t *testing.T, c *websocket.Conn) testFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		_, data, err := c.Read(ctx)
		require.NoError(t, err)
		var f testFrame
		require.NoError(t, json.Unmarshal(data, &f))
		if f.Type == frameSessionSnapshot {
			return f
		}
		require.Empty(t, f.Session, "a per-session frame reached the replay")
	}
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

	// Frame 1 has fallen off the ring, so the reconnect asks for a resync.
	c2 := dialStream(t, g, "?since=0")
	require.Equal(t, frameBootstrap, readFrame(t, c2).Type, "an expired ring answers a bootstrap")
	snap := subscribeStream(t, c2, "s1")
	var s SessionSnapshot
	require.NoError(t, json.Unmarshal(snap.Payload, &s))
	require.Len(t, s.Messages, 1)
}
