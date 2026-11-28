package workspace

import (
	"context"
	"testing"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

// settleCoordinator is the slice of agent.Coordinator the paging path uses:
// it runs the real settle against the workspace's message store, with the
// busy set the test gives it.
type settleCoordinator struct {
	agent.Coordinator
	messages message.Service
	busy     map[string]bool
}

func (c settleCoordinator) SettleSession(ctx context.Context, sessionID string) error {
	return agent.SettleSession(ctx, c.messages, func(id string) bool { return c.busy[id] }, sessionID)
}

// openToolCall stores the shape a process killed mid tool call leaves behind:
// an assistant message holding a call, no result anywhere and no finish part.
func openToolCall(t *testing.T, messages message.Service, sessionID, callID string) message.Message {
	t.Helper()
	msg, err := messages.Create(t.Context(), sessionID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: callID, Name: "bash", Input: `{"command":"sleep 30"}`, Finished: true},
		},
	})
	require.NoError(t, err)
	return msg
}

// resultsOf collects the tool results a page carries, keyed by call.
func resultsOf(msgs []message.Message) map[string]message.ToolResult {
	out := make(map[string]message.ToolResult)
	for _, m := range msgs {
		for _, tr := range m.ToolResults() {
			out[tr.ToolCallID] = tr
		}
	}
	return out
}

// TestListMessagesPageSettlesAnIdleSession pins the crash path web mode must
// not lose: a tab opening a session the process died in the middle of shows
// the interrupted call's result, so the page has to be settled before it is
// read from the store.
func TestListMessagesPageSettlesAnIdleSession(t *testing.T) {
	w, a, sess := shellTestWorkspace(t)
	a.AgentCoordinator = settleCoordinator{messages: a.Messages}
	assistant := openToolCall(t, a.Messages, sess.ID, "call_killed")

	page, _, err := w.ListMessagesPage(t.Context(), sess.ID, "", 50)
	require.NoError(t, err)

	result, ok := resultsOf(page)["call_killed"]
	require.True(t, ok, "the page a tab reads carries the orphan's result")
	require.True(t, result.IsError)

	reloaded, err := a.Messages.Get(t.Context(), assistant.ID)
	require.NoError(t, err)
	require.True(t, reloaded.ToolCalls()[0].Finished, "the call is marked settled")
	require.True(t, reloaded.IsFinished(), "the turn is marked finished")
}

// A live run owns its unanswered calls, so a busy session is paged exactly as
// stored: settling it would answer a call the run is about to.
func TestListMessagesPageLeavesABusySessionAlone(t *testing.T) {
	w, a, sess := shellTestWorkspace(t)
	a.AgentCoordinator = settleCoordinator{messages: a.Messages, busy: map[string]bool{sess.ID: true}}
	assistant := openToolCall(t, a.Messages, sess.ID, "call_running")

	page, _, err := w.ListMessagesPage(t.Context(), sess.ID, "", 50)
	require.NoError(t, err)
	require.Empty(t, resultsOf(page), "a busy session is paged without a repair")

	reloaded, err := a.Messages.Get(t.Context(), assistant.ID)
	require.NoError(t, err)
	require.False(t, reloaded.IsFinished(), "a live run's turn is not finished by a page")
}

// TestListMessagesPageSettlesBeforeTheAgentExists pins the tab that opens
// before the coder agent has initialised: with no coordinator nothing can be
// running the session, so the page still settles what a dead process left.
func TestListMessagesPageSettlesBeforeTheAgentExists(t *testing.T) {
	w, a, sess := shellTestWorkspace(t)
	require.Nil(t, a.Agent(), "the workspace starts with no coordinator")
	assistant := openToolCall(t, a.Messages, sess.ID, "call_killed")

	page, _, err := w.ListMessagesPage(t.Context(), sess.ID, "", 50)
	require.NoError(t, err)

	result, ok := resultsOf(page)["call_killed"]
	require.True(t, ok, "a page read before the agent exists still settles the orphan")
	require.True(t, result.IsError)

	reloaded, err := a.Messages.Get(t.Context(), assistant.ID)
	require.NoError(t, err)
	require.True(t, reloaded.IsFinished())
}
