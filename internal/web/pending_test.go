package web

import (
	"testing"

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

func TestPendingTracksQuestion(t *testing.T) {
	t.Parallel()
	p := newPending()
	q := question.Request{ID: "q1", SessionID: "s1"}
	p.observe(pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: q})
	require.Equal(t, &q, p.question())
	p.observe(pubsub.Event[question.Notification]{Payload: question.Notification{BatchID: "other"}})
	require.NotNil(t, p.question())
	p.observe(pubsub.Event[question.Notification]{Payload: question.Notification{BatchID: "q1"}})
	require.Nil(t, p.question())
}
