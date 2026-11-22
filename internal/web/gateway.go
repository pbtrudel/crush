// Package web implements web mode: a loopback HTTP gateway that serves a
// browser UI mirroring one crush session.
package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/workspace"
)

// Options configures a Gateway.
type Options struct {
	Workspace workspace.Workspace
	// Assets overrides the embedded frontend; nil uses the embedded dist.
	Assets fs.FS
	// OnSessionCreated is called with the id of a session the browser
	// started, so the terminal can open it. It may be nil.
	OnSessionCreated func(id string)
	// OnInputModeRequested is called when the browser asks to switch
	// between plan and code mode. The terminal performs the switch. It
	// may be nil.
	OnInputModeRequested func(plan bool)
	// InputMode is the terminal's mode when the gateway starts. Empty
	// means code.
	InputMode string
}

// Gateway is a running web mode server bound to the loopback interface.
type Gateway struct {
	ws         workspace.Workspace
	secret     string
	port       int
	cookieName string
	ln         net.Listener
	srv        *http.Server
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	hub        *hub
	pending    *pending

	// onMu guards the callbacks, which the controller may replace while
	// the gateway is running.
	onMu                 sync.RWMutex
	onSessionCreated     func(string)
	onInputModeRequested func(bool)

	modeMu    sync.RWMutex
	inputMode string
}

// Start binds 127.0.0.1 on a random port and serves until Shutdown.
func Start(ctx context.Context, opts Options) (*Gateway, error) {
	if opts.Workspace == nil {
		return nil, errors.New("web: workspace is required")
	}
	secret, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("web: generate token: %w", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("web: listen: %w", err)
	}
	assets := opts.Assets
	if assets == nil {
		assets = embeddedAssets()
	}
	gctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	port := ln.Addr().(*net.TCPAddr).Port
	g := &Gateway{
		ws:         opts.Workspace,
		secret:     secret,
		port:       port,
		cookieName: cookieNamePrefix + strconv.Itoa(port),
		ln:         ln,
		ctx:        gctx,
		cancel:     cancel,
		done:       make(chan struct{}),
		hub:        newHub(),
		pending:    newPending(),

		onSessionCreated:     opts.OnSessionCreated,
		onInputModeRequested: opts.OnInputModeRequested,
		inputMode:            normalizeInputMode(opts.InputMode),
	}
	g.srv = &http.Server{
		Handler:           g.guard(g.routes(assets)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Subscribe before Start returns so no event emitted right after it
	// is lost. The session the terminal views is read here, not in the
	// pump goroutine: a goroutine that starts late would adopt a session
	// changed in the meantime as its baseline and never announce the
	// change.
	events := g.ws.SubscribeEvents(g.ctx)
	go g.pump(events, g.ws.CurrentSessionID())
	go func() {
		defer close(g.done)
		if err := g.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Web gateway stopped", "error", err)
		}
	}()
	return g, nil
}

func (g *Gateway) routes(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	api := http.NewServeMux()
	g.registerAPI(api)
	mux.HandleFunc("POST /api/auth", g.handleAuth)
	mux.Handle("/api/", g.requireAuth(api))
	mux.Handle("/", assetHandler(assets))
	return mux
}

// URL returns the gateway origin, without the token.
func (g *Gateway) URL() string { return fmt.Sprintf("http://127.0.0.1:%d", g.port) }

// LaunchURL returns the URL that authenticates a browser tab. The token
// travels in the fragment so it never reaches server logs.
func (g *Gateway) LaunchURL() string { return g.URL() + "/#t=" + g.secret }

func (g *Gateway) token() string { return g.secret }

// setOnSessionCreated replaces the callback for sessions the browser
// starts. It is safe to call while a handler is notifying.
func (g *Gateway) setOnSessionCreated(fn func(string)) {
	g.onMu.Lock()
	g.onSessionCreated = fn
	g.onMu.Unlock()
}

// notifySessionCreated reports a browser-started session to the terminal.
// No callback means no terminal is attached, which is fine.
func (g *Gateway) notifySessionCreated(id string) {
	g.onMu.RLock()
	fn := g.onSessionCreated
	g.onMu.RUnlock()
	if fn != nil {
		fn(id)
	}
}

// setOnInputModeRequested replaces the callback for browser mode switches.
// It is safe to call while a handler is notifying.
func (g *Gateway) setOnInputModeRequested(fn func(bool)) {
	g.onMu.Lock()
	g.onInputModeRequested = fn
	g.onMu.Unlock()
}

// notifyInputModeRequested asks the terminal to switch modes. No callback
// means no terminal is attached, which is fine.
func (g *Gateway) notifyInputModeRequested(plan bool) {
	g.onMu.RLock()
	fn := g.onInputModeRequested
	g.onMu.RUnlock()
	if fn != nil {
		fn(plan)
	}
}

// InputMode returns the mode the terminal last reported.
func (g *Gateway) InputMode() string {
	g.modeMu.RLock()
	defer g.modeMu.RUnlock()
	return g.inputMode
}

// SetInputMode records the terminal's mode and tells every tab. It is a
// pure in-memory call: the hub only queues the frame.
func (g *Gateway) SetInputMode(plan bool) {
	mode := inputModeCode
	if plan {
		mode = inputModePlan
	}
	g.modeMu.Lock()
	changed := g.inputMode != mode
	g.inputMode = mode
	g.modeMu.Unlock()
	if changed {
		g.hub.publishJSON(frameInputMode, map[string]string{"mode": mode})
	}
}

const (
	inputModeCode = "code"
	inputModePlan = "plan"
)

func normalizeInputMode(mode string) string {
	if mode == inputModePlan {
		return inputModePlan
	}
	return inputModeCode
}

// Shutdown tells every tab the session ended, drains their queues and
// stops the server.
func (g *Gateway) Shutdown(ctx context.Context) error {
	g.hub.publishJSON(frameClosing, map[string]string{"reason": "crush exited"})
	g.hub.close()
	g.cancel()
	err := g.srv.Shutdown(ctx)
	<-g.done
	return err
}

// followInterval is how often the gateway checks which session the
// terminal is viewing. Polling keeps the TUI free of web-specific hooks.
const followInterval = 250 * time.Millisecond

// pump turns workspace events into frames and reports session changes.
// last is the session the terminal was viewing when the gateway started.
func (g *Gateway) pump(events <-chan any, last string) {
	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			g.pending.observe(ev)
			if typ, raw, ok := encode(ev); ok {
				g.hub.publish(typ, raw)
			}
		case <-ticker.C:
			if cur := g.ws.CurrentSessionID(); cur != last {
				last = cur
				g.hub.publishJSON(frameFollow, map[string]string{"session_id": cur})
			}
		}
	}
}
