package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

// fakeCatwalkClient counts network calls and can serve either a fresh
// list, a not-modified, or an error.
type fakeCatwalkClient struct {
	// calls is atomic because the background revalidation hits the client
	// from its own goroutine.
	calls    atomic.Int64
	response []catwalk.Provider
	err      error
}

func (f *fakeCatwalkClient) GetProviders(context.Context, string) ([]catwalk.Provider, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

func writeCatwalkCache(t *testing.T, path string, providers []catwalk.Provider) {
	t.Helper()
	require.NoError(t, newCache[[]catwalk.Provider](path).Store(providers))
}

func agedCache(t *testing.T, path string, age time.Duration) {
	t.Helper()
	stamp := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(path, stamp, stamp))
}

// A cached catalog younger than the max-stale window is served
// immediately: the process does not block on the network to learn
// something it already knows.
func TestCatwalkSync_FreshCacheSkipsBlockingFetch(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "providers.json")
	cached := []catwalk.Provider{{ID: "cached-provider"}}
	writeCatwalkCache(t, path, cached)

	client := &fakeCatwalkClient{response: []catwalk.Provider{{ID: "fresh-provider"}}}
	s := &catwalkSync{}
	s.Init(client, path, true)

	got, err := s.Get(t.Context())
	require.NoError(t, err)
	require.Equal(t, cached, got)
	require.Zero(t, client.calls.Load(), "the fast path must not touch the network")
}

// The background revalidation writes the refreshed catalog to disk and
// reports it via the refresh hook, so a changed catalog lands for the
// next run without holding up this one. These two tests share the
// package-level refresh hook registry, so they run sequentially instead of
// in parallel.
func TestCatwalkSync_BackgroundRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	cached := []catwalk.Provider{{ID: "cached-provider"}}
	writeCatwalkCache(t, path, cached)

	fresh := []catwalk.Provider{{ID: "fresh-provider"}}
	client := &fakeCatwalkClient{response: fresh}
	s := &catwalkSync{}
	s.Init(client, path, true)

	hooked := make(chan struct{}, 1)
	hook := func(context.Context) { hooked <- struct{}{} }
	registerCatalogRefreshedHookForTest(hook)
	t.Cleanup(func() { registerCatalogRefreshedHookForTest(nil) })

	got, err := s.Get(t.Context())
	require.NoError(t, err)
	require.Equal(t, cached, got, "the running process keeps serving its cached list")

	select {
	case <-hooked:
	case <-time.After(2 * time.Second):
		t.Fatal("the background refresh never completed")
	}
	require.True(t, s.Updated(), "a changed catalog reports itself as updated")

	var stored []catwalk.Provider
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, fresh, stored, "the disk cache holds the refreshed catalog")
}

// A cache past the max-stale window still refreshes synchronously: the
// process blocks on the network only when its knowledge is too old to
// trust.
func TestCatwalkSync_StaleCacheRefreshesSynchronously(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "providers.json")
	writeCatwalkCache(t, path, []catwalk.Provider{{ID: "cached-provider"}})
	agedCache(t, path, 48*time.Hour)

	fresh := []catwalk.Provider{{ID: "fresh-provider"}}
	client := &fakeCatwalkClient{response: fresh}
	s := &catwalkSync{}
	s.Init(client, path, true)

	got, err := s.Get(t.Context())
	require.NoError(t, err)
	require.Equal(t, fresh, got)
	require.Equal(t, int64(1), client.calls.Load())
}

// A background refresh that fails leaves everything exactly as it was:
// the cached catalog, the memoized result, and no hook.
func TestCatwalkSync_BackgroundRefreshFailureIsQuiet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	cached := []catwalk.Provider{{ID: "cached-provider"}}
	writeCatwalkCache(t, path, cached)

	client := &fakeCatwalkClient{err: errors.New("network down")}
	s := &catwalkSync{}
	s.Init(client, path, true)

	hooked := make(chan struct{}, 1)
	registerCatalogRefreshedHookForTest(func(context.Context) { hooked <- struct{}{} })
	t.Cleanup(func() { registerCatalogRefreshedHookForTest(nil) })

	got, err := s.Get(t.Context())
	require.NoError(t, err)
	require.Equal(t, cached, got)

	select {
	case <-hooked:
		t.Fatal("a failed refresh must not fire the hook")
	case <-time.After(100 * time.Millisecond):
	}
	require.False(t, s.Updated())
}

// countedHyperClient is race-safe because the background revalidation calls
// it from another goroutine.
type countedHyperClient struct {
	calls atomic.Int64
	resp  catwalk.Provider
	err   error
}

func (c *countedHyperClient) Get(context.Context, string) (catwalk.Provider, error) {
	c.calls.Add(1)
	return c.resp, c.err
}

func TestHyperSync_FreshCacheSkipsBlockingFetch(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hyper.json")
	cached := catwalk.Provider{ID: "hyper", Name: "Hyper", Models: []catwalk.Model{{ID: "cached-model"}}}
	require.NoError(t, newCache[catwalk.Provider](path).Store(cached))

	client := &countedHyperClient{resp: catwalk.Provider{ID: "hyper", Name: "Hyper", Models: []catwalk.Model{{ID: "new-model"}}}}
	s := &hyperSync{}
	s.Init(client, path, true)

	got, err := s.Get(t.Context())
	require.NoError(t, err)
	require.Equal(t, cached, got, "a fresh-enough cache is served without the network")
	require.Zero(t, client.calls.Load())
}

func TestHyperSync_BackgroundRefreshLandsOnDisk(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hyper.json")
	cached := catwalk.Provider{ID: "hyper", Name: "Hyper", Models: []catwalk.Model{{ID: "cached-model"}}}
	require.NoError(t, newCache[catwalk.Provider](path).Store(cached))

	fresh := catwalk.Provider{ID: "hyper", Name: "Hyper", Models: []catwalk.Model{{ID: "new-model"}}}
	client := &countedHyperClient{resp: fresh}
	s := &hyperSync{}
	s.Init(client, path, true)

	_, err := s.Get(t.Context())
	require.NoError(t, err)

	require.Eventually(t, func() bool { return client.calls.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		var stored catwalk.Provider
		raw, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		if err := json.Unmarshal(raw, &stored); err != nil {
			return false
		}
		return len(stored.Models) == 1 && stored.Models[0].ID == "new-model"
	}, 2*time.Second, 10*time.Millisecond, "the refreshed provider lands in the disk cache")

	// The running process keeps serving what it started with: swapping a
	// provider's endpoint mid-session is not what the fast path is for.
	require.Equal(t, cached, s.result)
}

func TestHyperSync_StaleCacheFetchesSynchronously(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "hyper.json")
	cached := catwalk.Provider{ID: "hyper", Name: "Hyper", Models: []catwalk.Model{{ID: "cached-model"}}}
	require.NoError(t, newCache[catwalk.Provider](path).Store(cached))
	agedCache(t, path, 48*time.Hour)

	fresh := catwalk.Provider{ID: "hyper", Name: "Hyper", Models: []catwalk.Model{{ID: "new-model"}}}
	client := &countedHyperClient{resp: fresh}
	s := &hyperSync{}
	s.Init(client, path, true)

	got, err := s.Get(t.Context())
	require.NoError(t, err)
	require.Equal(t, fresh, got)
	require.Equal(t, int64(1), client.calls.Load())
}
