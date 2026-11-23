package web

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDiffSlotsBoundConcurrency pins the two-slot bound without asserting
// on timing: two workers take the slots and hold them, so a third cannot
// start until one lets go.
func TestDiffSlotsBoundConcurrency(t *testing.T) {
	t.Parallel()
	d := newDiffSlots(2)
	var running, peak atomic.Int32
	started := make(chan struct{}, 6)
	unblock := make(chan struct{})
	errs := make(chan error, 6)
	var unblockOnce sync.Once
	releaseAll := func() { unblockOnce.Do(func() { close(unblock) }) }
	// Let every worker go even when an assertion below fails, so none stays
	// blocked past the test.
	defer releaseAll()
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			release, err := d.acquire(t.Context())
			if err != nil {
				errs <- err
				return
			}
			defer release()
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			started <- struct{}{}
			<-unblock
			running.Add(-1)
		})
	}
	// The first two workers hold the slots; the other four wait.
	for range 2 {
		<-started
	}
	require.Equal(t, 2, len(d.ch), "both slots are taken")
	require.Equal(t, int32(2), running.Load(), "no third diff starts while the slots are taken")
	releaseAll()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(2), peak.Load(), "two diffs ran at once, never three")
}

// TestDiffSlotsHonourCancel pins that a request whose client went away
// stops waiting for a slot.
func TestDiffSlotsHonourCancel(t *testing.T) {
	t.Parallel()
	d := newDiffSlots(1)
	release, err := d.acquire(t.Context())
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = d.acquire(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
