package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/charmbracelet/crush/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

// reasoningDialog answers with the action under test, standing in for the
// model dialog the user picked an effort in.
type reasoningDialog struct {
	action dialog.Action
}

func (d *reasoningDialog) ID() string { return dialog.ReasoningID }

func (d *reasoningDialog) HandleMsg(tea.Msg) dialog.Action { return d.action }

func (d *reasoningDialog) Draw(uv.Screen, uv.Rectangle) *tea.Cursor { return nil }

// reloadWorkspace fails the agent reload so the test can see whether the
// failure reaches the UI.
type reloadWorkspace struct {
	workspace.Workspace

	cfg    *config.Config
	reload error
	writes int
}

func (w *reloadWorkspace) Config() *config.Config { return w.cfg }

func (w *reloadWorkspace) AgentIsBusy() bool { return false }

func (w *reloadWorkspace) UpdatePreferredModel(config.Scope, config.SelectedModelType, config.SelectedModel) error {
	w.writes++
	return nil
}

func (w *reloadWorkspace) UpdateAgentModel(context.Context) error { return w.reload }

// TestReasoningEffortReportsReloadFailure pins that a failing
// UpdateAgentModel shows up as an error message. Returning the tea.Cmd
// itself instead of calling it would hand the UI a function value the
// status line never renders.
func TestReasoningEffortReportsReloadFailure(t *testing.T) {
	t.Parallel()

	ws := &reloadWorkspace{
		cfg: &config.Config{
			Models: map[config.SelectedModelType]config.SelectedModel{
				config.SelectedModelTypeMain: {Provider: "p1", Model: "m1"},
			},
			Agents: map[string]config.Agent{
				config.AgentCoder: {Model: config.SelectedModelTypeMain},
			},
		},
		reload: errors.New("reload failed"),
	}
	u := &UI{com: &common.Common{Workspace: ws}}
	u.dialog = dialog.NewOverlay(&reasoningDialog{
		action: dialog.ActionSelectReasoningEffort{Effort: "high"},
	})

	cmd := u.handleDialogMsg(tea.KeyPressMsg{})
	require.NotNil(t, cmd)
	require.Equal(t, 1, ws.writes, "the preferred model is written before the reload")

	msgs := runWebCmds(cmd)
	require.Contains(t, msgs, util.InfoMsg{Type: util.InfoTypeError, Msg: "reload failed"})
}
