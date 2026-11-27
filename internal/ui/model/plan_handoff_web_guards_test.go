package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResetPlanModeStateReportsTheClearedPlan pins that dropping a pending
// plan tells the gateway the plan is gone, after the report that announced
// it, so a stale plan cannot stay confirmable in the browser.
func TestResetPlanModeStateReportsTheClearedPlan(t *testing.T) {
	t.Parallel()
	u, ws := newPlanUI(t, "sess-1")
	warmCaches(u, false)
	recorder := &planReadyRecorder{}
	u.com.Web = recorder

	runCmdCollect(u.setPlanReadyPending("sess-1"))
	require.Equal(t, "sess-1", u.planReadySessionID)

	// Code mode keeps the reset to the report alone.
	u.mode = uiInputModeCode
	runCmdCollect(u.resetPlanModeState())

	require.Empty(t, u.planReadySessionID)
	require.Nil(t, u.activeInline)
	require.Empty(t, ws.runPrompts, "a reset sends no prompt")
	require.Len(t, recorder.reports, 2)
	require.Equal(t, "sess-1", recorder.reports[0].sessionID)
	require.Empty(t, recorder.reports[1].sessionID, "the gateway hears the plan is gone")
}

// TestWebPlanHandoffGuards pins every branch that leaves a browser confirm
// alone: code mode, no session, an empty id and another session's id.
func TestWebPlanHandoffGuards(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		id      string
		prepare func(*UI)
	}{
		{"code mode", "sess-1", func(u *UI) { u.mode = uiInputModeCode }},
		{"no session", "sess-1", func(u *UI) { u.session = nil }},
		{"empty id", "", nil},
		{"other session", "sess-2", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, ws := newPlanUI(t, "sess-1")
			warmCaches(u, false)
			require.Nil(t, u.setPlanReadyPending("sess-1"))
			if tc.prepare != nil {
				tc.prepare(u)
			}

			cmd := u.webPlanHandoff(WebPlanHandoffMsg{SessionID: tc.id})

			require.Nil(t, cmd, "a guard answers nothing")
			require.Empty(t, ws.runPrompts, "a guarded confirm sends no prompt")
			require.Empty(t, ws.setMainCalledWith, "a guarded confirm switches no agent")
			require.Equal(t, "sess-1", u.planReadySessionID, "the plan stays pending")
		})
	}
}

// TestSendMessageSupersedesAPendingPlan pins the other supersede path: a new
// prompt clears the pending plan and the gateway hears the empty id.
func TestSendMessageSupersedesAPendingPlan(t *testing.T) {
	t.Parallel()
	u, ws := newPlanUI(t, "sess-1")
	ws.agentReady = true
	warmCaches(u, false)
	recorder := &planReadyRecorder{}
	u.com.Web = recorder
	runCmdCollect(u.setPlanReadyPending("sess-1"))
	require.Equal(t, "sess-1", u.planReadySessionID)

	runCmdCollect(u.sendMessage("go ahead"))

	require.Empty(t, u.planReadySessionID, "a new prompt supersedes the plan")
	require.Len(t, ws.runPrompts, 1)
	require.Len(t, recorder.reports, 2)
	require.Equal(t, "sess-1", recorder.reports[0].sessionID)
	require.Empty(t, recorder.reports[1].sessionID, "the gateway hears the plan is gone")
}
