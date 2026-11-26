package model

import (
	"fmt"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/ui/anim"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
)

// subAgentSpinnerFrames animates the running marker. The sidebar is rebuilt
// on every animation tick while a dispatch item is spinning, so deriving the
// frame from the clock is enough to make it move without its own ticker.
var subAgentSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// subAgentSpinner returns the frame for the given moment. The glyph period
// is a whole number of animation frames so every glyph holds equally long.
func subAgentSpinner(now time.Time) string {
	frameEvery := 2 * anim.FrameInterval()
	i := (now.UnixNano() / int64(frameEvery)) % int64(len(subAgentSpinnerFrames))
	return subAgentSpinnerFrames[i]
}

// subAgentIdleRetention is how long a finished subagent stays in the
// sidebar. Without it the section only ever grows, since every dispatch
// leaves a row behind for the rest of the session. A retired subagent is
// hidden, not gone: agent_send can still reach it.
const subAgentIdleRetention = 5 * time.Minute

// subAgentStatuses reads the dispatched subagents out of the chat itself.
// Every dispatch is a tool call item in the transcript, and the subagent's
// tool calls already stream in as that item's nested tools, so the chat
// already knows both who is running and what they are doing.
//
// Subagents that finished more than [subAgentIdleRetention] ago are left
// out. Nothing repaints on a timer once every subagent is done, so a row
// retires on the next render rather than exactly on the deadline.
func (m *UI) subAgentStatuses() []chat.DispatchStatus {
	var statuses []chat.DispatchStatus
	now := time.Now()
	for i := range m.chat.Len() {
		item, ok := m.chat.MessageItemAt(i).(*chat.AgentToolMessageItem)
		if !ok {
			continue
		}
		status, ok := item.DispatchStatus()
		if !ok {
			continue
		}
		if !subAgentStatusRetained(status, now) {
			continue
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// subAgentStatusRetained reports whether a dispatched subagent is still
// worth showing. A running subagent always is; a finished one retires
// [subAgentIdleRetention] after its report landed.
func subAgentStatusRetained(status chat.DispatchStatus, now time.Time) bool {
	if status.Running || status.Finished.IsZero() {
		return true
	}
	return now.Sub(status.Finished) <= subAgentIdleRetention
}

// runningSubAgents counts the subagents still working.
func (m *UI) runningSubAgents() int {
	var n int
	for _, s := range m.subAgentStatuses() {
		if s.Running {
			n++
		}
	}
	return n
}

// subAgentRow renders one subagent's line: a spinner and elapsed time while
// it works; once finished, the icon's color alone says so.
func (m *UI) subAgentRow(status chat.DispatchStatus, width int) string {
	t := m.com.Styles
	now := time.Now()

	icon := t.Resource.WorkingIcon.Render(subAgentSpinner(now))
	state := "starting"
	switch {
	case !status.Running:
		icon, state = t.Resource.OfflineIcon.String(), ""
	case !status.Started.IsZero():
		state = common.FormatDuration(common.ElapsedSince(status.Started, now))
	}
	var extra string
	if state != "" {
		extra = t.Resource.RowDescBase.Foreground(t.Resource.DefaultDescFg).Render(state)
	}

	// The label is the description so Status truncates it, not the state.
	first, label := agent.SubagentDisplayName(status.Label)
	title := t.Tool.AgentName.Render(first)
	if first == "" {
		title, label = t.Tool.AgentLabel.Render(label), ""
	}
	return common.Status(t, common.StatusOpts{
		Icon:             icon,
		Title:            title,
		Description:      label,
		DescriptionColor: t.Resource.DefaultTitleFg,
		ExtraContent:     extra,
	}, width)
}

// subAgentsInfo renders the dispatched subagent section. It renders empty
// when nothing has been dispatched, so it costs no sidebar space until it has
// something to say. The returned hits map each rendered row to its content
// line so a click can open that subagent; the truncation hint is never mapped.
func (m *UI) subAgentsInfo(statuses []chat.DispatchStatus, width, maxItems int, isSection bool) (string, []sidebarAgentHit) {
	if len(statuses) == 0 {
		return "", nil
	}

	t := m.com.Styles
	title := t.Resource.Heading.Render("Subagents")
	if isSection {
		title = common.Section(t, title, width)
	}

	rows := make([]string, 0, len(statuses))
	for _, status := range statuses {
		// Wrap each row to the section width up front so its measured height
		// matches the lines it occupies once rendered, including wrapped rows.
		rows = append(rows, lipgloss.NewStyle().Width(width).Render(m.subAgentRow(status, width)))
	}

	// truncateRows keeps the first maxItems-1 rows plus a hint when they
	// overflow, and nothing when maxItems is non-positive. Only the kept
	// status rows are clickable, never the hint.
	kept := len(statuses)
	switch {
	case maxItems <= 0:
		kept = 0
	case len(statuses) > maxItems:
		kept = maxItems - 1
	}
	var hits []sidebarAgentHit
	if m.com.Workspace != nil {
		rootID := m.currentSessionID()
		rowLine := lipgloss.Height(title) + 1
		for i := 0; i < kept; i++ {
			status := statuses[i]
			height := lipgloss.Height(rows[i])
			if status.MessageID != "" && status.ToolCallID != "" {
				if sessionID := m.com.Workspace.CreateAgentToolSessionID(status.MessageID, status.ToolCallID); sessionID != "" {
					descriptor := dialog.AgentDescriptor{
						SessionID:       sessionID,
						ParentSessionID: rootID,
						Name:            status.Label,
						Kind:            agentrun.KindSubagent,
						Status:          agentRunStatusFromDispatch(status),
						Model:           status.Model,
						ModelProvider:   status.ModelProvider,
						StartedAt:       status.Started,
						FinishedAt:      status.Finished,
					}
					// A row may span several lines; map each so a click
					// anywhere on it opens the subagent.
					for l := rowLine; l < rowLine+height; l++ {
						hits = append(hits, sidebarAgentHit{line: l, descriptor: descriptor})
					}
				}
			}
			rowLine += height
		}
	}

	rows = truncateRows(rows, maxItems, t.Resource.AdditionalText)

	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf(
		"%s\n\n%s", title, lipgloss.JoinVertical(lipgloss.Left, rows...),
	)), hits
}
