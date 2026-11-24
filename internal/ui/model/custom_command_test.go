package model

import (
	"context"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/stretchr/testify/require"
)

// recordRunWorkspace adds the one call the busy stub lacks: the run itself.
type recordRunWorkspace struct {
	*countingWorkspace
	prompts []string
}

func (w *recordRunWorkspace) AgentRun(_ context.Context, _ string, prompt string, _ ...message.Attachment) error {
	w.prompts = append(w.prompts, prompt)
	return nil
}

// TestRunCustomCommandSubstitutesOnce pins that a custom command fills in
// each argument once: a value that looks like another placeholder is sent
// as is instead of being expanded again. The old replace loop visited the
// arguments in map order, so one run could pass by luck; every run must
// send the single-pass text.
func TestRunCustomCommandSubstitutesOnce(t *testing.T) {
	ws := &recordRunWorkspace{countingWorkspace: &countingWorkspace{ready: true}}
	m := newBusyUI(ws.countingWorkspace)
	m.com.Workspace = ws
	warmCaches(m, false)
	t.Cleanup(func() { common.StopTurn("s1") })

	for range 64 {
		runDialogAction(m, dialog.ActionRunCustomCommand{
			Content: "Fix $FILE then $OTHER",
			Args:    map[string]string{"FILE": "$OTHER", "OTHER": "expanded"},
		})
	}

	require.Len(t, ws.prompts, 64)
	for _, prompt := range ws.prompts {
		require.Equal(t, "Fix $OTHER then expanded", prompt)
	}
}
