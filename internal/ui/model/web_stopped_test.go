package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

// TestWebStoppedReportsHowToRestart checks that a detach from the browser
// tells the terminal how to start web mode again. The status bar drops the
// web indicator on its own, since it reads the controller's Running state
// every draw.
func TestWebStoppedReportsHowToRestart(t *testing.T) {
	t.Parallel()
	u, _ := newPlanUI(t, "sess-1")
	warmCaches(u, false)
	_, cmd := u.Update(WebStoppedMsg{Reason: "detached"})
	require.NotNil(t, cmd)
	var info *util.InfoMsg
	for _, msg := range runCmdCollect(cmd) {
		if m, ok := msg.(util.InfoMsg); ok {
			info = &m
		}
	}
	require.NotNil(t, info)
	require.Equal(t, util.InfoTypeInfo, info.Type)
	require.Equal(t, "Web mode stopped from the browser. Run /web to start it again.", info.Msg)
}
