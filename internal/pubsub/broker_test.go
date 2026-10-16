package pubsub

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A saturated subscriber produces one warning, not one per dropped event:
// saturation stays observable through the counter without log storms.
func TestPublish_RateLimitsDropLogs(t *testing.T) {
	b := NewBrokerWithOptions[struct{}](1)
	sub := b.Subscribe(t.Context())

	var logged atomic.Int64
	b.logDrop = func(EventType, uint64) { logged.Add(1) }

	for range 10 {
		b.Publish(UpdatedEvent, struct{}{})
	}
	require.Equal(t, uint64(9), b.DropCount())
	require.Equal(t, int64(1), logged.Load(), "drops within the window log once")
	_ = sub
}

// The first drop after a quiet period logs again.
func TestPublish_DropLogRecovers(t *testing.T) {
	b := NewBrokerWithOptions[struct{}](1)
	sub := b.Subscribe(t.Context())

	var logged atomic.Int64
	b.logDrop = func(EventType, uint64) { logged.Add(1) }

	// The first publish fills the one-slot buffer; the second is the first
	// drop and warns.
	b.Publish(UpdatedEvent, struct{}{})
	b.Publish(UpdatedEvent, struct{}{})
	require.Equal(t, uint64(1), b.DropCount())
	require.Equal(t, int64(1), logged.Load())

	// Further drops inside the window stay quiet.
	b.Publish(UpdatedEvent, struct{}{})
	require.Equal(t, int64(1), logged.Load(), "no warning storm while saturated")

	// Age the window so the next drop is allowed to log.
	b.lastDropLog.Store(time.Now().Add(-2 * time.Second).UnixNano())

	b.Publish(UpdatedEvent, struct{}{})
	require.Equal(t, int64(2), logged.Load(), "a later burst logs again")
	_ = sub
}
