package model

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/discord"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
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
type webModeReport struct {
	plan bool
	seq  uint64
}

type stubWeb struct {
	url      string
	startErr error
	openErr  error
	stopErr  error
	stops    int
	opens    int
	reports  []webModeReport
	refusals []string
}

func (s *stubWeb) Start(context.Context) (string, error) { return s.url, s.startErr }

func (s *stubWeb) Stop(context.Context) error {
	s.stops++
	return s.stopErr
}

func (s *stubWeb) OpenBrowser() error {
	s.opens++
	return s.openErr
}

func (s *stubWeb) Running() bool { return true }

func (s *stubWeb) SetInputMode(plan bool, seq uint64) {
	s.reports = append(s.reports, webModeReport{plan: plan, seq: seq})
}

func (s *stubWeb) ReportInputModeRefused(reason string) { s.refusals = append(s.refusals, reason) }

func newWebUI(web common.WebLauncher) *UI {
	ws := &slashWorkspace{cfg: &config.Config{Options: &config.Options{TUI: &config.TUIOptions{}}}}
	com := common.DefaultCommon(ws)
	com.Web = web
	m := newSlashUI(ws)
	m.com = com
	// The status bar reads the launcher from its own Common, which
	// newSlashUI built before the launcher was set, so rebuild it around
	// com.
	m.status = NewStatus(com, m)
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
	require.Equal(t, []webModeReport{{plan: false, seq: 1}}, web.reports,
		"the gateway starts with the terminal's code mode")
}

// TestWebStartedReportsInputMode checks that --web, which starts the gateway
// before the program exists, reports the mode once webStarted runs.
func TestWebStartedReportsInputMode(t *testing.T) {
	original := copyWebLinkToClipboard
	t.Cleanup(func() { copyWebLinkToClipboard = original })
	copyWebLinkToClipboard = func(_, successMessage, _ string, ttl time.Duration) tea.Cmd {
		return util.ReportInfoWithTTL(successMessage, ttl)
	}

	web := &stubWeb{url: webTestURL}
	m := newWebUI(web)
	m.mode = uiInputModePlan

	_, cmd := m.Update(WebStartedMsg{URL: webTestURL})
	runWebCmds(cmd)

	require.Equal(t, []webModeReport{{plan: true, seq: 1}}, web.reports)
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

// TestWebSessionCreatedLoadsSession checks that a session the browser starts
// opens in the terminal the same way picking it in the sessions dialog does.
func TestWebSessionCreatedLoadsSession(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	u.session = &session.Session{ID: "terminal-session"}
	calls := ws.setCurrentSessionCalls

	_, cmd := u.Update(WebSessionCreatedMsg{ID: "browser-session"})
	require.NotNil(t, cmd)

	var load loadSessionMsg
	for _, msg := range runCmdCollect(cmd) {
		if l, ok := msg.(loadSessionMsg); ok {
			load = l
		}
	}
	require.Equal(t, "browser-session", load.session.ID)
	require.Equal(t, calls+1, ws.setCurrentSessionCalls,
		"the workspace must report the new current session so the tab follows")
}

// TestWebInputModeMsgSwitchesPlan checks that the browser request goes
// through the regular agent switch and reports the settled mode back to the
// gateway.
func TestWebInputModeMsgSwitchesPlan(t *testing.T) {
	t.Parallel()

	u, ws := newPlanUI(t, "sess-1")
	u.mode = uiInputModeCode
	warmCaches(u, false)
	web := &stubWeb{}
	u.com.Web = web

	_, cmd := u.Update(WebInputModeMsg{Plan: true})
	require.NotNil(t, cmd)
	var switched modeSwitchedMsg
	for _, msg := range runCmdCollect(cmd) {
		if s, ok := msg.(modeSwitchedMsg); ok {
			switched = s
		}
	}
	require.Equal(t, uiInputModePlan, switched.mode)
	require.NoError(t, switched.err)

	_, cmd = u.Update(switched)
	require.NotNil(t, cmd)
	runCmdCollect(cmd)
	require.Equal(t, uiInputModePlan, u.mode)
	require.Equal(t, config.AgentPlan, ws.setMainCalledWith)
	require.Equal(t, []webModeReport{{plan: true, seq: 1}}, web.reports)
}

// TestWebInputModeMsgRefusedWhileBusy checks that a browser request cannot
// switch the agent while the terminal is busy, and that a request for the
// current mode is a no-op even then.
func TestWebInputModeMsgRefusedWhileBusy(t *testing.T) {
	t.Parallel()

	u, ws := newPlanUI(t, "sess-1")
	u.mode = uiInputModeCode
	warmCaches(u, false)
	u.agentBusyCache.set(true)
	web := &stubWeb{}
	u.com.Web = web

	_, cmd := u.Update(WebInputModeMsg{Plan: true})
	msgs := runCmdCollect(cmd)
	var warning *util.InfoMsg
	for i := range msgs {
		if msg, ok := msgs[i].(util.InfoMsg); ok {
			warning = &msg
		}
	}
	require.NotNil(t, warning)
	require.Equal(t, util.InfoTypeWarn, warning.Type)
	require.Equal(t, "Agent is busy, please wait before switching input mode...", warning.Msg)
	require.Equal(t, uiInputModeCode, u.mode)
	require.Empty(t, ws.setMainCalledWith)
	require.Empty(t, web.reports)
	require.Equal(t, []string{webInputModeBusy}, web.refusals,
		"the browser must learn why the switch did not happen")

	u.mode = uiInputModePlan
	_, cmd = u.Update(WebInputModeMsg{Plan: true})
	require.Nil(t, cmd, "asking for the current mode must do nothing")
	require.Empty(t, ws.setMainCalledWith)
}

// TestDiscordModeChangedReportsToWeb checks the other path that changes
// m.mode directly: remote control already switched the agent, and the
// gateway still needs the new mode.
func TestDiscordModeChangedReportsToWeb(t *testing.T) {
	t.Parallel()

	u, _ := newPlanUI(t, "sess-1")
	u.mode = uiInputModeCode
	warmCaches(u, false)
	web := &stubWeb{}
	u.com.Web = web

	_, cmd := u.Update(discord.ModeChangedMsg{Plan: true})
	require.Equal(t, uiInputModePlan, u.mode)
	runCmdCollect(cmd)
	require.Equal(t, []webModeReport{{plan: true, seq: 1}}, web.reports)
}

// runStopWebPalette picks Stop Web Mode in the command palette and returns
// the messages its commands produce.
func runStopWebPalette(t *testing.T, m *UI) []tea.Msg {
	t.Helper()
	m.openCommandsDialog()
	require.True(t, m.dialog.ContainsDialog(dialog.CommandsID))
	for _, r := range "stop web" {
		m.handleDialogMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, m.dialog.ContainsDialog(dialog.CommandsID), "the palette closes on the action")
	return runCmdCollect(cmd)
}

// TestStopWebActionStopsAndReports checks that the palette action stops the
// gateway through the launcher and reports how to start it again. The status
// bar reads the launcher's running state on every draw, so the web badge is
// gone once the stop lands.
func TestStopWebActionStopsAndReports(t *testing.T) {
	t.Parallel()

	web := &fakeWeb{running: true}
	m := newWebUI(web)
	require.Contains(t, m.status.badges(), "WEB")

	msgs := runStopWebPalette(t, m)

	require.Equal(t, 1, web.stops)
	require.False(t, web.running)
	require.Contains(t, msgs, util.InfoMsg{
		Type: util.InfoTypeInfo,
		Msg:  "Web mode stopped. Run /web to start it again.",
	})
	require.NotContains(t, m.status.badges(), "WEB")
}

// TestStopWebActionReportsFailure checks that a failed stop surfaces the
// error instead of claiming web mode stopped.
func TestStopWebActionReportsFailure(t *testing.T) {
	t.Parallel()

	web := &stubWeb{stopErr: errors.New("gateway already gone")}
	m := newWebUI(web)

	msgs := runStopWebPalette(t, m)

	require.Equal(t, 1, web.stops)
	var reported *util.InfoMsg
	for _, msg := range msgs {
		if info, ok := msg.(util.InfoMsg); ok {
			reported = &info
		}
	}
	require.NotNil(t, reported)
	require.Equal(t, util.InfoTypeError, reported.Type)
	require.Contains(t, reported.Msg, "gateway already gone")
}

// TestStopWebActionWithoutLauncher checks the stop command cannot run where
// web mode is unavailable.
func TestStopWebActionWithoutLauncher(t *testing.T) {
	t.Parallel()

	m := newWebUI(nil)

	cmd := m.stopWeb()
	require.NotNil(t, cmd)
	msgs := runCmdCollect(cmd)
	require.Contains(t, msgs, util.InfoMsg{
		Type: util.InfoTypeWarn,
		Msg:  "Web mode is not available here",
	})
}
