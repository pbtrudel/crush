package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// TestReloadConfigCommandSelectsAction pins the palette entry that re-reads
// the config files without restarting, including the action it emits.
func TestReloadConfigCommandSelectsAction(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	com := &common.Common{Workspace: &rcCommandsWorkspace{}, Styles: &sty}
	c, err := NewCommands(com, "", false, false, false, false, false, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "Reload Configuration", commandTitle(c, "reload_config"))

	for i, item := range c.list.FilteredItems() {
		if ci, ok := item.(*CommandItem); ok && ci.ID() == "reload_config" {
			c.list.SetSelected(i)
			break
		}
	}

	action := c.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, ok := action.(ActionReloadConfig)
	require.True(t, ok, "selecting Reload Configuration must emit ActionReloadConfig")
}
