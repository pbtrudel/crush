package model

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

// teamRefreshInterval is how often the sidebar re-reads the durable Team
// state. The state file is small and written atomically.
const teamRefreshInterval = 2 * time.Second

// teamStatusMsg carries one Team snapshot read off the update loop.
type teamStatusMsg struct {
	state agentteam.State
	ok    bool
}

// teamStatePath returns the durable Team state path for the workspace.
func (m *UI) teamStatePath() string {
	if m.com == nil || m.com.Config() == nil {
		return ""
	}
	dir := m.com.Config().Options.DataDirectory
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "agent-teams", "team.json")
}

// teamTick schedules the next Team state read.
func (m *UI) teamTick() tea.Cmd {
	path := m.teamStatePath()
	return tea.Tick(teamRefreshInterval, func(time.Time) tea.Msg {
		if path == "" {
			return teamStatusMsg{}
		}
		state, err := agentteam.LoadState(path)
		return teamStatusMsg{state: state, ok: err == nil}
	})
}

// handleTeamStatus stores a changed snapshot and invalidates the sidebar.
func (m *UI) handleTeamStatus(msg teamStatusMsg) tea.Cmd {
	if msg.ok {
		signature := teamSignature(msg.state)
		if signature != m.teamSignature {
			m.teamState = msg.state
			m.teamSignature = signature
			m.sidebarKeyValid = false
		}
	}
	return m.teamTick()
}

// teamSignature summarizes a Team snapshot for cheap change detection.
func teamSignature(state agentteam.State) string {
	var sb strings.Builder
	sb.WriteString(state.TeamID)
	for _, member := range state.Members {
		fmt.Fprintf(&sb, "|m:%s:%s:%s", member.Name, member.Status, strings.Join(member.Diagnostics, ","))
	}
	for _, task := range state.Tasks {
		fmt.Fprintf(&sb, "|t:%s:%d:%s:%s", task.ID, task.Revision, task.Status, task.OwnerID)
	}
	return sb.String()
}

// teamInfo renders the Agent Team sidebar section. It stays empty until a
// teammate exists, so solo sessions are unchanged.
func (m *UI) teamInfo(width int) string {
	if len(m.teamState.Members) == 0 {
		return ""
	}
	if m.com == nil || m.com.Config() == nil || !m.com.Config().Options.GetAgentTeams() {
		return ""
	}
	t := m.com.Styles
	title := common.Section(t, t.Resource.Heading.Render("Agent Team"), width)

	lines := []string{teamMemberLine(t, "lead", agentteam.StatusRunning, width)}
	for _, member := range m.teamState.Members {
		lines = append(lines, teamMemberLine(t, member.Name, member.Status, width))
	}

	active := make([]agentteam.TaskView, 0, len(m.teamState.Tasks))
	for _, task := range agentteam.TaskViews(m.teamState) {
		if task.Status != agentteam.TaskDeleted {
			active = append(active, task)
		}
	}
	const maxTasks = 3
	for i, task := range active {
		if i == maxTasks {
			lines = append(lines, t.Resource.AdditionalText.Render(
				fmt.Sprintf("  +%d more", len(active)-maxTasks),
			))
			break
		}
		lines = append(lines, teamTaskLine(t, task, width))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, body))
}

func teamMemberLine(t *styles.Styles, name string, status agentteam.Status, width int) string {
	line := fmt.Sprintf("  %s %s", teamStatusGlyph(status), name)
	if status == agentteam.StatusFailed {
		line += " failed"
	}
	return t.Resource.AdditionalText.Render(ansi.Truncate(line, width, "…"))
}

func teamTaskLine(t *styles.Styles, task agentteam.TaskView, width int) string {
	line := fmt.Sprintf("  %s %s %s", teamTaskGlyph(task), task.ID, task.Subject)
	return t.Resource.AdditionalText.Render(ansi.Truncate(line, width, "…"))
}

func teamStatusGlyph(status agentteam.Status) string {
	switch status {
	case agentteam.StatusRunning, agentteam.StatusProvisioning:
		return "◐"
	case agentteam.StatusFailed:
		return "✗"
	default:
		return "○"
	}
}

func teamTaskGlyph(task agentteam.TaskView) string {
	switch {
	case task.Status == agentteam.TaskCompleted:
		return "✓"
	case task.Status == agentteam.TaskInProgress:
		return "◐"
	case task.Ready:
		return "○"
	default:
		return "!"
	}
}
