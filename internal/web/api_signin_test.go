package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

const testKey = "sk-valid"

// testKnownProviders is the catalog every sign-in test starts from: the
// four OAuth providers and one API-key provider with a model.
func testKnownProviders(*config.Config) ([]catwalk.Provider, error) {
	return []catwalk.Provider{
		{ID: "hyper", Name: "Hyper"},
		{ID: catwalk.InferenceProviderCopilot, Name: "GitHub Copilot"},
		{ID: catwalk.InferenceProviderOpenAI, Name: "OpenAI"},
		{ID: catwalk.InferenceProviderXAI, Name: "xAI"},
		{ID: catwalk.InferenceProviderAnthropic, Name: "Anthropic", Models: []catwalk.Model{{ID: "claude", Name: "Claude"}}},
	}, nil
}

// acceptOnlyValidKey stands in for the provider check. Its error repeats
// part of the key, as real providers do, so a gateway that logged or
// forwarded it would fail the tests.
func acceptOnlyValidKey(_ context.Context, _ catwalk.Provider, key string, _ config.VariableResolver) error {
	if key != testKey {
		return fmt.Errorf("401 Incorrect API key provided: %s", key)
	}
	return nil
}

func signInGateway(t *testing.T) (*Gateway, *webtest.Fake) {
	t.Helper()
	g, f := startWith(t, Options{KnownProviders: testKnownProviders, TestAPIKey: acceptOnlyValidKey})
	known, _ := testKnownProviders(nil)
	f.SetCatalog(known)
	return g, f
}

func TestAPIKeySignInSavesAndAppliesTheModel(t *testing.T) {
	t.Parallel()
	g, f := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"anthropic","api_key":"  `+testKey+`  ","model":{"type":"main","model":"claude"}}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"signed_in":true,"model_applied":true}`, string(body))
	require.Contains(t, f.CallLog(), "SetProviderAPIKey:global:anthropic:key")
	require.NotContains(t, string(body), testKey)
}

func TestAPIKeyRejectedNeverEchoed(t *testing.T) {
	t.Parallel()
	g, f := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"anthropic","api_key":"sk-wrong"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusUnprocessableEntity, rsp.StatusCode)
	require.Contains(t, string(body), "key_rejected")
	require.NotContains(t, string(body), "sk-wrong")
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
}

func TestAPIKeyInQueryIsRefused(t *testing.T) {
	t.Parallel()
	g, f := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key?api_key="+testKey,
		`{"provider":"anthropic"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "secret_in_url")
	require.NotContains(t, string(body), testKey)
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
}

func TestAPIKeyRefusedForOAuthOnlyProvider(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"copilot","api_key":"`+testKey+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "api_key_not_supported")
}

func TestAPIKeySignInWhileBusyKeepsTheKey(t *testing.T) {
	t.Parallel()
	g, f := signInGateway(t)
	f.SetAgentBusy(true)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"anthropic","api_key":"`+testKey+`","model":{"type":"main","model":"claude"}}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.JSONEq(t, `{"signed_in":true,"model_applied":false,"reason":"agent_busy"}`, string(body))
}

// TestAPIKeyNeverLogged is not parallel: captureLogs replaces the logger.
func TestAPIKeyNeverLogged(t *testing.T) {
	logs := captureLogs(t)
	g, _ := signInGateway(t)
	c := authedClient(t, g)
	keys := []string{testKey, "sk-wrong-4f2a", "sk-broken-9c1d"}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"provider":"anthropic","api_key":"` + keys[0] + `"}`, http.StatusOK},
		{`{"provider":"anthropic","api_key":"` + keys[1] + `"}`, http.StatusUnprocessableEntity},
		{`{"provider":"anthropic","api_key":"` + keys[2], http.StatusBadRequest},
	} {
		rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/key", tc.body)
		require.Equal(t, tc.want, rsp.StatusCode, tc.body)
		for _, k := range keys {
			require.NotContains(t, string(body), k)
		}
		rsp.Body.Close()
	}
	for _, fr := range g.hub.frames() {
		for _, k := range keys {
			require.NotContains(t, string(fr.Payload), k)
		}
	}
	rsp, snap := doJSON(t, c, http.MethodGet, g.URL()+"/api/snapshot", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	for _, k := range keys {
		require.NotContains(t, string(snap), k)
	}
	for _, k := range keys {
		require.NotContains(t, logs.String(), k)
	}
}

func TestSignInProvidersRoute(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/auth/providers", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, string(body), `"id":"copilot","name":"GitHub Copilot","configured":false,"methods":["oauth"],"oauth_kind":"device"`)
}

func TestSignInProvidersUnavailable(t *testing.T) {
	t.Parallel()
	// The fake has no config and the default loader has nothing to read.
	g, _ := startWith(t, Options{})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/auth/providers", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadGateway, rsp.StatusCode)
	require.Contains(t, string(body), "providers_unavailable")
}

func TestSignInUnknownProvider(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"nobody","api_key":"`+testKey+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_provider")
}

func TestAPIKeyTooLongAndEmptyAreRefused(t *testing.T) {
	t.Parallel()
	g, f := signInGateway(t)
	for _, body := range []string{
		`{"provider":"anthropic","api_key":"   "}`,
		`{"provider":"anthropic","api_key":"` + strings.Repeat("x", 4097) + `"}`,
	} {
		rsp, respBody := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key", body)
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
		require.Contains(t, string(respBody), "bad_api_key")
		rsp.Body.Close()
	}
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
}

func TestAPIKeyBadModelType(t *testing.T) {
	t.Parallel()
	g, f := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"anthropic","api_key":"`+testKey+`","model":{"type":"huge","model":"claude"}}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "bad_type")
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
}

func TestAPIKeyInHeaderIsNeverRead(t *testing.T) {
	t.Parallel()
	g, f := signInGateway(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/auth/provider/key",
		strings.NewReader(`{"provider":"anthropic"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", g.URL())
	req.Header.Set("X-Api-Key", testKey)
	rsp := doReq(t, authedClient(t, g), req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	body, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "bad_api_key")
	require.NotContains(t, string(body), testKey)
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
}
