package web

import (
	"context"
	"encoding/json"
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
	h := http.Header{}
	h.Set("Cookie", cookieName+"="+g.token())
	// The 101 body is the hijacked connection, so the websocket owns it.
	c, _, err := websocket.Dial(ctx, strings.Replace(g.URL(), "http", "ws", 1)+"/api/stream"+query, //nolint:bodyclose // See above.
		&websocket.DialOptions{HTTPHeader: h})
	require.NoError(t, err)
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

func TestRingIsBounded(t *testing.T) {
	t.Parallel()
	h := newHub()
	for range ringSize * 3 {
		h.publish("message", json.RawMessage(`{}`))
	}
	require.Len(t, h.ring, ringSize)
	require.Equal(t, uint64(ringSize*3), h.ring[len(h.ring)-1].Seq)
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

func TestShutdownSendsClosing(t *testing.T) {
	t.Parallel()
	fake := webtestFake()
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	c := dialStream(t, g, "")
	readFrame(t, c)
	go func() { _ = g.Shutdown(context.Background()) }()
	require.Equal(t, "gateway_closing", readFrame(t, c).Type)
}

func TestStreamRequiresCookie(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	_, rsp, err := websocket.Dial(t.Context(), strings.Replace(g.URL(), "http", "ws", 1)+"/api/stream", nil)
	require.Error(t, err)
	require.NotNil(t, rsp)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, rsp.StatusCode)
}
