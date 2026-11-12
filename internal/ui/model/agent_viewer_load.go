package model

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agent"
	agenttools "github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/workspace"
)

type agentViewerLoadMsg struct {
	generation      uint64
	themeGeneration uint64
	sessionID       string
	reconcile       bool
	session         session.Session
	messages        []message.Message
	nestedMessages  map[string][]message.Message
	busy            bool
	err             error
}

func (v *agentViewer) Start() tea.Cmd {
	v.loading = true
	v.loadErr = nil
	v.chat.SetExternalAnimation(true)
	return v.loadSnapshot(false)
}

func (v *agentViewer) loadSnapshot(reconcile bool) tea.Cmd {
	generation := v.generation
	themeGeneration := v.themeGeneration
	sessionID := v.descriptor.SessionID
	ws := v.com.Workspace
	return func() tea.Msg {
		ctx, cancel := common.WorkspaceOpContext()
		defer cancel()
		sess, err := ws.GetSession(ctx, sessionID)
		if err != nil {
			return agentViewerLoadMsg{generation: generation, themeGeneration: themeGeneration, sessionID: sessionID, reconcile: reconcile, err: err}
		}
		msgs, err := ws.ListMessages(ctx, sessionID)
		if err != nil {
			return agentViewerLoadMsg{generation: generation, themeGeneration: themeGeneration, sessionID: sessionID, reconcile: reconcile, err: err}
		}
		nestedMessages := loadNestedTranscripts(ws, msgs)
		busy := ws.AgentIsSessionBusy(sessionID)
		return agentViewerLoadMsg{generation: generation, themeGeneration: themeGeneration, sessionID: sessionID, reconcile: reconcile, session: sess, messages: msgs, nestedMessages: nestedMessages, busy: busy}
	}
}

// loadNestedTranscripts recursively fetches the raw messages behind every
// agent, dispatch, and agentic-fetch call. It deliberately constructs no chat
// items and reads no presentation state; HandleLoad projects these transcripts
// later on the UI goroutine.
func loadNestedTranscripts(ws workspace.Workspace, root []message.Message) map[string][]message.Message {
	transcripts := make(map[string][]message.Message)
	var load func([]message.Message)
	load = func(msgs []message.Message) {
		for i := range msgs {
			for _, call := range msgs[i].ToolCalls() {
				if !nestedTranscriptTool(call.Name) {
					continue
				}
				sessionID := ws.CreateAgentToolSessionID(msgs[i].ID, call.ID)
				if _, seen := transcripts[sessionID]; seen {
					continue
				}
				// Mark the session before reading to make malformed cyclic
				// transcript references terminate.
				transcripts[sessionID] = nil
				ctx, cancel := common.WorkspaceOpContext()
				nested, err := ws.ListMessages(ctx, sessionID)
				cancel()
				if err != nil || len(nested) == 0 {
					continue
				}
				transcripts[sessionID] = nested
				load(nested)
			}
		}
	}
	load(root)
	return transcripts
}

func nestedTranscriptTool(name string) bool {
	switch name {
	case agent.AgentToolName, agent.AgentDispatchToolName, agenttools.AgenticFetchToolName:
		return true
	default:
		return false
	}
}
