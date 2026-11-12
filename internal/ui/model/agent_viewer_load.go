package model

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
)

type agentViewerLoadMsg struct {
	generation uint64
	sessionID  string
	reconcile  bool
	session    session.Session
	messages   []message.Message
	projection transcriptProjection
	busy       bool
	err        error
}

func (v *agentViewer) Start() tea.Cmd {
	v.loading = true
	v.loadErr = nil
	v.chat.SetExternalAnimation(true)
	return v.loadSnapshot(false)
}

func (v *agentViewer) loadSnapshot(reconcile bool) tea.Cmd {
	generation := v.generation
	sessionID := v.descriptor.SessionID
	return func() tea.Msg {
		ctx, cancel := common.WorkspaceOpContext()
		defer cancel()
		sess, err := v.com.Workspace.GetSession(ctx, sessionID)
		if err != nil {
			return agentViewerLoadMsg{generation: generation, sessionID: sessionID, reconcile: reconcile, err: err}
		}
		msgs, err := v.com.Workspace.ListMessages(ctx, sessionID)
		if err != nil {
			return agentViewerLoadMsg{generation: generation, sessionID: sessionID, reconcile: reconcile, err: err}
		}
		projection := projectTranscriptMessages(v.com, msgs)
		loadNestedToolCalls(v.com, projection.items)
		busy := v.com.Workspace.AgentIsSessionBusy(sessionID)
		return agentViewerLoadMsg{generation: generation, sessionID: sessionID, reconcile: reconcile, session: sess, messages: msgs, projection: projection, busy: busy}
	}
}
