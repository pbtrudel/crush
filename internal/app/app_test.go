package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// TestSetupSubscriber_NormalFlow verifies that events published to the source
// broker are forwarded to the output broker.
func TestSetupSubscriber_NormalFlow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	src := pubsub.NewBroker[string]()
	defer src.Shutdown()
	out := pubsub.NewBroker[tea.Msg]()
	defer out.Shutdown()

	ch := out.Subscribe(ctx)

	var wg sync.WaitGroup
	app := &App{serviceEventsWG: &wg, events: out}
	subscribe(app, ctx, "test", src.Subscribe)

	// Yield so the subscriber goroutine can call src.Subscribe before we publish.
	time.Sleep(10 * time.Millisecond)

	src.Publish(pubsub.CreatedEvent, "hello")
	src.Publish(pubsub.CreatedEvent, "world")

	for range 2 {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for forwarded event")
		}
	}

	cancel()
	wg.Wait()
}

// TestSetupSubscriber_ContextCancellation verifies the goroutine exits cleanly
// when the context is cancelled.
func TestSetupSubscriber_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	src := pubsub.NewBroker[string]()
	defer src.Shutdown()
	out := pubsub.NewBroker[tea.Msg]()
	defer out.Shutdown()

	var wg sync.WaitGroup
	app := &App{serviceEventsWG: &wg, events: out}
	subscribe(app, ctx, "test", src.Subscribe)

	src.Publish(pubsub.CreatedEvent, "event")
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("subscribe goroutine did not exit after context cancellation")
	}
}

// TestEvents_ZeroConsumers verifies that publishing with no subscribers does
// not block or panic.
func TestEvents_ZeroConsumers(t *testing.T) {
	t.Parallel()

	broker := pubsub.NewBroker[tea.Msg]()
	defer broker.Shutdown()

	require.Equal(t, 0, broker.GetSubscriberCount())

	// Must not block.
	done := make(chan struct{})
	go func() {
		broker.Publish(pubsub.UpdatedEvent, tea.Msg("msg1"))
		broker.Publish(pubsub.UpdatedEvent, tea.Msg("msg2"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish with zero consumers blocked")
	}
}

// TestEvents_OneConsumer verifies that a single subscriber receives every event
// exactly once.
func TestEvents_OneConsumer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	broker := pubsub.NewBroker[tea.Msg]()
	defer broker.Shutdown()

	ch := broker.Subscribe(ctx)

	const n = 10
	for i := range n {
		broker.Publish(pubsub.UpdatedEvent, tea.Msg(i))
	}

	for i := range n {
		select {
		case ev := <-ch:
			require.Equal(t, tea.Msg(i), ev.Payload)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

// TestEvents_NConsumers verifies that every subscriber receives every event
// exactly once, regardless of how many concurrent consumers are attached.
func TestEvents_NConsumers(t *testing.T) {
	t.Parallel()

	for _, n := range []int{2, 5, 10} {
		t.Run(fmt.Sprintf("consumers=%d", n), func(t *testing.T) {
			t.Parallel()
			testNConsumers(t, n)
		})
	}
}

func testNConsumers(t *testing.T, n int) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	broker := pubsub.NewBroker[tea.Msg]()
	defer broker.Shutdown()

	// Subscribe all N consumers before publishing.
	channels := make([]<-chan pubsub.Event[tea.Msg], n)
	for i := range n {
		channels[i] = broker.Subscribe(ctx)
	}
	require.Equal(t, n, broker.GetSubscriberCount())

	const numEvents = 20
	for i := range numEvents {
		broker.Publish(pubsub.UpdatedEvent, tea.Msg(i))
	}

	// Each consumer must receive all numEvents messages.
	var wg sync.WaitGroup
	for i, ch := range channels {
		wg.Go(func() {
			for j := range numEvents {
				select {
				case ev := <-ch:
					require.Equal(t, tea.Msg(j), ev.Payload,
						"consumer %d: wrong payload for event %d", i, j)
				case <-time.After(5 * time.Second):
					t.Errorf("consumer %d: timed out waiting for event %d", i, j)
					return
				}
			}
		})
	}
	wg.Wait()
}

func staleSnapshot(changed ...string) config.StalenessResult {
	return config.StalenessResult{Dirty: len(changed) > 0, Changed: changed}
}

// TestStaleWatcherRequiresTwoPolls guards the debounce: a config write Crush
// is performing itself shows up dirty for one poll at most, and must not
// produce a reload prompt.
func TestStaleWatcherRequiresTwoPolls(t *testing.T) {
	t.Parallel()

	var w staleWatcher
	_, ok := w.observe(staleSnapshot("/home/u/.config/crush/crush.json"))
	require.False(t, ok, "first dirty poll must only arm the watcher")

	_, ok = w.observe(staleSnapshot())
	require.False(t, ok, "a clean poll must disarm the watcher")

	_, ok = w.observe(staleSnapshot("/home/u/.config/crush/crush.json"))
	require.False(t, ok, "the change has to be seen twice in a row")
}

// TestStaleWatcherAnnouncesOnce verifies a persistent change is announced
// exactly once, and that a different change set is announced again.
func TestStaleWatcherAnnouncesOnce(t *testing.T) {
	t.Parallel()

	var w staleWatcher
	changed := staleSnapshot("/home/u/.config/crush/crushrc", "/home/u/.config/crush/crush.json")

	_, ok := w.observe(changed)
	require.False(t, ok)
	notice, ok := w.observe(changed)
	require.True(t, ok)
	require.Equal(t, changed.Changed, notice.Changed)

	_, ok = w.observe(changed)
	require.False(t, ok, "an unchanged change set must not be announced twice")

	// A different change set is a new notice, but it still has to be seen
	// twice (the debounce is about the change, not just its first sighting).
	other := staleSnapshot("/home/u/.config/crush/crushrc")
	_, ok = w.observe(other)
	require.False(t, ok)
	notice, ok = w.observe(other)
	require.True(t, ok, "a different change set is a new notice")
	require.Equal(t, other.Changed, notice.Changed)
}

// TestStaleWatcherRearmsAfterCleanPoll verifies a reload (clean files) lets
// the same edit be noticed again, e.g. when a user reverts and re-applies it.
func TestStaleWatcherRearmsAfterCleanPoll(t *testing.T) {
	t.Parallel()

	var w staleWatcher
	changed := staleSnapshot("/home/u/.config/crush/crush.json")

	_, _ = w.observe(changed)
	_, ok := w.observe(changed)
	require.True(t, ok)

	_, ok = w.observe(staleSnapshot())
	require.False(t, ok)

	_, _ = w.observe(changed)
	_, ok = w.observe(changed)
	require.True(t, ok, "the same path must be announced again after a clean poll")
}

// TestStaleWatcherReportsMissingFiles verifies deletions are reported, since a
// removed config file also needs a reload to take effect.
func TestStaleWatcherReportsMissingFiles(t *testing.T) {
	t.Parallel()

	var w staleWatcher
	missing := config.StalenessResult{Dirty: true, Missing: []string{"/home/u/.config/crush/crush.json"}}

	_, _ = w.observe(missing)
	notice, ok := w.observe(missing)
	require.True(t, ok)
	require.Equal(t, missing.Missing, notice.Missing)
}
