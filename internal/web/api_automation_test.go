package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

func TestAutomationGoesThroughTheTerminal(t *testing.T) {
	t.Parallel()
	got := make(chan [3]string, 1)
	g, f := startWith(t, Options{OnAutomation: func(_ context.Context, sid, name, args string) (string, error) {
		got <- [3]string{sid, name, args}
		return "Goal set: Ship the parser", nil
	}})
	f.AddSession(session.Session{ID: "s1"})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/automation",
		`{"name":"goal","args":"Ship the parser --max-turns 5"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.JSONEq(t, `{"reply":"Goal set: Ship the parser"}`, string(body))
	require.Equal(t, [3]string{"s1", "goal", "Ship the parser --max-turns 5"}, <-got)
}

func TestAutomationRefusesUnknownNames(t *testing.T) {
	t.Parallel()
	g, f := startWith(t, Options{OnAutomation: func(context.Context, string, string, string) (string, error) { return "", nil }})
	f.AddSession(session.Session{ID: "s1"})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/automation", `{"name":"rm"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "bad_name")
}

func TestAutomationRefusesLongArgs(t *testing.T) {
	t.Parallel()
	g, f := startWith(t, Options{OnAutomation: func(context.Context, string, string, string) (string, error) { return "", nil }})
	f.AddSession(session.Session{ID: "s1"})
	body := `{"name":"goal","args":"` + strings.Repeat("x", 4097) + `"}`
	rsp, rspBody := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/automation", body)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(rspBody))
	require.Contains(t, string(rspBody), "args_too_long")
}

func TestAutomationUnknownSession(t *testing.T) {
	t.Parallel()
	called := false
	g, _ := startWith(t, Options{OnAutomation: func(context.Context, string, string, string) (string, error) {
		called = true
		return "", nil
	}})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/nope/automation", `{"name":"goal"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusNotFound, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "unknown_session")
	require.False(t, called, "an unknown session never reaches the terminal")
}

func TestAutomationTimesOut(t *testing.T) {
	t.Parallel()
	g, f := startWith(t, Options{
		automationTimeout: 50 * time.Millisecond,
		OnAutomation:      func(ctx context.Context, _, _, _ string) (string, error) { <-ctx.Done(); return "", ctx.Err() },
	})
	f.AddSession(session.Session{ID: "s1"})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/automation", `{"name":"loop","args":"status"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusGatewayTimeout, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "terminal_timeout")
}

func TestAutomationWithoutTerminal503(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSession(session.Session{ID: "s1"})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/automation", `{"name":"goal","args":"x"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "no_terminal")
}

// TestControllerSetOnAutomation pins the terminal callback on the real
// controller: the production order sets it after Start, and a callback set
// before Start must survive into the gateway's options too.
func TestControllerSetOnAutomation(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	c.isWSL = func() bool { return false }
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	c.SetOnAutomation(func(_ context.Context, _, _, args string) (string, error) {
		return "before: " + args, nil
	})
	_, err := c.Start(t.Context())
	require.NoError(t, err)
	c.mu.Lock()
	g := c.gw
	c.mu.Unlock()
	require.NotNil(t, g)

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/automation", `{"name":"goal","args":"one"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.JSONEq(t, `{"reply":"before: one"}`, string(body))

	c.SetOnAutomation(func(_ context.Context, _, _, args string) (string, error) {
		return "after: " + args, nil
	})
	rsp, body = doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/automation", `{"name":"loop","args":"two"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.JSONEq(t, `{"reply":"after: two"}`, string(body))
}

// TestAutomationCanceledClientIsNotA500 covers a tab that left before the
// terminal answered: the cancel is not a workspace failure, so it neither
// answers a 500 nor reaches the log.
func TestAutomationCanceledClientIsNotA500(t *testing.T) {
	logs := captureLogs(t)
	g, f := startWith(t, Options{OnAutomation: func(ctx context.Context, _, _, _ string) (string, error) {
		return "", ctx.Err()
	}})
	f.AddSession(session.Session{ID: "s1"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/sessions/s1/automation", strings.NewReader(`{"name":"goal"}`))
	req.SetPathValue("sid", "s1")
	rec := httptest.NewRecorder()
	g.handleAutomation(rec, req)
	require.Empty(t, rec.Body.String(), "a departed client gets no body")
	require.NotContains(t, logs.String(), "Web API call failed", "a cancel is not a workspace failure")
	require.NotContains(t, logs.String(), "automation_failed")
}

// TestAutomationDoesNotRelayTerminalErrors covers the terminal's
// "Error: <detail>" reply: the browser gets a fixed sentence and the
// detail stays in the server log.
func TestAutomationDoesNotRelayTerminalErrors(t *testing.T) {
	logs := captureLogs(t)
	g, f := startWith(t, Options{OnAutomation: func(context.Context, string, string, string) (string, error) {
		return "Error: dial unix /tmp/crush-secret.sock: connection refused", nil
	}})
	f.AddSession(session.Session{ID: "s1"})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/automation", `{"name":"goal"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.JSONEq(t, `{"reply":"The command could not run. The terminal log has the details."}`, string(body))
	require.NotContains(t, string(body), "crush-secret", "the terminal error stays out of the body")
	require.NotContains(t, string(body), "Error:")
	require.Contains(t, logs.String(), "crush-secret", "the terminal error is logged server-side")
}

// TestAutomationEmptyReplyIsHandled mirrors the Discord bridge: an empty
// terminal reply still tells the tab the command was handled.
func TestAutomationEmptyReplyIsHandled(t *testing.T) {
	t.Parallel()
	g, f := startWith(t, Options{OnAutomation: func(context.Context, string, string, string) (string, error) {
		return "", nil
	}})
	f.AddSession(session.Session{ID: "s1"})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/sessions/s1/automation", `{"name":"loop","args":"clear"}`)
	defer rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.JSONEq(t, `{"reply":"/loop handled."}`, string(body))
}
