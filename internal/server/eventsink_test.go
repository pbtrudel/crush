package server

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestEventSink_EncodesOnceForAllClients(t *testing.T) {
	t.Parallel()

	sink := newEventSink()
	a := sink.attach(8)
	b := sink.attach(8)

	var encoded int
	sink.encode = func(any) ([]byte, string) {
		encoded++
		return []byte(`{"n":1}`), "message:m1"
	}
	sink.publish("whatever")

	require.Equal(t, 1, encoded, "one event must cost one encode regardless of client count")
	require.Equal(t, [][]byte{[]byte(`{"n":1}`)}, frameDatas(a.drain()))
	require.Equal(t, [][]byte{[]byte(`{"n":1}`)}, frameDatas(b.drain()))
}

// A slow reader must not accumulate every intermediate snapshot: pending
// frames for the same entity collapse to the newest one, which is the
// whole point of coalescing at the fan-out.
func TestEventSink_CoalescesSameKeyFrames(t *testing.T) {
	t.Parallel()

	sink := newEventSink()
	sub := sink.attach(8)
	sink.encode = func(payload any) ([]byte, string) {
		return []byte(payload.(string)), "message:m1"
	}

	sink.publish("snap1")
	sink.publish("snap2")
	sink.publish("snap3")

	frames := sub.drain()
	require.Len(t, frames, 1, "intermediate snapshots collapse")
	require.Equal(t, []byte("snap3"), frames[0].data)
}

// Different entities are independent: collapsing must never reorder or
// drop one entity's frame because another entity produced traffic.
func TestEventSink_KeepsDistinctKeysInOrder(t *testing.T) {
	t.Parallel()

	sink := newEventSink()
	sub := sink.attach(8)
	keys := map[string]string{"m1": "message:m1", "m2": "message:m2"}
	sink.encode = func(payload any) ([]byte, string) {
		text := payload.(string)
		return []byte(text), keys[text]
	}

	sink.publish("m1")
	sink.publish("m2")
	sink.publish("m1")

	require.Equal(t, [][]byte{[]byte("m1"), []byte("m2")}, frameDatas(sub.drain()))
}

// A reader that stops consuming is bounded: the backlog cap drops the
// oldest pending frames rather than growing without limit.
func TestEventSink_BoundedBacklog(t *testing.T) {
	t.Parallel()

	sink := newEventSink()
	sub := sink.attach(2)
	var n int
	sink.encode = func(any) ([]byte, string) {
		n++
		return []byte("x"), string(rune('a' + n)) // every frame its own key
	}

	for range 10 {
		sink.publish("x")
	}

	require.LessOrEqual(t, len(sub.drain()), 2, "backlog stays inside the cap")
	require.Equal(t, uint64(8), sub.dropped.Load())
}

func TestEventSink_DetachStopsDelivery(t *testing.T) {
	t.Parallel()

	sink := newEventSink()
	sub := sink.attach(4)
	sink.detach(sub)

	sink.encode = func(any) ([]byte, string) { return []byte("y"), "k" }
	sink.publish("y")

	require.Empty(t, sub.drain(), "a detached subscriber gets nothing")
	require.Zero(t, sink.subCount())
}

// Frames that cannot be attributed to an entity must never collapse into
// each other, or a client would silently lose an event.
func TestEventKey_UniqueForUnattributedPayloads(t *testing.T) {
	t.Parallel()

	first := eventKey("some-other-payload")
	second := eventKey("some-other-payload")
	require.NotEqual(t, first, second)
}

func TestEventKey_IdentifiesEntities(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		eventKey(pubsub.Event[message.Message]{Payload: message.Message{ID: "m1"}}),
		eventKey(pubsub.Event[message.Message]{Payload: message.Message{ID: "m1"}}),
	)
	require.NotEqual(t,
		eventKey(pubsub.Event[message.Message]{Payload: message.Message{ID: "m1"}}),
		eventKey(pubsub.Event[message.Message]{Payload: message.Message{ID: "m2"}}),
	)
}

// A drained backlog must leave the wake-up signal alone: the writer loop
// re-checks until it sees an empty drain, and a signal eaten by the
// previous drain would strand a frame that arrived just after it.
func TestEventSink_DrainDoesNotConsumeSignal(t *testing.T) {
	t.Parallel()

	sink := newEventSink()
	sub := sink.attach(8)
	sink.encode = func(payload any) ([]byte, string) {
		return []byte(payload.(string)), "message:" + payload.(string)
	}

	sink.publish("m1")
	require.Len(t, sub.drain(), 1)

	select {
	case <-sub.wait():
	default:
		t.Fatal("the wake-up signal must still be pending for the writer loop to re-check")
	}

	// A frame pushed after that drain has to be reachable by the same
	// wake-up: the writer loops until a drain comes back empty.
	sink.publish("m2")
	frames := sub.drain()
	require.Equal(t, [][]byte{[]byte("m2")}, frameDatas(frames))
	require.Empty(t, sub.drain())
}
