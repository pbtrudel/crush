// Package web implements web mode: a loopback HTTP gateway that serves a
// browser UI mirroring one crush session.
package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/workspace"
)

// Options configures a Gateway.
type Options struct {
	Workspace workspace.Workspace
	// Assets overrides the embedded frontend; nil uses the embedded dist.
	Assets fs.FS
	// OnSessionCreated is called with the id of a session the terminal
	// should open, whether the browser created it or followed it. It may
	// be nil.
	OnSessionCreated func(id string)
	// OnInputModeRequested is called when the browser asks to switch
	// between plan and code mode. The terminal performs the switch. It
	// may be nil.
	OnInputModeRequested func(plan bool)
	// OnPlanHandoff is called when the browser confirms a ready plan.
	// The terminal makes the switch and clears the plan. It may be nil.
	OnPlanHandoff func(sessionID string, yolo bool)
	// InputMode is the terminal's mode when the gateway starts. Empty
	// means code.
	InputMode string
	// PlanReady is the session whose ready plan waits for confirmation
	// when the gateway starts. Empty means none.
	PlanReady string
	// AgentTeamsDir holds team.json and agents.json. Empty derives it from
	// the workspace config's data directory; with no config there is no
	// team to watch.
	AgentTeamsDir string
	// LoadCommands loads the custom commands the browser lists and runs.
	// Nil uses commands.LoadCustomCommands with the workspace config,
	// which lists none when there is no config.
	LoadCommands func(*config.Config) ([]commands.CustomCommand, error)
	// idleTimeout overrides the idle read deadline in tests.
	idleTimeout time.Duration
	// teamPollInterval overrides teamPollInterval in tests.
	teamPollInterval time.Duration
	// mcpAuthPoll overrides how long an MCP auth request waits for the
	// OAuth URL in tests.
	mcpAuthPoll time.Duration
	// launchFileTTL overrides how long an unused launch file lives in
	// tests.
	launchFileTTL time.Duration
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
	// securePubMu serialises publishing the open secure prompts, so a
	// publish that read an older set can never land after a newer one.
	securePubMu sync.Mutex
	idle        time.Duration
	// team rereads the Agent Team files; teamDone closes when its
	// watcher settles on shutdown.
	team     *teamWatcher
	teamDone chan struct{}
	// modelDone closes when the model watch goroutine settles on
	// shutdown.
	modelDone chan struct{}
	// fileStats memoizes the edited-file line counts every snapshot and
	// file list asks for.
	fileStats *fileStatCache
	// diffs bounds the diff work every tab shares, so three tabs
	// refreshing at once queue instead of stacking up.
	diffs *diffSlots
	// apiRoutes lists every token-guarded /api pattern, method included,
	// in registration order. The auth test walks it so a route cannot
	// ship without being covered by the token check. The login route is
	// not here: it has to answer without a token.
	apiRoutes []string
	// loadCommandsFn is Options.LoadCommands; nil falls back to reading
	// the workspace config.
	loadCommandsFn func(*config.Config) ([]commands.CustomCommand, error)
	// shells holds the cancel func of every running shell command, keyed
	// by run id. shellWG lets Shutdown wait for them before the hub
	// closes, and shellsClosed refuses a run started during shutdown.
	shellMu      sync.Mutex
	shells       map[string]context.CancelFunc
	shellWG      sync.WaitGroup
	shellsClosed bool
	// auths holds the cancel func of every running MCP OAuth flow, keyed
	// by server name. authWG lets Shutdown wait for them before the hub
	// closes, and authsClosed refuses a flow started during shutdown.
	authMu      sync.Mutex
	auths       map[string]context.CancelFunc
	authWG      sync.WaitGroup
	authsClosed bool
	// authPoll overrides how long an auth request waits for the URL.
	authPoll time.Duration
	// launchMu guards launch, the private redirect file handed to the
	// browser opener so the token stays out of its arguments.
	launchMu  sync.Mutex
	launch    *launchFile
	launchTTL time.Duration

	// onMu guards the callbacks, which the controller may replace while
	// the gateway is running.
	onMu                 sync.RWMutex
	onSessionCreated     func(string)
	onInputModeRequested func(bool)
	onPlanHandoff        func(string, bool)

	modeMu    sync.RWMutex
	inputMode string
	planReady string

	// modelMu guards lastModel, the model info the hub last announced. The
	// handlers and the watch both announce through it, so a switch made in
	// the browser reaches every tab once and the watch does not repeat it.
	modelMu   sync.Mutex
	lastModel ModelInfo
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
	slots := newDiffSlots(maxConcurrentDiffs)
	g := &Gateway{
		ws:        opts.Workspace,
		secret:    secret,
		port:      port,
		ln:        ln,
		ctx:       gctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		hub:       newHub(),
		pending:   newPending(),
		idle:      cmp.Or(opts.idleTimeout, idleTimeout),
		team:      newTeamWatcher(teamDir(opts)),
		teamDone:  make(chan struct{}),
		modelDone: make(chan struct{}),
		fileStats: newFileStatCache(slots),
		diffs:     slots,
		shells:    make(map[string]context.CancelFunc),
		auths:     make(map[string]context.CancelFunc),
		authPoll:  opts.mcpAuthPoll,
		launchTTL: cmp.Or(opts.launchFileTTL, launchFileTTL),

		loadCommandsFn:       opts.LoadCommands,
		onSessionCreated:     opts.OnSessionCreated,
		onInputModeRequested: opts.OnInputModeRequested,
		onPlanHandoff:        opts.OnPlanHandoff,
		inputMode:            normalizeInputMode(opts.InputMode),
		planReady:            opts.PlanReady,
	}
	g.srv = &http.Server{
		Handler:           g.guard(g.routes(assets)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Seed the team watcher without publishing, so the first snapshot
	// already carries the roster and a fresh hub still starts at seq 0.
	g.team.poll()
	go g.watchTeam(cmp.Or(opts.teamPollInterval, teamPollInterval))
	// The model the hub starts from is the one at load, seeded before the
	// pump starts so an unchanged model never publishes a frame.
	g.seedModel()
	go g.watchModelLoop(modelWatchInterval)
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
	// The login route must work without a token, so it bypasses
	// requireAuth and the registry g.route fills.
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

// setOnSessionCreated replaces the callback for sessions the terminal
// should open. It is safe to call while a handler is notifying.
func (g *Gateway) setOnSessionCreated(fn func(string)) {
	g.onMu.Lock()
	g.onSessionCreated = fn
	g.onMu.Unlock()
}

// notifyOpenSession asks the terminal to open a session, whether the
// browser created it or followed an existing one. No callback means no
// terminal is attached, which is fine.
func (g *Gateway) notifyOpenSession(id string) {
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

// setOnPlanHandoff replaces the callback for browser plan confirms. It is
// safe to call while a handler is notifying.
func (g *Gateway) setOnPlanHandoff(fn func(string, bool)) {
	g.onMu.Lock()
	g.onPlanHandoff = fn
	g.onMu.Unlock()
}

// notifyPlanHandoff asks the terminal to confirm a ready plan. It reports
// whether a terminal is attached, so the caller can answer the browser
// instead of pretending the confirm went through.
func (g *Gateway) notifyPlanHandoff(sessionID string, yolo bool) bool {
	g.onMu.RLock()
	fn := g.onPlanHandoff
	g.onMu.RUnlock()
	if fn == nil {
		return false
	}
	fn(sessionID, yolo)
	return true
}

// InputMode returns the mode the terminal last reported.
func (g *Gateway) InputMode() string {
	g.modeMu.RLock()
	defer g.modeMu.RUnlock()
	return g.inputMode
}

// SetInputMode records the terminal's mode and tells every tab. It is a
// pure in-memory call: the hub only queues the frame. The publish happens
// under the same lock as the update so concurrent reports cannot publish an
// older mode after a newer one.
func (g *Gateway) SetInputMode(plan bool) {
	mode := inputModeCode
	if plan {
		mode = inputModePlan
	}
	g.modeMu.Lock()
	defer g.modeMu.Unlock()
	changed := g.inputMode != mode
	g.inputMode = mode
	if changed {
		g.hub.publishJSON(frameInputMode, map[string]string{"mode": mode})
	}
}

// PlanReadySessionID returns the session whose ready plan waits for
// confirmation, or the empty string when none does.
func (g *Gateway) PlanReadySessionID() string {
	g.modeMu.RLock()
	defer g.modeMu.RUnlock()
	return g.planReady
}

// SetPlanReady records the terminal's pending ready plan and tells every
// tab. An empty id clears it. Like SetInputMode it is a pure in-memory call,
// and the publish happens under the same lock as the update so concurrent
// reports cannot publish an older plan after a newer one.
func (g *Gateway) SetPlanReady(sessionID string) {
	g.modeMu.Lock()
	defer g.modeMu.Unlock()
	if g.planReady == sessionID {
		return
	}
	g.planReady = sessionID
	g.hub.publishJSON(framePlanReady, map[string]string{"session_id": sessionID})
}

// reportInputModeRefused tells every tab why the terminal kept its mode.
// A refused switch is a busy agent, so the frame carries the same agent_busy
// code the routes answer on a conflict. It is a contract for clients; the
// browser does not branch on it in M3.
func (g *Gateway) reportInputModeRefused(reason string) {
	g.hub.publishJSON(frameError, webErrorPayload("", "agent_busy", reason))
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
	// Cancel every running shell and MCP OAuth flow, then wait for them
	// before the hub closes, so no final frame is published into a closed
	// hub. The wait honours the caller's deadline: work that ignores
	// cancellation must not hold shutdown open.
	g.shellMu.Lock()
	g.shellsClosed = true
	for _, cancel := range g.shells {
		cancel()
	}
	g.shellMu.Unlock()
	g.authMu.Lock()
	g.authsClosed = true
	for _, cancel := range g.auths {
		cancel()
	}
	g.authMu.Unlock()
	workDone := make(chan struct{})
	go func() {
		g.shellWG.Wait()
		g.authWG.Wait()
		close(workDone)
	}()
	select {
	case <-workDone:
	case <-ctx.Done():
	}
	g.hub.publishJSON(frameClosing, map[string]string{"reason": "crush exited"})
	g.hub.close()
	g.removeLaunchFile()
	g.cancel()
	// The watchers settle on their own, but a caller's deadline outranks
	// them: an expired context must not leave Shutdown waiting on a file
	// read or a workspace call that never returns.
	select {
	case <-g.teamDone:
	case <-ctx.Done():
	}
	select {
	case <-g.modelDone:
	case <-ctx.Done():
	}
	err := g.srv.Shutdown(ctx)
	<-g.done
	return err
}

// followInterval is how often the gateway checks which session the
// terminal is viewing. Polling keeps the TUI free of web-specific hooks.
const followInterval = 250 * time.Millisecond

// modelWatchInterval is how often the gateway checks the agent's model. A
// change made in the terminal reaches a tab within two seconds, which is
// often enough for the HUD and cheap enough to poll.
const modelWatchInterval = 2 * time.Second

// pump turns workspace events into frames and reports session and model
// changes the terminal makes. last is the session the terminal was viewing
// when the gateway started.
func (g *Gateway) pump(events <-chan any, last string) {
	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				// The workspace stopped sending events, but the terminal
				// can still switch sessions and models; keep following it.
				slog.Warn("Workspace events stopped; the tab keeps following the terminal")
				events = nil
				continue
			}
			if g.pending.observe(ev) {
				g.publishSecurePrompts()
			}
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

// publishSecurePrompts tells every tab the open prompts. The set is read
// and published under one lock, so a publish that read an older set can
// never land after one that read a newer set.
func (g *Gateway) publishSecurePrompts() {
	g.securePubMu.Lock()
	defer g.securePubMu.Unlock()
	g.hub.publishJSON(frameSecurePrompt, map[string][]SecurePrompt{"prompts": g.pending.securePrompts()})
}
