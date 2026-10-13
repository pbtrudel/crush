package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Discord gateway opcodes.
const (
	gwOpDispatch       = 0
	gwOpHeartbeat      = 1
	gwOpIdentify       = 2
	gwOpResume         = 6
	gwOpReconnect      = 7
	gwOpInvalidSession = 9
	gwOpHello          = 10
	gwOpHeartbeatAck   = 11
)

// Gateway intents the bridge needs: guild message events (to hear the
// thread, which is covered by guild messages), message content (to read
// message text), and reactions (for permission approvals).
const (
	gwIntentGuilds                = 1 << 0
	gwIntentGuildMessages         = 1 << 9
	gwIntentGuildMessageReactions = 1 << 10
	gwIntentMessageContent        = 1 << 15

	defaultGatewayIntents = gwIntentGuilds | gwIntentGuildMessages |
		gwIntentGuildMessageReactions | gwIntentMessageContent
)

const (
	// gatewayBackoffCap bounds reconnect delays.
	gatewayBackoffCap = 60 * time.Second

	// gatewayCloseAuthFailed and friends are permanent failure codes
	// worth a one-time user-visible note instead of silent retries.
	gatewayCloseAuthFailed        = 4004
	gatewayCloseInvalidIntents    = 4013
	gatewayCloseDisallowedIntents = 4014
)

// gatewayPayload is the envelope of every gateway frame.
type gatewayPayload struct {
	Op int             `json:"op"`
	T  string          `json:"t,omitempty"`
	S  *int64          `json:"s,omitempty"`
	D  json.RawMessage `json:"d,omitempty"`
}

// gatewayMessage is the MESSAGE_CREATE payload subset the bridge reads.
type gatewayMessage struct {
	ID          string              `json:"id"`
	ChannelID   string              `json:"channel_id"`
	Content     string              `json:"content"`
	Attachments []gatewayAttachment `json:"attachments"`
	Author      struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Bot      bool   `json:"bot"`
	} `json:"author"`
}

// gatewayAttachment is a Discord attachment reference.
type gatewayAttachment struct {
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// gatewayReaction is the MESSAGE_REACTION_ADD payload subset.
type gatewayReaction struct {
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id"`
	MessageID string `json:"message_id"`
	Emoji     struct {
		Name string `json:"name"`
	} `json:"emoji"`
}

// gateway is a minimal Discord gateway (WebSocket) client: connect,
// identify or resume, heartbeat, dispatch the few events the bridge
// cares about, and reconnect with backoff. It is intentionally tiny;
// the bridge owns everything semantic.
type gateway struct {
	token   string
	intents int
	url     string // gateway base url from GET /gateway/bot

	onMessage  func(gatewayMessage)
	onReaction func(gatewayReaction)
	onReady    func()

	// userID is the bot's own user id once READY arrives. MESSAGE_CREATE
	// events authored by the bot are dropped.
	userID string

	writeMu sync.Mutex // serializes websocket writes (heartbeat vs read loop)
	stateMu sync.Mutex
	seq     *int64
	session string

	// dial is the websocket dialer; overridable in tests.
	dial func(ctx context.Context, url string) (*websocket.Conn, error)
}

func newGateway(token string, intents int) *gateway {
	return &gateway{token: token, intents: intents}
}

// run keeps the gateway connected until ctx is done, reconnecting with
// capped exponential backoff. Fatal close codes (auth, intents) are
// reported through onFatal once per code, but retries continue: a
// Discord outage or a token fixed elsewhere should heal without a TUI
// restart.
func (g *gateway) run(ctx context.Context, onFatal func(error)) error {
	backoff := time.Second
	notedCodes := map[int]bool{}
	for {
		err := g.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil || errors.Is(err, errGatewayReconnect) {
			backoff = time.Second
			continue
		}
		if code := closeCode(err); code != 0 {
			slog.Warn("Discord gateway closed", "code", code, "error", err.Error())
			if isFatalCloseCode(code) && !notedCodes[code] {
				notedCodes[code] = true
				if onFatal != nil {
					onFatal(fmt.Errorf("discord gateway closed with code %d: %w", code, err))
				}
			}
		} else {
			slog.Warn("Discord gateway connection failed", "error", err.Error())
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, gatewayBackoffCap)
	}
}

// errGatewayReconnect reports a clean reconnect request (op 7 or an
// invalid session); the next attempt must not count as a failure.
var errGatewayReconnect = errors.New("gateway reconnect requested")

// connect dials once, performs the handshake, and reads until the
// connection drops. The returned error describes why; nil is never
// returned except when ctx ends (run checks ctx first).
func (g *gateway) connect(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	dial := g.dial
	if dial == nil {
		dial = func(ctx context.Context, url string) (*websocket.Conn, error) {
			// The response body belongs to the returned connection on
			// success and is already closed by Dial on failure.
			conn, _, err := websocket.Dial(ctx, url, nil) //nolint:bodyclose
			return conn, err
		}
	}
	conn, err := dial(dialCtx, g.url+"?v=10&encoding=json")
	if err != nil {
		return fmt.Errorf("dial gateway: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()

	// The first frame must be HELLO with the heartbeat interval.
	_, data, err := conn.Read(dialCtx)
	if err != nil {
		return fmt.Errorf("read HELLO: %w", err)
	}
	var hello gatewayPayload
	if err := json.Unmarshal(data, &hello); err != nil {
		return fmt.Errorf("decode HELLO: %w", err)
	}
	if hello.Op != gwOpHello {
		return fmt.Errorf("expected HELLO (op %d), got op %d", gwOpHello, hello.Op)
	}
	interval := 45 * time.Second
	var helloData struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(hello.D, &helloData); err == nil && helloData.HeartbeatInterval > 0 {
		interval = time.Duration(helloData.HeartbeatInterval) * time.Millisecond
	}

	if err := g.handshake(ctx, conn); err != nil {
		return err
	}

	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go g.heartbeatLoop(hbCtx, conn, interval)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var p gatewayPayload
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		switch p.Op {
		case gwOpDispatch:
			if p.S != nil {
				g.stateMu.Lock()
				g.seq = p.S
				g.stateMu.Unlock()
			}
			g.dispatch(p)
		case gwOpHeartbeat:
			g.sendHeartbeat(ctx, conn)
		case gwOpReconnect:
			return errGatewayReconnect
		case gwOpInvalidSession:
			g.stateMu.Lock()
			g.session = ""
			g.seq = nil
			g.stateMu.Unlock()
			// Discord asks for a fresh identify between 1 and 5 seconds
			// after an invalid session.
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			return errGatewayReconnect
		case gwOpHeartbeatAck:
			// Nothing to track; a missing ack is covered by the read
			// deadline imposed by the caller's context.
		}
	}
}

// handshake sends RESUME when a session is established, otherwise a
// fresh IDENTIFY.
func (g *gateway) handshake(ctx context.Context, conn *websocket.Conn) error {
	g.stateMu.Lock()
	session, seq := g.session, g.seq
	g.stateMu.Unlock()

	if session != "" && seq != nil {
		return g.write(ctx, conn, map[string]any{
			"op": gwOpResume,
			"d": map[string]any{
				"token":      g.token,
				"session_id": session,
				"seq":        *seq,
			},
		})
	}
	return g.write(ctx, conn, map[string]any{
		"op": gwOpIdentify,
		"d": map[string]any{
			"token":   g.token,
			"intents": g.intents,
			"properties": map[string]string{
				"os":      runtime.GOOS,
				"browser": "crush",
				"device":  "crush",
			},
		},
	})
}

// dispatch routes one dispatch event.
func (g *gateway) dispatch(p gatewayPayload) {
	switch p.T {
	case "READY":
		var d struct {
			SessionID string `json:"session_id"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
		}
		if err := json.Unmarshal(p.D, &d); err != nil {
			return
		}
		g.stateMu.Lock()
		g.session = d.SessionID
		g.userID = d.User.ID
		g.stateMu.Unlock()
		if g.onReady != nil {
			g.onReady()
		}
	case "MESSAGE_CREATE":
		if g.onMessage == nil {
			return
		}
		var d gatewayMessage
		if err := json.Unmarshal(p.D, &d); err != nil {
			return
		}
		g.stateMu.Lock()
		own := d.Author.ID == g.userID
		g.stateMu.Unlock()
		if d.Author.Bot || own {
			return
		}
		g.onMessage(d)
	case "MESSAGE_REACTION_ADD":
		if g.onReaction == nil {
			return
		}
		var d gatewayReaction
		if err := json.Unmarshal(p.D, &d); err != nil {
			return
		}
		g.onReaction(d)
	}
}

// heartbeatLoop sends keepalives at the interval Discord asked for until
// the connection or bridge context ends.
func (g *gateway) heartbeatLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.sendHeartbeat(ctx, conn)
		}
	}
}

// sendHeartbeat writes one heartbeat frame carrying the last sequence.
func (g *gateway) sendHeartbeat(ctx context.Context, conn *websocket.Conn) {
	g.stateMu.Lock()
	var d any
	if g.seq != nil {
		d = *g.seq
	}
	g.stateMu.Unlock()
	if err := g.write(ctx, conn, map[string]any{"op": gwOpHeartbeat, "d": d}); err != nil {
		slog.Debug("Discord heartbeat failed", "error", err.Error())
	}
}

// write marshals and sends one frame. coder/websocket allows one
// concurrent writer, so all writes share a mutex.
func (g *gateway) write(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode gateway frame: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	g.writeMu.Lock()
	defer g.writeMu.Unlock()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

// closeCode extracts the websocket close code from a read error, or 0
// when the error is not a close status.
func closeCode(err error) int {
	code := int(websocket.CloseStatus(err))
	if code < 0 {
		// coder/websocket reports no close status as -1.
		return 0
	}
	return code
}

// isFatalCloseCode reports whether a close code indicates a condition a
// retry cannot fix without operator action.
func isFatalCloseCode(code int) bool {
	switch code {
	case gatewayCloseAuthFailed, gatewayCloseInvalidIntents, gatewayCloseDisallowedIntents:
		return true
	}
	return false
}
