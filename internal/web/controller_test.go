package web

import (
	"context"
	"errors"
	"strings"
	"testing"

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
