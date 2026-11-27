package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/env"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/coder/websocket"
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
	snap := snapshotJSON(t, g, "")
	for _, k := range keys {
		require.NotContains(t, snap, k)
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

// TestProviderTestConfigIgnoresATemplatedEndpoint pins the client/server
// case: the identity resolver leaves $VAR untouched, so a templated
// endpoint must not become the BaseURL the key is tested against. No
// network runs.
func TestProviderTestConfigIgnoresATemplatedEndpoint(t *testing.T) {
	t.Parallel()
	pc := providerTestConfig(catwalk.Provider{
		ID:          catwalk.InferenceProviderOpenAI,
		Name:        "OpenAI",
		Type:        catwalk.TypeMessages,
		APIEndpoint: "$OPENAI_API_ENDPOINT",
	}, "sk-literal", config.IdentityResolver())
	require.Empty(t, pc.BaseURL, "a templated endpoint falls back to the provider default")
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

// signInGatewayWithFlows starts a gateway whose sign-in flows come from
// next, one call per start.
func signInGatewayWithFlows(t *testing.T, next func() signin.Flow) (*Gateway, *webtest.Fake) {
	t.Helper()
	g, f := startWith(t, Options{
		KnownProviders: testKnownProviders, TestAPIKey: acceptOnlyValidKey,
		SignInFlow: func(string) (signin.Flow, error) { return next(), nil },
	})
	known, _ := testKnownProviders(nil)
	f.SetCatalog(known)
	return g, f
}

// signInGatewayWithFlow starts a gateway that runs f for every start.
func signInGatewayWithFlow(t *testing.T, f *webtest.FakeFlow) (*Gateway, *webtest.Fake) {
	t.Helper()
	return signInGatewayWithFlows(t, func() signin.Flow { return f })
}

// frameUntil reads signin frames on the test goroutine until one contains
// want, failing at the deadline.
func frameUntil(t *testing.T, c *websocket.Conn, want string) testFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		fr := nextFrameOfType(ctx, t, c, frameSignIn)
		if strings.Contains(string(fr.Payload), want) {
			return fr
		}
	}
}

// waitFor blocks until done closes, failing at the deadline. A bare
// receive hangs the whole test binary when the code under test never gets
// there.
func waitFor(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// waitSignInWG waits for the gateway's sign-in goroutines, failing at the
// deadline instead of hanging the test binary.
func waitSignInWG(t *testing.T, g *Gateway) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		g.signinWG.Wait()
		close(done)
	}()
	waitFor(t, done, "the sign-in goroutines")
}

// statusUntil polls the status route on the test goroutine.
func statusUntil(t *testing.T, c *http.Client, g *Gateway, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rsp, s := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
		rsp.Body.Close()
		if strings.Contains(string(s), want) {
			return
		}
		require.True(t, time.Now().Before(deadline), "status never reached %s: %s", want, s)
		time.Sleep(20 * time.Millisecond)
	}
}

// startSignIn starts a flow and returns its state.
func startSignIn(t *testing.T, c *http.Client, g *Gateway, provider string) SignInState {
	t.Helper()
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"`+provider+`"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var st SignInState
	require.NoError(t, json.Unmarshal(body, &st))
	return st
}

// submitCode sends a pasted code for a flow.
func submitCode(t *testing.T, c *http.Client, g *Gateway, flowID, code string) (*http.Response, []byte) {
	t.Helper()
	return doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/code",
		`{"flow_id":"`+flowID+`","code":"`+code+`"}`)
}

func TestSignInDeviceFlowCompletes(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.FlowName = "GitHub Copilot"
	flow.Challenge = signin.Challenge{
		DeviceCode: "dc", UserCode: "WDJB-MJHT",
		VerificationURL: "https://github.com/login/device", ExpiresIn: 900,
	}
	flow.Token = &oauth.Token{AccessToken: "gho_secret"}
	g, f := signInGatewayWithFlow(t, flow)
	c := dialStream(t, g, "")
	_ = readFrame(t, c)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, string(body), `"state":"waiting","kind":"device","user_code":"WDJB-MJHT"`)
	require.NotContains(t, string(body), `"dc"`, "the device code stays in the process")
	_ = frameUntil(t, c, `"state":"waiting"`)
	flow.Finish()
	_ = frameUntil(t, c, `"state":"done"`)
	require.Contains(t, f.CallLog(), "SetProviderAPIKey:global:copilot:oauth")
	for _, fr := range g.hub.frames() {
		require.NotContains(t, string(fr.Payload), "gho_secret")
	}
}

func TestSignInStartReplacesFlow(t *testing.T) {
	t.Parallel()
	first := webtest.NewFakeFlow(t)
	first.Challenge = signin.Challenge{UserCode: "ONE"}
	second := webtest.NewFakeFlow(t)
	second.Challenge = signin.Challenge{UserCode: "TWO"}
	var mu sync.Mutex
	flows := []*webtest.FakeFlow{first, second}
	g, _ := signInGatewayWithFlows(t, func() signin.Flow {
		mu.Lock()
		defer mu.Unlock()
		f := flows[0]
		flows = flows[1:]
		return f
	})
	c := authedClient(t, g)
	rsp, _ := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Contains(t, string(body), `"user_code":"TWO"`)
	require.Eventually(t, first.Closed, time.Second, 10*time.Millisecond, "the replaced flow frees its listener")
	statusUntil(t, c, g, `"user_code":"TWO"`)
}

func TestSignInCancel(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1455/start"}
	g, f := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"openai"}`)
	rsp.Body.Close()
	var st SignInState
	require.NoError(t, json.Unmarshal(body, &st))
	require.Equal(t, "browser", st.Kind)
	rsp, _ = doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/cancel", `{"flow_id":"`+st.FlowID+`"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.True(t, flow.Closed())
	statusUntil(t, c, g, `"state":"cancelled"`)
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.NotContains(t, string(status), `"error":`, "a cancelled state carries no sentence")
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
}

func TestSignInCancelledOnShutdown(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "U"}
	fake := webtest.NewFake()
	g, err := Start(t.Context(), Options{
		Workspace: fake, KnownProviders: testKnownProviders,
		SignInFlow: func(string) (signin.Flow, error) { return flow, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	rsp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, g.Shutdown(ctx))
	require.True(t, flow.Closed())
}

func TestSignInFallbackShowsTheNextChallenge(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	flow.Next = &signin.Challenge{DeviceCode: "dc", UserCode: "NEXT-CODE", VerificationURL: "https://x.ai/device"}
	g, _ := signInGatewayWithFlow(t, flow)
	c := dialStream(t, g, "")
	_ = readFrame(t, c)
	rsp, _ := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"xai"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	_ = frameUntil(t, c, `"kind":"device","user_code":"NEXT-CODE"`)
}

func TestSignInStartFailureHidesProviderText(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.StartErr = errors.New("upstream said: client_secret=abc")
	g, _ := signInGatewayWithFlow(t, flow)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadGateway, rsp.StatusCode)
	require.Contains(t, string(body), "signin_start_failed")
	require.NotContains(t, string(body), "client_secret")
}

func TestSignInImportsCopilot(t *testing.T) {
	t.Parallel()
	var called atomic.Bool
	g, f := signInGatewayWithFlows(t, func() signin.Flow { called.Store(true); return webtest.NewFakeFlow(t) })
	f.SetCopilotImport(&oauth.Token{AccessToken: "imported"})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	rsp.Body.Close()
	require.Contains(t, string(body), `"state":"done"`)
	require.Contains(t, string(body), `"imported":true`)
	require.False(t, called.Load(), "no flow starts when an existing login was imported")
}

func TestSignInRefusesAPIKeyOnlyProvider(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"anthropic"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "oauth_not_supported")
}

// TestSignInLogsOnlyTheErrorClass is not parallel: captureLogs replaces
// the logger.
func TestSignInLogsOnlyTheErrorClass(t *testing.T) {
	logs := captureLogs(t)
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "U"}
	flow.WaitErr = errors.New(`poll failed: {"error":"bad","body":"provider-body-7731"}`)
	g, _ := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	rsp, _ := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	rsp.Body.Close()
	flow.Finish()
	statusUntil(t, c, g, `"state":"error"`)
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), "The sign-in did not finish. Try again.")
	require.NotContains(t, string(status), "provider-body-7731")
	require.NotContains(t, logs.String(), "provider-body-7731")
	require.Contains(t, logs.String(), "reason=provider")
	require.True(t, flow.Closed(), "a failed wait frees the flow's listener")
}

// TestSignInCodeNeverLogged is not parallel: captureLogs replaces the logger.
func TestSignInCodeNeverLogged(t *testing.T) {
	logs := captureLogs(t)
	const code = "ac_9f8e7d6c5b4a"
	flow := webtest.NewFakeFlow(t)
	flow.HasCodeEntry = true
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	flow.CodeOK = "something-else"
	g, _ := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"xai"}`)
	rsp.Body.Close()
	var st SignInState
	require.NoError(t, json.Unmarshal(body, &st))
	require.True(t, st.CodeEntry)
	rsp, body = submitCode(t, c, g, st.FlowID, code)
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	require.NotContains(t, string(body), code)
	statusUntil(t, c, g, `"state":"error"`)
	rsp, body = doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/code?code="+code, `{"flow_id":"x"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.NotContains(t, string(body), code)
	for _, fr := range g.hub.frames() {
		require.NotContains(t, string(fr.Payload), code)
	}
	require.NotContains(t, logs.String(), code)
}

// TestSignInCodeCompletesABrowserFlow pins the code path from the tab to a
// saved token. The token never reaches an answer or a frame.
func TestSignInCodeCompletesABrowserFlow(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.HasCodeEntry = true
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	flow.CodeOK = "good-code"
	flow.Token = &oauth.Token{AccessToken: "tok-from-code"}
	g, f := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "xai")
	rsp, body := submitCode(t, c, g, st.FlowID, "good-code")
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	require.NotContains(t, string(body), "tok-from-code")
	statusUntil(t, c, g, `"state":"done"`)
	require.Contains(t, f.CallLog(), "SetProviderAPIKey:global:xai:oauth")
	for _, fr := range g.hub.frames() {
		require.NotContains(t, string(fr.Payload), "tok-from-code")
	}
}

// TestSignInGrokDeviceStartRefusesACode pins Ruling A4-a: Grok's flow
// reports code entry as a capability, so the gateway gates a code on the
// challenge in force. A device start shows a user code, not a paste field.
func TestSignInGrokDeviceStartRefusesACode(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.FlowName = "Grok"
	flow.HasCodeEntry = true
	flow.Challenge = signin.Challenge{
		DeviceCode: "dc", UserCode: "GROK-CODE",
		VerificationURL: "https://accounts.x.ai/device",
	}
	g, _ := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "xai")
	require.Equal(t, "device", st.Kind)
	require.False(t, st.CodeEntry)
	rsp, body := submitCode(t, c, g, st.FlowID, "GROK-CODE")
	rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode)
	require.Contains(t, string(body), "code_entry_unavailable")
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"state":"waiting"`)
	require.NotContains(t, string(status), "That code did not work")
}

// TestSignInDeviceFallbackRefusesACode pins that a device challenge the
// flow falls back to takes no pasted code, even though the flow reports
// code entry as a capability.
func TestSignInDeviceFallbackRefusesACode(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.HasCodeEntry = true
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	flow.Next = &signin.Challenge{DeviceCode: "dc", UserCode: "NEXT-CODE", VerificationURL: "https://x.ai/device"}
	g, _ := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "xai")
	require.True(t, st.CodeEntry, "a browser challenge takes a pasted code")
	statusUntil(t, c, g, `"kind":"device","user_code":"NEXT-CODE"`)
	rsp, body := submitCode(t, c, g, st.FlowID, "some-code")
	rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode)
	require.Contains(t, string(body), "code_entry_unavailable")
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"state":"waiting"`)
	require.NotContains(t, string(status), "That code did not work")
}

// TestSignInCodeHeaderIsIgnored is not parallel: captureLogs replaces the
// logger. A code in a header is never read; the body code is the one the
// flow sees.
func TestSignInCodeHeaderIsIgnored(t *testing.T) {
	logs := captureLogs(t)
	const headerCode = "hdr-code-7731"
	flow := webtest.NewFakeFlow(t)
	flow.HasCodeEntry = true
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	flow.CodeOK = "body-code"
	flow.Token = &oauth.Token{AccessToken: "tok-from-body"}
	g, _ := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "xai")
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, g.URL()+"/api/auth/provider/oauth/code",
		strings.NewReader(`{"flow_id":"`+st.FlowID+`","code":"body-code"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", g.URL())
	req.Header.Set("X-Code", headerCode)
	rsp := doReq(t, c, req)
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	rsp.Body.Close()
	statusUntil(t, c, g, `"state":"done"`)
	require.NotContains(t, logs.String(), headerCode)
	for _, fr := range g.hub.frames() {
		require.NotContains(t, string(fr.Payload), headerCode)
	}
}

func TestSignInStatusWithoutAFlow(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "no_flow")
}

// TestSignInStatusKeepsTheLastState pins that a finished sign-in stays
// readable, so a tab that loads later still shows the outcome.
func TestSignInStatusKeepsTheLastState(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "LAST"}
	flow.Token = &oauth.Token{AccessToken: "tok-last"}
	g, _ := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "copilot")
	flow.Finish()
	statusUntil(t, c, g, `"state":"done"`)
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"flow_id":"`+st.FlowID+`"`)
	// A finished flow is not running, so there is nothing to cancel.
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/cancel", `{"flow_id":"`+st.FlowID+`"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "no_flow")
}

// TestSignInSnapshotCarriesTheState pins the snapshot field a reloading tab
// reads.
func TestSignInSnapshotCarriesTheState(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "SNAP"}
	g, _ := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "copilot")
	require.Contains(t, snapshotJSON(t, g, ""), `"signin":{"flow_id":"`+st.FlowID+`"`)
}

func TestSignInCancelUnknownFlow(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/cancel", `{"flow_id":"nope"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "no_flow")
}

func TestSignInStartDuringShutdown(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	g.signinMu.Lock()
	g.signinClosed = true
	g.signinMu.Unlock()
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode)
	require.Contains(t, string(body), "shutting_down")
}

func TestSignInCodeDuringShutdown(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.HasCodeEntry = true
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	g, _ := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "xai")
	g.signinMu.Lock()
	g.signinClosed = true
	g.signinMu.Unlock()
	rsp, body := submitCode(t, c, g, st.FlowID, "late-code")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode)
	require.Contains(t, string(body), "shutting_down")
}

func TestSignInStartUnknownProvider(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"nobody"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode)
	require.Contains(t, string(body), "unknown_provider")
}

func TestSignInStartBadModelType(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start",
		`{"provider":"copilot","model":{"type":"huge","model":"claude"}}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
	require.Contains(t, string(body), "bad_type")
}

// TestSignInBusyCallbackPort pins the 409 a second browser flow on a busy
// port answers: the first one holds 1455, so the tab is told to finish or
// cancel it.
func TestSignInBusyCallbackPort(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.StartErr = fmt.Errorf("listen tcp 127.0.0.1:1455: %w", syscall.EADDRINUSE)
	g, _ := signInGatewayWithFlow(t, flow)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"openai"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode)
	require.Contains(t, string(body), "signin_busy")
	require.Contains(t, string(body), "Another sign-in is using the callback port. Finish or cancel it first.")
	require.True(t, flow.Closed(), "a refused start frees its listener")
}

// TestSignInSaveFailureShowsTheSentence pins the fixed save sentence, never
// the workspace error.
func TestSignInSaveFailureShowsTheSentence(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "U"}
	flow.Token = &oauth.Token{AccessToken: "tok-save"}
	g, f := signInGatewayWithFlow(t, flow)
	f.SetProviderKeyErr(errors.New("the config could not be written: /home/u/.config/crush/crush.json"))
	c := authedClient(t, g)
	startSignIn(t, c, g, "copilot")
	flow.Finish()
	statusUntil(t, c, g, `"state":"error"`)
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), "Crush could not save the sign-in.")
	require.NotContains(t, string(status), "the config could not be written")
	require.True(t, flow.Closed(), "a failed save frees the flow's listener")
	require.ErrorIs(t, runContext(t, g).Err(), context.Canceled, "a failed save cancels the run")
}

// runContext returns the current run's context.
func runContext(t *testing.T, g *Gateway) context.Context {
	t.Helper()
	g.signinMu.Lock()
	defer g.signinMu.Unlock()
	require.NotNil(t, g.signin)
	return g.signin.ctx
}

// TestSignInDoneCancelsTheRun pins that a finished sign-in cancels its
// run's context, so a flow still polling after the token is freed.
func TestSignInDoneCancelsTheRun(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "U"}
	flow.Token = &oauth.Token{AccessToken: "tok-done"}
	g, _ := signInGatewayWithFlow(t, flow)
	c := authedClient(t, g)
	startSignIn(t, c, g, "copilot")
	flow.Finish()
	statusUntil(t, c, g, `"state":"done"`)
	require.ErrorIs(t, runContext(t, g).Err(), context.Canceled, "a done run cancels its context")
}

func TestSignInErrorClass(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"canceled", context.Canceled, "canceled"},
		{"timeout", context.DeadlineExceeded, "timeout"},
		{"port_busy", fmt.Errorf("listen tcp 127.0.0.1:1455: %w", syscall.EADDRINUSE), "port_busy"},
		{"network", &net.DNSError{Err: "no such host", Name: "accounts.x.ai"}, "network"},
		{"provider", errors.New(`upstream said: {"error":"bad"}`), "provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, signInErrorClass(tc.err))
		})
	}
}

// TestSignInOAuthAppliesTheChosenModel pins that a start's model switches
// once the token is saved, and that the done state says so.
func TestSignInOAuthAppliesTheChosenModel(t *testing.T) {
	t.Parallel()
	known := []catwalk.Provider{
		{ID: catwalk.InferenceProviderXAI, Name: "xAI", Models: []catwalk.Model{{ID: "grok-4", Name: "Grok 4"}}},
	}
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "U"}
	flow.Token = &oauth.Token{AccessToken: "tok-model"}
	fake := webtest.NewFake()
	g, err := Start(t.Context(), Options{
		Workspace: fake,
		KnownProviders: func(*config.Config) ([]catwalk.Provider, error) {
			return known, nil
		},
		SignInFlow: func(string) (signin.Flow, error) { return flow, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	fake.SetCatalog(known)
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start",
		`{"provider":"xai","model":{"type":"main","model":"grok-4"}}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	flow.Finish()
	statusUntil(t, c, g, `"state":"done"`)
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"model_applied":true`)
	require.Contains(t, fake.CallLog(), "UpdatePreferredModel:global:main:xai/grok-4")
}

// stuckFlow is a sign-in flow whose Wait ignores the context, the way a
// provider poll already in flight does: only Close releases the token. A
// cancel or a replacement therefore still delivers it, which is the race
// Ruling A6-b item 3 guards against.
type stuckFlow struct {
	*webtest.FakeFlow
	token    *oauth.Token
	closed   chan struct{}
	returned chan struct{}
	once     sync.Once
}

func newStuckFlow(t *testing.T, tok *oauth.Token) *stuckFlow {
	t.Helper()
	return &stuckFlow{
		FakeFlow: webtest.NewFakeFlow(t),
		token:    tok,
		closed:   make(chan struct{}),
		returned: make(chan struct{}),
	}
}

// Close frees the wait, so no test leaves a goroutine blocked.
func (f *stuckFlow) Close() {
	f.FakeFlow.Close()
	f.once.Do(func() { close(f.closed) })
}

// Wait ignores ctx and delivers its token once the flow is closed. It
// closes returned first, so a test knows the token is on its way.
func (f *stuckFlow) Wait(context.Context, signin.Challenge) (*oauth.Token, *signin.Challenge, error) {
	<-f.closed
	close(f.returned)
	return f.token, nil, nil
}

// hookWorkspace lets a test intervene while a token is saved.
type hookWorkspace struct {
	*webtest.Fake
	// onSave runs when a save starts, on the sign-in goroutine.
	onSave func()
	// onModel runs when the model step starts, on the sign-in goroutine.
	onModel func(context.Context)
	// saving closes when the first save starts.
	saving chan struct{}
	// release, when set, holds a save until the test releases it.
	release     chan struct{}
	releaseOnce sync.Once
	save        sync.Once
}

func newHookWorkspace() *hookWorkspace {
	return &hookWorkspace{Fake: webtest.NewFake(), saving: make(chan struct{})}
}

// hold makes the next save wait until release, and releases it at cleanup
// so a failed test cannot leave Shutdown waiting on the save.
func (w *hookWorkspace) hold(t *testing.T) {
	t.Helper()
	w.release = make(chan struct{})
	t.Cleanup(func() {
		w.releaseOnce.Do(func() { close(w.release) })
	})
}

// releaseSave lets a held save finish.
func (w *hookWorkspace) releaseSave() {
	w.releaseOnce.Do(func() {
		if w.release != nil {
			close(w.release)
		}
	})
}

func (w *hookWorkspace) SetProviderAPIKey(scope config.Scope, providerID string, apiKey any) error {
	w.save.Do(func() { close(w.saving) })
	if w.onSave != nil {
		w.onSave()
	}
	if w.release != nil {
		<-w.release
	}
	return w.Fake.SetProviderAPIKey(scope, providerID, apiKey)
}

// UpdateAgentModel refuses a cancelled context, the way the real rebuild
// does, so a test can see which context the model step used.
func (w *hookWorkspace) UpdateAgentModel(ctx context.Context) error {
	if w.onModel != nil {
		w.onModel(ctx)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.Fake.UpdateAgentModel(ctx)
}

// startSignInOn starts a gateway over ws that runs flow for every
// sign-in.
func startSignInOn(t *testing.T, ws workspace.Workspace, flow signin.Flow) *Gateway {
	t.Helper()
	g, err := Start(t.Context(), Options{
		Workspace: ws, KnownProviders: testKnownProviders, TestAPIKey: acceptOnlyValidKey,
		SignInFlow: func(string) (signin.Flow, error) { return flow, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	return g
}

// statusState returns the phase the status route reports.
func statusState(t *testing.T, c *http.Client, g *Gateway) string {
	t.Helper()
	rsp, body := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	var st SignInState
	require.NoError(t, json.Unmarshal(body, &st))
	return st.State
}

// TestSignInRefusedCodeEndsTheRun pins Ruling A6-b item 1: a refused code
// stops the run and frees its listener, and a token the flow was still
// going to deliver neither saves nor changes the error state.
func TestSignInRefusedCodeEndsTheRun(t *testing.T) {
	t.Parallel()
	flow := newStuckFlow(t, &oauth.Token{AccessToken: "late-token"})
	flow.HasCodeEntry = true
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	flow.CodeOK = "right-code"
	g, f := signInGatewayWithFlows(t, func() signin.Flow { return flow })
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "xai")
	rsp, body := submitCode(t, c, g, st.FlowID, "wrong-code")
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	require.NotContains(t, string(body), "wrong-code")
	statusUntil(t, c, g, `"state":"error"`)
	require.True(t, flow.Closed(), "a refused code ends the run and frees its listener")
	waitSignInWG(t, g)
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"state":"error"`)
	require.Contains(t, string(status), "That code did not work")
	require.NotContains(t, string(status), `"state":"done"`)
}

// TestSignInCancelledRunNeverSaves pins Ruling A6-b item 3: a token that
// arrives after a cancel is not saved and does not overwrite the cancelled
// state, even though the flow's Wait ignored the cancelled context.
func TestSignInCancelledRunNeverSaves(t *testing.T) {
	t.Parallel()
	flow := newStuckFlow(t, &oauth.Token{AccessToken: "late-token"})
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	g, f := signInGatewayWithFlows(t, func() signin.Flow { return flow })
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "openai")
	rsp, _ := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/cancel", `{"flow_id":"`+st.FlowID+`"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusNoContent, rsp.StatusCode)
	require.True(t, flow.Closed())
	waitSignInWG(t, g)
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"state":"cancelled"`)
}

// TestSignInReplacedRunNeverSaves pins Ruling A6-b item 3 for a replaced
// run: its late token is not saved and its phases never overwrite the new
// run's state.
func TestSignInReplacedRunNeverSaves(t *testing.T) {
	t.Parallel()
	first := newStuckFlow(t, &oauth.Token{AccessToken: "late-token"})
	first.Challenge = signin.Challenge{UserCode: "ONE"}
	second := webtest.NewFakeFlow(t)
	second.Challenge = signin.Challenge{UserCode: "TWO"}
	var mu sync.Mutex
	flows := []signin.Flow{first, second}
	g, f := signInGatewayWithFlows(t, func() signin.Flow {
		mu.Lock()
		defer mu.Unlock()
		fl := flows[0]
		flows = flows[1:]
		return fl
	})
	c := authedClient(t, g)
	startSignIn(t, c, g, "copilot")
	newState := startSignIn(t, c, g, "copilot")
	require.True(t, first.Closed(), "the replaced flow frees its listener")
	waitFor(t, first.returned, "the replaced run's wait")
	// The new run is still waiting, so only a window can show that the
	// replaced run saved nothing. CallLog is safe to read off the test
	// goroutine.
	require.Never(t, func() bool {
		return strings.Contains(strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
	}, 250*time.Millisecond, 10*time.Millisecond)
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"flow_id":"`+newState.FlowID+`"`)
	require.Contains(t, string(status), `"user_code":"TWO"`)
}

// TestSignInImportStopsTheRunningFlow pins Ruling A6-b item 2: an imported
// Copilot login replaces the running flow, as any start does.
func TestSignInImportStopsTheRunningFlow(t *testing.T) {
	t.Parallel()
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	g, f := signInGatewayWithFlow(t, flow)
	f.SetCopilotImport(&oauth.Token{AccessToken: "imported"})
	c := authedClient(t, g)
	startSignIn(t, c, g, "xai")
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.Contains(t, string(body), `"imported":true`)
	require.True(t, flow.Closed(), "an imported login stops the running flow")
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"imported":true`)
	require.Contains(t, string(status), `"state":"done"`)
}

// TestSignInImportIsRecorded pins Ruling A6-b item 2's record: status, the
// snapshot and a frame all answer the imported login, and the chosen model
// is applied.
func TestSignInImportIsRecorded(t *testing.T) {
	t.Parallel()
	known := []catwalk.Provider{{
		ID: catwalk.InferenceProviderCopilot, Name: "GitHub Copilot",
		Models: []catwalk.Model{{ID: "gpt-4o", Name: "GPT-4o"}},
	}}
	fake := webtest.NewFake()
	g, err := Start(t.Context(), Options{
		Workspace:      fake,
		KnownProviders: func(*config.Config) ([]catwalk.Provider, error) { return known, nil },
		TestAPIKey:     acceptOnlyValidKey,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	fake.SetCatalog(known)
	fake.SetCopilotImport(&oauth.Token{AccessToken: "imported"})
	c := authedClient(t, g)
	stream := dialStream(t, g, "")
	_ = readFrame(t, stream)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start",
		`{"provider":"copilot","model":{"type":"main","model":"gpt-4o"}}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var st SignInState
	require.NoError(t, json.Unmarshal(body, &st))
	require.Equal(t, "done", st.State)
	require.True(t, st.Imported)
	require.True(t, st.ModelApplied)
	require.Contains(t, fake.CallLog(), "UpdatePreferredModel:global:main:copilot/gpt-4o")
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"flow_id":"`+st.FlowID+`"`)
	require.Contains(t, string(status), `"imported":true`)
	require.Contains(t, snapshotJSON(t, g, ""), `"signin":{"flow_id":"`+st.FlowID+`"`)
	frame := frameUntil(t, stream, `"imported":true`)
	var framed SignInState
	require.NoError(t, json.Unmarshal(frame.Payload, &framed))
	require.Equal(t, st.FlowID, framed.FlowID)
}

// TestSignInStartWhenCatalogUnavailable pins Ruling A6-b item 4's minor:
// start answers the same 502 as A5's key route when the catalog fails and
// nothing is known.
func TestSignInStartWhenCatalogUnavailable(t *testing.T) {
	t.Parallel()
	g, f := startWith(t, Options{
		KnownProviders: func(*config.Config) ([]catwalk.Provider, error) {
			return nil, errors.New("catalog down")
		},
		TestAPIKey: acceptOnlyValidKey,
	})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadGateway, rsp.StatusCode)
	require.Contains(t, string(body), "providers_unavailable")
	require.NotContains(t, strings.Join(f.CallLog(), "\n"), "SetProviderAPIKey")
}

// TestSignInCodeDuringSaving pins Ruling A6-b item 4's minor: the code
// gate requires a waiting run, so a save in flight takes no code.
func TestSignInCodeDuringSaving(t *testing.T) {
	t.Parallel()
	ws := newHookWorkspace()
	flow := webtest.NewFakeFlow(t)
	flow.HasCodeEntry = true
	flow.Challenge = signin.Challenge{VerificationURL: "http://localhost:1/start"}
	flow.CodeOK = "good-code"
	flow.Token = &oauth.Token{AccessToken: "tok-saving"}
	g := startSignInOn(t, ws, flow)
	ws.hold(t)
	c := authedClient(t, g)
	st := startSignIn(t, c, g, "xai")
	rsp, _ := submitCode(t, c, g, st.FlowID, "good-code")
	rsp.Body.Close()
	require.Equal(t, http.StatusAccepted, rsp.StatusCode)
	waitFor(t, ws.saving, "the save to start")
	require.Equal(t, "saving", statusState(t, c, g))
	rsp, body := submitCode(t, c, g, st.FlowID, "good-code")
	rsp.Body.Close()
	require.Equal(t, http.StatusConflict, rsp.StatusCode)
	require.Contains(t, string(body), "code_entry_unavailable")
	ws.releaseSave()
	statusUntil(t, c, g, `"state":"done"`)
}

// TestSignInModelStepSurvivesACancelDuringSaving pins Ruling A6-b item 3's
// model context: a cancel that lands while the token is saved does not fail
// the model step.
func TestSignInModelStepSurvivesACancelDuringSaving(t *testing.T) {
	t.Parallel()
	known := []catwalk.Provider{{
		ID: catwalk.InferenceProviderXAI, Name: "xAI",
		Models: []catwalk.Model{{ID: "grok-4", Name: "Grok 4"}},
	}}
	ws := newHookWorkspace()
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "U"}
	flow.Token = &oauth.Token{AccessToken: "tok-cancel-save"}
	g, err := Start(t.Context(), Options{
		Workspace:      ws,
		KnownProviders: func(*config.Config) ([]catwalk.Provider, error) { return known, nil },
		SignInFlow:     func(string) (signin.Flow, error) { return flow, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	ws.SetCatalog(known)
	ws.onSave = func() {
		g.signinMu.Lock()
		run := g.signin
		g.signinMu.Unlock()
		if run != nil {
			run.cancel()
		}
	}
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start",
		`{"provider":"xai","model":{"type":"main","model":"grok-4"}}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	flow.Finish()
	statusUntil(t, c, g, `"state":"done"`)
	rsp, status := doJSON(t, c, http.MethodGet, g.URL()+"/api/auth/provider/oauth/status", "")
	rsp.Body.Close()
	require.Contains(t, string(status), `"model_applied":true`)
	require.Contains(t, ws.CallLog(), "UpdatePreferredModel:global:main:xai/grok-4")
}

// TestSignInExpiresAtUsesTheGatewayClock pins the clock seam: a device
// code's expiry is measured from Options.Now, so webfake can date a flow
// from WEBFAKE_CLOCK.
func TestSignInExpiresAtUsesTheGatewayClock(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "U", ExpiresIn: 900}
	fake := webtest.NewFake()
	g, err := Start(t.Context(), Options{
		Workspace:      fake,
		KnownProviders: testKnownProviders,
		TestAPIKey:     acceptOnlyValidKey,
		SignInFlow:     func(string) (signin.Flow, error) { return flow, nil },
		Now:            func() time.Time { return fixed },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	known, _ := testKnownProviders(nil)
	fake.SetCatalog(known)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var st SignInState
	require.NoError(t, json.Unmarshal(body, &st))
	require.Equal(t, fixed.Unix()+900, st.ExpiresAt)
}

// modelCall is what the model step saw of its context.
type modelCall struct {
	deadline time.Time
	ok       bool
}

// TestSignInModelStepHasADeadline pins that the switch a completed sign-in
// makes cannot hang the run: it is detached from the run's context, so it
// carries a deadline of its own.
func TestSignInModelStepHasADeadline(t *testing.T) {
	t.Parallel()
	known := []catwalk.Provider{{
		ID: catwalk.InferenceProviderXAI, Name: "xAI",
		Models: []catwalk.Model{{ID: "grok-4", Name: "Grok 4"}},
	}}
	ws := newHookWorkspace()
	calls := make(chan modelCall, 1)
	ws.onModel = func(ctx context.Context) {
		deadline, ok := ctx.Deadline()
		calls <- modelCall{deadline: deadline, ok: ok}
	}
	flow := webtest.NewFakeFlow(t)
	flow.Challenge = signin.Challenge{UserCode: "U"}
	flow.Token = &oauth.Token{AccessToken: "tok-deadline"}
	g, err := Start(t.Context(), Options{
		Workspace:      ws,
		KnownProviders: func(*config.Config) ([]catwalk.Provider, error) { return known, nil },
		SignInFlow:     func(string) (signin.Flow, error) { return flow, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	ws.SetCatalog(known)
	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodPost, g.URL()+"/api/auth/provider/oauth/start",
		`{"provider":"xai","model":{"type":"main","model":"grok-4"}}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	flow.Finish()
	statusUntil(t, c, g, `"state":"done"`)
	select {
	case call := <-calls:
		require.True(t, call.ok, "the model step has a deadline")
		require.WithinDuration(t, time.Now().Add(30*time.Second), call.deadline, 5*time.Second)
	case <-time.After(2 * time.Second):
		t.Fatal("the model step never ran")
	}
}

// TestSignInCatalogFailureLogsTheErrorClass is not parallel: captureLogs
// replaces the logger. A catalog failure logs its class, never the error,
// whose text can carry a provider URL.
func TestSignInCatalogFailureLogsTheErrorClass(t *testing.T) {
	logs := captureLogs(t)
	g, _ := startWith(t, Options{
		KnownProviders: func(*config.Config) ([]catwalk.Provider, error) {
			return nil, errors.New("catalog down: GET https://catalog.example/v1 failed")
		},
		TestAPIKey: acceptOnlyValidKey,
	})
	c := authedClient(t, g)
	for _, req := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/auth/providers", ""},
		{http.MethodPost, "/api/auth/provider/key", `{"provider":"anthropic","api_key":"` + testKey + `"}`},
		{http.MethodPost, "/api/auth/provider/oauth/start", `{"provider":"copilot"}`},
	} {
		rsp, _ := doJSON(t, c, req.method, g.URL()+req.path, req.body)
		rsp.Body.Close()
		require.Equal(t, http.StatusBadGateway, rsp.StatusCode, req.path)
	}
	require.Contains(t, logs.String(), "reason=provider")
	require.NotContains(t, logs.String(), "catalog.example")
}

// endpointResolver resolves every catalog endpoint to one value, the way a
// catalog entry's api_endpoint names the provider's address.
type endpointResolver string

func (r endpointResolver) ResolveValue(string) (string, error) { return string(r), nil }

// TestTestAPIKeyHonoursItsContext pins that the route's deadline reaches
// the provider check: a provider that never answers returns the deadline
// error instead of holding the request for TestConnection's own 5 s limit.
func TestTestAPIKeyHonoursItsContext(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	// Registered after the server's cleanup, so it runs first and the
	// close below does not wait on the held handler.
	t.Cleanup(unblock)

	p := catwalk.Provider{
		ID:          catwalk.InferenceProviderAnthropic,
		Name:        "Anthropic",
		Type:        catwalk.TypeMessages,
		APIEndpoint: srv.URL,
	}
	resolver := endpointResolver(srv.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()
	err := testAPIKey(ctx, p, "sk-test", resolver, resolver)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 2*time.Second, "the check honoured the 50 ms deadline")
}

// logLine returns the first captured log line containing msg.
func logLine(t *testing.T, logs, msg string) string {
	t.Helper()
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, msg) {
			return line
		}
	}
	t.Fatalf("no log line with %q in:\n%s", msg, logs)
	return ""
}

// TestAPIKeyRejectionLogNamesTheClass is not parallel: captureLogs replaces
// the logger. The rejected-key line names the provider and the failure
// class, and carries neither the key nor the provider's error text.
func TestAPIKeyRejectionLogNamesTheClass(t *testing.T) {
	logs := captureLogs(t)
	g, _ := signInGateway(t)
	const key = "sk-wrong-7d1"
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/key",
		`{"provider":"anthropic","api_key":"`+key+`"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusUnprocessableEntity, rsp.StatusCode, string(body))

	line := logLine(t, logs.String(), "Web sign-in key rejected")
	require.Contains(t, line, "provider=anthropic")
	require.Contains(t, line, "reason=provider")
	require.NotContains(t, line, key)
	require.NotContains(t, line, "Incorrect API key")
	require.NotContains(t, string(body), key)
}

// TestSignInStartFailureLogNamesTheClass is not parallel: captureLogs
// replaces the logger. The failed-start line names the provider and the
// failure class, and carries neither the provider's error text nor the
// answer it produced.
func TestSignInStartFailureLogNamesTheClass(t *testing.T) {
	logs := captureLogs(t)
	flow := webtest.NewFakeFlow(t)
	flow.StartErr = errors.New("upstream said: client_secret=abc")
	g, _ := signInGatewayWithFlow(t, flow)

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/auth/provider/oauth/start", `{"provider":"copilot"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusBadGateway, rsp.StatusCode, string(body))

	line := logLine(t, logs.String(), "Web sign-in did not start")
	require.Contains(t, line, "provider=copilot")
	require.Contains(t, line, "reason=provider")
	require.NotContains(t, line, "client_secret")
	require.NotContains(t, string(body), "client_secret")
}

// TestRecordImportedSignInAfterShutdown records nothing once the gateway
// is closing: nothing would publish the run it left behind.
func TestRecordImportedSignInAfterShutdown(t *testing.T) {
	t.Parallel()
	g, _ := signInGateway(t)
	require.NoError(t, g.Shutdown(context.Background()))
	require.Nil(t, g.signin)

	st, recorded := g.recordImportedSignIn(t.Context(), "copilot", "GitHub Copilot", nil)
	require.False(t, recorded)
	require.Equal(t, SignInState{}, st)
	require.Nil(t, g.signin, "a closing gateway records no run")
}
