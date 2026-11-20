package dialog

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
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

// A row names the model its agent runs on, so two agents on different models
// are told apart without opening them. An agent whose model is unknown shows
// its kind alone.
func TestAgentsRowsShowTheAgentModel(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	d := NewAgents(&common.Common{Styles: &sty}, []AgentDescriptor{
		{
			SessionID: "one", Name: "reviewer", Kind: agentrun.KindTeammate,
			Status: agentrun.StatusRunning, Model: "glm-5", ModelProvider: "hyper",
		},
		{SessionID: "two", Name: "implementer", Kind: agentrun.KindSubagent, Status: agentrun.StatusDone},
	})

	scr := uv.NewScreenBuffer(100, 30)
	d.Draw(scr, scr.Bounds())
	rendered := ansi.Strip(scr.Render())
	require.Contains(t, rendered, "teammate · glm-5 · hyper")
	require.Equal(t, 2, strings.Count(rendered, "·"), "the row without a model adds no separator")
	require.Contains(t, rendered, "subagent")
}

// The model is part of a row's search text, so a model name finds its agents.
func TestAgentsFiltersByModel(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	d := NewAgents(&common.Common{Styles: &sty}, []AgentDescriptor{
		{SessionID: "one", Name: "reviewer", Kind: agentrun.KindTeammate, Model: "glm-5", ModelProvider: "hyper"},
		{SessionID: "two", Name: "implementer", Kind: agentrun.KindSubagent, Model: "opus-4", ModelProvider: "anthropic"},
	})

	for _, r := range "opus" {
		d.HandleMsg(keyMsg(r))
	}
	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	selected, ok := action.(ActionSelectAgent)
	require.True(t, ok)
	require.Equal(t, "two", selected.Agent.SessionID)
}
