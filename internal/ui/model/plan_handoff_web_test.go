package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/charmbracelet/crush/internal/web"
	"github.com/stretchr/testify/require"
)

// planReadyReport is one pending-plan report the terminal sent to the
// gateway.
type planReadyReport struct {
	sessionID string
	seq       uint64
}

// planReadyRecorder is a launcher that also implements the optional plan
// report interface. It embeds the existing stub so that interface is the
// only difference.
type planReadyRecorder struct {
	stubWeb
	reports []planReadyReport
}

func (r *planReadyRecorder) SetPlanReady(sessionID string, seq uint64) {
	r.reports = append(r.reports, planReadyReport{sessionID: sessionID, seq: seq})
}

// The update loop reports through this optional interface, so the real
// launcher must keep matching it even though WebLauncher does not name it.
var _ planReadyReporter = (*web.Controller)(nil)

// TestWebPlanHandoffConfirms checks that a browser confirm goes through the
// same path as the inline prompt: the pending plan clears, the inline closes
// and the agent switches to code mode with the implement prompt.
func TestWebPlanHandoffConfirms(t *testing.T) {
	t.Parallel()
	u, ws := newPlanUI(t, "sess-1")
	ws.agentReady = true
	warmCaches(u, false)
	u.handlePlanHandoff(notify.RunComplete{
		SessionID: "sess-1",
		Text:      "plan\n<!-- CRUSH_PLAN_READY -->",
	})
	require.True(t, isPlanHandoffInline(u))

	_, cmd := u.Update(WebPlanHandoffMsg{SessionID: "sess-1"})
	require.NotNil(t, cmd)
	require.Empty(t, u.planReadySessionID)
	require.Nil(t, u.activeInline, "the browser confirm replaces the inline prompt")

	var switched modeSwitchedMsg
	for _, msg := range runCmdCollect(cmd) {
		if s, ok := msg.(modeSwitchedMsg); ok {
			switched = s
		}
	}
	require.NoError(t, switched.err)
	require.Equal(t, uiInputModeCode, switched.mode)
	require.Equal(t, "sess-1", switched.continueSessionID)

	_, cmd = u.Update(switched)
	runCmdCollect(cmd)
	require.Equal(t, uiInputModeCode, u.mode)
	require.Equal(t, config.AgentCoder, ws.setMainCalledWith)
	require.Equal(t, []string{"Implement the plan."}, ws.runPrompts)
}

// TestWebPlanHandoffStaleSessionIgnored checks that a confirm for a plan the
// terminal no longer holds changes nothing.
func TestWebPlanHandoffStaleSessionIgnored(t *testing.T) {
	t.Parallel()
	u, _ := newPlanUI(t, "sess-1")
	warmCaches(u, false)
	u.handlePlanHandoff(notify.RunComplete{
		SessionID: "sess-1",
		Text:      "plan\n<!-- CRUSH_PLAN_READY -->",
	})
	require.True(t, isPlanHandoffInline(u))

	_, cmd := u.Update(WebPlanHandoffMsg{SessionID: "sess-other"})
	for _, msg := range runCmdCollect(cmd) {
		_, switched := msg.(modeSwitchedMsg)
		require.False(t, switched, "a stale confirm must not switch modes")
	}
	require.Equal(t, "sess-1", u.planReadySessionID)
	require.True(t, isPlanHandoffInline(u))
	require.Equal(t, uiInputModePlan, u.mode)
}

// TestWebPlanHandoffRefusedWhileBusy checks that a browser confirm cannot
// switch the agent while the terminal is busy, and that the plan stays
// pending for later.
func TestWebPlanHandoffRefusedWhileBusy(t *testing.T) {
	t.Parallel()
	u, ws := newPlanUI(t, "sess-1")
	ws.agentReady = true
	warmCaches(u, false)
	launcher := &stubWeb{}
	u.com.Web = launcher
	u.handlePlanHandoff(notify.RunComplete{
		SessionID: "sess-1",
		Text:      "plan\n<!-- CRUSH_PLAN_READY -->",
	})
	u.agentBusyCache.set(true)

	_, cmd := u.Update(WebPlanHandoffMsg{SessionID: "sess-1"})
	var warning *util.InfoMsg
	for _, msg := range runCmdCollect(cmd) {
		if info, ok := msg.(util.InfoMsg); ok {
			warning = &info
		}
	}
	require.NotNil(t, warning)
	require.Equal(t, util.InfoTypeWarn, warning.Type)
	require.Equal(t, "Agent is busy, please wait before switching input mode...", warning.Msg)
	require.Equal(t, uiInputModePlan, u.mode)
	require.Empty(t, ws.setMainCalledWith)
	require.Equal(t, "sess-1", u.planReadySessionID, "the plan stays pending")
	require.True(t, isPlanHandoffInline(u))
	require.Equal(t, []string{webInputModeBusy}, launcher.refusals,
		"the browser must learn why the confirm did not happen")
}

// TestSetPlanReadyPendingReportsToWeb checks that pending-plan changes reach
// the gateway off the update loop, in order, and that an unchanged value or
// a launcher without the optional method reports nothing.
func TestSetPlanReadyPendingReportsToWeb(t *testing.T) {
	t.Parallel()
	u, _ := newPlanUI(t, "")
	recorder := &planReadyRecorder{}
	u.com.Web = recorder

	cmd := u.setPlanReadyPending("s1")
	require.NotNil(t, cmd)
	require.Equal(t, "s1", u.planReadySessionID)
	runCmdCollect(cmd)

	require.Nil(t, u.setPlanReadyPending("s1"), "an unchanged value reports nothing")

	cmd = u.setPlanReadyPending("")
	require.NotNil(t, cmd)
	runCmdCollect(cmd)

	require.Len(t, recorder.reports, 2)
	require.Equal(t, "s1", recorder.reports[0].sessionID)
	require.Empty(t, recorder.reports[1].sessionID)
	require.Greater(t, recorder.reports[1].seq, recorder.reports[0].seq,
		"reports carry an increasing sequence")

	u.com.Web = &stubWeb{}
	require.Nil(t, u.setPlanReadyPending("s1"),
		"a launcher without the optional method reports nothing")
}
