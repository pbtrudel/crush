package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

// teamCommandsWorkspace overrides the shared mock's config so the Agent Teams
// toggle can be tested in both states.
type teamCommandsWorkspace struct {
	rcCommandsWorkspace
	enabled bool
}

func (w *teamCommandsWorkspace) Config() *config.Config {
	return &config.Config{
		Options: &config.Options{
			TUI:        &config.TUIOptions{},
			AgentTeams: &w.enabled,
		},
	}
}

// TestAgentTeamsToggleCommand pins the palette entry that enables or
// disables Agent Teams and the action it emits.
func TestAgentTeamsToggleCommand(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("enabled offers disable", func(t *testing.T) {
		t.Parallel()
		com := &common.Common{Workspace: &teamCommandsWorkspace{enabled: true}, Styles: &sty}
		c, err := NewCommands(com, "", false, false, false, false, false, nil, nil)
		require.NoError(t, err)
		require.Equal(t, "Disable Agent Teams", commandTitle(c, "toggle_agent_teams"))

		action := selectCommand(t, c, "toggle_agent_teams")
		_, ok := action.(ActionToggleAgentTeams)
		require.True(t, ok, "selecting the Agent Teams toggle must emit ActionToggleAgentTeams")
	})

	t.Run("disabled offers enable", func(t *testing.T) {
		t.Parallel()
		com := &common.Common{Workspace: &teamCommandsWorkspace{enabled: false}, Styles: &sty}
		c, err := NewCommands(com, "", false, false, false, false, false, nil, nil)
		require.NoError(t, err)
		require.Equal(t, "Enable Agent Teams", commandTitle(c, "toggle_agent_teams"))
	})
}

func selectCommand(t *testing.T, c *Commands, id string) Action {
	t.Helper()
	for i, item := range c.list.FilteredItems() {
		if ci, ok := item.(*CommandItem); ok && ci.ID() == id {
			c.list.SetSelected(i)
			return c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
	}
	require.Failf(t, "command not found in palette", "%s", id)
	return nil
}
