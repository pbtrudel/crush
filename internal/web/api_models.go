package web

import (
	"errors"
	"net/http"
	"slices"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/workspace"
)

// registerModelRoutes installs the model routes: the choices the selector
// lists, a switch, and the reasoning controls. Like every API route they
// register through g.route so the auth test walks them.
func (g *Gateway) registerModelRoutes(api *http.ServeMux) {
	g.route(api, "GET /api/providers", g.handleProviders)
	g.route(api, "POST /api/models/select", g.handleSelectModel)
	g.route(api, "POST /api/models/reasoning", g.handleReasoning)
}

// handleProviders lists the models the browser may switch to. The
// workspace applies the same configured and enabled filter the terminal's
// model dialog uses, so every choice works without a sign-in.
func (g *Gateway) handleProviders(w http.ResponseWriter, _ *http.Request) {
	choices := workspace.ModelChoices(g.ws.Config())
	if choices == nil {
		choices = []workspace.ModelChoice{}
	}
	writeJSON(w, http.StatusOK, map[string][]workspace.ModelChoice{"choices": choices})
}

// handleSelectModel switches one model type and answers with the model info
// every tab should now show.
func (g *Gateway) handleSelectModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type     string `json:"type"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	// A starting agent is a state to wait out, and the helpers would refuse
	// the switch anyway: answer before touching the workspace.
	if err := g.ws.AgentReadyErr(); err != nil {
		writeWorkspaceError(w, "select_model", err)
		return
	}
	typ, ok := selectedModelType(body.Type)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_type", "type must be main or small")
		return
	}
	m := config.SelectedModel{Provider: body.Provider, Model: body.Model}
	if err := workspace.SelectModel(r.Context(), g.ws, typ, m); err != nil {
		writeModelError(w, "select_model", err)
		return
	}
	writeJSON(w, http.StatusOK, g.announceModel())
}

// handleReasoning sets the coder model's reasoning effort or its thinking
// mode and answers with the model info every tab should now show.
func (g *Gateway) handleReasoning(w http.ResponseWriter, r *http.Request) {
	// Pointers tell an absent field from a zero value, so a request names
	// exactly one control.
	var body struct {
		Effort *string `json:"effort"`
		Think  *bool   `json:"think"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := g.ws.AgentReadyErr(); err != nil {
		writeWorkspaceError(w, "set_reasoning", err)
		return
	}
	// A config with no providers is a reload in flight, not a bad request:
	// the pre-checks have no model to read, so answer a state the browser
	// can retry instead of a 400 the user cannot fix.
	cfg := g.ws.Config()
	if cfg == nil || cfg.Providers == nil {
		writeError(w, http.StatusServiceUnavailable, "agent_starting", agentStartingText)
		return
	}
	if (body.Effort == nil) == (body.Think == nil) {
		writeError(w, http.StatusBadRequest, "bad_reasoning", "send exactly one of effort or think")
		return
	}
	var err error
	if body.Effort != nil {
		// workspace.SetReasoningEffort refuses an unknown level with a plain
		// error, which would answer 500; refuse it here, as the terminal
		// does, before the helper runs.
		if _, levels := workspace.ReasoningControls(mainCatalogModel(cfg)); !slices.Contains(levels, *body.Effort) {
			writeError(w, http.StatusBadRequest, "bad_reasoning", "the model does not offer that reasoning effort")
			return
		}
		err = workspace.SetReasoningEffort(r.Context(), g.ws, *body.Effort)
	} else {
		// Thinking is a mode a model either has or does not; the helper
		// would store it on a model that never reads it.
		if canReason, _ := workspace.ReasoningControls(mainCatalogModel(cfg)); !canReason {
			writeError(w, http.StatusBadRequest, "bad_reasoning", "the model cannot reason")
			return
		}
		err = workspace.SetThinking(r.Context(), g.ws, *body.Think)
	}
	if err != nil {
		writeModelError(w, "set_reasoning", err)
		return
	}
	writeJSON(w, http.StatusOK, g.announceModel())
}

// selectedModelType maps the wire type to the config's. Only the two the
// terminal uses exist, so any other name is a client bug.
func selectedModelType(name string) (config.SelectedModelType, bool) {
	switch typ := config.SelectedModelType(name); typ {
	case config.SelectedModelTypeMain, config.SelectedModelTypeSmall:
		return typ, true
	}
	return "", false
}

// writeModelError answers a failed model switch. A busy agent is a
// conflict; a model the credentials cannot serve needs a sign-in in the
// terminal; an app with no config or coder agent is still starting. Any
// other failure is the workspace's to report.
func writeModelError(w http.ResponseWriter, code string, err error) {
	switch {
	case errors.Is(err, workspace.ErrAgentBusy):
		writeError(w, http.StatusConflict, "agent_busy", "the agent is busy")
	case errors.Is(err, workspace.ErrModelUnavailable):
		writeError(w, http.StatusBadRequest, "unknown_model", "Choose this model in the terminal; it needs a sign-in")
	case errors.Is(err, workspace.ErrNoConfig), errors.Is(err, workspace.ErrNoAgentConfig):
		writeError(w, http.StatusServiceUnavailable, "agent_starting", agentStartingText)
	default:
		writeWorkspaceError(w, code, err)
	}
}
