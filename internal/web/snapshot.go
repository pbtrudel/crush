package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/version"
)

// ModelInfo describes the active large model for the HUD.
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	ContextWindow int64  `json:"context_window"`
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
// fields hold the same JSON as the matching live event payloads. The
// config summary the spec mentions is deferred (see the plan deviations).
type Snapshot struct {
	Version          string                `json:"version"`
	WorkingDir       string                `json:"working_dir"`
	Branch           string                `json:"branch,omitempty"`
	CurrentSessionID string                `json:"current_session_id"`
	Session          json.RawMessage       `json:"session,omitempty"`
	Messages         []json.RawMessage     `json:"messages"`
	Permissions      []json.RawMessage     `json:"permissions"`
	Question         json.RawMessage       `json:"question,omitempty"`
	PermissionMode   string                `json:"permission_mode"`
	Model            ModelInfo             `json:"model"`
	Busy             bool                  `json:"busy"`
	Queued           []string              `json:"queued"`
	LSP              map[string]ServerInfo `json:"lsp"`
	MCP              map[string]ServerInfo `json:"mcp"`
}

func (g *Gateway) buildSnapshot(ctx context.Context, sessionID string) Snapshot {
	s := Snapshot{
		Version:          version.Version,
		WorkingDir:       g.ws.WorkingDir(),
		CurrentSessionID: sessionID,
		Messages:         []json.RawMessage{},
		Permissions:      []json.RawMessage{},
		Queued:           []string{},
		LSP:              map[string]ServerInfo{},
		MCP:              map[string]ServerInfo{},
		PermissionMode:   string(proto.PermissionModeToProto(g.ws.PermissionMode())),
		Model:            modelInfo(g),
	}
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
	if q := g.pending.question(); q != nil {
		s.Question = innerPayload(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: *q})
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

func modelInfo(g *Gateway) ModelInfo {
	m := g.ws.AgentModel()
	return ModelInfo{
		ID:            m.ModelCfg.Model,
		Name:          m.CatwalkCfg.Name,
		Provider:      m.ModelCfg.Provider,
		ContextWindow: m.CatwalkCfg.ContextWindow,
	}
}
