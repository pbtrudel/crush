package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/wire"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type testFrame struct {
	Seq     uint64          `json:"seq"`
	Type    string          `json:"type"`
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

func TestStreamStartsWithSnapshotThenLive(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	snap := readFrame(t, c)
	require.Equal(t, "snapshot", snap.Type)
	var s Snapshot
	require.NoError(t, json.Unmarshal(snap.Payload, &s))
	require.Equal(t, "s1", s.CurrentSessionID)
	require.Equal(t, "/tmp/project", s.WorkingDir)
	require.Equal(t, "code", s.InputMode)

	fake.Emit(msgEvent("m1", "s1", "hello"))
	f := readFrame(t, c)
	require.Equal(t, "message", f.Type)
	require.Equal(t, snap.Seq+1, f.Seq)
	require.Contains(t, string(f.Payload), `"hello"`)
}

func TestStreamSinceReplays(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Snapshot.
	fake.Emit(msgEvent("m1", "s1", "one"))
	fake.Emit(msgEvent("m2", "s1", "two"))
	first := readFrame(t, c)
	readFrame(t, c)
	c.Close(websocket.StatusNormalClosure, "")

	c2 := dialStream(t, g, "?since="+itoa(first.Seq))
	f := readFrame(t, c2)
	require.Equal(t, "message", f.Type)
	require.Equal(t, first.Seq+1, f.Seq)
	require.Contains(t, string(f.Payload), `"two"`)
}

func TestStreamSinceTooOldGetsSnapshot(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for i := range ringSize + 10 {
		fake.Emit(msgEvent("m"+itoa(uint64(i)), "s1", "x"))
	}
	require.Eventually(t, func() bool { return g.hub.head() >= ringSize+10 }, 5*time.Second, 10*time.Millisecond)
	c := dialStream(t, g, "?since=1")
	require.Equal(t, "snapshot", readFrame(t, c).Type)
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
	s := g.buildSnapshot(t.Context(), "s1")
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

func TestSinceAheadOfHeadOrMalformedGetsSnapshot(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	for _, q := range []string{"?since=999", "?since=abc", "?since=-4"} {
		c := dialStream(t, g, q)
		require.Equal(t, "snapshot", readFrame(t, c).Type, q)
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
	require.Equal(t, "plan", g.buildSnapshot(t.Context(), "s1").InputMode)
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
// that pair still selects crush.v1.
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
	require.Error(t, err, "an upgrade without crush.v1 must be refused")
	require.NotNil(t, rsp)
	defer rsp.Body.Close()
	body, readErr := io.ReadAll(rsp.Body)
	require.NoError(t, readErr)
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "missing_protocol")
	require.Empty(t, fake.CallLog(), "a refused upgrade reaches no workspace call")

	c = dialStream(t, g, "")
	require.Equal(t, streamProtocol, c.Subprotocol())
}

func TestStreamSelectsThePlainProtocol(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	c := dialStream(t, g, "")
	require.Equal(t, streamProtocol, c.Subprotocol())
	require.NotContains(t, c.Subprotocol(), authProtocolPrefix)
}
