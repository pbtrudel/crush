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
	"time"

	"github.com/charmbracelet/crush/internal/workspace"
)

// Options configures a Gateway.
type Options struct {
	Workspace workspace.Workspace
	// Assets overrides the embedded frontend; nil uses the embedded dist.
	Assets fs.FS
}

// Gateway is a running web mode server bound to the loopback interface.
type Gateway struct {
	ws      workspace.Workspace
	secret  string
	port    int
	ln      net.Listener
	srv     *http.Server
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	hub     *hub
	pending *pending
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
	g := &Gateway{
		ws:      opts.Workspace,
		secret:  secret,
		port:    ln.Addr().(*net.TCPAddr).Port,
		ln:      ln,
		ctx:     gctx,
		cancel:  cancel,
		done:    make(chan struct{}),
		hub:     newHub(),
		pending: newPending(),
	}
	g.srv = &http.Server{
		Handler:           g.guard(g.routes(assets)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Subscribe before Start returns so no event emitted right after it
	// is lost.
	events := g.ws.SubscribeEvents(g.ctx)
	go g.pump(events)
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
func (g *Gateway) pump(events <-chan any) {
	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	last := g.ws.CurrentSessionID()
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
