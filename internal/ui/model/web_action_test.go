package model

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

const webTestURL = "http://127.0.0.1:1/#t=x"

// webInfo and webWarn build the status messages webStarted reports. The
// link stays on screen long enough to read and copy.
func webInfo(text string) util.InfoMsg {
	return util.InfoMsg{Type: util.InfoTypeInfo, Msg: text, TTL: 30 * time.Second}
}

func webWarn(text string) util.InfoMsg {
	return util.InfoMsg{Type: util.InfoTypeWarn, Msg: text, TTL: 30 * time.Second}
}

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

// sequenceCmds returns the commands inside a tea.Sequence result. Bubble Tea
// keeps the sequence type unexported, so read the slice reflectively.
func sequenceCmds(t *testing.T, msg tea.Msg) ([]tea.Cmd, bool) {
	t.Helper()
	value := reflect.ValueOf(msg)
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return nil, false
	}
	cmds := make([]tea.Cmd, value.Len())
	for i := range cmds {
		cmd, ok := value.Index(i).Interface().(tea.Cmd)
		if !ok {
			return nil, false
		}
		cmds[i] = cmd
	}
	return cmds, true
}

// runWebCmds executes a command tree in runtime order, including the
// unexported tea.Sequence slice, and returns the messages it produced.
func runWebCmds(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runWebCmds(c)...)
		}
		return out
	}
	value := reflect.ValueOf(msg)
	if value.IsValid() && value.Kind() == reflect.Slice {
		var out []tea.Msg
		for i := 0; i < value.Len(); i++ {
			if c, ok := value.Index(i).Interface().(tea.Cmd); ok {
				out = append(out, runWebCmds(c)...)
			}
		}
		return out
	}
	return []tea.Msg{msg}
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

func TestWebStartedShowsNoticeBeforeOpening(t *testing.T) {
	// This test replaces a package-level clipboard seam, so it must not
	// run in parallel with the other tests in this package.
	original := copyWebLinkToClipboard
	t.Cleanup(func() { copyWebLinkToClipboard = original })
	copyWebLinkToClipboard = func(_, successMessage, _ string, ttl time.Duration) tea.Cmd {
		return util.ReportInfoWithTTL(successMessage, ttl)
	}

	web := &stubWeb{url: webTestURL}
	m := newWebUI(web)

	cmds, ok := sequenceCmds(t, m.webStarted(WebStartedMsg{URL: webTestURL})())
	require.True(t, ok, "webStarted must sequence the notice before the rest")
	require.Len(t, cmds, 2)
	require.Zero(t, web.opens, "the opener must not run before the notice")

	require.Equal(t, webInfo("Opening browser: "+webTestURL), cmds[0]())
	require.Zero(t, web.opens, "the opener must run only after the notice")

	msgs := runWebCmds(cmds[1])
	require.Equal(t, 1, web.opens)
	require.Contains(t, msgs, webInfo("Opening browser. Web link copied: "+webTestURL))
}

func TestWebStartedCopiesLinkAndOpensBrowser(t *testing.T) {
	original := copyWebLinkToClipboard
	t.Cleanup(func() { copyWebLinkToClipboard = original })

	var copied []string
	copyWebLinkToClipboard = func(text, successMessage, _ string, ttl time.Duration) tea.Cmd {
		copied = append(copied, text)
		return util.ReportInfoWithTTL(successMessage, ttl)
	}

	web := &stubWeb{url: webTestURL}
	m := newWebUI(web)

	_, cmd := m.Update(WebStartedMsg{URL: webTestURL})
	require.NotNil(t, cmd)

	msgs := runWebCmds(cmd)
	require.Equal(t, []string{webTestURL}, copied)
	require.Equal(t, 1, web.opens, "the browser must be opened in the background")
	require.Contains(t, msgs, webInfo("Opening browser: "+webTestURL))
	require.Contains(t, msgs, webInfo("Opening browser. Web link copied: "+webTestURL))
}

func TestWebStartedWarnsWhenBrowserFails(t *testing.T) {
	original := copyWebLinkToClipboard
	t.Cleanup(func() { copyWebLinkToClipboard = original })
	copyWebLinkToClipboard = func(_, successMessage, _ string, ttl time.Duration) tea.Cmd {
		return util.ReportInfoWithTTL(successMessage, ttl)
	}

	web := &stubWeb{url: webTestURL, openErr: errors.New("no browser")}
	m := newWebUI(web)

	_, cmd := m.Update(WebStartedMsg{URL: webTestURL})
	msgs := runWebCmds(cmd)

	require.Equal(t, 1, web.opens)
	require.Contains(t, msgs, webWarn("Web mode is running. Open "+webTestURL))
}

func TestWebStartedCopyFailureKeepsLink(t *testing.T) {
	original := copyWebLinkToClipboard
	t.Cleanup(func() { copyWebLinkToClipboard = original })
	copyWebLinkToClipboard = func(_, _, failureMessage string, ttl time.Duration) tea.Cmd {
		return util.ReportWarnWithTTL(failureMessage, ttl)
	}

	web := &stubWeb{url: webTestURL}
	m := newWebUI(web)

	_, cmd := m.Update(WebStartedMsg{URL: webTestURL})
	msgs := runWebCmds(cmd)

	require.Contains(t, msgs, webWarn("Failed to copy the web link to the clipboard. Open "+webTestURL))
}

func TestWebStartedWithoutWebLauncher(t *testing.T) {
	original := copyWebLinkToClipboard
	t.Cleanup(func() { copyWebLinkToClipboard = original })
	copyWebLinkToClipboard = func(_, successMessage, _ string, ttl time.Duration) tea.Cmd {
		return util.ReportInfoWithTTL(successMessage, ttl)
	}

	m := newWebUI(nil)

	_, cmd := m.Update(WebStartedMsg{URL: webTestURL})
	require.NotNil(t, cmd)

	msgs := runWebCmds(cmd)
	require.Contains(t, msgs, webInfo("Opening browser: "+webTestURL))
	require.NotContains(t, msgs, webWarn("Web mode is running. Open "+webTestURL))
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
