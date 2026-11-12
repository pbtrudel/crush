package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAgentsSelectsFilteredDescriptor(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	com := &common.Common{Styles: &sty}
	d := NewAgents(com, []AgentDescriptor{
		{SessionID: "one", Name: "reviewer", Kind: agentrun.KindTeammate, Status: agentrun.StatusRunning},
		{SessionID: "two", Name: "implementer", Kind: agentrun.KindSubagent, Status: agentrun.StatusDone},
	})

	d.HandleMsg(keyMsg('i'))
	d.HandleMsg(keyMsg('m'))
	d.HandleMsg(keyMsg('p'))
	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	selected, ok := action.(ActionSelectAgent)
	require.True(t, ok)
	require.Equal(t, "two", selected.Agent.SessionID)
}

func TestAgentsEmptyState(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	d := NewAgents(&common.Common{Styles: &sty}, nil)
	require.Contains(t, ansi.Strip(d.list.Render()), "No agents for this session")
}
