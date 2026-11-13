package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/oauth/copilot"
	"golang.org/x/sync/singleflight"
)

// fusionLimitsKey identifies one account's limits by endpoint and the exact
// credentials and headers used to fetch them. The credential is compared, never
// hashed or persisted, so a re-login invalidates its own entry.
type fusionLimitsKey struct {
	baseURL    string
	credential string
	headers    string
}
type fusionLimitsEntry struct {
	limits  map[string]copilot.ModelLimits
	expires time.Time
}
type fusionLimitsCache struct {
	mu      sync.Mutex
	entries map[fusionLimitsKey]fusionLimitsEntry
	flights singleflight.Group
	now     func() time.Time
}

var sharedFusionLimits fusionLimitsCache

func (cache *fusionLimitsCache) get(ctx context.Context, baseURL, apiKey string, headers map[string]string) map[string]copilot.ModelLimits {
	headerData, _ := json.Marshal(headers)
	key := fusionLimitsKey{baseURL, apiKey, string(headerData)}
	now := time.Now
	if cache.now != nil {
		now = cache.now
	}
	lookup := func() (map[string]copilot.ModelLimits, bool) {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		entry, ok := cache.entries[key]
		return entry.limits, ok && now().Before(entry.expires)
	}
	if limits, ok := lookup(); ok {
		return limits
	}
	flightKey := baseURL + "\x00" + key.credential + "\x00" + key.headers
	result := cache.flights.DoChan(flightKey, func() (any, error) {
		if limits, ok := lookup(); ok {
			return limits, nil
		}
		limits, err := copilot.FetchModelLimits(context.WithoutCancel(ctx), baseURL, apiKey, headers)
		ttl := time.Hour
		if err != nil {
			limits = nil
			ttl = 5 * time.Minute
		}
		cache.mu.Lock()
		if cache.entries == nil {
			cache.entries = make(map[fusionLimitsKey]fusionLimitsEntry)
		}
		for oldKey, entry := range cache.entries {
			if !now().Before(entry.expires) {
				delete(cache.entries, oldKey)
			}
		}
		if len(cache.entries) >= 128 {
			var oldest fusionLimitsKey
			var expiry time.Time
			for oldKey, entry := range cache.entries {
				if expiry.IsZero() || entry.expires.Before(expiry) {
					oldest, expiry = oldKey, entry.expires
				}
			}
			delete(cache.entries, oldest)
		}
		cache.entries[key] = fusionLimitsEntry{limits, now().Add(ttl)}
		cache.mu.Unlock()
		return limits, nil
	})
	select {
	case <-ctx.Done():
		return nil
	case value := <-result:
		if value.Val == nil {
			return nil
		}
		return value.Val.(map[string]copilot.ModelLimits)
	}
}

func fusionUsableInput(limits copilot.ModelLimits, reservedOutput int64) (int64, bool) {
	budget := limits.MaxPromptTokens
	if budget < 0 {
		budget = 0
	}
	if reservedOutput <= 0 {
		reservedOutput = limits.MaxOutputTokens
	}
	if limits.MaxContextWindowTokens > 0 && reservedOutput > 0 {
		window := limits.MaxContextWindowTokens - reservedOutput
		if window <= 0 {
			return 0, true
		}
		if budget == 0 || window < budget {
			budget = window
		}
	}
	return budget, budget > 0
}
