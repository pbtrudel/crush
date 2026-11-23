package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strconv"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/version"
	"github.com/charmbracelet/crush/internal/workspace"
)

// ModelRef identifies the small model. The HUD shows it without the rest
// of the model info.
type ModelRef struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

// ModelInfo describes the active main model for the HUD and the model
// selector: what it can do, how it reasons right now, the small model and
// whether the agent can take work yet.
type ModelInfo struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Provider        string    `json:"provider"`
	ContextWindow   int64     `json:"context_window"`
	CanReason       bool      `json:"can_reason,omitempty"`
	EffortLevels    []string  `json:"effort_levels,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	Think           bool      `json:"think,omitempty"`
	Small           *ModelRef `json:"small,omitempty"`
	AgentReady      bool      `json:"agent_ready"`
}

// ServerInfo is the state of one LSP or MCP server. State uses the same
// text as the matching live event: LSP states are the numeric
// lsp.ServerState as a string, MCP states are the mcp.State text.
type ServerInfo struct {
	State           string `json:"state"`
	DiagnosticCount int    `json:"diagnostic_count,omitempty"`
	ToolCount       int    `json:"tool_count,omitempty"`
	Error           string `json:"error,omitempty"`
}

// Snapshot is the full state a tab needs to render from scratch. Entity
// fields hold the same JSON as the matching live event payloads.
type Snapshot struct {
	Version          string                `json:"version"`
	WorkingDir       string                `json:"working_dir"`
	Branch           string                `json:"branch,omitempty"`
	CurrentSessionID string                `json:"current_session_id"`
	Session          json.RawMessage       `json:"session,omitempty"`
	Messages         []json.RawMessage     `json:"messages"`
	Files            []FileSummary         `json:"files"`
	Permissions      []json.RawMessage     `json:"permissions"`
	Questions        []json.RawMessage     `json:"questions,omitempty"`
	PermissionMode   string                `json:"permission_mode"`
	InputMode        string                `json:"input_mode"`
	Model            ModelInfo             `json:"model"`
	AgentReady       bool                  `json:"agent_ready"`
	Busy             bool                  `json:"busy"`
	Queued           []string              `json:"queued"`
	LSP              map[string]ServerInfo `json:"lsp"`
	MCP              map[string]ServerInfo `json:"mcp"`
	Team             TeamState             `json:"team"`
	AgentRuns        []agentrun.Run        `json:"agent_runs"`
}

func (g *Gateway) buildSnapshot(ctx context.Context, sessionID string) Snapshot {
	s := Snapshot{
		Version:          version.Version,
		WorkingDir:       g.ws.WorkingDir(),
		CurrentSessionID: sessionID,
		Messages:         []json.RawMessage{},
		Files:            []FileSummary{},
		Permissions:      []json.RawMessage{},
		Queued:           []string{},
		LSP:              map[string]ServerInfo{},
		MCP:              map[string]ServerInfo{},
		PermissionMode:   string(proto.PermissionModeToProto(g.ws.PermissionMode())),
		InputMode:        g.InputMode(),
	}
	// One readiness call feeds both fields, so the snapshot cannot say the
	// agent is ready while its model says otherwise.
	s.Model = modelInfo(g)
	s.AgentReady = s.Model.AgentReady
	s.Team, s.AgentRuns = g.team.current()
	if branch, err := g.ws.GitBranch(ctx); err == nil {
		s.Branch = branch
	}
	for name, info := range g.ws.LSPGetStates() {
		s.LSP[name] = ServerInfo{
			State:           strconv.Itoa(int(info.State)),
			DiagnosticCount: info.DiagnosticCount,
			Error:           errText(info.Error),
		}
	}
	for name, info := range g.ws.MCPGetStates() {
		s.MCP[name] = ServerInfo{
			State:     info.State.String(),
			ToolCount: info.Counts.Tools,
			Error:     errText(info.Error),
		}
	}
	s.Permissions = entities(g.pending.permissions())
	for _, q := range g.pending.questions() {
		s.Questions = append(s.Questions, innerPayload(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: q}))
	}
	if sessionID == "" {
		return s
	}
	if sess, err := g.ws.GetSession(ctx, sessionID); err == nil {
		s.Session = innerPayload(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: sess})
	} else {
		slog.Debug("Web snapshot could not load session", "session_id", sessionID, "error", err)
	}
	if msgs, err := g.ws.ListMessages(ctx, sessionID); err == nil {
		s.Messages = entities(msgs)
	}
	if files, err := g.fileSummaries(ctx, sessionID); err == nil {
		s.Files = files
	} else {
		slog.Debug("Web snapshot could not list edited files", "session_id", sessionID, "error", err)
	}
	s.Busy = g.ws.AgentIsSessionBusy(sessionID)
	if q := g.ws.AgentQueuedPromptsList(sessionID); q != nil {
		s.Queued = q
	}
	return s
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// modelInfo describes the model the agent runs now, how it reasons and
// whether the agent is ready. The reasoning controls come from the selected
// main model in the config rather than from the model choices, so a
// provider the selector filters out keeps them; the switch helpers validate
// against the same model.
func modelInfo(g *Gateway) ModelInfo {
	m := g.ws.AgentModel()
	info := ModelInfo{
		ID:            m.ModelCfg.Model,
		Name:          m.CatwalkCfg.Name,
		Provider:      m.ModelCfg.Provider,
		ContextWindow: m.CatwalkCfg.ContextWindow,
		AgentReady:    g.ws.AgentReadyErr() == nil,
	}
	cfg := g.ws.Config()
	if cfg == nil {
		return info
	}
	if main, ok := cfg.Models[config.SelectedModelTypeMain]; ok {
		info.ReasoningEffort = main.ReasoningEffort
		info.Think = main.Think
	}
	info.CanReason, info.EffortLevels = workspace.ReasoningControls(mainCatalogModel(cfg))
	if small, ok := cfg.Models[config.SelectedModelTypeSmall]; ok {
		info.Small = &ModelRef{ID: small.Model, Provider: small.Provider}
	}
	return info
}

// mainCatalogModel returns the catalog entry of the selected main model, or
// nil when the config has no providers yet (a reload in flight).
func mainCatalogModel(cfg *config.Config) *catwalk.Model {
	if cfg == nil || cfg.Providers == nil {
		return nil
	}
	return cfg.GetModelByType(config.SelectedModelTypeMain)
}

// announceModel publishes the current model info when it differs from the
// one the hub last announced, and returns it. Handlers announce after a
// successful switch, so the watch does not repeat the frame; the pump
// announces every eighth follow tick to follow changes made in the
// terminal.
func (g *Gateway) announceModel() ModelInfo {
	g.modelMu.Lock()
	defer g.modelMu.Unlock()
	info := modelInfo(g)
	if reflect.DeepEqual(info, g.lastModel) {
		return info
	}
	g.lastModel = info
	g.hub.publishJSON(frameModelChanged, info)
	return info
}
