package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// frameRecorder collects gateway frames a fake server received.
type frameRecorder struct {
	mu     sync.Mutex
	frames []gatewayPayload
}

func (r *frameRecorder) add(p gatewayPayload) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, p)
}

func (r *frameRecorder) first(op int) (gatewayPayload, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.frames {
		if p.Op == op {
			return p, true
		}
	}
	return gatewayPayload{}, false
}

func writeGatewayFrame(t *testing.T, ctx context.Context, conn *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, conn.Write(ctx, websocket.MessageText, data))
}

func TestGatewayIdentifiesHeartbeatsAndDispatches(t *testing.T) {
	t.Parallel()

	var (
		frames  frameRecorder
		msgCh   = make(chan gatewayMessage, 4)
		readyCh = make(chan struct{}, 1)
		hbCh    = make(chan struct{}, 1)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx := r.Context()

		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpHello, D: json.RawMessage(`{"heartbeat_interval":40}`)})

		// First client frame must be IDENTIFY.
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var identify gatewayPayload
		if json.Unmarshal(data, &identify) == nil {
			frames.add(identify)
		}

		seq := int64(1)
		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "READY", S: &seq, D: json.RawMessage(`{"session_id":"s1","user":{"id":"bot1"}}`)})
		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "MESSAGE_CREATE", D: json.RawMessage(`{"id":"m1","channel_id":"c1","content":"hello","author":{"id":"u1","username":"bob"}}`)})
		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "MESSAGE_CREATE", D: json.RawMessage(`{"id":"m2","channel_id":"c1","content":"self","author":{"id":"bot1","username":"crush","bot":true}}`)})
		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "MESSAGE_CREATE", D: json.RawMessage(`{"id":"m3","channel_id":"c1","content":"other bot","author":{"id":"b2","username":"other","bot":true}}`)})

		// Wait for the first heartbeat.
		for range 100 {
			readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, data, err := conn.Read(readCtx)
			cancel()
			if err != nil {
				return
			}
			var frame gatewayPayload
			if json.Unmarshal(data, &frame) != nil {
				continue
			}
			frames.add(frame)
			if frame.Op == gwOpHeartbeat {
				select {
				case hbCh <- struct{}{}:
				default:
				}
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	g := newGateway("tok", defaultGatewayIntents)
	g.url = srv.URL
	g.onReady = func() {
		select {
		case readyCh <- struct{}{}:
		default:
		}
	}
	g.onMessage = func(m gatewayMessage) { msgCh <- m }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = g.run(ctx, nil) }()

	select {
	case <-readyCh:
	case <-time.After(5 * time.Second):
		t.Fatal("READY was not dispatched")
	}

	select {
	case m := <-msgCh:
		require.Equal(t, "hello", m.Content)
	case <-time.After(5 * time.Second):
		t.Fatal("MESSAGE_CREATE was not dispatched")
	}
	select {
	case m := <-msgCh:
		t.Fatalf("bot-authored message was not filtered: %+v", m)
	case <-time.After(100 * time.Millisecond):
	}

	select {
	case <-hbCh:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat was not sent")
	}

	identify, ok := frames.first(gwOpIdentify)
	require.True(t, ok, "IDENTIFY must be sent")
	require.Contains(t, string(identify.D), `"token":"tok"`)
	require.Contains(t, string(identify.D), `"intents":`+strconv.Itoa(defaultGatewayIntents))
}

func TestGatewayDispatchesApplicationCommands(t *testing.T) {
	t.Parallel()

	interactions := make(chan gatewayInteraction, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx := r.Context()

		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpHello, D: json.RawMessage(`{"heartbeat_interval":40}`)})

		// First client frame must be IDENTIFY.
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}

		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "INTERACTION_CREATE", D: json.RawMessage(`{"id":"i1","token":"itok","type":2,"channel_id":"c1","data":{"name":"status"},"member":{"user":{"id":"u1"}}}`)})
		// Message-component presses arrive as interaction type 3.
		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "INTERACTION_CREATE", D: json.RawMessage(`{"id":"i2","token":"itok","type":3,"channel_id":"c1","data":{"custom_id":"b1","component_type":2},"member":{"user":{"id":"u1"}}}`)})
		// Modal submissions arrive as interaction type 5 and carry the
		// submitted form values.
		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "INTERACTION_CREATE", D: json.RawMessage(`{"id":"i3","token":"itok","type":5,"channel_id":"c1","data":{"custom_id":"cqm:req-1:0","components":[{"type":1,"components":[{"type":4,"custom_id":"answer","value":"typed"}]}]},"member":{"user":{"id":"u1"}}}`)})
		// Any other interaction type is ignored.
		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "INTERACTION_CREATE", D: json.RawMessage(`{"id":"i4","token":"itok","type":4,"channel_id":"c1","data":{}}`)})

		// Give the client time to read the frames before the handler
		// returns and the connection closes.
		time.Sleep(250 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	g := newGateway("tok", defaultGatewayIntents)
	g.url = srv.URL
	g.onInteraction = func(in gatewayInteraction) { interactions <- in }

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = g.run(ctx, nil) }()

	select {
	case in := <-interactions:
		require.Equal(t, "i1", in.ID)
		require.Equal(t, "itok", in.Token)
		require.Equal(t, "c1", in.ChannelID)
		require.Equal(t, "status", in.Data.Name)
		require.Equal(t, "u1", in.userID())
	case <-time.After(5 * time.Second):
		t.Fatal("INTERACTION_CREATE was not dispatched")
	}
	select {
	case in := <-interactions:
		require.Equal(t, "i2", in.ID)
		require.Equal(t, gwInteractionMessageComponent, in.Type)
		require.Equal(t, "b1", in.Data.CustomID)
		require.Equal(t, "u1", in.userID())
	case <-time.After(5 * time.Second):
		t.Fatal("component interaction was not dispatched")
	}
	select {
	case in := <-interactions:
		require.Equal(t, "i3", in.ID)
		require.Equal(t, gwInteractionModalSubmit, in.Type)
		require.Equal(t, "cqm:req-1:0", in.Data.CustomID)
		require.Equal(t, "typed", modalInputValue(in.Data.Components))
	case <-time.After(5 * time.Second):
		t.Fatal("modal submit interaction was not dispatched")
	}
	select {
	case in := <-interactions:
		t.Fatalf("non-answerable interaction was not filtered: %+v", in)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGatewayResumesWithSessionState(t *testing.T) {
	t.Parallel()

	var (
		frames   frameRecorder
		conns    atomic.Int32
		resumeCh = make(chan gatewayPayload, 1)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := conns.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx := r.Context()

		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpHello, D: json.RawMessage(`{"heartbeat_interval":5000}`)})

		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var handshake gatewayPayload
		if json.Unmarshal(data, &handshake) != nil {
			return
		}
		frames.add(handshake)

		if n == 1 {
			seq := int64(1)
			writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "READY", S: &seq, D: json.RawMessage(`{"session_id":"sess-resume","user":{"id":"bot1"}}`)})
			time.Sleep(50 * time.Millisecond)
			return // Drop the connection.
		}

		writeGatewayFrame(t, ctx, conn, gatewayPayload{Op: gwOpDispatch, T: "RESUMED", D: json.RawMessage(`{}`)})
		select {
		case resumeCh <- handshake:
		default:
		}
		<-ctx.Done()
	}))
	t.Cleanup(srv.Close)

	g := newGateway("tok", defaultGatewayIntents)
	g.url = srv.URL

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = g.run(ctx, nil) }()

	select {
	case handshake := <-resumeCh:
		require.Equal(t, gwOpResume, handshake.Op, "second connection must RESUME")
		require.Contains(t, string(handshake.D), `"session_id":"sess-resume"`)
		require.Contains(t, string(handshake.D), `"seq":`)
	case <-time.After(10 * time.Second):
		t.Fatal("gateway did not resume after the drop")
	}
}

func TestCloseCodeClassification(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0, closeCode(context.Canceled))
	require.True(t, isFatalCloseCode(gatewayCloseAuthFailed))
	require.True(t, isFatalCloseCode(gatewayCloseDisallowedIntents))
	require.False(t, isFatalCloseCode(1000))
}
