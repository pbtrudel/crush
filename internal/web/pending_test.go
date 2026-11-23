package web

import (
	"testing"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/stretchr/testify/require"
)

func TestPendingTracksPermissions(t *testing.T) {
	t.Parallel()
	p := newPending()
	req := permission.PermissionRequest{ID: "p1", SessionID: "s1", ToolCallID: "tc1", ToolName: "bash"}
	p.observe(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: req})
	got, ok := p.permission("p1")
	require.True(t, ok)
	require.Equal(t, req, got)
	require.Len(t, p.permissions(), 1)

	p.observe(pubsub.Event[permission.PermissionNotification]{Payload: permission.PermissionNotification{ToolCallID: "tc1", Granted: true}})
	_, ok = p.permission("p1")
	require.False(t, ok)
	require.Empty(t, p.permissions())
}

func TestPendingTracksQuestionsByBatch(t *testing.T) {
	t.Parallel()
	p := newPending()
	first := question.Request{ID: "q1", SessionID: "s1"}
	second := question.Request{ID: "q2", SessionID: "s2"}
	p.observe(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: first})
	p.observe(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: second})
	require.Equal(t, []question.Request{first, second}, p.questions(), "arrival order is preserved")

	got, ok := p.question("q1")
	require.True(t, ok)
	require.Equal(t, first, got)

	p.observe(pubsub.Event[question.Notification]{Payload: question.Notification{BatchID: "other"}})
	require.Len(t, p.questions(), 2, "a notification for an unknown batch changes nothing")
	p.observe(pubsub.Event[question.Notification]{Payload: question.Notification{BatchID: "q1"}})
	require.Equal(t, []question.Request{second}, p.questions())

	p.observe(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: first})
	require.Equal(t, []question.Request{second, first}, p.questions(), "a resubscribed batch moves to the back")
	p.forgetQuestion("q2")
	require.Equal(t, []question.Request{first}, p.questions(), "forgetting one batch keeps the others")
}

func TestPendingIgnoresNotificationWithoutToolCall(t *testing.T) {
	t.Parallel()
	p := newPending()
	p.observe(pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{ID: "p1", SessionID: "s1"}})
	p.observe(pubsub.Event[permission.PermissionNotification]{Type: pubsub.UpdatedEvent, Payload: permission.PermissionNotification{SessionID: "s1"}})
	require.Len(t, p.permissions(), 1, "an empty tool call id matches nothing")
}

func TestPendingForgetsFinishedSession(t *testing.T) {
	t.Parallel()
	p := newPending()
	p.observe(pubsub.Event[permission.PermissionRequest]{Payload: permission.PermissionRequest{ID: "p1", SessionID: "s1", ToolCallID: "tc1"}})
	p.observe(pubsub.Event[permission.PermissionRequest]{Payload: permission.PermissionRequest{ID: "p2", SessionID: "s2", ToolCallID: "tc2"}})
	p.observe(pubsub.Event[question.Request]{Payload: question.Request{ID: "q1", SessionID: "s1"}})
	p.observe(pubsub.Event[question.Request]{Payload: question.Request{ID: "q2", SessionID: "s2"}})

	p.observe(pubsub.Event[notify.RunComplete]{Payload: notify.RunComplete{SessionID: "s1"}})

	_, ok := p.permission("p1")
	require.False(t, ok)
	_, ok = p.permission("p2")
	require.True(t, ok)
	_, ok = p.question("q1")
	require.False(t, ok, "the finished session's batch is forgotten")
	_, ok = p.question("q2")
	require.True(t, ok, "another session's batch stays pending")
}
