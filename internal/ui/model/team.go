package model

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

// teamRefreshInterval is how often the sidebar re-reads the durable Team
// state. The state file is small and written atomically.
const teamRefreshInterval = time.Second

// teamStatusMsg carries one Team snapshot read off the update loop.
type teamStatusMsg struct {
	state agentteam.State
	runs  []agentrun.Run
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

// agentRunsPath returns the durable agent run registry path.
func (m *UI) agentRunsPath() string {
	if m.com == nil || m.com.Config() == nil {
		return ""
	}
	dir := m.com.Config().Options.DataDirectory
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "agent-teams", "agents.json")
}

// teamTick schedules the next Team state read.
func (m *UI) teamTick() tea.Cmd {
	path := m.teamStatePath()
	runsPath := m.agentRunsPath()
	return tea.Tick(teamRefreshInterval, func(time.Time) tea.Msg {
		if path == "" {
			return teamStatusMsg{}
		}
		state, err := agentteam.LoadState(path)
		return teamStatusMsg{state: state, runs: agentrun.Load(runsPath), ok: err == nil}
	})
}

// handleTeamStatus stores a changed snapshot and invalidates the sidebar.
func (m *UI) handleTeamStatus(msg teamStatusMsg) tea.Cmd {
	if msg.ok {
		signature := teamSignature(msg.state)
		runsSignature := agentRunsSignature(msg.runs)
		if signature != m.teamSignature || runsSignature != m.agentRunsSignature {
			m.teamState = msg.state
			m.teamSignature = signature
			m.agentRuns = msg.runs
			m.agentRunsSignature = runsSignature
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
		fmt.Fprintf(&sb, "|t:%s:%d:%s:%s:%s", task.ID, task.Revision, task.Status, task.OwnerID, task.Subject)
	}
	return sb.String()
}

// agentRunsSignature summarizes the live agent registry for cheap change
// detection.
func agentRunsSignature(runs []agentrun.Run) string {
	var sb strings.Builder
	for _, run := range runs {
		fmt.Fprintf(&sb, "|a:%s:%s:%s", run.ID, run.Status, run.Name)
	}
	return sb.String()
}

// teamInfo renders the Agent Team sidebar section. It prefers the teammates
// spawned by the current session and falls back to the whole workspace roster,
// so switching sessions never hides a team that is still live.
func (m *UI) teamInfo(width int) string {
	if m.com == nil || m.com.Config() == nil || !m.com.Config().Options.GetAgentTeams() {
		return ""
	}
	if len(m.teamState.Members) == 0 {
		return ""
	}
	sessionID := m.currentSessionID()
	members := sessionMembers(m.teamState, sessionID)
	if len(members) == 0 {
		members = m.teamState.Members
	}
	owners := map[string]bool{"lead": true}
	for _, member := range members {
		owners[member.ID] = true
	}
	t := m.com.Styles
	title := common.Section(t, t.Resource.Heading.Render("▮ Agent Team"), width)

	busy := make(map[string]bool)
	for _, task := range agentteam.TaskViews(m.teamState) {
		if task.Status == agentteam.TaskInProgress && owners[task.OwnerID] {
			busy[task.OwnerName] = true
		}
	}
	lines := []string{teamMemberLine(t, "lead", agentteam.StatusRunning, busy["lead"], width)}
	for _, member := range members {
		lines = append(lines, teamMemberLine(t, member.Name, member.Status, busy[member.Name], width))
	}

	active, completed := teamTaskBuckets(m.teamState, owners)
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
	if completed > 0 {
		lines = append(lines, t.Resource.AdditionalText.Render(
			fmt.Sprintf("  ✓ %d completed", completed),
		))
	}
	if len(active) == 0 && completed == 0 {
		lines = append(lines, t.Resource.AdditionalText.Render("  no active tasks"))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, body))
}

// sessionMembers returns the teammates spawned by sessionID, including the
// teammates those teammates spawned. An empty session id matches nothing:
// before a session loads there is no team to scope to.
func sessionMembers(state agentteam.State, sessionID string) []agentteam.Member {
	if sessionID == "" {
		return nil
	}
	links := make([][2]string, 0, len(state.Members))
	for _, member := range state.Members {
		if member.ParentID != "" && member.SessionID != "" {
			links = append(links, [2]string{member.ParentID, member.SessionID})
		}
	}
	sessions := sessionTree(sessionID, links)
	members := make([]agentteam.Member, 0, len(state.Members))
	for _, member := range state.Members {
		if sessions[member.ParentID] {
			members = append(members, member)
		}
	}
	return members
}

// sessionAgentRuns returns the live agent runs spawned by sessionID,
// including runs started by agents that session spawned. An empty session id
// matches nothing.
func sessionAgentRuns(runs []agentrun.Run, sessionID string) []agentrun.Run {
	if sessionID == "" {
		return nil
	}
	links := make([][2]string, 0, len(runs))
	for _, run := range runs {
		if run.ParentSessionID != "" && run.SessionID != "" {
			links = append(links, [2]string{run.ParentSessionID, run.SessionID})
		}
	}
	sessions := sessionTree(sessionID, links)
	filtered := make([]agentrun.Run, 0, len(runs))
	for _, run := range runs {
		if sessions[run.ParentSessionID] {
			filtered = append(filtered, run)
		}
	}
	return filtered
}

// sessionTree returns sessionID plus every session reachable from it through
// parent→child links.
func sessionTree(sessionID string, links [][2]string) map[string]bool {
	sessions := map[string]bool{sessionID: true}
	for {
		changed := false
		for _, link := range links {
			if sessions[link[0]] && !sessions[link[1]] {
				sessions[link[1]] = true
				changed = true
			}
		}
		if !changed {
			return sessions
		}
	}
}

// agentsInfo renders the live agent section for the current session. It
// stays empty when this session has no running agent.
func (m *UI) agentsInfo(width int) string {
	if m.com == nil {
		return ""
	}
	sessionID := m.currentSessionID()
	runs := sessionAgentRuns(m.agentRuns, sessionID)
	if len(runs) == 0 {
		return ""
	}
	t := m.com.Styles
	title := common.Section(t, t.Resource.Heading.Render("▮ Agents"), width)
	lines := make([]string, 0, len(runs))
	for _, run := range runs {
		lines = append(lines, agentRunLine(t, run, width))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, body))
}

func agentRunLine(t *styles.Styles, run agentrun.Run, width int) string {
	line := fmt.Sprintf("  %s %s", agentRunGlyph(run), run.Name)
	return t.Resource.AdditionalText.Render(ansi.Truncate(line, width, "…"))
}

func agentRunGlyph(run agentrun.Run) string {
	switch run.Status {
	case agentrun.StatusRunning:
		return "◐"
	case agentrun.StatusFailed:
		return "✗"
	default:
		return "✓"
	}
}

// teamTaskBuckets splits the current session's tasks into the live ones the
// sidebar lists, in priority order, and a count of completed tasks. Deleted
// tasks and tasks owned by other sessions are dropped.
func teamTaskBuckets(state agentteam.State, owners map[string]bool) (active []agentteam.TaskView, completed int) {
	for _, task := range agentteam.TaskViews(state) {
		if !owners[task.OwnerID] {
			continue
		}
		switch task.Status {
		case agentteam.TaskCompleted:
			completed++
		case agentteam.TaskDeleted:
		default:
			active = append(active, task)
		}
	}
	slices.SortStableFunc(active, func(a, b agentteam.TaskView) int {
		return teamTaskRank(a) - teamTaskRank(b)
	})
	return active, completed
}

// teamTaskRank orders live tasks: work in flight first, then ready work,
// then everything still blocked.
func teamTaskRank(task agentteam.TaskView) int {
	switch {
	case task.Status == agentteam.TaskInProgress:
		return 0
	case task.Ready:
		return 1
	default:
		return 2
	}
}

func teamMemberLine(t *styles.Styles, name string, status agentteam.Status, hasActiveTask bool, width int) string {
	line := fmt.Sprintf("  %s %s", teamStatusGlyph(status, hasActiveTask), name)
	if status == agentteam.StatusFailed {
		line += " failed"
	}
	return t.Resource.AdditionalText.Render(ansi.Truncate(line, width, "…"))
}

func teamTaskLine(t *styles.Styles, task agentteam.TaskView, width int) string {
	line := fmt.Sprintf("  %s %s %s", teamTaskGlyph(task), task.ID, task.Subject)
	if task.OwnerName != "" {
		line += " · " + task.OwnerName
	}
	return t.Resource.AdditionalText.Render(ansi.Truncate(line, width, "…"))
}

func teamStatusGlyph(status agentteam.Status, hasActiveTask bool) string {
	switch status {
	case agentteam.StatusRunning, agentteam.StatusProvisioning:
		return "◐"
	case agentteam.StatusFailed:
		return "✗"
	default:
		if hasActiveTask {
			return "!"
		}
		return "✓"
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
