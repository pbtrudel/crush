package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
)

// sinkBacklog caps how many distinct entities a single slow client can
// accumulate before the oldest pending frames are dropped. It replaces the
// old fixed channel depth with the same lossy-under-saturation behavior,
// but expressed in coalesced frames rather than raw events.
const sinkBacklog = 256

// sinkFrame is one encoded SSE payload plus the entity it describes.
type sinkFrame struct {
	data []byte
	key  string
}

// eventSink fans one workspace's event stream out to every attached SSE
// connection. Encoding happens once per event here instead of once per
// connection, and pending frames for the same entity collapse into the
// newest one, so a slow reader drops intermediate snapshots rather than
// whole events.
type eventSink struct {
	// encode converts a raw event payload into wire bytes and a
	// coalescing key. Replaced in tests to count encodes.
	encode func(payload any) ([]byte, string)

	mu   sync.Mutex
	subs map[*sinkSub]struct{}
}

func newEventSink() *eventSink {
	return &eventSink{
		encode: encodeEventPayload,
		subs:   make(map[*sinkSub]struct{}),
	}
}

// sinkSub is one connection's view of the stream: a bounded, per-entity
// coalescing backlog plus a signal that the writer loop drains it.
type sinkSub struct {
	cap int

	// notify is a one-slot signal. Frames live in the backlog, not the
	// channel, which is what lets same-entity frames replace each other.
	notify chan struct{}

	mu      sync.Mutex
	order   []string
	byKey   map[string]sinkFrame
	dropped atomic.Uint64
}

func (s *eventSink) attach(cap int) *sinkSub {
	sub := &sinkSub{
		cap:    cap,
		notify: make(chan struct{}, 1),
		byKey:  make(map[string]sinkFrame, cap),
	}
	s.mu.Lock()
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	return sub
}

func (s *eventSink) detach(sub *sinkSub) {
	s.mu.Lock()
	delete(s.subs, sub)
	s.mu.Unlock()
}

func (s *eventSink) subCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

// publish encodes once and hands the frame to every subscriber.
func (s *eventSink) publish(payload any) {
	data, key := s.encode(payload)
	if data == nil {
		return
	}
	frame := sinkFrame{data: data, key: key}

	s.mu.Lock()
	defer s.mu.Unlock()
	for sub := range s.subs {
		sub.push(frame)
	}
}

func (q *sinkSub) push(f sinkFrame) {
	q.mu.Lock()
	if _, ok := q.byKey[f.key]; !ok {
		if len(q.order) >= q.cap {
			// Bounded backlog: drop the oldest pending frame. A client
			// this far behind reconciles from the terminal events it does
			// receive, exactly as with the old lossy channel.
			oldest := q.order[0]
			q.order = q.order[1:]
			delete(q.byKey, oldest)
			q.dropped.Add(1)
		}
		q.order = append(q.order, f.key)
	}
	q.byKey[f.key] = f
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// drain returns the pending frames in arrival order and empties the
// backlog. It never blocks, and it deliberately leaves the wake-up signal
// alone: the writer loop drains until it sees an empty batch, so a frame
// that lands just after a drain can never be stranded by a consumed
// signal.
func (q *sinkSub) drain() []sinkFrame {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.order) == 0 {
		return nil
	}
	out := make([]sinkFrame, 0, len(q.order))
	for _, key := range q.order {
		out = append(out, q.byKey[key])
		delete(q.byKey, key)
	}
	q.order = q.order[:0]
	return out
}

func (q *sinkSub) wait() <-chan struct{} { return q.notify }

// frameDatas is a test-facing projection of a drained batch.
func frameDatas(frames []sinkFrame) [][]byte {
	out := make([][]byte, 0, len(frames))
	for _, f := range frames {
		out = append(out, f.data)
	}
	return out
}

// encodeEventPayload converts a raw event into its SSE bytes and the key
// used to coalesce it. Frames whose entity cannot be identified get a
// unique key so they are never collapsed into each other.
func encodeEventPayload(payload any) ([]byte, string) {
	wrapped := wrapEvent(payload)
	if wrapped == nil {
		return nil, ""
	}
	data, err := json.Marshal(wrapped)
	if err != nil {
		return nil, ""
	}
	return data, eventKey(payload)
}

var unkeyedSeq atomic.Uint64

// eventKey identifies the entity an event describes. Only same-entity
// frames may replace each other, and a snapshot of an entity always
// supersedes the one before it.
func eventKey(payload any) string {
	switch e := payload.(type) {
	case pubsub.Event[message.Message]:
		return "message:" + e.Payload.ID
	case pubsub.Event[session.Session]:
		return "session:" + e.Payload.ID
	case pubsub.Event[permission.PermissionRequest]:
		return "permission:" + e.Payload.ToolCallID
	case pubsub.Event[permission.PermissionNotification]:
		return "permission-result:" + e.Payload.ToolCallID
	case pubsub.Event[question.Request]:
		return "question:" + e.Payload.ID
	case pubsub.Event[question.Notification]:
		return "question-result:" + e.Payload.BatchID
	default:
		return fmt.Sprintf("#%d", unkeyedSeq.Add(1))
	}
}

// sinkRegistry keeps one encoder fan-out alive per workspace while at
// least one client is attached, and retires it when the last leaves.
type sinkRegistry struct {
	mu     sync.Mutex
	byName map[string]*registeredSink
}

type registeredSink struct {
	sink   *eventSink
	refs   int
	cancel context.CancelFunc
	// dead is set when the upstream event channel closes, which means the
	// workspace is gone and the next attach must build a fresh sink.
	dead atomic.Bool
}

func newSinkRegistry() *sinkRegistry {
	return &sinkRegistry{byName: make(map[string]*registeredSink)}
}

// attach subscribes to the workspace's events (once, no matter how many
// clients share it) and returns this client's backlog plus its release.
func (r *sinkRegistry) attach(ctx context.Context, be *backend.Backend, workspaceID string) (*sinkSub, func(), error) {
	r.mu.Lock()
	reg, ok := r.byName[workspaceID]
	if !ok || reg.dead.Load() {
		child, cancel := context.WithCancel(context.Background())
		events, err := be.SubscribeEvents(child, workspaceID)
		if err != nil {
			cancel()
			r.mu.Unlock()
			return nil, nil, err
		}
		reg = &registeredSink{sink: newEventSink(), cancel: cancel}
		r.byName[workspaceID] = reg
		go pumpWorkspaceEvents(reg, events, child.Done())
	}
	reg.refs++
	sub := reg.sink.attach(sinkBacklog)
	r.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			reg.sink.detach(sub)
			r.release(workspaceID, reg)
		})
	}
	return sub, release, nil
}

func (r *sinkRegistry) release(workspaceID string, reg *registeredSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reg.refs--
	if reg.refs > 0 {
		return
	}
	if r.byName[workspaceID] == reg {
		delete(r.byName, workspaceID)
	}
	reg.cancel()
}

// pumpWorkspaceEvents encodes every upstream event once and fans it out.
func pumpWorkspaceEvents(reg *registeredSink, events <-chan pubsub.Event[tea.Msg], done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case ev, ok := <-events:
			if !ok {
				// The broker is gone: shutdown or workspace removal. Flag
				// the sink so the next attach builds a fresh one instead
				// of subscribing to a dead stream.
				reg.dead.Store(true)
				return
			}
			reg.sink.publish(ev.Payload)
		}
	}
}
