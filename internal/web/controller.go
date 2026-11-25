package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/browser"
	"github.com/charmbracelet/crush/internal/workspace"
)

// detachTimeout bounds the gateway shutdown a browser detach runs. The
// gateway stops before the notice reaches the terminal, so the status bar
// never shows web mode running after it is gone.
const detachTimeout = 5 * time.Second

// Controller owns the lifetime of one process's gateway. It starts the
// gateway lazily, so the TUI pays nothing until web mode is requested.
type Controller struct {
	ws   workspace.Workspace
	open func(string) error
	// isWSL and wslPath translate a Linux launch path into one the Windows
	// browser can read. Tests replace both.
	isWSL   func() bool
	wslPath func(ctx context.Context, p string) (string, error)

	mu sync.Mutex
	gw *Gateway

	onSessionCreated     func(string)
	onInputModeRequested func(bool)
	onPlanHandoff        func(string, bool)
	onStopped            func(reason string)
	inputMode            bool
	inputModeSeq         uint64
	planReady            string
	planReadySeq         uint64
}

// NewController returns a controller for ws that opens tabs with the
// system browser.
func NewController(ws workspace.Workspace) *Controller {
	return &Controller{
		ws:    ws,
		open:  browser.OpenURL,
		isWSL: func() bool { return os.Getenv("WSL_DISTRO_NAME") != "" },
		wslPath: func(ctx context.Context, p string) (string, error) {
			out, err := exec.CommandContext(ctx, "wslpath", "-w", p).Output()
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(out)), nil
		},
	}
}

// Start runs the gateway if needed and returns the launch URL.
func (c *Controller) Start(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gw == nil {
		mode := inputModeCode
		if c.inputMode {
			mode = inputModePlan
		}
		// The detach callback belongs to the gateway being started: a
		// delayed detach from an old gateway must never stop a newer one.
		var gw *Gateway
		gw, err := Start(ctx, Options{
			Workspace:            c.ws,
			InputMode:            mode,
			PlanReady:            c.planReady,
			OnSessionCreated:     c.onSessionCreated,
			OnInputModeRequested: c.onInputModeRequested,
			OnPlanHandoff:        c.onPlanHandoff,
			OnDetach:             func() { c.detach(gw) },
		})
		if err != nil {
			return "", err
		}
		c.gw = gw
		slog.Info("Web mode started", "url", gw.URL())
	}
	return c.gw.LaunchURL(), nil
}

// SetOnSessionCreated sets the callback for sessions the terminal should
// open: ones the browser created, or ones it followed. It may be called
// after Start; the running gateway is updated too.
func (c *Controller) SetOnSessionCreated(fn func(string)) {
	c.mu.Lock()
	c.onSessionCreated = fn
	gw := c.gw
	c.mu.Unlock()
	if gw != nil {
		gw.setOnSessionCreated(fn)
	}
}

// SetOnInputModeRequested sets the callback for browser mode switches. It
// may be called after Start; the running gateway is updated too.
func (c *Controller) SetOnInputModeRequested(fn func(bool)) {
	c.mu.Lock()
	c.onInputModeRequested = fn
	gw := c.gw
	c.mu.Unlock()
	if gw != nil {
		gw.setOnInputModeRequested(fn)
	}
}

// SetOnPlanHandoff sets the callback for browser plan confirms. It may be
// called after Start; the running gateway is updated too.
func (c *Controller) SetOnPlanHandoff(fn func(sessionID string, yolo bool)) {
	c.mu.Lock()
	c.onPlanHandoff = fn
	gw := c.gw
	c.mu.Unlock()
	if gw != nil {
		gw.setOnPlanHandoff(fn)
	}
}

// SetOnStopped sets the callback that reports why web mode stopped. It
// runs after the gateway is down, so /web can start a new one. It may be
// nil.
func (c *Controller) SetOnStopped(fn func(reason string)) {
	c.mu.Lock()
	c.onStopped = fn
	c.mu.Unlock()
}

// detach stops gw for the browser's detach request, then reports why so the
// terminal can say how to start it again. The detach route runs it in a
// goroutine after its answer, so the 202 is flushed before the server
// stops. Only the gateway the controller still runs is stopped, so a
// delayed detach from an old gateway leaves a newer one serving.
func (c *Controller) detach(gw *Gateway) {
	if gw == nil {
		return
	}
	c.mu.Lock()
	if c.gw != gw {
		c.mu.Unlock()
		return
	}
	c.gw = nil
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), detachTimeout)
	defer cancel()
	if err := gw.Shutdown(ctx); err != nil {
		slog.Warn("Failed to stop web mode", "error", err)
	}
	c.mu.Lock()
	fn := c.onStopped
	c.mu.Unlock()
	if fn != nil {
		fn(detachReason)
	}
}

// SetInputMode records the terminal's input mode for the snapshot and
// updates a running gateway. Reports run in separate commands, so seq makes
// the newest report win even when an older one arrives late. The controller
// lock covers the gateway publish too, so two reports cannot interleave.
func (c *Controller) SetInputMode(plan bool, seq uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq < c.inputModeSeq {
		return
	}
	c.inputMode = plan
	c.inputModeSeq = seq
	if c.gw != nil {
		c.gw.SetInputMode(plan)
	}
}

// SetPlanReady records the session whose ready plan waits for confirmation
// for the snapshot and updates a running gateway. Like SetInputMode, seq
// makes the newest report win even when an older one arrives late.
func (c *Controller) SetPlanReady(sessionID string, seq uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq < c.planReadySeq {
		return
	}
	c.planReady = sessionID
	c.planReadySeq = seq
	if c.gw != nil {
		c.gw.SetPlanReady(sessionID)
	}
}

// ReportInputModeRefused tells the browser why the terminal kept its mode.
// The gateway publishes it through the existing error frame and toast.
func (c *Controller) ReportInputModeRefused(reason string) {
	c.mu.Lock()
	gw := c.gw
	c.mu.Unlock()
	if gw != nil {
		gw.reportInputModeRefused(reason)
	}
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

// OpenBrowser opens a tab on the gateway's launch file. The opener gets a
// file URL, never the launch URL: the token stays in the private file and
// out of the opener's process arguments. It never starts the gateway, which
// lets callers open the browser once the TUI is up instead of waiting for a
// slow opener during startup.
func (c *Controller) OpenBrowser() error {
	c.mu.Lock()
	gw := c.gw
	c.mu.Unlock()
	if gw == nil {
		return errors.New("web mode is not running")
	}
	path, err := gw.LaunchFile()
	if err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target, err := c.launchTarget(ctx, path)
	if err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	if err := c.open(target); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}

// launchTarget turns the launch file path into a URL the browser can read.
// Off WSL a plain file URL works. On Windows the path already is one, and on
// WSL only the Windows view of the Linux path is reachable, so wslpath maps
// it to a UNC or drive path first. A failure there fails the open rather
// than falling back to the token URL.
func (c *Controller) launchTarget(ctx context.Context, path string) (string, error) {
	if c.isWSL() {
		win, err := c.wslPath(ctx, path)
		if err != nil {
			return "", fmt.Errorf("wslpath: %w", err)
		}
		return windowsFileURL(win)
	}
	if runtime.GOOS == "windows" {
		return windowsFileURL(path)
	}
	return (&url.URL{Scheme: "file", Path: path}).String(), nil
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
