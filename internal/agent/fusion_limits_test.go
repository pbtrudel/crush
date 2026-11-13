package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/oauth/copilot"
	"github.com/stretchr/testify/require"
)

func TestFusionLimitsCache(t *testing.T) {
	var requests atomic.Int64
	var status atomic.Int64
	status.Store(200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"data":[{"id":"model","capabilities":{"limits":{"max_prompt_tokens":100}}}]}`))
	}))
	defer server.Close()
	cache := &fusionLimitsCache{}
	now := time.Now()
	cache.now = func() time.Time { return now }
	got := cache.get(context.Background(), server.URL, "key", nil)
	require.Equal(t, int64(100), got["model"].MaxPromptTokens)
	cache.get(context.Background(), server.URL, "key", nil)
	require.Equal(t, int64(1), requests.Load())
	cache.get(context.Background(), server.URL, "other", nil)
	require.Equal(t, int64(2), requests.Load())
	now = now.Add(time.Hour + time.Second)
	status.Store(500)
	require.Nil(t, cache.get(context.Background(), server.URL, "key", nil))
	cache.get(context.Background(), server.URL, "key", nil)
	require.Equal(t, int64(3), requests.Load())
	now = now.Add(5*time.Minute + time.Second)
	status.Store(200)
	require.NotNil(t, cache.get(context.Background(), server.URL, "key", nil))
	require.Equal(t, int64(4), requests.Load())
}

func TestFusionLimitsCacheHeaderIsolationAndExpiry(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte(`{"data":[]}`)) }))
	defer server.Close()
	cache := &fusionLimitsCache{}
	now := time.Now()
	cache.now = func() time.Time { return now }
	cache.get(context.Background(), server.URL, "key", map[string]string{"Tenant": "one"})
	cache.get(context.Background(), server.URL, "key", map[string]string{"Tenant": "two"})
	require.Equal(t, int64(2), requests.Load())
	now = now.Add(2 * time.Hour)
	cache.get(context.Background(), server.URL, "new", nil)
	require.Len(t, cache.entries, 1)
}

func TestFusionLimitsCacheSingleflight(t *testing.T) {
	var requests atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	cache := &fusionLimitsCache{}
	var workers sync.WaitGroup
	for range 20 {
		workers.Add(1)
		go func() { defer workers.Done(); cache.get(context.Background(), server.URL, "key", nil) }()
	}
	<-entered
	close(release)
	workers.Wait()
	require.Equal(t, int64(1), requests.Load())
}

func TestFusionUsableInput(t *testing.T) {
	tests := []struct {
		name     string
		limits   copilot.ModelLimits
		reserved int64
		want     int64
		known    bool
	}{
		{"prompt only", copilot.ModelLimits{MaxPromptTokens: 100}, 20, 100, true},
		{"window and catalog output", copilot.ModelLimits{MaxContextWindowTokens: 200, MaxOutputTokens: 50}, 30, 170, true},
		{"window and api output", copilot.ModelLimits{MaxContextWindowTokens: 200, MaxOutputTokens: 50}, 0, 150, true},
		{"mixed", copilot.ModelLimits{MaxPromptTokens: 120, MaxContextWindowTokens: 200, MaxOutputTokens: 50}, 0, 120, true},
		{"unknown", copilot.ModelLimits{}, 0, 0, false},
		{"missing output", copilot.ModelLimits{MaxContextWindowTokens: 200}, 0, 0, false},
		{"exhausted window", copilot.ModelLimits{MaxPromptTokens: 100, MaxContextWindowTokens: 20}, 30, 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, known := fusionUsableInput(test.limits, test.reserved)
			require.Equal(t, test.want, got)
			require.Equal(t, test.known, known)
		})
	}
}
