package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/workspace"
)

const (
	// mcpAuthPollInterval is how often an auth request checks for the
	// OAuth URL the flow publishes, and mcpAuthPollTimeout how long it
	// waits for one before answering an empty URL.
	mcpAuthPollInterval = 100 * time.Millisecond
	mcpAuthPollTimeout  = 3 * time.Second
)

// mcpServerInfo is one MCP server as the browser lists it.
type mcpServerInfo struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	Tools     int    `json:"tools"`
	Prompts   int    `json:"prompts"`
	Error     string `json:"error,omitempty"`
	Disabled  bool   `json:"disabled"`
	NeedsAuth bool   `json:"needs_auth"`
	// Channel marks a server that is a channel, is opted in as one, or
	// could be one, which is what the terminal's channels dialog lists.
	Channel bool `json:"channel"`
}

// dockerMCPInfo is the Docker MCP state the browser lists beside the MCP
// servers.
type dockerMCPInfo struct {
	Available bool `json:"available"`
	Enabled   bool `json:"enabled"`
}

// lspDiagnostics is one LSP server's diagnostic counts.
type lspDiagnostics struct {
	Error       int `json:"error"`
	Warning     int `json:"warning"`
	Information int `json:"information"`
	Hint        int `json:"hint"`
}

// lspServerInfo is one LSP server as the browser lists it. State is the
// numeric lsp.ServerState as a string, the same text the snapshot and the
// lsp_event frames carry.
type lspServerInfo struct {
	Name        string         `json:"name"`
	State       string         `json:"state"`
	Error       string         `json:"error,omitempty"`
	Diagnostics lspDiagnostics `json:"diagnostics"`
}

// registerSystemRoutes installs the MCP, LSP and config routes.
func (g *Gateway) registerSystemRoutes(api *http.ServeMux) {
	g.route(api, "GET /api/mcp", g.handleListMCP)
	g.route(api, "POST /api/mcp/{name}/{action}", g.handleMCPAction)
	g.route(api, "GET /api/lsp", g.handleListLSP)
	g.route(api, "GET /api/lsp/{name}/diagnostics", g.handleLSPDiagnostics)
	g.route(api, "POST /api/lsp/start", g.handleLSPStart)
	g.route(api, "POST /api/lsp/stop", g.handleLSPStop)
	g.route(api, "POST /api/config/reload", g.handleConfigReload)
	g.route(api, "POST /api/config/set", g.handleConfigSet)
	g.route(api, "POST /api/config/remove", g.handleConfigRemove)
	g.route(api, "POST /api/agent-teams", g.handleAgentTeams)
	// The Docker MCP routes live under their own prefix: a server named
	// docker would make /api/mcp/docker/enable ambiguous with the
	// per-server action route.
	g.route(api, "POST /api/docker-mcp/enable", g.handleDockerMCPEnable)
	g.route(api, "POST /api/docker-mcp/disable", g.handleDockerMCPDisable)
}

func (g *Gateway) handleListMCP(w http.ResponseWriter, r *http.Request) {
	states := g.ws.MCPGetStates()
	repoDisabled := g.mcpRepoDisabled(r.Context())
	// A server disabled for this repository is not asked to authenticate:
	// disabling a server that needs OAuth must stop the prompt, the way
	// the TUI's filterAuthPending does.
	pendingAuth := make(map[string]struct{})
	for _, p := range g.ws.MCPPendingAuth() {
		if _, disabled := repoDisabled[p.Name]; !disabled {
			pendingAuth[p.Name] = struct{}{}
		}
	}
	servers := make([]mcpServerInfo, 0, len(states))
	for name, info := range states {
		_, disabled := repoDisabled[name]
		_, needsAuth := pendingAuth[name]
		servers = append(servers, mcpServerInfo{
			Name:      name,
			State:     info.State.String(),
			Tools:     info.Counts.Tools,
			Prompts:   info.Counts.Prompts,
			Error:     errText(info.Error),
			Disabled:  disabled || info.State == mcp.StateDisabled,
			NeedsAuth: needsAuth,
			Channel:   info.Channel || info.ChannelOptIn || info.ChannelCapable,
		})
	}
	slices.SortFunc(servers, func(a, b mcpServerInfo) int {
		return strings.Compare(a.Name, b.Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"servers": servers,
		"docker":  g.dockerMCPInfo(),
	})
}

// dockerMCPInfo reports the shared Docker MCP state: whether it can be
// enabled and whether the config already has it.
func (g *Gateway) dockerMCPInfo() dockerMCPInfo {
	enabled := false
	if cfg := g.ws.Config(); cfg != nil {
		enabled = cfg.IsDockerMCPEnabled()
	}
	return dockerMCPInfo{Available: g.dockerMCPAvailable(), Enabled: enabled}
}

// dockerMCPAvailable answers whether Docker MCP can be enabled. It shares
// the cache the terminal's dialog fills: a fresh answer is reused and a
// stale or unknown one refreshes it. Options.DockerMCPAvailable overrides
// the check for tests and webfake.
func (g *Gateway) dockerMCPAvailable() bool {
	if g.dockerMCPAvailableFn != nil {
		return g.dockerMCPAvailableFn()
	}
	if available, known := config.DockerMCPAvailabilityCached(); known {
		return available
	}
	return config.RefreshDockerMCPAvailability()
}

// handleDockerMCPEnable enables Docker MCP through the workspace, which
// validates the daemon again and persists the config, the way the
// terminal's command does.
func (g *Gateway) handleDockerMCPEnable(w http.ResponseWriter, r *http.Request) {
	if !g.dockerMCPAvailable() {
		writeError(w, http.StatusConflict, "docker_unavailable", "Docker MCP is not available. Make sure Docker is installed and 'docker mcp version' works.")
		return
	}
	// A server the enable starts must outlive the request, like the MCP
	// start action.
	ctx := context.WithoutCancel(r.Context())
	if err := g.ws.EnableDockerMCP(ctx); err != nil {
		writeWorkspaceError(w, "docker_mcp", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDockerMCPDisable removes the Docker MCP config through the
// workspace.
func (g *Gateway) handleDockerMCPDisable(w http.ResponseWriter, _ *http.Request) {
	if err := g.ws.DisableDockerMCP(); err != nil {
		writeWorkspaceError(w, "docker_mcp", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAgentTeams toggles Agent Teams and reloads the config, the way the
// terminal's "Toggle Agent Teams" command does. The browser shows the new
// value from GET /api/config once the reload lands.
func (g *Gateway) handleAgentTeams(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := workspace.WriteAgentTeams(g.ws, body.Enabled); err != nil {
		writeWorkspaceError(w, "config_write", err)
		return
	}
	if err := g.ws.ReloadConfig(context.WithoutCancel(r.Context())); err != nil {
		writeReloadError(w, "config_write", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mcpRepoDisabled returns the servers disabled for this repository, with
// the enabled overrides removed. A read failure fails open with an empty
// set, so a broken session store cannot hide a live server or stop an
// authentication prompt, matching the TUI.
func (g *Gateway) mcpRepoDisabled(ctx context.Context) map[string]struct{} {
	disabled, err := g.ws.MCPServersDisabled(ctx)
	if err != nil {
		slog.Warn("Web MCP list could not read the disabled servers", "error", err)
		return nil
	}
	enabled, err := g.ws.MCPServersEnabled(ctx)
	if err != nil {
		slog.Warn("Web MCP list could not read the enabled servers", "error", err)
		return nil
	}
	off := make(map[string]struct{}, len(disabled))
	for _, name := range disabled {
		off[name] = struct{}{}
	}
	for _, name := range enabled {
		delete(off, name)
	}
	return off
}

// errMCPStateUnavailable reports that the gateway could not tell whether a
// server is known: the disabled list it reads failed, and the name has no
// live state either.
var errMCPStateUnavailable = errors.New("mcp state unavailable")

// mcpKnown reports whether the workspace knows name, either as a live
// state or as a server disabled for this repository. A live state needs no
// list read, so it stays known whatever the store says. A name only an
// unreadable disabled list could know reports errMCPStateUnavailable, so
// the caller answers the unavailable state instead of claiming the server
// does not exist.
func (g *Gateway) mcpKnown(ctx context.Context, name string) (bool, error) {
	if _, ok := g.ws.MCPGetStates()[name]; ok {
		return true, nil
	}
	disabled, err := g.ws.MCPServersDisabled(ctx)
	if err != nil {
		slog.Warn("Web MCP action could not read the disabled servers", "error", err)
		return false, errMCPStateUnavailable
	}
	return slices.Contains(disabled, name), nil
}

// handleMCPAction dispatches one action on a server. The literal
// /api/mcp/prompts/run route wins over this pattern for that one action,
// which is harmless: no MCP server action is named run.
func (g *Gateway) handleMCPAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	action := r.PathValue("action")
	switch action {
	case "start", "refresh", "enable", "disable", "auth":
	default:
		writeError(w, http.StatusNotFound, "unknown_action", "no such MCP action")
		return
	}
	known, err := g.mcpKnown(r.Context(), name)
	if errors.Is(err, errMCPStateUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "mcp_state_unavailable", "Crush could not read the MCP server state. Try again in a moment.")
		return
	}
	if !known {
		writeError(w, http.StatusNotFound, "unknown_server", "no such MCP server")
		return
	}
	// Ending the request must not kill a server the action starts, the way
	// the TUI's background commands outlive the key press that ran them.
	ctx := context.WithoutCancel(r.Context())
	switch action {
	case "start":
		if err := g.ws.MCPStartServer(ctx, name); err != nil {
			writeWorkspaceError(w, "mcp_start", err)
			return
		}
	case "refresh":
		g.ws.RefreshMCPTools(ctx, name)
		g.ws.MCPRefreshPrompts(ctx, name)
		g.ws.MCPRefreshResources(ctx, name)
	case "enable", "disable":
		if err := g.ws.MCPSetServerDisabled(ctx, name, action == "disable"); err != nil {
			writeWorkspaceError(w, "toggle_mcp", err)
			return
		}
	case "auth":
		g.authenticateMCP(w, r, name)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authenticateMCP starts the server's OAuth flow and answers the URL the
// browser should open, or an empty URL when the flow needs no browser.
func (g *Gateway) authenticateMCP(w http.ResponseWriter, r *http.Request, name string) {
	// A server disabled for this repository is not asked to authenticate,
	// the same filter the list applies to its pending flag: disabling a
	// server that needs a sign-in must stop the prompt.
	if _, disabled := g.mcpRepoDisabled(r.Context())[name]; disabled {
		writeError(w, http.StatusConflict, "auth_not_needed", "the MCP server is disabled for this repository")
		return
	}
	if !g.mcpAuthRunning(name) {
		pending := slices.ContainsFunc(g.ws.MCPPendingAuth(), func(p mcp.PendingAuthServer) bool {
			return p.Name == name
		})
		if !pending {
			writeError(w, http.StatusConflict, "auth_not_needed", "the MCP server is not waiting for authentication")
			return
		}
		if !g.startMCPAuth(name) {
			writeError(w, http.StatusServiceUnavailable, "shutting_down", "Crush is shutting down")
			return
		}
	}
	raw := g.waitMCPAuthURL(r.Context(), name)
	if !mcpAuthURLAllowed(raw) {
		writeError(w, http.StatusBadGateway, "bad_auth_url", "the MCP server did not return a web address")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": raw})
}

// mcpAuthRunning reports whether a flow for name is already in progress.
// The flow moves the server out of the pending state as soon as it starts,
// so the registry, not the pending list, decides whether a request reuses
// it.
func (g *Gateway) mcpAuthRunning(name string) bool {
	g.authMu.Lock()
	defer g.authMu.Unlock()
	_, ok := g.auths[name]
	return ok
}

// startMCPAuth starts the OAuth flow for name unless one is already
// running. The flow owns a context derived from the gateway's, never the
// request's: MCPAuthenticate blocks for the whole flow and the browser only
// waits for the URL. It reports false once the gateway is shutting down.
func (g *Gateway) startMCPAuth(name string) bool {
	g.authMu.Lock()
	defer g.authMu.Unlock()
	if g.authsClosed {
		return false
	}
	if _, ok := g.auths[name]; ok {
		return true
	}
	ctx, cancel := context.WithCancel(g.ctx)
	g.auths[name] = cancel
	g.authWG.Add(1)
	go g.runMCPAuth(ctx, name)
	return true
}

// runMCPAuth runs one OAuth flow to completion and drops its registry
// entry. The outcome reaches the browser through the workspace's own
// mcp_event frames; only a failure that is not a cancellation is reported
// separately, with a fixed message.
func (g *Gateway) runMCPAuth(ctx context.Context, name string) {
	defer g.authWG.Done()
	defer func() {
		g.authMu.Lock()
		delete(g.auths, name)
		g.authMu.Unlock()
	}()
	if err := g.ws.MCPAuthenticate(ctx, name); err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return
		}
		slog.Warn("Web MCP authentication failed", "server", name, "error", err)
		g.hub.publishJSON(frameError, mcpAuthFailure())
	}
}

// waitMCPAuthURL polls for the flow's authorization URL. An empty answer
// means the flow has not published one yet or needs no browser.
func (g *Gateway) waitMCPAuthURL(ctx context.Context, name string) string {
	timeout := g.authPoll
	if timeout <= 0 {
		timeout = mcpAuthPollTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(mcpAuthPollInterval)
	defer ticker.Stop()
	for {
		if raw := g.ws.MCPAuthURL(name); raw != "" {
			return raw
		}
		select {
		case <-ctx.Done():
			return ""
		case <-ticker.C:
		case <-deadline.C:
			return ""
		}
	}
}

// mcpAuthURLAllowed reports whether raw is a URL a browser may open. An
// empty URL is allowed: it means the flow finished without one.
func mcpAuthURLAllowed(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func (g *Gateway) handleListLSP(w http.ResponseWriter, _ *http.Request) {
	states := g.ws.LSPGetStates()
	servers := make([]lspServerInfo, 0, len(states))
	for name, info := range states {
		servers = append(servers, lspServerInfo{
			Name:        name,
			State:       strconv.Itoa(int(info.State)),
			Error:       errText(info.Error),
			Diagnostics: diagnosticsInfo(g.ws.LSPGetDiagnosticCounts(name)),
		})
	}
	slices.SortFunc(servers, func(a, b lspServerInfo) int {
		return strings.Compare(a.Name, b.Name)
	})
	writeJSON(w, http.StatusOK, map[string][]lspServerInfo{"servers": servers})
}

func (g *Gateway) handleLSPDiagnostics(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := g.ws.LSPGetStates()[name]; !ok {
		writeError(w, http.StatusNotFound, "unknown_server", "no such LSP server")
		return
	}
	writeJSON(w, http.StatusOK, diagnosticsInfo(g.ws.LSPGetDiagnosticCounts(name)))
}

func diagnosticsInfo(counts lsp.DiagnosticCounts) lspDiagnostics {
	return lspDiagnostics{
		Error:       counts.Error,
		Warning:     counts.Warning,
		Information: counts.Information,
		Hint:        counts.Hint,
	}
}

// handleLSPStart starts the LSP servers for a path inside the working
// directory. The path is cleaned and joined to the working directory, so a
// traversal cannot reach outside it.
func (g *Gateway) handleLSPStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	workdir := g.ws.WorkingDir()
	path := body.Path
	if path == "" {
		path = workdir
	} else {
		if filepath.IsAbs(path) {
			writeError(w, http.StatusBadRequest, "bad_path", "path must stay inside the working directory")
			return
		}
		path = filepath.Join(workdir, filepath.Clean(path))
	}
	rel, err := filepath.Rel(workdir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		writeError(w, http.StatusBadRequest, "bad_path", "path must stay inside the working directory")
		return
	}
	// LSPManager.Start waits for every server to finish its handshake,
	// which can take seconds (or the configured timeout), so the browser
	// is not held for it. Start filters its servers by the path, so a
	// request for another path is queued rather than folded into the
	// running one: it runs on the same drain, in order, before that
	// goroutine exits.
	switch g.lspStart(path) {
	case lspStartStarted:
		w.WriteHeader(http.StatusNoContent)
	case lspStartQueued:
		// This path is running or waiting on the drain, so the request
		// only learns the work is under way.
		w.WriteHeader(http.StatusAccepted)
	default:
		writeError(w, http.StatusServiceUnavailable, "shutting_down", "Crush is shutting down")
	}
}

// lspStartOutcome tells a start request what the gateway did with it.
type lspStartOutcome int

const (
	// lspStartStarted means this request starts the drain.
	lspStartStarted lspStartOutcome = iota
	// lspStartQueued means the path is running or already queued.
	lspStartQueued
	// lspStartClosing means the gateway is shutting down.
	lspStartClosing
)

// lspStart schedules the LSP servers for path. One drain goroutine runs at a
// time: it starts the path it owns and then every queued path in order
// before it exits, all under one lspWG token, so Shutdown waits for the
// whole drain within its deadline. A path already running or queued adds
// nothing; a different path is queued once.
func (g *Gateway) lspStart(path string) lspStartOutcome {
	g.lspMu.Lock()
	if g.lspClosed {
		g.lspMu.Unlock()
		return lspStartClosing
	}
	if g.lspStarting {
		if path != g.lspPath {
			if _, queued := g.lspQueued[path]; !queued {
				g.lspQueued[path] = struct{}{}
				g.lspQueue = append(g.lspQueue, path)
			}
		}
		g.lspMu.Unlock()
		return lspStartQueued
	}
	g.lspStarting = true
	g.lspPath = path
	g.lspWG.Add(1)
	g.lspMu.Unlock()
	go g.drainLSPStarts()
	return lspStartStarted
}

// drainLSPStarts starts the LSP servers for the running path and then for
// every queued path, in order. It is the only goroutine that calls the
// manager for a browser request, so one handshake runs at a time. Once the
// gateway is closing it stops at the next path boundary and drops the rest
// of the queue: the in-process manager runs its servers on its own context,
// so a drain that outlived Shutdown would otherwise keep starting servers
// after Shutdown returned.
func (g *Gateway) drainLSPStarts() {
	defer g.lspWG.Done()
	for {
		g.lspMu.Lock()
		path := g.lspPath
		g.lspMu.Unlock()
		g.ws.LSPStart(g.lspCtx, path)
		g.lspMu.Lock()
		if g.lspClosed {
			g.lspQueue = nil
			g.lspQueued = map[string]struct{}{}
			g.lspStarting = false
			g.lspPath = ""
			g.lspMu.Unlock()
			return
		}
		if len(g.lspQueue) == 0 {
			g.lspStarting = false
			g.lspPath = ""
			g.lspMu.Unlock()
			return
		}
		path = g.lspQueue[0]
		g.lspQueue = g.lspQueue[1:]
		delete(g.lspQueued, path)
		g.lspPath = path
		g.lspMu.Unlock()
	}
}

func (g *Gateway) handleLSPStop(w http.ResponseWriter, r *http.Request) {
	g.ws.LSPStopAll(context.WithoutCancel(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

// handleConfigReload rereads the config files and applies them to the
// running app, the way the TUI's "Reload Configuration" command does.
func (g *Gateway) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	err := g.ws.ReloadConfig(context.WithoutCancel(r.Context()))
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeReloadError(w, "config_reload", err)
}

// writeReloadError answers a failed config reload with the route's own code.
// A busy agent is the same conflict the terminal reports; a starting or
// unreachable workspace keeps the writeWorkspaceError mapping, and anything
// else is a 500 without the error text.
func writeReloadError(w http.ResponseWriter, code string, err error) {
	if errors.Is(err, app.ErrConfigReloadBusy) {
		writeError(w, http.StatusConflict, "agent_busy", "The agent is busy with a run; reload once it finishes")
		return
	}
	writeWorkspaceError(w, code, err)
}
