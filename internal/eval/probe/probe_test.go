package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	hyperp "github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/stretchr/testify/require"
)

// probeRequest is one request the fake provider received.
type probeRequest struct {
	Path    string
	Header  http.Header
	Body    map[string]any
	Content []map[string]any // the messages, as sent.
}

// newProvider serves chat completions and answers with the nonce only when
// the request carries the extra field the probe's second pass merges in.
func newProvider(t *testing.T, nonceIf string) (*httptest.Server, *[]probeRequest) {
	t.Helper()
	var (
		requests []probeRequest
		mu       sync.Mutex
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		var messages []map[string]any
		if raw, ok := body["messages"].([]any); ok {
			for _, m := range raw {
				messages = append(messages, m.(map[string]any))
			}
		}
		mu.Lock()
		requests = append(requests, probeRequest{Path: r.URL.Path, Header: r.Header.Clone(), Body: body, Content: messages})
		mu.Unlock()

		answer := "OK"
		if nonceIf != "" {
			if _, ok := body[nonceIf]; ok {
				answer = Nonce
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": answer}}},
		})
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// completionsRoute is an openai-compatible route pointed at the fake
// provider.
func completionsRoute(server *httptest.Server) *route.Route {
	return &route.Route{
		Name:     "fake",
		Provider: "commandcode",
		Model:    "deepseek/deepseek-v4.1-flash",
		ProviderConfig: map[string]any{
			"base_url": server.URL + "/v1",
			"api_key":  "test-key",
		},
	}
}

// TestRun is one probe: the same three turns twice, once as Crush builds the
// request and once with an extra field, then the recall of each pass.
func TestRun(t *testing.T) {
	t.Parallel()

	server, requests := newProvider(t, "effort")
	rt := completionsRoute(server)
	report, err := Run(context.Background(), Options{
		Route: rt,
		Home:  home.At(t.TempDir()),
		Extra: map[string]any{"effort": "high"},
		Reps:  2,
	})
	require.NoError(t, err)

	require.Equal(t, 2, report.Reps)
	require.Equal(t, 0, report.RecalledWithout, "without the extra field the nonce is not echoed")
	require.Equal(t, 2, report.RecalledWith, "with the extra field the nonce is echoed")
	require.Equal(t, []string{"OK", "OK"}, report.Without)
	require.Equal(t, []string{Nonce, Nonce}, report.With)

	require.Len(t, *requests, 4)
	for i, request := range *requests {
		require.Equal(t, "/v1/chat/completions", request.Path)
		require.Equal(t, "Bearer test-key", request.Header.Get("Authorization"))
		require.Equal(t, "application/json", request.Header.Get("Content-Type"))
		require.Equal(t, rt.Model, request.Body["model"])
		require.Equal(t, false, request.Body["stream"], "the probe does not stream")

		require.Len(t, request.Content, 3)
		require.Equal(t, "user", request.Content[0]["role"])
		require.Equal(t, "assistant", request.Content[1]["role"])
		require.Equal(t, Nonce, request.Content[1]["reasoning_content"], "the nonce rides in the reasoning")
		require.Equal(t, "OK", request.Content[1]["content"])
		require.Equal(t, "user", request.Content[2]["role"])
		require.Contains(t, request.Content[2]["content"], "codeword")
		require.NotContains(t, request.Content[2]["content"], Nonce, "the nonce is never named in a prompt")

		_, hasExtra := request.Body["effort"]
		require.Equal(t, i >= 2, hasExtra, "only the second pass carries the extra field")
	}
}

// TestRunDefaultReps checks that a probe without --reps sends one request per
// pass.
func TestRunDefaultReps(t *testing.T) {
	t.Parallel()

	server, requests := newProvider(t, "")
	report, err := Run(context.Background(), Options{Route: completionsRoute(server), Home: home.At(t.TempDir())})
	require.NoError(t, err)
	require.Equal(t, 1, report.Reps)
	require.Len(t, report.Without, 1)
	require.Len(t, report.With, 1)
	require.Len(t, *requests, 2)
}

// TestRunShortAnswers checks that an answer is one line of at most forty
// characters, so a report stays readable and a stray long reply does not fill
// the terminal.
func TestRunShortAnswers(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("word ", 40) + "\n" + strings.Repeat("more ", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": long}}},
		})
	}))
	t.Cleanup(server.Close)

	report, err := Run(context.Background(), Options{Route: completionsRoute(server), Home: home.At(t.TempDir())})
	require.NoError(t, err)
	for _, answer := range append(report.Without, report.With...) {
		require.LessOrEqual(t, len([]rune(answer)), maxAnswer)
	}
	require.NotContains(t, report.Without[0], "\n")
}

// TestRunStatusError checks that a refused request stops the probe with the
// status and, when the body is JSON, the provider's own error type or code.
// The body text never appears: a provider can echo a key in it.
func TestRunStatusError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		body    string
		want    []string
		notWant []string
	}{
		{
			name:    "nested type",
			status:  http.StatusInternalServerError,
			body:    `{"error":{"type":"server_error","message":"upstream said sk-test-1234"}}`,
			want:    []string{"status 500", "server_error"},
			notWant: []string{"upstream said", "sk-test-1234"},
		},
		{
			name:    "top-level type and code",
			status:  http.StatusUnauthorized,
			body:    `{"type":"authentication_error","code":"invalid_api_key","message":"key sk-test-1234 is not valid"}`,
			want:    []string{"status 401", "authentication_error"},
			notWant: []string{"not valid", "sk-test-1234"},
		},
		{
			name:    "code only",
			status:  http.StatusBadRequest,
			body:    `{"error":{"code":"unsupported_model"}}`,
			want:    []string{"status 400", "unsupported_model"},
			notWant: []string{"error"},
		},
		{
			name:    "plain text",
			status:  http.StatusBadGateway,
			body:    "bad gateway sk-test-1234",
			want:    []string{"status 502"},
			notWant: []string{"bad gateway", "sk-test-1234"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(server.Close)

			_, err := Run(context.Background(), Options{Route: completionsRoute(server), Home: home.At(t.TempDir())})
			require.Error(t, err)
			for _, want := range tt.want {
				require.ErrorContains(t, err, want)
			}
			for _, notWant := range tt.notWant {
				require.NotContains(t, err.Error(), notWant)
			}
		})
	}
}

// TestRunWithoutBaseURL checks that a route the probe cannot address fails
// with the route's name instead of an empty request.
func TestRunWithoutBaseURL(t *testing.T) {
	t.Parallel()

	_, err := Run(context.Background(), Options{
		Route: &route.Route{Name: "odd", Provider: "commandcode", Model: "m"},
		Home:  home.At(t.TempDir()),
	})
	require.ErrorContains(t, err, "odd")
	require.ErrorContains(t, err, "base_url")
}

// TestEndpointCopilot checks the Copilot request the probe builds: the API
// host the jail allows, the headers Crush sends, and the stored token as the
// bearer, with no token endpoint involved.
func TestEndpointCopilot(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	storeToken(t, h, route.ProviderCopilot, &oauth.Token{
		AccessToken:  "copilot-token",
		RefreshToken: "github-token",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	})

	url, header, err := endpoint(context.Background(), Options{
		Route: &route.Route{Name: "gpt", Provider: route.ProviderCopilot},
		Home:  h,
	})
	require.NoError(t, err)
	require.Equal(t, "https://api.githubcopilot.com/chat/completions", url)
	require.Equal(t, "Bearer copilot-token", header.Get("Authorization"))
	require.NotEmpty(t, header.Get("User-Agent"))
	require.NotEmpty(t, header.Get("Copilot-Integration-Id"))
}

// TestEndpointHyper checks the Hyper request the probe builds: the chat
// completions endpoint one path below the catalog endpoint Crush ships, with
// the stored token as the bearer.
func TestEndpointHyper(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	storeToken(t, h, "hyper", &oauth.Token{
		AccessToken:  "hyper-token",
		RefreshToken: "hyper-refresh",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	})

	url, header, err := endpoint(context.Background(), Options{
		Route: &route.Route{Name: "glm", Provider: "hyper"},
		Home:  h,
	})
	require.NoError(t, err)
	// The base URL is Crush's own, so the expectation follows $HYPER_URL.
	require.Equal(t, strings.TrimSuffix(hyperp.BaseURL(), "/")+"/v1/chat/completions", url)
	require.Equal(t, "Bearer hyper-token", header.Get("Authorization"))
}

// TestEndpointProviderConfig checks that a route with its own endpoint sends
// its key and its extra headers, with the $NAME references resolved from the
// environment the way Crush resolves them.
func TestEndpointProviderConfig(t *testing.T) {
	t.Setenv("PROBE_TEST_KEY", "resolved-key")
	t.Setenv("PROBE_TEST_HEADER", "resolved-header")

	rt := &route.Route{
		Name:     "fake",
		Provider: "commandcode",
		ProviderConfig: map[string]any{
			"base_url":      "https://api.commandcode.ai/provider/v1",
			"api_key":       "$PROBE_TEST_KEY",
			"extra_headers": map[string]any{"User-Agent": "probe", "x-cmd-zdr": "$PROBE_TEST_HEADER"},
		},
	}
	url, header, err := endpoint(context.Background(), Options{Route: rt, Home: home.At(t.TempDir())})
	require.NoError(t, err)
	require.Equal(t, "https://api.commandcode.ai/provider/v1/chat/completions", url)
	require.Equal(t, "Bearer resolved-key", header.Get("Authorization"))
	require.Equal(t, "probe", header.Get("User-Agent"))
	require.Equal(t, "resolved-header", header.Get("x-cmd-zdr"))
}

// storeToken writes a token the way `crush-eval login` leaves one.
func storeToken(t *testing.T, h *home.Home, provider string, token *oauth.Token) {
	t.Helper()
	doc := map[string]any{
		"providers": map[string]any{
			provider: map[string]any{"api_key": token.AccessToken, "oauth": token},
		},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(h.DataDir(), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(h.DataDir(), "crush.json"), data, 0o600))
}

// dataConfig is the eval home's data config, to prove a probe left it alone.
func dataConfig(t *testing.T, h *home.Home) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.DataDir(), "crush.json"))
	require.NoError(t, err)
	return data
}

// TestEndpointHyperDoesNotExchangeAnExpiredToken checks that the probe never
// exchanges a Hyper refresh token: Hyper rotates them, and Crush guards the
// exchange with a cross-process lock the probe does not hold, so an exchange
// here could revoke a live batch's token family.
func TestEndpointHyperDoesNotExchangeAnExpiredToken(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	storeToken(t, h, "hyper", &oauth.Token{
		AccessToken:  "old-token",
		RefreshToken: "hyper-refresh",
		ExpiresAt:    time.Now().Add(-time.Minute).Unix(),
	})
	before := dataConfig(t, h)

	_, _, err := endpoint(context.Background(), Options{
		Route: &route.Route{Name: "glm", Provider: "hyper"},
		Home:  h,
		Refresh: func(context.Context, string, string) (*oauth.Token, error) {
			t.Error("the probe must not exchange a Hyper refresh token")
			return nil, nil
		},
	})
	require.ErrorContains(t, err, "crush-eval login glm")
	require.Equal(t, before, dataConfig(t, h), "the eval home is not written")
}

// TestEndpointCopilotRefreshesInMemory checks that an expired Copilot token is
// renewed for the probe alone: Crush's own writes go through a lock the probe
// does not hold, so the fresh token is never persisted.
func TestEndpointCopilotRefreshesInMemory(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	storeToken(t, h, route.ProviderCopilot, &oauth.Token{
		AccessToken:  "old-token",
		RefreshToken: "github-token",
		ExpiresAt:    time.Now().Add(-time.Minute).Unix(),
	})
	before := dataConfig(t, h)

	var gotProvider, gotRefresh string
	_, header, err := endpoint(context.Background(), Options{
		Route: &route.Route{Name: "gpt", Provider: route.ProviderCopilot},
		Home:  h,
		Refresh: func(_ context.Context, provider, refreshToken string) (*oauth.Token, error) {
			gotProvider, gotRefresh = provider, refreshToken
			return &oauth.Token{AccessToken: "fresh-token", ExpiresAt: time.Now().Add(time.Hour).Unix()}, nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, "Bearer fresh-token", header.Get("Authorization"))
	require.Equal(t, route.ProviderCopilot, gotProvider)
	require.Equal(t, "github-token", gotRefresh)
	require.Equal(t, before, dataConfig(t, h), "the fresh token is kept in memory only")
}

// TestEndpointWithoutAStoredToken checks that a route signed out on the runner
// says how to sign it back in.
func TestEndpointWithoutAStoredToken(t *testing.T) {
	t.Parallel()

	_, _, err := endpoint(context.Background(), Options{
		Route: &route.Route{Name: "gpt", Provider: route.ProviderCopilot},
		Home:  home.At(t.TempDir()),
	})
	require.ErrorContains(t, err, "crush-eval login gpt")
}

// TestRunCopilotFailureHint checks the one hint a failed Copilot probe adds:
// the probe speaks chat completions, so a model that Copilot serves only
// through /responses cannot be probed. An auth failure gets no such hint.
func TestRunCopilotFailureHint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		wantHint bool
	}{
		{name: "model not served here", status: http.StatusNotFound, wantHint: true},
		{name: "bad request", status: http.StatusBadRequest, wantHint: true},
		{name: "unauthorized", status: http.StatusUnauthorized, wantHint: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := home.At(t.TempDir())
			require.NoError(t, h.Ensure())
			storeToken(t, h, route.ProviderCopilot, &oauth.Token{
				AccessToken: "token",
				ExpiresAt:   time.Now().Add(time.Hour).Unix(),
			})

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, `{"error":{"type":"not_found"}}`)
			}))
			t.Cleanup(server.Close)

			rt := &route.Route{
				Name: "gpt", Provider: route.ProviderCopilot, Model: "gpt-6-luna",
				ProviderConfig: map[string]any{"base_url": server.URL},
			}
			_, err := Run(context.Background(), Options{Route: rt, Home: h})
			require.ErrorContains(t, err, fmt.Sprintf("status %d", tt.status))
			if tt.wantHint {
				require.ErrorContains(t, err, "chat completions")
				require.ErrorContains(t, err, "/responses")
			} else {
				require.NotContains(t, err.Error(), "/responses")
			}
		})
	}
}
