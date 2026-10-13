package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/stretchr/testify/require"
)

// vibeRequestCapture records what a test server saw on its last request.
// The mutex keeps the handler goroutine and the test goroutine race-free
// under -race.
type vibeRequestCapture struct {
	mu  sync.Mutex
	got capturedVibeRequest
}

type capturedVibeRequest struct {
	header http.Header
	body   []byte
}

func (c *vibeRequestCapture) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = capturedVibeRequest{header: r.Header.Clone(), body: body}
}

func (c *vibeRequestCapture) snapshot() capturedVibeRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got
}

// post sends one JSON request through client and returns what the server saw.
func (c *vibeRequestCapture) post(t *testing.T, client *http.Client, url, body, sessionAffinity string) capturedVibeRequest {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if sessionAffinity != "" {
		req.Header.Set("x-session-affinity", sessionAffinity)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return c.snapshot()
}

// serveMistralVibeCapture serves a minimal non-streaming completion so both
// raw transport calls and provider-level calls succeed.
func serveMistralVibeCapture(t *testing.T) (*httptest.Server, *vibeRequestCapture) {
	t.Helper()
	capture := &vibeRequestCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

// TestMistralVibeTransportSetsVibeUserAgent pins the wire User-Agent to the
// exact string the Vibe CLI sends, so api.mistral.ai sees a Vibe client
// instead of Charm-Crush.
func TestMistralVibeTransportSetsVibeUserAgent(t *testing.T) {
	t.Parallel()

	srv, capture := serveMistralVibeCapture(t)
	client := &http.Client{Transport: newMistralVibeTransport(http.DefaultTransport)}

	got := capture.post(t, client, srv.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, "")

	require.Equal(t, "mistral-client-python/Mistral-Vibe/2.26.1", got.header.Get("User-Agent"))
}

// TestMistralVibeTransportMirrorsSessionAffinity checks x-affinity carries
// Crush's per-session affinity value (the Vibe CLI sends its session id
// there), and stays absent when there is no session affinity to mirror.
func TestMistralVibeTransportMirrorsSessionAffinity(t *testing.T) {
	t.Parallel()

	srv, capture := serveMistralVibeCapture(t)
	client := &http.Client{Transport: newMistralVibeTransport(http.DefaultTransport)}

	got := capture.post(t, client, srv.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, "session-hash-123")
	require.Equal(t, "session-hash-123", got.header.Get("x-affinity"))

	got = capture.post(t, client, srv.URL+"/v1/chat/completions", `{"model":"m","messages":[]}`, "")
	require.Empty(t, got.header.Get("x-affinity"))
}

// TestMistralVibeTransportInjectsVibeMetadata checks the completion body
// carries the metadata fields the Vibe CLI sends (call_source "vibe_code" et
// al.) without clobbering metadata a caller already set, and that non-chat
// request bodies pass through untouched.
func TestMistralVibeTransportInjectsVibeMetadata(t *testing.T) {
	t.Parallel()

	srv, capture := serveMistralVibeCapture(t)
	client := &http.Client{Transport: newMistralVibeTransport(http.DefaultTransport)}

	got := capture.post(t, client, srv.URL+"/v1/chat/completions", `{"model":"m","messages":[],"metadata":{"keep":"me"}}`, "aff-1")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(got.body, &payload))
	meta, ok := payload["metadata"].(map[string]any)
	require.True(t, ok, "metadata missing from %s", got.body)
	require.Equal(t, "vibe_code", meta["call_source"])
	require.Equal(t, "local", meta["host_kind"])
	require.Equal(t, "vibe_cli", meta["client_name"])
	require.Equal(t, "cli", meta["agent_entrypoint"])
	require.Equal(t, "aff-1", meta["session_id"])
	require.Equal(t, "me", meta["keep"], "existing metadata must survive")

	got = capture.post(t, client, srv.URL+"/v1/models", `{"probe":true}`, "aff-1")
	require.JSONEq(t, `{"probe":true}`, string(got.body), "non-chat bodies must pass through untouched")
}

// TestMistralVibeTransportSignsProviderRequests runs one real provider call
// through the transport to prove call-level session headers reach it and the
// signed shape lands on the wire end to end.
func TestMistralVibeTransportSignsProviderRequests(t *testing.T) {
	t.Parallel()

	srv, capture := serveMistralVibeCapture(t)

	provider, err := openaicompat.New(
		openaicompat.WithBaseURL(srv.URL+"/v1"),
		openaicompat.WithAPIKey("x"),
		openaicompat.WithHTTPClient(&http.Client{Transport: newMistralVibeTransport(http.DefaultTransport)}),
	)
	require.NoError(t, err)
	lm, err := provider.LanguageModel(context.Background(), "mistral-large-4")
	require.NoError(t, err)

	_, err = lm.Generate(context.Background(), fantasy.Call{
		Prompt: fantasy.Prompt{
			{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "hi"}}},
		},
		Headers: map[string]string{"x-session-affinity": "provider-aff"},
	})
	require.NoError(t, err)

	got := capture.snapshot()
	require.Equal(t, "mistral-client-python/Mistral-Vibe/2.26.1", got.header.Get("User-Agent"))
	require.Equal(t, "provider-aff", got.header.Get("x-affinity"))
	var payload map[string]any
	require.NoError(t, json.Unmarshal(got.body, &payload))
	meta, ok := payload["metadata"].(map[string]any)
	require.True(t, ok, "metadata missing from %s", got.body)
	require.Equal(t, "vibe_code", meta["call_source"])
	require.Equal(t, "provider-aff", meta["session_id"])
}
