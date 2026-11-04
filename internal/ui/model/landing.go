package model

import (
	"image"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/charmbracelet/ultraviolet/layout"
)

// selectedLargeModel returns the currently selected large language model as
// memoized by the off-thread busy/agent probe (see workspace_cache.go), or
// nil when the agent isn't ready. It must never probe the workspace: it is
// called on every frame and AgentIsReady/AgentModel are synchronous HTTP
// round-trips in client/server mode.
func (m *UI) selectedLargeModel() *workspace.AgentModel {
	if m.agentReady {
		model := m.agentModel
		return &model
	}
	return nil
}

// landingView renders the landing page view showing the current working
// directory, model information, and LSP/MCP status in a two-column layout.
func (m *UI) landingView() string {
	t := m.com.Styles
	width := m.layout.main.Dx()
	cwd := common.PrettyPathInline(t, m.com.Workspace.WorkingDir(), m.gitBranch, width)

	hint := t.Chrome.TopRuleEdge.Render(">") + " " +
		t.Header.Charm.Render("READY") + " " +
		t.Header.WorkingDir.Render("▸ type a task, paste an error, or press ctrl+p")

	parts := []string{
		cwd,
		"",
		hint,
	}

	parts = append(parts, "", m.modelInfo(width))
	headerBlock := lipgloss.JoinVertical(lipgloss.Left, parts...)

	var remainingHeightArea image.Rectangle
	layout.Vertical(
		layout.Len(lipgloss.Height(headerBlock)+1),
		layout.Fill(1),
	).Split(m.layout.main).Assign(new(image.Rectangle), &remainingHeightArea)

	// On very narrow or short terminals the panels would overflow, so
	// fall back to the readout alone.
	if width < 46 || remainingHeightArea.Dy() < 5 {
		return lipgloss.NewStyle().
			Width(width).
			Height(m.layout.main.Dy() - 1).
			PaddingTop(1).
			Render(headerBlock)
	}

	// Three borderless HUD columns, separated by whitespace.
	colWidth := max(6, min(30, (width-4)/3))
	maxItems := max(1, remainingHeightArea.Dy()-2)

	lspSection := m.lspInfo(colWidth, maxItems, false)
	mcpSection := m.mcpInfo(colWidth, maxItems, false)
	skillsSection := m.skillsInfo(colWidth, maxItems, false)

	content := lipgloss.JoinHorizontal(lipgloss.Left, lspSection, "  ", mcpSection, "  ", skillsSection)

	return lipgloss.NewStyle().
		Width(width).
		Height(m.layout.main.Dy() - 1).
		PaddingTop(1).
		Render(
			lipgloss.JoinVertical(lipgloss.Left, headerBlock, "", content),
		)
}
