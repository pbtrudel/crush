package web

import (
	"testing"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pinentry"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/sshaskpass"
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

func TestPendingTracksSecurePrompts(t *testing.T) {
	t.Parallel()
	p := newPending()
	req := pinentry.PromptRequest{
		ID: "p1", Prompt: "Unlock the signing key", Kind: pinentry.KindPassphrase,
		Key: pinentry.Key{KeyID: "ABCDEF0123456789"},
	}
	require.True(t, p.observe(pubsub.Event[pinentry.PromptRequest]{Type: pubsub.CreatedEvent, Payload: req}))
	prompts := p.securePrompts()
	require.Len(t, prompts, 1)
	require.Equal(t, "pinentry", prompts[0].Source)
	require.Equal(t, "GPG passphrase", prompts[0].Title)
	require.Equal(t, []string{"Key: ABCDEF0123456789"}, prompts[0].Details)

	require.False(t, p.observe(pubsub.Event[pinentry.PromptRequest]{Type: pubsub.CreatedEvent, Payload: req}),
		"an unchanged prompt publishes nothing")

	require.False(t, p.observe(pubsub.Event[pinentry.Notification]{Payload: pinentry.Notification{RequestID: "other"}}),
		"a notification for an unknown prompt changes nothing")
	require.True(t, p.observe(pubsub.Event[pinentry.Notification]{Payload: pinentry.Notification{RequestID: "p1"}}))
	require.Empty(t, p.securePrompts())

	ssh := sshaskpass.PromptRequest{ID: "s1", Prompt: "git@github.com's password: "}
	require.True(t, p.observe(pubsub.Event[sshaskpass.PromptRequest]{Type: pubsub.CreatedEvent, Payload: ssh}))
	prompts = p.securePrompts()
	require.Len(t, prompts, 1)
	require.Equal(t, "ssh", prompts[0].Source)
	require.Equal(t, "password", prompts[0].Kind)
	require.Equal(t, "SSH Password", prompts[0].Title)
	require.Equal(t, []string{"git@github.com"}, prompts[0].Details)
}
