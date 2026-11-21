package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// promptCacheKeyRetrySSE is the shortest chat-completions stream an
// OpenAI-compatible provider accepts, one text delta and a stop.
const promptCacheKeyRetrySSE = `{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}
{"id":"chatcmpl-x","object":"chat.completion.chunk","created":1791599590,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`

// recordingServer answers every request with handler and records the request
// bodies it saw.
type recordingServer struct {
	server *httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newRecordingServer(t *testing.T, handler func(w http.ResponseWriter, body string, count int)) *recordingServer {
	t.Helper()
	rec := &recordingServer{}
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, string(body))
		count := len(rec.bodies)
		rec.mu.Unlock()
		handler(w, string(body), count)
	}))
	t.Cleanup(rec.server.Close)
	return rec
}

// seen returns a copy of the request bodies recorded so far.
func (r *recordingServer) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

// postChatCompletion sends one chat-completions request carrying a prompt
// cache key through client.
func postChatCompletion(t *testing.T, client *http.Client, url, model string) *http.Response {
	t.Helper()
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"prompt_cache_key":"session-key"}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url+"/chat/completions", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// TestPromptCacheKeyRetryDropsRejectedField pins the fallback: a 400 that
// names prompt_cache_key is retried once without the field, and every later
// request to that endpoint and model leaves it out, even from a transport
// built after the rejection.
func TestPromptCacheKeyRetryDropsRejectedField(t *testing.T) {
	const model = "rejected-model"

	srv := newRecordingServer(t, func(w http.ResponseWriter, _ string, count int) {
		if count == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Unrecognized request argument supplied: prompt_cache_key","type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","choices":[]}`)
	})

	client := &http.Client{Transport: newPromptCacheKeyTransport(http.DefaultTransport)}
	resp := postChatCompletion(t, client, srv.server.URL, model)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	bodies := srv.seen()
	require.Len(t, bodies, 2, "one rejected request and one retry without the field")
	require.Contains(t, bodies[0], `"prompt_cache_key"`)
	require.NotContains(t, bodies[1], "prompt_cache_key")

	// A transport built later, as a provider rebuild would install, must
	// not send the field either.
	fresh := &http.Client{Transport: newPromptCacheKeyTransport(http.DefaultTransport)}
	resp = postChatCompletion(t, fresh, srv.server.URL, model)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	bodies = srv.seen()
	require.Len(t, bodies, 3)
	require.NotContains(t, bodies[2], "prompt_cache_key")
}

// TestPromptCacheKeyRetryIgnoresOtherRejections pins that only a 400 naming
// the field triggers the fallback: any other rejection reaches the caller and
// the field keeps being sent.
func TestPromptCacheKeyRetryIgnoresOtherRejections(t *testing.T) {
	const model = "other-rejection-model"

	srv := newRecordingServer(t, func(w http.ResponseWriter, _ string, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"messages: field required","type":"invalid_request_error"}}`)
	})

	client := &http.Client{Transport: newPromptCacheKeyTransport(http.DefaultTransport)}
	for range 2 {
		resp := postChatCompletion(t, client, srv.server.URL, model)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		_ = resp.Body.Close()
	}

	bodies := srv.seen()
	require.Len(t, bodies, 2, "a rejection that does not name the field is not retried")
	for _, body := range bodies {
		require.Contains(t, body, `"prompt_cache_key"`)
	}
}

// TestBuildOpenaiCompatProviderRetriesWithoutPromptCacheKey pins the wiring:
// the provider the coordinator builds for an OpenAI-compatible endpoint is
// the one that retries without the field.
func TestBuildOpenaiCompatProviderRetriesWithoutPromptCacheKey(t *testing.T) {
	const modelID = "retry-wiring-model"

	srv := newRecordingServer(t, func(w http.ResponseWriter, _ string, count int) {
		if count == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Unsupported parameter: prompt_cache_key"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for line := range strings.Lines(promptCacheKeyRetrySSE) {
			line = strings.TrimSpace(line)
			if line != "" {
				_, _ = io.WriteString(w, "data: "+line+"\n\n")
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})

	env := testEnv(t)
	providerCfg := config.ProviderConfig{
		ID:      "neuralwatt",
		Type:    catwalk.TypeCompletions,
		BaseURL: srv.server.URL + "/v1",
	}
	coord := newTestCoordinator(t, env, providerCfg.ID, providerCfg)
	provider, err := coord.buildOpenaiCompatProvider(
		providerCfg.BaseURL, "test-key", nil, nil, providerCfg.ID, false, catwalk.TypeCompletions,
	)
	require.NoError(t, err)
	languageModel, err := provider.LanguageModel(t.Context(), modelID)
	require.NoError(t, err)

	stream, err := languageModel.Stream(t.Context(), fantasy.Call{
		Prompt: fantasy.Prompt{{
			Role:    fantasy.MessageRoleUser,
			Content: []fantasy.MessagePart{fantasy.TextPart{Text: "hi"}},
		}},
		ProviderOptions: fantasy.ProviderOptions{
			openaicompat.Name: &openaicompat.ProviderOptions{
				ExtraBody: map[string]any{"prompt_cache_key": "session-key"},
			},
		},
	})
	require.NoError(t, err)
	var text strings.Builder
	for part := range stream {
		if part.Type == fantasy.StreamPartTypeTextDelta {
			text.WriteString(part.Delta)
		}
	}
	require.Equal(t, "ok", text.String())

	bodies := srv.seen()
	require.Len(t, bodies, 2, "one rejected request and one retry without the field")
	require.Contains(t, bodies[0], `"prompt_cache_key"`)
	require.NotContains(t, bodies[1], "prompt_cache_key")
}

// roundTripFunc adapts a function to an http.RoundTripper, so a test can
// answer a request without a server.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// closeRecorder is a response body that counts Close calls.
type closeRecorder struct {
	io.Reader
	closed atomic.Int32
}

func (c *closeRecorder) Close() error {
	c.closed.Add(1)
	return nil
}

// TestPromptCacheKeyRetryClosesRejectedBody pins the connection handling of a
// retried rejection: the 400 body is drained and closed before the retry, so
// the transport does not leak it.
func TestPromptCacheKeyRetryClosesRejectedBody(t *testing.T) {
	rejected := &closeRecorder{
		Reader: strings.NewReader(`{"error":{"message":"Unrecognized request argument supplied: prompt_cache_key"}}`),
	}
	calls := 0
	client := withPromptCacheKeyRetry(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusBadRequest, Body: rejected, Header: http.Header{}}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl-1","choices":[]}`)),
			Header:     http.Header{},
		}, nil
	})})

	resp := postChatCompletion(t, client, "https://example.com", "close-retry-model")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	require.Equal(t, 2, calls, "the rejection is retried once")
	require.EqualValues(t, 1, rejected.closed.Load(), "the rejected body is closed")
}

// TestPromptCacheKeyRetryKeepsRejectionBody pins the other side: a 400
// passed on to the caller still delivers its body and closes the original.
func TestPromptCacheKeyRetryKeepsRejectionBody(t *testing.T) {
	body := &closeRecorder{
		Reader: strings.NewReader(`{"error":{"message":"messages: field required"}}`),
	}
	client := withPromptCacheKeyRetry(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: body, Header: http.Header{}}, nil
	})})

	resp := postChatCompletion(t, client, "https://example.com", "pass-through-model")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	read, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(read), "messages: field required")
	_ = resp.Body.Close()

	require.EqualValues(t, 1, body.closed.Load(), "passing the rejection on must close the original body")
}

// TestPromptCacheKeyRememberedScopeSkipsRetry pins the remembered scope:
// once an endpoint and model rejected the field, later requests leave it out
// before they are sent instead of earning a second 400.
func TestPromptCacheKeyRememberedScopeSkipsRetry(t *testing.T) {
	const model = "remembered-model"

	var badRequests atomic.Int32
	srv := newRecordingServer(t, func(w http.ResponseWriter, body string, _ int) {
		if strings.Contains(body, promptCacheKeyField) {
			badRequests.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Unsupported parameter: prompt_cache_key"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","choices":[]}`)
	})

	for range 2 {
		client := withPromptCacheKeyRetry(&http.Client{})
		resp := postChatCompletion(t, client, srv.server.URL, model)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
	}

	require.EqualValues(t, 1, badRequests.Load(), "only the first request may be rejected")
	bodies := srv.seen()
	require.Len(t, bodies, 3, "the second request is sent stripped, without a retry")
	require.Contains(t, bodies[0], promptCacheKeyField)
	require.NotContains(t, bodies[1], promptCacheKeyField)
	require.NotContains(t, bodies[2], promptCacheKeyField)
}

// TestPromptCacheKeyRejectionIsModelScoped pins that a rejection for one
// model does not strip the field from another model on the same endpoint: a
// gateway can accept the field for a model it knows.
func TestPromptCacheKeyRejectionIsModelScoped(t *testing.T) {
	const (
		rejectedModel = "model-a"
		otherModel    = "model-b"
	)

	srv := newRecordingServer(t, func(w http.ResponseWriter, body string, _ int) {
		rejected := strings.Contains(body, `"model":"`+rejectedModel+`"`) &&
			strings.Contains(body, promptCacheKeyField)
		if rejected {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Unrecognized request argument supplied: prompt_cache_key"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","choices":[]}`)
	})

	client := withPromptCacheKeyRetry(&http.Client{})
	resp := postChatCompletion(t, client, srv.server.URL, rejectedModel)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	resp = postChatCompletion(t, client, srv.server.URL, otherModel)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	bodies := srv.seen()
	require.Len(t, bodies, 3, "the other model is answered without a retry")
	require.NotContains(t, bodies[1], promptCacheKeyField, "the rejected model is stripped")
	require.Contains(t, bodies[2], promptCacheKeyField, "the other model keeps the field")
}
