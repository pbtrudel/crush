package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// queueWorkspace extends channelWorkspace with the queue operations the
// cancel paths touch; it records which of them ran so tests can pin the
// escape behavior.
type queueWorkspace struct {
	channelWorkspace
	calls []string
}

func (w *queueWorkspace) AgentCancel(string)     { w.calls = append(w.calls, "cancel") }
func (w *queueWorkspace) AgentClearQueue(string) { w.calls = append(w.calls, "clear") }

// TestCancelAgentRestoresQueuedPrompts pins that the double-escape cancel
// hands the queued prompts back to the editor before the cancel discards
// them, so nothing the user already typed is lost.
func TestCancelAgentRestoresQueuedPrompts(t *testing.T) {
	ws := &queueWorkspace{channelWorkspace: channelWorkspace{ready: true}}
	m := newChannelUI(ws)
	m.session = &session.Session{ID: "sess-1"}
	m.agentReady = true
	m.isCanceling = true

	m.promptQueue = 2
	m.promptQueueItems = []string{"first queued", "second queued"}

	m.cancelAgent()

	require.Equal(t, []string{"cancel"}, ws.calls,
		"the restore must come from the cached mirror, not a workspace probe")
	require.Equal(t, "first queued\n\nsecond queued", m.textarea.Value())
	require.Zero(t, m.promptQueue, "the queue mirror must be cleared after the restore")
}

// TestCancelAgentRestoreKeepsDraft pins that an in-progress draft stays in
// the editor, below the restored queue.
func TestCancelAgentRestoreKeepsDraft(t *testing.T) {
	ws := &queueWorkspace{channelWorkspace: channelWorkspace{ready: true}}
	m := newChannelUI(ws)
	m.session = &session.Session{ID: "sess-1"}
	m.agentReady = true
	m.isCanceling = true
	m.promptQueue = 1
	m.promptQueueItems = []string{"queued"}
	m.textarea.InsertString("draft in progress")

	m.cancelAgent()

	require.Equal(t, "queued\n\ndraft in progress", m.textarea.Value())
}

// TestClearQueueRestoresPrompts pins that the single-escape queue clear
// also hands the texts back instead of erasing them.
func TestClearQueueRestoresPrompts(t *testing.T) {
	ws := &queueWorkspace{channelWorkspace: channelWorkspace{ready: true}}
	m := newChannelUI(ws)
	m.session = &session.Session{ID: "sess-1"}
	m.agentReady = true
	m.promptQueue = 1
	m.promptQueueItems = []string{"queued one"}

	m.cancelAgent()

	require.Equal(t, []string{"clear"}, ws.calls,
		"clearing must not probe the workspace from the Update goroutine")
	require.Equal(t, "queued one", m.textarea.Value())
}
