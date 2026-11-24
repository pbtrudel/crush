package web

import "context"

// maxConcurrentDiffs bounds the diff work of every tab together. One diff
// of two 8 MiB sides can hold a few hundred megabytes while it runs, so
// three tabs refreshing at once must queue instead of stacking up.
const maxConcurrentDiffs = 2

// diffSlots is a counting semaphore for diff work. A request whose client
// went away stops waiting.
type diffSlots struct{ ch chan struct{} }

func newDiffSlots(n int) *diffSlots { return &diffSlots{ch: make(chan struct{}, n)} }

func (d *diffSlots) acquire(ctx context.Context) (func(), error) {
	select {
	case d.ch <- struct{}{}:
		return func() { <-d.ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
