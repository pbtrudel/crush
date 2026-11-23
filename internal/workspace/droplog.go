package workspace

import (
	"sync"
	"time"
)

// dropWarnInterval spaces the warnings about events dropped for a slow
// subscriber, like the pubsub broker's own drop warning.
const dropWarnInterval = 5 * time.Second

// dropWarner counts dropped events and says when a warning is due. The
// count is cumulative, so one warning still says how much was lost.
type dropWarner struct {
	mu    sync.Mutex
	total int64
	last  time.Time
}

func (d *dropWarner) drop(now time.Time) (total int64, warn bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.total++
	if !d.last.IsZero() && now.Sub(d.last) < dropWarnInterval {
		return d.total, false
	}
	d.last = now
	return d.total, true
}
