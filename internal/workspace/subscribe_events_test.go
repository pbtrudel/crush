package workspace

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestClientWorkspaceSubscribeEventsFansOut(t *testing.T) {
	t.Parallel()
	w := &ClientWorkspace{}
	ctx, cancel := context.WithCancel(t.Context())
	a := w.SubscribeEvents(ctx)
	b := w.SubscribeEvents(ctx)

	ev := pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{ID: "m1"}}
	w.broadcast(ev)

	for _, ch := range []<-chan any{a, b} {
		select {
		case got := <-ch:
			require.Equal(t, ev, got)
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}

	cancel()
	require.Eventually(t, func() bool {
		_, open := <-a
		return !open
	}, time.Second, 10*time.Millisecond)
}

func TestClientWorkspaceCurrentSessionID(t *testing.T) {
	t.Parallel()
	w := &ClientWorkspace{}
	require.Empty(t, w.CurrentSessionID())
	w.mu.Lock()
	w.lastSession = "s1"
	w.mu.Unlock()
	require.Equal(t, "s1", w.CurrentSessionID())
}

func TestClientWorkspaceConsumeEventsFansOutAndStillSends(t *testing.T) {
	t.Parallel()
	w := &ClientWorkspace{}
	sub := w.SubscribeEvents(t.Context())

	evc := make(chan any, 2)
	evc <- pubsub.Event[proto.PermissionModeEvent]{
		Type:    pubsub.UpdatedEvent,
		Payload: proto.PermissionModeEvent{Mode: proto.WorkspacePermissionModeYolo},
	}
	evc <- pubsub.Event[proto.HostThemeState]{Type: pubsub.UpdatedEvent}
	close(evc)

	var sent []tea.Msg
	w.ConsumeEventsForTest(evc, func(msg tea.Msg) { sent = append(sent, msg) })

	// translateEvent returns nil for mode changes, but subscribers still
	// see them as domain events.
	select {
	case got := <-sub:
		ev, ok := got.(pubsub.Event[permission.ModeChangedEvent])
		require.True(t, ok, "got %T", got)
		require.Equal(t, permission.PermissionModeYolo, ev.Payload.Mode)
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the mode change")
	}
	select {
	case got := <-sub:
		require.IsType(t, HostThemeEvent{}, got)
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the theme event")
	}
	// The TUI callback still receives translated events.
	require.Len(t, sent, 1)
	require.IsType(t, HostThemeEvent{}, sent[0])
}

func TestAppWorkspaceSubscribeEventsCoexistsWithTUISubscription(t *testing.T) {
	t.Parallel()
	a := app.NewForTest(t.Context())
	t.Cleanup(a.ShutdownForTest)
	w := NewAppWorkspace(a, nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	web := w.SubscribeEvents(ctx)
	tui := a.Events(ctx) // The TUI subscribes through app.Events as well.

	require.Eventually(t, func() bool {
		a.SendEvent(pubsub.Event[message.Message]{
			Type:    pubsub.CreatedEvent,
			Payload: message.Message{ID: "m1"},
		})
		select {
		case got := <-web:
			ev, ok := got.(pubsub.Event[message.Message])
			return ok && ev.Payload.ID == "m1"
		case <-time.After(20 * time.Millisecond):
			return false
		}
	}, 2*time.Second, 10*time.Millisecond)

	select {
	case ev := <-tui:
		_, ok := ev.Payload.(pubsub.Event[message.Message])
		require.True(t, ok, "TUI subscription still receives events")
	case <-time.After(time.Second):
		t.Fatal("TUI subscription starved by the gateway subscription")
	}
}

func TestClientWorkspaceSubscribeAfterShutdownIsClosed(t *testing.T) {
	t.Parallel()
	w := &ClientWorkspace{}
	w.closeFan()
	ch := w.SubscribeEvents(t.Context())
	select {
	case _, open := <-ch:
		require.False(t, open, "a subscription after shutdown must be closed at once")
	case <-time.After(time.Second):
		t.Fatal("subscription after shutdown stayed open")
	}
}

func TestDropWarnerThrottles(t *testing.T) {
	t.Parallel()
	var d dropWarner
	start := time.Unix(1000, 0)
	total, warn := d.drop(start)
	require.True(t, warn)
	require.EqualValues(t, 1, total)
	_, warn = d.drop(start.Add(time.Second))
	require.False(t, warn, "a second drop inside the interval stays quiet")
	total, warn = d.drop(start.Add(dropWarnInterval + time.Second))
	require.True(t, warn)
	require.EqualValues(t, 3, total, "the warning carries every drop since the start")
}
