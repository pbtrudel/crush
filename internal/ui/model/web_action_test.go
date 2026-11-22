package model

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

// stubWeb is a configurable web launcher for the web mode message tests.
type stubWeb struct {
	url      string
	startErr error
	openErr  error
	opens    int
}

func (s *stubWeb) Start(context.Context) (string, error) { return s.url, s.startErr }

func (s *stubWeb) OpenBrowser() error {
	s.opens++
	return s.openErr
}

func (s *stubWeb) Running() bool { return true }

func newWebUI(web common.WebLauncher) *UI {
	ws := &slashWorkspace{cfg: &config.Config{Options: &config.Options{}}}
	com := common.DefaultCommon(ws)
	com.Web = web
	m := newSlashUI(ws)
	m.com = com
	// Keep the Update tail from probing the fake workspace.
	m.agentBusyCache.set(false)
	m.permModeCache.set(permission.PermissionModeNormal)
	m.promptQueueCheckedAt = time.Now()
	m.lspCheckedAt = time.Now()
	m.mcpCheckedAt = time.Now()
	return m
}

func runOpenWeb(t *testing.T, web common.WebLauncher) util.InfoMsg {
	t.Helper()
	m := newWebUI(web)
	cmd := m.openWeb()
	require.NotNil(t, cmd)
	msg, ok := cmd().(util.InfoMsg)
	require.True(t, ok, "a failed start must report through an info message")
	return msg
}

func TestOpenWebReturnsStartedMessage(t *testing.T) {
	t.Parallel()

	web := &stubWeb{url: "http://127.0.0.1:1/#t=x"}
	m := newWebUI(web)

	cmd := m.openWeb()
	require.NotNil(t, cmd)
	msg, ok := cmd().(WebStartedMsg)
	require.True(t, ok, "openWeb must hand the link to the update loop")
	require.Equal(t, "http://127.0.0.1:1/#t=x", msg.URL)
	require.Zero(t, web.opens, "openWeb must not open the browser itself")
}

func TestWebStartedCopiesLinkAndOpensBrowser(t *testing.T) {
	// This test replaces a package-level clipboard seam, so it must not
	// run in parallel with the other tests in this package.
	original := copyWebLinkToClipboard
	t.Cleanup(func() { copyWebLinkToClipboard = original })

	var copied []string
	copyWebLinkToClipboard = func(text, successMessage, _ string) tea.Cmd {
		copied = append(copied, text)
		return util.ReportInfo(successMessage)
	}

	web := &stubWeb{url: "http://127.0.0.1:1/#t=x"}
	m := newWebUI(web)

	_, cmd := m.Update(WebStartedMsg{URL: "http://127.0.0.1:1/#t=x"})
	require.NotNil(t, cmd)

	msgs := runCmdCollect(cmd)
	require.Equal(t, []string{"http://127.0.0.1:1/#t=x"}, copied)
	require.Equal(t, 1, web.opens, "the browser must be opened in the background")
	require.Contains(t, msgs, util.NewInfoMsg("Opening browser. Web link copied: http://127.0.0.1:1/#t=x"))
}

func TestWebStartedWarnsWhenBrowserFails(t *testing.T) {
	original := copyWebLinkToClipboard
	t.Cleanup(func() { copyWebLinkToClipboard = original })
	copyWebLinkToClipboard = func(_, successMessage, _ string) tea.Cmd {
		return util.ReportInfo(successMessage)
	}

	web := &stubWeb{url: "http://127.0.0.1:1/#t=x", openErr: errors.New("no browser")}
	m := newWebUI(web)

	_, cmd := m.Update(WebStartedMsg{URL: "http://127.0.0.1:1/#t=x"})
	msgs := runCmdCollect(cmd)

	require.Equal(t, 1, web.opens)
	require.Contains(t, msgs, util.NewWarnMsg("Web mode is running. Open http://127.0.0.1:1/#t=x"))
}

func TestWebStartedCopyFailureKeepsLink(t *testing.T) {
	original := copyWebLinkToClipboard
	t.Cleanup(func() { copyWebLinkToClipboard = original })
	copyWebLinkToClipboard = func(_, _, failureMessage string) tea.Cmd {
		return util.ReportWarn(failureMessage)
	}

	web := &stubWeb{url: "http://127.0.0.1:1/#t=x"}
	m := newWebUI(web)

	_, cmd := m.Update(WebStartedMsg{URL: "http://127.0.0.1:1/#t=x"})
	msgs := runCmdCollect(cmd)

	require.Contains(t, msgs, util.NewWarnMsg("Failed to copy the web link to the clipboard. Open http://127.0.0.1:1/#t=x"))
}

func TestOpenWebReportsStartFailure(t *testing.T) {
	t.Parallel()

	web := &stubWeb{startErr: errors.New("port busy")}
	msg := runOpenWeb(t, web)

	require.Equal(t, util.InfoTypeError, msg.Type)
	require.Contains(t, msg.Msg, "port busy")
	require.Zero(t, web.opens)
}

func TestOpenWebReportsUnavailable(t *testing.T) {
	t.Parallel()

	msg := runOpenWeb(t, nil)

	require.Equal(t, util.InfoTypeWarn, msg.Type)
	require.Equal(t, "Web mode is not available here", msg.Msg)
}
