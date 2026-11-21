package model

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

// stubWeb is a configurable web launcher for the openWeb message tests.
type stubWeb struct {
	url string
	err error
}

func (s stubWeb) Open(context.Context) (string, error) { return s.url, s.err }

func (s stubWeb) Running() bool { return true }

func openWebMsg(t *testing.T, web common.WebLauncher) util.InfoMsg {
	t.Helper()
	ws := &slashWorkspace{cfg: &config.Config{Options: &config.Options{}}}
	com := common.DefaultCommon(ws)
	com.Web = web
	m := &UI{com: com}
	cmd := m.openWeb()
	require.NotNil(t, cmd)
	msg, ok := cmd().(util.InfoMsg)
	require.True(t, ok, "openWeb must report through an info message")
	return msg
}

func TestOpenWebReportsLink(t *testing.T) {
	t.Parallel()

	msg := openWebMsg(t, stubWeb{url: "http://127.0.0.1:1/#t=x"})

	require.Equal(t, util.InfoTypeInfo, msg.Type)
	require.Contains(t, msg.Msg, "http://127.0.0.1:1/#t=x")
}

func TestOpenWebWarnsWhenBrowserFails(t *testing.T) {
	t.Parallel()

	msg := openWebMsg(t, stubWeb{url: "http://127.0.0.1:1/#t=x", err: errors.New("no browser")})

	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.Contains(t, msg.Msg, "http://127.0.0.1:1/#t=x")
}

func TestOpenWebReportsStartFailure(t *testing.T) {
	t.Parallel()

	msg := openWebMsg(t, stubWeb{err: errors.New("port busy")})

	require.Equal(t, util.InfoTypeError, msg.Type)
	require.Contains(t, msg.Msg, "port busy")
}

func TestOpenWebReportsUnavailable(t *testing.T) {
	t.Parallel()

	msg := openWebMsg(t, nil)

	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.Equal(t, "Web mode is not available here", msg.Msg)
}
