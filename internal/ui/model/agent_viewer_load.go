package model

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
)

type agentViewerLoadMsg struct {
	generation      uint64
	themeGeneration uint64
	sessionID       string
	reconcile       bool
	session         session.Session
	messages        []message.Message
	projection      transcriptProjection
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
	projectionOptions := newTranscriptProjectionOptions(v.com)
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
		projection := projectTranscriptMessagesWithOptions(projectionOptions, msgs)
		loadNestedToolCallsWithOptions(ws, projectionOptions, projection.items)
		busy := ws.AgentIsSessionBusy(sessionID)
		return agentViewerLoadMsg{generation: generation, themeGeneration: themeGeneration, sessionID: sessionID, reconcile: reconcile, session: sess, messages: msgs, projection: projection, busy: busy}
	}
}
