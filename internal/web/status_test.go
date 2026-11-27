package web

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/stretchr/testify/require"
)

// TestSessionStatusRanksPendingOverBusy pins the precedence the sidebar
// relies on: a session waiting for the person outranks one that is just
// working, which outranks one with a queued prompt.
func TestSessionStatusRanksPendingOverBusy(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	require.Equal(t, statusIdle, g.sessionStatus("s1"))

	fake.SetSessionBusy("s1", true)
	require.Equal(t, statusRunning, g.sessionStatus("s1"))

	fake.SetSessionQueued("s1", []string{"second prompt"})
	require.Equal(t, statusRunning, g.sessionStatus("s1"), "a busy session outranks its queue")

	g.pending.observe(permissionEvent("p1", "s1", "tc-1"))
	require.Equal(t, statusPendingInteraction, g.sessionStatus("s1"))

	g.pending.observe(permissionResolvedEvent("s1", "tc-1"))
	require.Equal(t, statusRunning, g.sessionStatus("s1"))

	fake.SetSessionBusy("s1", false)
	require.Equal(t, statusQueued, g.sessionStatus("s1"))

	fake.SetSessionQueued("s1", nil)
	require.Equal(t, statusIdle, g.sessionStatus("s1"))
}

// TestSessionStatusCountsAQuestion pins the other interaction source.
func TestSessionStatusCountsAQuestion(t *testing.T) {
	t.Parallel()
	g, _ := startTest(t)
	g.pending.observe(questionEvent("b1", "s2"))
	require.Equal(t, statusPendingInteraction, g.sessionStatus("s2"))
	require.Equal(t, statusIdle, g.sessionStatus("s1"))
}

// TestSessionStatusPublishesOnChangeOnly pins the dedupe: recomputing an
// unchanged status publishes nothing, so a reconnect does not replay a burst
// of unchanged values.
func TestSessionStatusPublishesOnChangeOnly(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	g.seedSessionStatuses(t.Context())

	g.refreshSessionStatus("s1")
	require.Empty(t, statusFrames(g), "the seeded status did not change")

	fake.SetSessionBusy("s1", true)
	g.refreshSessionStatus("s1")
	require.Equal(t, []string{`{"session_id":"s1","status":"running"}`}, statusFrames(g))

	g.refreshSessionStatus("s1")
	require.Len(t, statusFrames(g), 1, "an unchanged status publishes nothing")

	fake.SetSessionBusy("s1", false)
	g.refreshSessionStatus("s1")
	require.Equal(t, []string{
		`{"session_id":"s1","status":"running"}`,
		`{"session_id":"s1","status":"idle"}`,
	}, statusFrames(g))
}

// TestStatusSessionID pins which events can change a status, and for which
// session: the pump recomputes only that session, with no periodic poll.
func TestStatusSessionID(t *testing.T) {
	t.Parallel()
	require.Equal(t, "s2", statusSessionID(permissionEvent("p1", "s2", "tc-1")))
	require.Equal(t, "s2", statusSessionID(permissionResolvedEvent("s2", "tc-1")))
	require.Equal(t, "s3", statusSessionID(questionEvent("b1", "s3")))
	require.Equal(t, "s4", statusSessionID(pubsub.Event[question.Notification]{
		Type: pubsub.UpdatedEvent, Payload: question.Notification{BatchID: "b1", SessionID: "s4"},
	}))
	require.Equal(t, "s1", statusSessionID(runCompleteEvent("s1")))
	require.Equal(t, "s1", statusSessionID(pubsub.Event[message.Message]{
		Type: pubsub.CreatedEvent, Payload: message.Message{SessionID: "s1", Role: message.User},
	}))
	require.Empty(t, statusSessionID(pubsub.Event[message.Message]{
		Type: pubsub.UpdatedEvent, Payload: message.Message{SessionID: "s1", Role: message.Assistant},
	}), "a streamed assistant chunk changes no status")
	require.Empty(t, statusSessionID(pubsub.Event[history.File]{Type: pubsub.CreatedEvent}))
}

// TestStatusClearsWhenARunEnds pins the event path: the run_complete event
// the workspace emits clears the marker, with no manual refresh and no
// periodic poll anywhere in the gateway.
func TestStatusClearsWhenARunEnds(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.SetSessionBusy("s1", true)
	g.refreshSessionStatus("s1")
	require.Equal(t, []string{`{"session_id":"s1","status":"running"}`}, statusFrames(g))

	fake.SetSessionBusy("s1", false)
	fake.Emit(runCompleteEvent("s1"))
	require.Eventually(t, func() bool {
		return len(statusFrames(g)) == 2
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, `{"session_id":"s1","status":"idle"}`, statusFrames(g)[1])
}

// statusFrames returns every session_status payload in the replay ring,
// oldest first, as JSON text.
func statusFrames(g *Gateway) []string {
	var out []string
	for _, f := range g.hub.frames() {
		if f.Type == frameSessionStatus {
			out = append(out, string(f.Payload))
		}
	}
	return out
}

func permissionEvent(id, sessionID, toolCallID string) any {
	return pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
		ID: id, SessionID: sessionID, ToolCallID: toolCallID, ToolName: "bash", Description: "Run tests",
	}}
}

func permissionResolvedEvent(sessionID, toolCallID string) any {
	return pubsub.Event[permission.PermissionNotification]{Type: pubsub.UpdatedEvent, Payload: permission.PermissionNotification{
		SessionID: sessionID, ToolCallID: toolCallID, Granted: true,
	}}
}

func questionEvent(batchID, sessionID string) any {
	return pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: question.Request{
		ID: batchID, SessionID: sessionID,
		Questions: []question.Question{{ID: "q1", Type: question.TypeSingleChoice, Text: "Which?"}},
	}}
}

func runCompleteEvent(sessionID string) any {
	return pubsub.Event[notify.RunComplete]{Type: pubsub.UpdatedEvent, Payload: notify.RunComplete{
		SessionID: sessionID, RunID: "r1", MessageID: "a1", Text: "done",
	}}
}

// TestSessionsListCarriesStatus pins the row the sidebar renders.
func TestSessionsListCarriesStatus(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.SetSessionBusy("s1", true)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/sessions", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	var rows []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(body, &rows))
	require.Len(t, rows, 1)
	require.Equal(t, "s1", rows[0].ID)
	require.Equal(t, statusRunning, rows[0].Status)
}
