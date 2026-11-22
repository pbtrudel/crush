package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

func TestControllerOpenStartsOnceAndOpensBrowser(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	var opened []string
	c.open = func(u string) error { opened = append(opened, u); return nil }
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	require.False(t, c.Running())
	u1, err := c.Open(t.Context())
	require.NoError(t, err)
	require.True(t, c.Running())
	u2, err := c.Open(t.Context())
	require.NoError(t, err)
	require.Equal(t, u1, u2)
	require.Equal(t, []string{u1, u1}, opened)
	require.True(t, strings.HasPrefix(u1, "http://127.0.0.1:"))
}

func TestControllerOpenBrowserFailureKeepsURL(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	c.open = func(string) error { return errors.New("no display") }
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	u, err := c.Open(t.Context())
	require.Error(t, err)
	require.Contains(t, u, "/#t=")
	require.True(t, c.Running())
}

func TestControllerShutdownIdempotent(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	require.NoError(t, c.Shutdown(t.Context()))
	_, err := c.Start(t.Context())
	require.NoError(t, err)
	require.NoError(t, c.Shutdown(t.Context()))
	require.False(t, c.Running())
	require.NoError(t, c.Shutdown(t.Context()))
}

func TestControllerStartDoesNotOpenBrowser(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	var opened []string
	c.open = func(u string) error { opened = append(opened, u); return nil }
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	u, err := c.Start(t.Context())
	require.NoError(t, err)
	require.Empty(t, opened, "Start must not open the browser")
	require.Equal(t, u, c.LaunchURL())
	require.NoError(t, c.OpenBrowser())
	require.Equal(t, []string{u}, opened)
}

func TestControllerOpenBrowserRequiresGateway(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	called := false
	c.open = func(string) error { called = true; return nil }

	require.Error(t, c.OpenBrowser())
	require.False(t, called)
}

// TestControllerForwardsSessionsCreatedInBrowser checks that the callback
// set after Start reaches the running gateway, where the root command sets
// it once the program exists.
func TestControllerForwardsSessionsCreatedInBrowser(t *testing.T) {
	t.Parallel()
	c := NewController(webtest.NewFake())
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	_, err := c.Start(t.Context())
	require.NoError(t, err)

	created := make(chan string, 1)
	c.SetOnSessionCreated(func(id string) { created <- id })

	rsp, data := doJSON(t, authedClient(t, c.gw), http.MethodPost, c.gw.URL()+"/api/sessions", "{}")
	defer rsp.Body.Close()
	require.Equal(t, http.StatusCreated, rsp.StatusCode)

	var sess struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(data, &sess))
	select {
	case id := <-created:
		require.Equal(t, sess.ID, id)
	case <-time.After(2 * time.Second):
		t.Fatal("the controller did not forward the created session")
	}
}
