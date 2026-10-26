package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

type persistenceWorkspace struct {
	workspace.Workspace
	cfg   *config.Config
	scope config.Scope
	key   string
	value any
}

func (w *persistenceWorkspace) Config() *config.Config {
	return w.cfg
}

func (w *persistenceWorkspace) SetConfigField(scope config.Scope, key string, value any) error {
	w.scope, w.key, w.value = scope, key, value
	return nil
}

type toggleActionDialog struct {
	showAll bool
}

func (d *toggleActionDialog) ID() string {
	return "toggle-test"
}

func (d *toggleActionDialog) HandleMsg(tea.Msg) dialog.Action {
	return dialog.ActionToggleShowAllProviders{ShowAll: d.showAll}
}

func (d *toggleActionDialog) Draw(uv.Screen, uv.Rectangle) *tea.Cursor {
	return nil
}

func TestHandleDialogMsgPersistsShowAllProviders(t *testing.T) {
	t.Parallel()

	ws := &persistenceWorkspace{cfg: &config.Config{Options: &config.Options{}}}
	u := &UI{com: &common.Common{Workspace: ws}}
	u.dialog = dialog.NewOverlay(&toggleActionDialog{showAll: true})

	cmd := u.handleDialogMsg(tea.KeyPressMsg{})
	require.NotNil(t, cmd)
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				c()
			}
		}
	}

	require.Equal(t, config.ScopeGlobal, ws.scope)
	require.Equal(t, "options.models.show_all_providers", ws.key)
	require.Equal(t, true, ws.value)
}
