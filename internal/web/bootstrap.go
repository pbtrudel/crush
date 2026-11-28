package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/version"
)

// Bootstrap is the global state a shell renders before it subscribes to any
// session: what the gateway is, what the agent runs, and the interactions
// that wait for the person.
type Bootstrap struct {
	Version            string                `json:"version"`
	WorkingDir         string                `json:"working_dir"`
	Branch             string                `json:"branch,omitempty"`
	CurrentSessionID   string                `json:"current_session_id"`
	Sessions           []sessionRow          `json:"sessions"`
	Model              ModelInfo             `json:"model"`
	AgentReady         bool                  `json:"agent_ready"`
	PermissionMode     string                `json:"permission_mode"`
	InputMode          string                `json:"input_mode"`
	PlanReadySessionID string                `json:"plan_ready_session_id,omitempty"`
	LSP                map[string]ServerInfo `json:"lsp"`
	MCP                map[string]ServerInfo `json:"mcp"`
	Team               TeamState             `json:"team"`
	AgentRuns          []agentrun.Run        `json:"agent_runs"`
	Permissions        []json.RawMessage     `json:"permissions"`
	Questions          []json.RawMessage     `json:"questions,omitempty"`
	SecurePrompts      []SecurePrompt        `json:"secure_prompts,omitempty"`
	SignIn             *SignInState          `json:"signin,omitempty"`
	AWSSSO             *AWSSSO               `json:"aws_sso,omitempty"`
}

// SessionSnapshot is the state of one subscribed session: the session, its
// latest page of messages, the edited files, and whether a run is in flight
// or queued.
type SessionSnapshot struct {
	SessionID string            `json:"session_id"`
	Session   json.RawMessage   `json:"session,omitempty"`
	Messages  []json.RawMessage `json:"messages"`
	HasMore   bool              `json:"has_more"`
	Files     []FileSummary     `json:"files"`
	Busy      bool              `json:"busy"`
	Queued    []string          `json:"queued"`
}

// buildBootstrap reads the global half of the old snapshot, plus the session
// list with its live statuses.
func (g *Gateway) buildBootstrap(ctx context.Context) Bootstrap {
	b := Bootstrap{
		Version:            version.Version,
		WorkingDir:         g.ws.WorkingDir(),
		CurrentSessionID:   g.ws.CurrentSessionID(),
		Sessions:           []sessionRow{},
		Permissions:        []json.RawMessage{},
		LSP:                map[string]ServerInfo{},
		MCP:                map[string]ServerInfo{},
		PermissionMode:     string(proto.PermissionModeToProto(g.ws.PermissionMode())),
		InputMode:          g.InputMode(),
		PlanReadySessionID: g.PlanReadySessionID(),
	}
	// One readiness call feeds both fields, so a tab cannot read the agent
	// as ready while the model says otherwise.
	readyErr := g.ws.AgentReadyErr()
	b.Model = modelInfo(g, readyErr == nil)
	b.AgentReady = b.Model.AgentReady
	// Every array in the payload is non-nil: the frontend's zod schema reads
	// a null array as a contract break, and the workspace returns nil slices
	// when it has no runs, no roster or no questions.
	b.Team, b.AgentRuns = g.team.current()
	if b.AgentRuns == nil {
		b.AgentRuns = []agentrun.Run{}
	}
	if b.Team.Members == nil {
		b.Team.Members = []agentteam.Member{}
	}
	if b.Team.Tasks == nil {
		b.Team.Tasks = []TeamTask{}
	}
	if branch, err := g.ws.GitBranch(ctx); err == nil {
		b.Branch = branch
	}
	if list, err := g.ws.ListSessions(ctx); err == nil {
		sortSessionsNewestFirst(list)
		b.Sessions = g.sessionRows(list)
	} else {
		slog.Debug("Web bootstrap could not list sessions", "error", err)
	}
	for name, info := range g.ws.LSPGetStates() {
		b.LSP[name] = ServerInfo{
			State:           strconv.Itoa(int(info.State)),
			DiagnosticCount: info.DiagnosticCount,
			Error:           errText(info.Error),
		}
	}
	for name, info := range g.ws.MCPGetStates() {
		b.MCP[name] = ServerInfo{
			State:     info.State.String(),
			ToolCount: info.Counts.Tools,
			Error:     errText(info.Error),
		}
	}
	b.Permissions = entities(g.pending.permissions())
	b.Questions = []json.RawMessage{}
	if prompts := g.pending.securePrompts(); len(prompts) > 0 {
		b.SecurePrompts = prompts
	}
	b.SignIn = g.signInState()
	b.AWSSSO = g.pending.awsSSO()
	for _, q := range g.pending.questions() {
		b.Questions = append(b.Questions, innerPayload(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: q}))
	}
	return b
}

// buildSessionSnapshot reads one session's page of state from the session the
// caller already read, so no state can change between the two reads.
func (g *Gateway) buildSessionSnapshot(ctx context.Context, sess session.Session, before string, limit int) SessionSnapshot {
	s := SessionSnapshot{
		SessionID: sess.ID,
		Session:   innerPayload(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: sess}),
		Messages:  []json.RawMessage{},
		Files:     []FileSummary{},
		Queued:    []string{},
	}
	if page, hasMore, err := g.messagePage(ctx, sess.ID, before, limit); err == nil {
		s.Messages = entities(page)
		s.HasMore = hasMore
	} else {
		slog.Debug("Web session snapshot could not list messages", "session_id", sess.ID, "error", err)
	}
	if files, err := g.fileSummaries(ctx, sess.ID); err == nil {
		s.Files = files
	} else {
		slog.Debug("Web session snapshot could not list edited files", "session_id", sess.ID, "error", err)
	}
	s.Busy = g.ws.AgentIsSessionBusy(sess.ID)
	if q := g.ws.AgentQueuedPromptsList(sess.ID); q != nil {
		s.Queued = q
	}
	return s
}

// handleBootstrap answers the global state a shell renders before it
// subscribes to a session.
func (g *Gateway) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.buildBootstrap(r.Context()))
}
