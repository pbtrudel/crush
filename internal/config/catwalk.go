package config

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

// defaultCatalogMaxStale is how long a cached provider catalog may be
// served without checking the network. A launch inside the window uses the
// cached catalog and revalidates in the background, so time-to-first-frame
// no longer waits on a round trip for information the disk already has.
const defaultCatalogMaxStale = 24 * time.Hour

// catalogMaxStale resolves the window, letting a deployment that needs
// faster catalog propagation trade startup latency for freshness.
func catalogMaxStale() time.Duration {
	raw := os.Getenv("CRUSH_CATALOG_MAX_STALE")
	if raw == "" {
		return defaultCatalogMaxStale
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("Ignoring invalid CRUSH_CATALOG_MAX_STALE", "value", raw, "error", err)
		return defaultCatalogMaxStale
	}
	return d
}

var (
	catalogHooksMu sync.Mutex
	catalogHooks   []func(context.Context)
)

// RegisterCatalogRefreshedHook adds a function run after a background
// catalog refresh changes the providers list. Hooks let catalogs that
// shadow Catwalk's renew themselves without the fetch holding up startup.
// Hooks run on the refresh goroutine with a bounded context.
func RegisterCatalogRefreshedHook(hook func(context.Context)) {
	if hook == nil {
		return
	}
	catalogHooksMu.Lock()
	defer catalogHooksMu.Unlock()
	catalogHooks = append(catalogHooks, hook)
}

func runCatalogRefreshedHooks(ctx context.Context) {
	catalogHooksMu.Lock()
	hooks := slices.Clone(catalogHooks)
	catalogHooksMu.Unlock()
	for _, hook := range hooks {
		hook(ctx)
	}
}

// registerCatalogRefreshedHookForTest replaces the whole hook list for one
// test and restores it when passed nil.
func registerCatalogRefreshedHookForTest(hook func(context.Context)) {
	catalogHooksMu.Lock()
	defer catalogHooksMu.Unlock()
	if hook == nil {
		catalogHooks = nil
		return
	}
	catalogHooks = []func(context.Context){hook}
}

type catwalkClient interface {
	GetProviders(context.Context, string) ([]catwalk.Provider, error)
}

var _ syncer[[]catwalk.Provider] = (*catwalkSync)(nil)

type catwalkSync struct {
	once       sync.Once
	result     []catwalk.Provider
	err        error
	cache      cache[[]catwalk.Provider]
	client     catwalkClient
	autoupdate bool
	init       atomic.Bool
	updated    atomic.Bool
}

func (s *catwalkSync) Init(client catwalkClient, path string, autoupdate bool) {
	s.client = client
	s.cache = newCache[[]catwalk.Provider](path)
	s.autoupdate = autoupdate
	s.init.Store(true)
}

func (s *catwalkSync) Get(ctx context.Context) ([]catwalk.Provider, error) {
	if !s.init.Load() {
		panic("called Get before Init")
	}

	// The result and the error are memoized together so that every caller
	// sees the same outcome, not just the one that won the once.
	s.once.Do(func() {
		if !s.autoupdate {
			cached, _, cachedErr := s.cache.Get()
			if len(cached) > 0 && cachedErr == nil {
				slog.Info("Using cached Catwalk providers (auto-update disabled)")
				s.result = cached
				return
			}
			slog.Info("Using embedded Catwalk providers")
			s.result = embedded.GetAll()
			return
		}

		cached, etag, cachedErr := s.cache.Get()
		if len(cached) == 0 || cachedErr != nil {
			// if cached file is empty, default to embedded providers
			cached = embedded.GetAll()
		}

		// Stale-while-revalidate: a catalog still inside the max-stale
		// window is served now and revalidated in the background, so a
		// normal launch never waits on the network for providers it
		// already has. The refresh writes the disk cache and fires the
		// hooks; it deliberately does not swap this process's memoized
		// list, which would change the model set under a running session.
		if len(cached) > 0 && cachedErr == nil {
			if age, ok := s.cache.Age(); ok && age < catalogMaxStale() {
				slog.Info("Using cached Catwalk providers", "age", age.Round(time.Second))
				s.result = cached
				go s.revalidate(etag)
				return
			}
		}

		slog.Info("Fetching providers from Catwalk")
		result, err := s.client.GetProviders(ctx, etag)
		if errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("Catwalk providers not updated in time")
			s.result = cached
			return
		}
		if errors.Is(err, catwalk.ErrNotModified) {
			slog.Info("Catwalk providers not modified")
			s.result = cached
			return
		}
		if err != nil {
			// Fall back to cached (which defaults to embedded if empty).
			// Being offline is routine and the fallback is sound, so this
			// is logged rather than reported to the caller.
			slog.Warn("Could not fetch providers from Catwalk", "error", err)
			s.result = cached
			return
		}
		if len(result) == 0 {
			s.result = cached
			s.err = errors.New("empty providers list from catwalk")
			return
		}

		// The catalog is usable from here on. A cache write failure only
		// costs the next run a refresh, so it is reported alongside a valid
		// result rather than in place of one.
		s.result = result
		s.err = s.cache.Store(result)
		s.updated.Store(true)
	})
	return s.result, s.err
}

// revalidate refreshes the cached catalog off the startup path. Failure is
// not an error for the caller: this process already has a usable catalog,
// and the next launch simply tries again.
func (s *catwalkSync) revalidate(etag string) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	result, err := s.client.GetProviders(ctx, etag)
	if err != nil {
		if !errors.Is(err, catwalk.ErrNotModified) {
			slog.Debug("Background Catwalk refresh failed", "error", err)
		}
		return
	}
	if len(result) == 0 {
		return
	}
	if err := s.cache.Store(result); err != nil {
		slog.Debug("Could not cache refreshed Catwalk providers", "error", err)
	}
	s.updated.Store(true)
	runCatalogRefreshedHooks(ctx)
}

// Updated reports whether the last Get replaced the catalog with fresh
// data from Catwalk rather than falling back to the cache, the embedded
// copy, or a 304 Not Modified response. It is false before Get runs and
// stays false for the whole process when auto-update is disabled, which
// makes it the signal for refreshing catalogs that shadow Catwalk's,
// such as the ChatGPT model catalog.
func (s *catwalkSync) Updated() bool {
	return s.updated.Load()
}
