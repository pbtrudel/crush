package model

import (
	"errors"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

// modelReloadConfig is a configured main model the reload tests can switch
// away from. The active theme is set so a switch does not touch component
// styles a bare UI does not have.
func modelReloadConfig() *config.Config {
	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("p1", config.ProviderConfig{
		ID:     "p1",
		Models: []catwalk.Model{{ID: "m2", Name: "Model Two"}},
	})
	return &config.Config{
		Options:   &config.Options{TUI: &config.TUIOptions{ActiveTheme: "charmtone-pantera"}},
		Providers: providers,
		Models: map[config.SelectedModelType]config.SelectedModel{
			config.SelectedModelTypeMain:  {Provider: "p1", Model: "m1"},
			config.SelectedModelTypeSmall: {Provider: "p1", Model: "m1"},
		},
		Agents: map[string]config.Agent{
			config.AgentCoder: {Model: config.SelectedModelTypeMain},
		},
	}
}

// TestSelectModelReportsReloadFailure pins that a failed agent model
// rebuild after a model switch reaches the UI. Returning the tea.Cmd itself
// instead of calling it would hand the Update loop a function value it
// never runs, so the user would see no error at all.
func TestSelectModelReportsReloadFailure(t *testing.T) {
	t.Parallel()

	ws := &reloadWorkspace{cfg: modelReloadConfig(), reload: errors.New("reload failed")}
	u := &UI{com: &common.Common{Workspace: ws}, dialog: dialog.NewOverlay(), state: uiChat}

	cmd := u.handleSelectModel(dialog.ActionSelectModel{
		Model:     config.SelectedModel{Provider: "p1", Model: "m2"},
		ModelType: config.SelectedModelTypeMain,
	})
	require.NotNil(t, cmd)
	require.Equal(t, 1, ws.writes, "the preferred model is written before the reload")

	msgs := runWebCmds(cmd)
	require.Contains(t, msgs, util.InfoMsg{Type: util.InfoTypeError, Msg: "reload failed"})
}

// TestRestoreModelReportsReloadFailure pins the same error path on the
// session restore that follows a switch to an older session.
func TestRestoreModelReportsReloadFailure(t *testing.T) {
	t.Parallel()

	ws := &reloadWorkspace{cfg: modelReloadConfig(), reload: errors.New("reload failed")}
	u := &UI{com: &common.Common{Workspace: ws}}

	cmd := u.restoreModelFromSession([]message.Message{{
		ID: "m1", SessionID: "s1", Role: message.Assistant, Provider: "p1", Model: "m2",
	}})
	require.NotNil(t, cmd)
	require.Equal(t, 1, ws.writes, "the restored model is written before the reload")

	msgs := runWebCmds(cmd)
	require.Contains(t, msgs, util.InfoMsg{Type: util.InfoTypeError, Msg: "reload failed"})
}
