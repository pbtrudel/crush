package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// promptCacheTTLBody is an Anthropic messages request carrying cache_control
// blocks in every place the API accepts one: tools, system, and message
// content. One block already sets a ttl, one is ephemeral without one, and
// one uses another type.
const promptCacheTTLBody = `{"model":"claude-sonnet-4-5","max_tokens":64,"system":[{"type":"text","text":"be brief","cache_control":{"type":"ephemeral"}}],"tools":[{"name":"read","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}},{"name":"write","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"5m"}},{"name":"grep","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}},{"type":"text","text":"no cache","cache_control":{"type":"auto"}}]}]}`

// promptCacheRecorder starts a server that records the bodies and
// anthropic-beta headers it receives and answers with status.
func promptCacheRecorder(t *testing.T, status int, response string) (*httptest.Server, func() (bodies, betas []string)) {
	t.Helper()
	return promptCacheHandlerRecorder(t, func(w http.ResponseWriter, _ int) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	})
}

// promptCacheHandlerRecorder is promptCacheRecorder with a handler that also
// sees the request count, for tests that reject the first request.
func promptCacheHandlerRecorder(t *testing.T, handler func(w http.ResponseWriter, count int)) (*httptest.Server, func() (bodies, betas []string)) {
	t.Helper()
	var mu sync.Mutex
	var recordedBodies, recordedBetas []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		recordedBodies = append(recordedBodies, string(body))
		recordedBetas = append(recordedBetas, strings.Join(r.Header.Values("anthropic-beta"), "|"))
		count := len(recordedBodies)
		mu.Unlock()
		handler(w, count)
	}))
	t.Cleanup(srv.Close)
	return srv, func() ([]string, []string) {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), recordedBodies...), append([]string(nil), recordedBetas...)
	}
}

// postPromptCacheJSON sends one JSON POST with optional extra headers.
func postPromptCacheJSON(t *testing.T, client *http.Client, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// jsonObject asserts value is a decoded JSON object.
func jsonObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected a JSON object, got %T", value)
	return object
}

// jsonArray asserts value is a decoded JSON array.
func jsonArray(t *testing.T, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	require.True(t, ok, "expected a JSON array, got %T", value)
	return array
}

// cacheControlOf returns the cache_control object carried by one decoded
// request part.
func cacheControlOf(t *testing.T, part any) map[string]any {
	t.Helper()
	object := jsonObject(t, part)
	control, ok := object["cache_control"].(map[string]any)
	require.True(t, ok, "part must carry a cache_control object")
	return control
}

// TestPromptCacheTTLTransportAddsExtendedTTL pins the rewrite: every
// ephemeral cache_control block without a ttl gets "1h", anywhere it sits in
// the body, while explicit ttls and other types are left alone.
func TestPromptCacheTTLTransportAddsExtendedTTL(t *testing.T) {
	t.Parallel()

	srv, received := promptCacheRecorder(t, http.StatusOK, `{}`)
	client := &http.Client{Transport: newPromptCacheTTLTransport(http.DefaultTransport)}
	resp := postPromptCacheJSON(t, client, srv.URL, promptCacheTTLBody, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	bodies, _ := received()
	require.Len(t, bodies, 1)
	got := jsonObject(t, decodePromptCacheBody(t, bodies[0]))

	system := jsonArray(t, got["system"])
	require.Equal(t, promptCacheTTLExtendedTTL, cacheControlOf(t, system[0])["ttl"])

	tools := jsonArray(t, got["tools"])
	require.Equal(t, promptCacheTTLExtendedTTL, cacheControlOf(t, tools[0])["ttl"], "an ephemeral block without a ttl gets the extended one")
	require.Equal(t, "5m", cacheControlOf(t, tools[1])["ttl"], "an explicit ttl is left alone")
	require.Equal(t, promptCacheTTLExtendedTTL, cacheControlOf(t, tools[2])["ttl"], "an extended ttl is kept")

	messages := jsonArray(t, got["messages"])
	content := jsonArray(t, jsonObject(t, messages[0])["content"])
	require.Equal(t, promptCacheTTLExtendedTTL, cacheControlOf(t, content[0])["ttl"], "message content is rewritten too")
	_, hasTTL := cacheControlOf(t, content[1])["ttl"]
	require.False(t, hasTTL, "a non-ephemeral cache_control must not gain a ttl")
	require.Equal(t, "auto", cacheControlOf(t, content[1])["type"])
}

// TestPromptCacheTTLTransportKeepsUnchangedBodiesByteIdentical pins that the
// rewrite only re-encodes a body that actually changes, so requests with
// nothing to gain from the extended ttl reach the wire byte for byte.
func TestPromptCacheTTLTransportKeepsUnchangedBodiesByteIdentical(t *testing.T) {
	t.Parallel()

	bodies := map[string]string{
		"no cache control":   `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`,
		"every ttl is set":   `{"model":"claude-sonnet-4-5","tools":[{"name":"read","cache_control":{"type":"ephemeral","ttl":"5m"}}],"messages":[]}`,
		"non ephemeral only": `{"model":"claude-sonnet-4-5","system":[{"type":"text","text":"hi","cache_control":{"type":"auto"}}],"messages":[]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, received := promptCacheRecorder(t, http.StatusOK, `{}`)
			client := &http.Client{Transport: newPromptCacheTTLTransport(http.DefaultTransport)}
			resp := postPromptCacheJSON(t, client, srv.URL, body, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_ = resp.Body.Close()

			got, _ := received()
			require.Len(t, got, 1)
			require.Equal(t, body, got[0])
		})
	}
}

// TestPromptCacheTTLTransportAppendsBetaHeader pins the beta header: it is
// appended to whatever anthropic-beta values the request already carries and
// never duplicated.
func TestPromptCacheTTLTransportAppendsBetaHeader(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		existing string
		want     string
	}{
		{name: "no header", want: promptCacheTTLBeta},
		{name: "other betas", existing: "prompt-caching-2024-07-31", want: "prompt-caching-2024-07-31," + promptCacheTTLBeta},
		{name: "already requested", existing: "prompt-caching-2024-07-31," + promptCacheTTLBeta, want: "prompt-caching-2024-07-31," + promptCacheTTLBeta},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, received := promptCacheRecorder(t, http.StatusOK, `{}`)
			client := &http.Client{Transport: newPromptCacheTTLTransport(http.DefaultTransport)}
			var headers map[string]string
			if tc.existing != "" {
				headers = map[string]string{"anthropic-beta": tc.existing}
			}
			resp := postPromptCacheJSON(t, client, srv.URL, promptCacheTTLBody, headers)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_ = resp.Body.Close()

			_, betas := received()
			require.Len(t, betas, 1)
			require.Equal(t, tc.want, betas[0])
			require.Equal(t, 1, strings.Count(betas[0], promptCacheTTLBeta), "the beta token must appear exactly once")
		})
	}
}

// TestPromptCacheTTLWiring pins the provider wiring: the default lifetime
// installs no transport and no client of its own, the extended lifetime wraps
// the Anthropic client, and other Messages-wire providers keep their own
// endpoint untouched.
func TestPromptCacheTTLWiring(t *testing.T) {
	t.Parallel()

	newCoordinator := func(t *testing.T) *coordinator {
		t.Helper()
		env := testEnv(t)
		return newTestCoordinator(t, env, "anthropic", config.ProviderConfig{ID: "anthropic"})
	}

	t.Run("default installs nothing", func(t *testing.T) {
		t.Parallel()
		coord := newCoordinator(t)
		coord.cfg.Config().Options.PromptCacheTTL = config.PromptCacheTTLDefault

		client := &http.Client{}
		require.Same(t, client, coord.withPromptCacheTTL(client))
		require.Nil(t, client.Transport, "the default lifetime must leave the client untouched")
		require.Nil(t, coord.anthropicHTTPClient(string(catwalk.InferenceProviderAnthropic), ""), "no debug logging and no extended ttl means the SDK default client")
	})

	t.Run("extended lifetime wraps the anthropic client", func(t *testing.T) {
		t.Parallel()
		coord := newCoordinator(t)
		coord.cfg.Config().Options.PromptCacheTTL = config.PromptCacheTTLExtended

		client := coord.anthropicHTTPClient(string(catwalk.InferenceProviderAnthropic), "https://api.anthropic.com")
		require.NotNil(t, client)
		require.IsType(t, &promptCacheTTLTransport{}, client.Transport)

		variant := &http.Client{}
		require.Same(t, variant, coord.withPromptCacheTTL(variant))
		require.IsType(t, &promptCacheTTLTransport{}, variant.Transport)
	})

	t.Run("other messages providers are left alone", func(t *testing.T) {
		t.Parallel()
		coord := newCoordinator(t)
		coord.cfg.Config().Options.PromptCacheTTL = config.PromptCacheTTLExtended

		for _, tc := range []struct {
			name       string
			providerID string
			baseURL    string
		}{
			{name: "minimax gateway", providerID: "minimax", baseURL: "https://api.minimax.io/anthropic"},
			{name: "custom endpoint", providerID: "my-anthropic-proxy", baseURL: "https://anthropic.internal.example.com/v1"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				require.Nil(t, coord.anthropicHTTPClient(tc.providerID, tc.baseURL), "only Anthropic itself understands the extended cache ttl")
			})
		}

		for _, tc := range []struct {
			name       string
			providerID string
			baseURL    string
		}{
			{name: "anthropic host on a custom id", providerID: "my-gateway", baseURL: "https://api.anthropic.com/v1"},
			{name: "empty base url is the anthropic default", providerID: "my-gateway", baseURL: ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := coord.anthropicHTTPClient(tc.providerID, tc.baseURL)
				require.NotNil(t, client)
				require.IsType(t, &promptCacheTTLTransport{}, client.Transport)
			})
		}
	})
}

// TestPromptCacheRetentionTransportAdds24h pins the OpenAI hint: it is added
// to Responses and chat completions bodies, and an explicit value is kept.
func TestPromptCacheRetentionTransportAdds24h(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "chat completions", body: `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`},
		{name: "responses", body: `{"model":"gpt-5","input":[{"role":"user","content":"hi"}]}`},
		{name: "explicit value", body: `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"prompt_cache_retention":"in_memory"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, received := promptCacheRecorder(t, http.StatusOK, `{"id":"resp-1"}`)
			client := &http.Client{Transport: newPromptCacheRetentionTransport(http.DefaultTransport)}
			resp := postPromptCacheJSON(t, client, srv.URL, tc.body, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			_ = resp.Body.Close()

			bodies, _ := received()
			require.Len(t, bodies, 1)
			got := jsonObject(t, decodePromptCacheBody(t, bodies[0]))
			if tc.name == "explicit value" {
				require.Equal(t, "in_memory", got[promptCacheRetentionField], "an explicit retention must win")
				return
			}
			require.Equal(t, promptCacheRetentionValue, got[promptCacheRetentionField])
		})
	}
}

// TestPromptCacheRetentionDropsRejectedField pins the fallback: a 400 that
// names prompt_cache_retention is retried once without the field, and every
// later request to that endpoint and model leaves it out.
func TestPromptCacheRetentionDropsRejectedField(t *testing.T) {
	t.Parallel()

	srv := newRecordingServer(t, func(w http.ResponseWriter, _ string, count int) {
		if count == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Unrecognized request argument supplied: prompt_cache_retention","type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp-1","output":[]}`)
	})

	client := withPromptCacheRetention(&http.Client{})
	resp := postPromptCacheJSON(t, client, srv.server.URL, `{"model":"retention-rejected-model","input":[{"role":"user","content":"hi"}]}`, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	bodies := srv.seen()
	require.Len(t, bodies, 2, "one rejected request and one retry without the field")
	require.Contains(t, bodies[0], `"prompt_cache_retention":"24h"`)
	require.NotContains(t, bodies[1], promptCacheRetentionField)

	// A transport built later, as a provider rebuild would install, must not
	// send the field either.
	fresh := withPromptCacheRetention(&http.Client{})
	resp = postPromptCacheJSON(t, fresh, srv.server.URL, `{"model":"retention-rejected-model","input":[{"role":"user","content":"hi"}]}`, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	bodies = srv.seen()
	require.Len(t, bodies, 3)
	require.NotContains(t, bodies[2], promptCacheRetentionField)
}

// TestPromptCacheRetentionIgnoresOtherRejections pins that only a 400 naming
// the field drops it: any other rejection reaches the caller and later
// requests keep sending it.
func TestPromptCacheRetentionIgnoresOtherRejections(t *testing.T) {
	t.Parallel()

	srv := newRecordingServer(t, func(w http.ResponseWriter, _ string, _ int) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"model not found","type":"invalid_request_error"}}`)
	})

	client := withPromptCacheRetention(&http.Client{})
	resp := postPromptCacheJSON(t, client, srv.server.URL, `{"model":"retention-other-model","input":[{"role":"user","content":"hi"}]}`, nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	_ = resp.Body.Close()

	resp = postPromptCacheJSON(t, client, srv.server.URL, `{"model":"retention-other-model","input":[{"role":"user","content":"hi"}]}`, nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	_ = resp.Body.Close()

	bodies := srv.seen()
	require.Len(t, bodies, 2)
	for _, body := range bodies {
		require.Contains(t, body, promptCacheRetentionField)
	}
}

// TestPromptCacheRetentionWiring pins that only OpenAI's own provider gets
// the retention transport, and only for the extended lifetime.
func TestPromptCacheRetentionWiring(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := newTestCoordinator(t, env, "openai", config.ProviderConfig{ID: "openai"})

	coord.cfg.Config().Options.PromptCacheTTL = config.PromptCacheTTLDefault
	require.Nil(t, coord.withOpenAIPromptCacheRetention(nil, string(catwalk.InferenceProviderOpenAI)))

	coord.cfg.Config().Options.PromptCacheTTL = config.PromptCacheTTLExtended
	gateway := &http.Client{}
	require.Same(t, gateway, coord.withOpenAIPromptCacheRetention(gateway, string(catwalk.InferenceProviderCopilot)))
	require.Nil(t, gateway.Transport, "a gateway provider must stay untouched")

	client := coord.withOpenAIPromptCacheRetention(nil, string(catwalk.InferenceProviderOpenAI))
	require.NotNil(t, client)
	require.IsType(t, &promptCacheRetentionTransport{}, client.Transport)
}

// TestPromptCacheTTLTransportDropsRejectedExtension pins the safety net: a
// 400 naming the ttl or the beta header is retried once with the original
// body and headers, and later requests to that endpoint and model skip the
// rewrite.
func TestPromptCacheTTLTransportDropsRejectedExtension(t *testing.T) {
	t.Parallel()

	srv, received := promptCacheHandlerRecorder(t, func(w http.ResponseWriter, count int) {
		if count == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Unexpected field cache_control.ttl","type":"invalid_request_error"}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	})

	client := &http.Client{Transport: newPromptCacheTTLTransport(http.DefaultTransport)}
	resp := postPromptCacheJSON(t, client, srv.URL, promptCacheTTLBody, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	bodies, betas := received()
	require.Len(t, bodies, 2, "one rejected request and one retry without the rewrite")
	require.Equal(t, 4, strings.Count(bodies[0], `"ttl":"1h"`), "the rejected request asked for the extended ttl")
	require.Contains(t, betas[0], promptCacheTTLBeta)
	require.Equal(t, promptCacheTTLBody, bodies[1], "the retry must send the original body")
	require.Equal(t, 1, strings.Count(bodies[1], `"ttl":"1h"`), "the original body keeps only its explicit ttl")
	require.Empty(t, betas[1], "the retry must send the original headers")

	// A transport built later, as a provider rebuild would install, must not
	// rewrite the request either.
	fresh := &http.Client{Transport: newPromptCacheTTLTransport(http.DefaultTransport)}
	resp = postPromptCacheJSON(t, fresh, srv.URL, promptCacheTTLBody, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	bodies, betas = received()
	require.Len(t, bodies, 3)
	require.Equal(t, promptCacheTTLBody, bodies[2])
	require.Empty(t, betas[2])
}

// decodePromptCacheBody decodes a recorded request body into a generic JSON
// value.
func decodePromptCacheBody(t *testing.T, body string) any {
	t.Helper()
	var value any
	require.NoError(t, json.Unmarshal([]byte(body), &value))
	return value
}
