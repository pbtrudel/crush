package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	ringSize           = 2048
	sendQueue          = 512
	idleTimeout        = 45 * time.Second
	writeTimeout       = 5 * time.Second
	closeResync        = websocket.StatusCode(4001)
	frameBootstrap     = "bootstrap"
	frameFollow        = "session_follow"
	frameInputMode     = "input_mode_changed"
	frameTeamState     = "team_state"
	frameAgentRuns     = "agent_runs"
	frameClosing       = "gateway_closing"
	frameError         = "web_error"
	frameShellOutput   = "shell_output"
	frameModelChanged  = "model_changed"
	framePlanReady     = "plan_ready"
	frameSecurePrompt  = "secure_prompt"
	frameSignIn        = "signin"
	frameSessionStatus = "session_status"

	frameSessionSnapshot = "session_snapshot"
	clientSubscribe      = "subscribe"
	clientUnsubscribe    = "unsubscribe"
	// maxSubscriptions is the most sessions one connection may hold at once:
	// a tab subscribes to the open session plus every background run.
	maxSubscriptions = 32
)

type frame struct {
	Seq     uint64          `json:"seq"`
	Type    string          `json:"type"`
	Session string          `json:"session_id,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

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
	h.appendLocked(f)
	for c := range h.subs {
		h.sendLocked(c, f)
	}
	return f
}

// appendLocked records a frame in the replay ring. The caller holds h.mu.
func (h *hub) appendLocked(f frame) {
	// A head index instead of a shift: publishing stays O(1) under the
	// lock however full the ring is.
	if len(h.ring) < ringSize {
		h.ring = append(h.ring, f)
		return
	}
	h.ring[h.start] = f
	h.start = (h.start + 1) % ringSize
}

// publishSession tags a frame with its session, records its stub in the ring
// and delivers the full frame to every connection that tracks the session,
// which includes one whose snapshot is still being built.
func (h *hub) publishSession(sessionID, typ string, raw json.RawMessage) frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	f := frame{Seq: h.seq, Type: typ, Session: sessionID, Payload: raw}
	// The ring keeps a stub, not the frame: a per-session frame is never
	// replayed, so its payload would only cost memory for as long as it
	// sits in the ring. The sequence still has to advance, because attach
	// computes its replay offset from the seqs the ring holds.
	h.appendLocked(frame{Seq: f.Seq, Type: f.Type, Session: f.Session})
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
// the socket, so every frame, reply or not, travels this queue. A connection
// that is already gone is skipped, because detach closed its queue.
func (h *hub) enqueue(c *conn, f frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[c]; !ok {
		return
	}
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

// attach registers a connection. since < 0 asks for a bootstrap. Replay and
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

// offersStreamProtocol reports whether the upgrade offers crush.v2. The
// page offers it beside the token entry, and a client that offers only the
// token cannot be served, so the handshake is refused before it completes.
func offersStreamProtocol(r *http.Request) bool {
	for _, field := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(field, ",") {
			if strings.TrimSpace(p) == streamProtocol {
				return true
			}
		}
	}
	return false
}

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request) {
	if !offersStreamProtocol(r) {
		writeError(w, http.StatusBadRequest, "missing_protocol", "the stream requires the crush.v2 subprotocol")
		return
	}
	since := int64(-1)
	if s := r.URL.Query().Get("since"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n >= 0 {
			since = n
		}
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{streamProtocol}})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	// The page offers crush.v2 beside the token entry. A client that sent
	// the token but not crush.v2 cannot be served.
	if ws.Subprotocol() != streamProtocol {
		ws.Close(websocket.StatusPolicyViolation, "missing stream protocol")
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c, replay, needBootstrap, head := g.hub.attach(since)
	defer g.hub.detach(c)

	go g.readLoop(ctx, cancel, c, ws)
	go g.keepAlive(ctx, ws)

	if needBootstrap {
		raw, err := json.Marshal(g.buildBootstrap(ctx))
		if err != nil {
			return
		}
		if !writeFrame(ctx, ws, frame{Seq: head, Type: frameBootstrap, Payload: raw}) {
			return
		}
	}
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

// keepAlive pings the client on an interval well inside the idle limit and
// ends the connection when a pong does not come back. A browser answers a
// WebSocket protocol ping natively, with no JavaScript, so this is what keeps
// a hidden tab alive: Chrome throttles a tab's timers to about once a minute
// after five hidden minutes, which is longer than the idle limit, so the
// tab's own interval ping cannot arrive in time.
func (g *Gateway) keepAlive(ctx context.Context, ws *websocket.Conn) {
	every := g.idle / 3
	if every <= 0 {
		every = g.idle
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, g.idle)
			err := ws.Ping(pctx)
			cancel()
			if err != nil {
				// No pong: the tab is gone or cannot answer, so close the
				// socket and let the read loop end with it.
				_ = ws.CloseNow()
				return
			}
		}
	}
}

// readLoop reads the client's messages. A subscribe or unsubscribe is handled
// here; its reply travels the connection's queue, so the single writer loop
// stays the only thing writing to the socket. Liveness is keepAlive's job: a
// read that never answers a ping ends there, so the read itself has no
// deadline of its own.
func (g *Gateway) readLoop(ctx context.Context, cancel context.CancelFunc, c *conn, ws *websocket.Conn) {
	defer cancel()
	for {
		typ, data, err := ws.Read(ctx)
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

func writeFrame(ctx context.Context, ws *websocket.Conn, f frame) bool {
	data, err := json.Marshal(f)
	if err != nil {
		return false
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, data) == nil
}
