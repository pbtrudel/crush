package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	ringSize          = 2048
	sendQueue         = 512
	idleTimeout       = 45 * time.Second
	writeTimeout      = 5 * time.Second
	closeResync       = websocket.StatusCode(4001)
	frameSnapshot     = "snapshot"
	frameFollow       = "session_follow"
	frameInputMode    = "input_mode_changed"
	frameTeamState    = "team_state"
	frameAgentRuns    = "agent_runs"
	frameClosing      = "gateway_closing"
	frameError        = "web_error"
	frameShellOutput  = "shell_output"
	frameModelChanged = "model_changed"
	framePlanReady    = "plan_ready"
)

type frame struct {
	Seq     uint64          `json:"seq"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type conn struct {
	ch       chan frame
	overflow chan struct{}
	once     sync.Once
}

// hub sequences frames, keeps a bounded replay ring, and fans frames out
// to browser connections without ever blocking the publisher.
type hub struct {
	mu     sync.Mutex
	seq    uint64
	ring   []frame
	start  int
	subs   map[*conn]struct{}
	closed bool
}

func newHub() *hub {
	return &hub{subs: make(map[*conn]struct{})}
}

func (h *hub) head() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

func (h *hub) publish(typ string, raw json.RawMessage) frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := frame{Seq: h.seq, Type: typ, Payload: raw}
	// A head index instead of a shift: publishing stays O(1) under the
	// lock however full the ring is.
	if len(h.ring) < ringSize {
		h.ring = append(h.ring, f)
	} else {
		h.ring[h.start] = f
		h.start = (h.start + 1) % ringSize
	}
	for c := range h.subs {
		select {
		case c.ch <- f:
		default:
			c.once.Do(func() { close(c.overflow) })
			delete(h.subs, c)
		}
	}
	return f
}

// frames returns the ring oldest first. The caller holds no lock.
func (h *hub) frames() []frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.orderedLocked()
}

func (h *hub) orderedLocked() []frame {
	out := make([]frame, 0, len(h.ring))
	out = append(out, h.ring[h.start:]...)
	return append(out, h.ring[:h.start]...)
}

func (h *hub) publishJSON(typ string, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		slog.Error("Failed to marshal web frame", "type", typ, "error", err)
		return
	}
	h.publish(typ, raw)
}

// attach registers a connection. since < 0 asks for a snapshot. Replay and
// registration happen under one lock so no frame is lost or duplicated.
func (h *hub) attach(since int64) (*conn, []frame, bool, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := &conn{ch: make(chan frame, sendQueue), overflow: make(chan struct{})}
	if h.closed {
		close(c.ch)
		return c, nil, false, h.seq
	}
	h.subs[c] = struct{}{}
	if since < 0 || uint64(since) > h.seq {
		return c, nil, true, h.seq
	}
	ordered := h.orderedLocked()
	if len(ordered) == 0 || ordered[0].Seq > uint64(since)+1 {
		return c, nil, true, h.seq
	}
	// Seqs are contiguous, so the replay starts at a computed offset.
	offset := int(uint64(since) + 1 - ordered[0].Seq)
	return c, append([]frame(nil), ordered[offset:]...), false, h.seq
}

func (h *hub) detach(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[c]; ok {
		delete(h.subs, c)
		close(c.ch)
	}
}

// close ends every connection after its queued frames are written.
func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for c := range h.subs {
		delete(h.subs, c)
		close(c.ch)
	}
}

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request) {
	since := int64(-1)
	if s := r.URL.Query().Get("since"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
			since = n
		}
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c, replay, needSnapshot, head := g.hub.attach(since)
	defer g.hub.detach(c)

	go g.readLoop(ctx, cancel, ws)

	if needSnapshot {
		raw, err := json.Marshal(g.buildSnapshot(ctx, g.ws.CurrentSessionID()))
		if err != nil {
			return
		}
		if !writeFrame(ctx, ws, frame{Seq: head, Type: frameSnapshot, Payload: raw}) {
			return
		}
	}
	for _, f := range replay {
		if !writeFrame(ctx, ws, f) {
			return
		}
	}
	for {
		select {
		case f, ok := <-c.ch:
			if !ok {
				ws.Close(websocket.StatusGoingAway, "gateway closing")
				return
			}
			if !writeFrame(ctx, ws, f) {
				return
			}
		case <-c.overflow:
			ws.Close(closeResync, "resync")
			return
		case <-ctx.Done():
			return
		}
	}
}

// readLoop consumes client pings and cancels the connection when the
// client goes quiet for the idle timeout.
func (g *Gateway) readLoop(ctx context.Context, cancel context.CancelFunc, ws *websocket.Conn) {
	defer cancel()
	for {
		rctx, rcancel := context.WithTimeout(ctx, g.idle)
		_, _, err := ws.Read(rctx)
		rcancel()
		if err != nil {
			return
		}
	}
}

func writeFrame(ctx context.Context, ws *websocket.Conn, f frame) bool {
	data, err := json.Marshal(f)
	if err != nil {
		return false
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, data) == nil
}
