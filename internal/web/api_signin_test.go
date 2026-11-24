package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/env"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/workspace"
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

// countingWorkspace records resolver calls and can fail the model rebuild.
type countingWorkspace struct {
	*webtest.Fake
	resolverCalls atomic.Int64
	updateErr     error
	// resolver, when set, is what Resolver returns; nil means the
	// identity resolver.
	resolver config.VariableResolver
}

func (w *countingWorkspace) Resolver() config.VariableResolver {
	w.resolverCalls.Add(1)
	if w.resolver != nil {
		return w.resolver
	}
	return config.IdentityResolver()
}

func (w *countingWorkspace) UpdateAgentModel(ctx context.Context) error {
	if w.updateErr != nil {
		return w.updateErr
	}
	return w.Fake.UpdateAgentModel(ctx)
}

func signInGatewayWithWorkspace(t *testing.T, updateErr error, test func(context.Context, catwalk.Provider, string, config.VariableResolver) error) (*Gateway, *webtest.Fake, *countingWorkspace) {
	t.Helper()
	fake := webtest.NewFake()
	ws := &countingWorkspace{Fake: fake, updateErr: updateErr}
	g, err := Start(t.Context(), Options{Workspace: ws, KnownProviders: testKnownProviders, TestAPIKey: test})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	known, _ := testKnownProviders(nil)
	fake.SetCatalog(known)
	return g, fake, ws
}

// keyVerification is what the test API key check saw.
type keyVerification struct {
	key      string
	resolved string
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
	pc, ok := f.Config().Providers.Get("anthropic")
	require.True(t, ok)
	require.Empty(t, pc.APIKey, "the fake marks the provider configured without storing the key")
}

func TestAPIKeySaveFailure(t *testing.T) {
	t.Parallel()
	g, f := signInGateway(t)
	f.SetProviderKeyErr(errors.New("the config could not be written"))
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"anthropic","api_key":"`+testKey+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusInternalServerError, rsp.StatusCode)
	require.Contains(t, string(body), "save_api_key")
	require.NotContains(t, string(body), testKey)
}

func TestAPIKeyModelApplicationReasons(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		model     string
		updateErr error
		want      string
	}{
		{"agent_starting", "claude", workspace.ErrAgentNotInitialized, "agent_starting"},
		{"model_unavailable", "missing", nil, "model_unavailable"},
		{"model_failed", "claude", errors.New("the agent model could not be rebuilt"), "model_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, f, _ := signInGatewayWithWorkspace(t, tc.updateErr, acceptOnlyValidKey)
			rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
				`{"provider":"anthropic","api_key":"`+testKey+`","model":{"type":"main","model":"`+tc.model+`"}}`)
			defer rsp.Body.Close()
			require.Equal(t, http.StatusOK, rsp.StatusCode)
			require.JSONEq(t, `{"signed_in":true,"model_applied":false,"reason":"`+tc.want+`"}`, string(body))
			require.Contains(t, f.CallLog(), "SetProviderAPIKey:global:anthropic:key")
		})
	}
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

func TestAPIKeyHeaderIsIgnored(t *testing.T) {
	t.Parallel()
	g, f := signInGateway(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/auth/provider/key",
		strings.NewReader(`{"provider":"anthropic","api_key":"`+testKey+`"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", g.URL())
	req.Header.Set("X-Api-Key", "sk-from-the-header")
	rsp := doReq(t, authedClient(t, g), req)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, "the body key is the one verified")
	body, err := io.ReadAll(rsp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(body), testKey)
	require.NotContains(t, string(body), "sk-from-the-header")
	require.Contains(t, f.CallLog(), "SetProviderAPIKey:global:anthropic:key")
}

func TestAPIKeyWithShellExpressionIsRefused(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"$(touch /tmp/crush-api-key-pwned)", "sk-`id`"} {
		var testCalls atomic.Int64
		g, f, ws := signInGatewayWithWorkspace(t, nil, func(context.Context, catwalk.Provider, string, config.VariableResolver) error {
			testCalls.Add(1)
			return nil
		})
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
			`{"provider":"anthropic","api_key":"`+key+`"}`)
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, key)
		require.Contains(t, string(body), "bad_api_key")
		require.NotContains(t, string(body), key)
		require.Zero(t, testCalls.Load(), "a shell expression reaches no provider check")
		require.Zero(t, ws.resolverCalls.Load(), "a shell expression reaches no resolver")
		require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
		rsp.Body.Close()
	}
}

func TestAPIKeyIsVerifiedLiterally(t *testing.T) {
	t.Parallel()
	got := make(chan keyVerification, 1)
	g, f, ws := signInGatewayWithWorkspace(t, nil, func(_ context.Context, _ catwalk.Provider, key string, r config.VariableResolver) error {
		resolved, _ := r.ResolveValue("$(echo hi)")
		got <- keyVerification{key: key, resolved: resolved}
		return nil
	})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"anthropic","api_key":"  `+testKey+`  "}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.NotContains(t, string(body), testKey)
	v := <-got
	require.Equal(t, testKey, v.key)
	require.Equal(t, "$(echo hi)", v.resolved, "the provider check resolves nothing")
	require.Zero(t, ws.resolverCalls.Load(), "the route uses the identity resolver")
	require.Contains(t, f.CallLog(), "SetProviderAPIKey:global:anthropic:key")
}

// endpointRequest is what the test provider server saw.
type endpointRequest struct {
	path string
	key  string
}

// TestDefaultAPIKeyCheckResolvesTheEndpoint exercises the real default
// TestAPIKey against a local provider. The catalog's api_endpoint is a
// shell template, as catwalk's entries are, so the check must resolve it
// with the workspace resolver before TestConnection builds its URL.
//
// t.Setenv forbids t.Parallel.
func TestDefaultAPIKeyCheckResolvesTheEndpoint(t *testing.T) {
	requests := make(chan endpointRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- endpointRequest{path: r.URL.Path, key: r.Header.Get("x-api-key")}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("X_ENDPOINT", srv.URL)

	fake := webtest.NewFake()
	ws := &countingWorkspace{Fake: fake, resolver: config.NewShellVariableResolver(env.New())}
	g, err := Start(t.Context(), Options{
		Workspace: ws,
		KnownProviders: func(*config.Config) ([]catwalk.Provider, error) {
			return []catwalk.Provider{{
				ID:          catwalk.InferenceProviderAnthropic,
				Name:        "Anthropic",
				Type:        catwalk.TypeMessages,
				APIEndpoint: "$X_ENDPOINT",
			}}, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"anthropic","api_key":"`+testKey+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	req := <-requests
	require.Equal(t, "/v1/models", req.path)
	require.Equal(t, testKey, req.key, "the provider receives the key as typed")
	require.Contains(t, fake.CallLog(), "SetProviderAPIKey:global:anthropic:key")
}

// TestProviderTestConfigFallsBackForAnUnsetEndpoint pins that a catalog
// endpoint whose variable is unset leaves BaseURL empty, which is how
// TestConnection reaches the provider's own default URL. No network runs.
func TestProviderTestConfigFallsBackForAnUnsetEndpoint(t *testing.T) {
	t.Parallel()
	resolver := config.NewShellVariableResolver(env.NewFromMap(nil))
	pc := providerTestConfig(catwalk.Provider{
		ID:          catwalk.InferenceProviderAnthropic,
		Name:        "Anthropic",
		Type:        catwalk.TypeMessages,
		APIEndpoint: "$CRUSH_UNSET_ENDPOINT",
	}, "sk-literal", resolver)
	require.Empty(t, pc.BaseURL)
	require.Equal(t, "sk-literal", pc.APIKey)
}

func TestAPIKeySignInWhenCatalogUnavailable(t *testing.T) {
	t.Parallel()
	g, f := startWith(t, Options{
		KnownProviders: func(*config.Config) ([]catwalk.Provider, error) {
			return nil, errors.New("catalog down")
		},
		TestAPIKey: acceptOnlyValidKey,
	})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"anthropic","api_key":"`+testKey+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadGateway, rsp.StatusCode)
	require.Contains(t, string(body), "providers_unavailable")
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
}
