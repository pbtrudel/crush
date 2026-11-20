package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
// status and the provider's own words, not a bogus recall count.
func TestRunStatusError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	}))
	t.Cleanup(server.Close)

	_, err := Run(context.Background(), Options{Route: completionsRoute(server), Home: home.At(t.TempDir())})
	require.ErrorContains(t, err, "500")
	require.ErrorContains(t, err, "boom")
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

	url, header, err := endpoint(context.Background(), h, &route.Route{Name: "gpt", Provider: route.ProviderCopilot})
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

	url, header, err := endpoint(context.Background(), h, &route.Route{Name: "glm", Provider: "hyper"})
	require.NoError(t, err)
	require.Equal(t, "https://hyper.charm.land/api/v1/fantasy/chat/completions", url)
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
	url, header, err := endpoint(context.Background(), home.At(t.TempDir()), rt)
	require.NoError(t, err)
	require.Equal(t, "https://api.commandcode.ai/provider/v1/chat/completions", url)
	require.Equal(t, "Bearer resolved-key", header.Get("Authorization"))
	require.Equal(t, "probe", header.Get("User-Agent"))
	require.Equal(t, "resolved-header", header.Get("x-cmd-zdr"))
}

// storeToken writes a token the way `crush-eval login` leaves one.
func storeToken(t *testing.T, h *home.Home, provider string, token *oauth.Token) {
	t.Helper()
	require.NoError(t, route.StoreToken(h, provider, token))
	require.FileExists(t, filepath.Join(h.DataDir(), "crush.json"))
}
