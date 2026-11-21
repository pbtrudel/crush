package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/charmbracelet/crush/internal/browser"
	"github.com/charmbracelet/crush/internal/workspace"
)

// Controller owns the lifetime of one process's gateway. It starts the
// gateway lazily, so the TUI pays nothing until web mode is requested.
type Controller struct {
	ws   workspace.Workspace
	open func(string) error

	mu sync.Mutex
	gw *Gateway
}

// NewController returns a controller for ws that opens tabs with the
// system browser.
func NewController(ws workspace.Workspace) *Controller {
	return &Controller{ws: ws, open: browser.OpenURL}
}

// Start runs the gateway if needed and returns the launch URL.
func (c *Controller) Start(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gw == nil {
		gw, err := Start(ctx, Options{Workspace: c.ws})
		if err != nil {
			return "", err
		}
		c.gw = gw
		slog.Info("Web mode started", "url", gw.URL())
	}
	return c.gw.LaunchURL(), nil
}

// Open starts the gateway if needed and opens a browser tab. When the
// browser cannot be opened the URL is still returned so the caller can
// show it.
func (c *Controller) Open(ctx context.Context) (string, error) {
	u, err := c.Start(ctx)
	if err != nil {
		return "", err
	}
	if err := c.OpenBrowser(); err != nil {
		return u, err
	}
	return u, nil
}

// LaunchURL returns the launch URL of the running gateway, or the empty
// string when the gateway is stopped. The URL carries the session token, so
// callers must not log it.
func (c *Controller) LaunchURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gw == nil {
		return ""
	}
	return c.gw.LaunchURL()
}

// OpenBrowser opens a tab on the launch URL of an already-started gateway.
// It never starts the gateway, which lets callers open the browser once the
// TUI is up instead of waiting for a slow opener during startup.
func (c *Controller) OpenBrowser() error {
	u := c.LaunchURL()
	if u == "" {
		return errors.New("web mode is not running")
	}
	if err := c.open(u); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

// Running reports whether the gateway is serving.
func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gw != nil
}

// Shutdown stops the gateway if it runs. It is safe to call more than once
// and while other calls are in flight: the gateway pointer is cleared under
// the lock, and the blocking shutdown runs without it.
func (c *Controller) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	gw := c.gw
	c.gw = nil
	c.mu.Unlock()
	if gw == nil {
		return nil
	}
	return gw.Shutdown(ctx)
}
