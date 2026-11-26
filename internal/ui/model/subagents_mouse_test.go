package model

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/chat"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func subagentDispatchItem(u *UI, messageID, callID, label string) chat.MessageItem {
	call := message.ToolCall{
		ID:       callID,
		Name:     agent.AgentDispatchToolName,
		Input:    fmt.Sprintf(`{"prompt":"look into it","label":%q}`, label),
		Finished: true,
	}
	return chat.NewToolMessageItem(u.com.Styles, messageID, call, &message.ToolResult{
		ToolCallID: callID,
		Content:    "Started subagent.",
	}, false, u.com.Workspace.WorkingDir())
}

func TestSubagentsHitMapAlignsWithContent(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	u.chat.SetMessages(
		subagentDispatchItem(u, "msg-a", "call-a", "alpha-sub"),
		subagentDispatchItem(u, "msg-b", "call-b", "beta-sub"),
	)
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()

	require.NotEmpty(t, u.sidebarAgentHits)
	for label, wantSession := range map[string]string{
		"alpha-sub": "nested:msg-a:call-a",
		"beta-sub":  "nested:msg-b:call-b",
	} {
		line := sidebarAgentLine(t, u, label)
		hit, ok := u.sidebarAgentHits[line]
		require.Truef(t, ok, "line %d (%s) must map to a subagent", line, label)
		require.Equal(t, wantSession, hit.SessionID)
	}
}

func TestSubagentsClickOpensViewer(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	u.chat.SetMessages(subagentDispatchItem(u, "msg-a", "call-a", "alpha-sub"))
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()

	line := sidebarAgentLine(t, u, "alpha-sub")
	contentMinY := u.layout.sidebar.Min.Y + lipgloss.Height(u.sidebarDrawLogo)
	y := contentMinY + (line - u.sidebarOffset)

	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: u.layout.sidebar.Min.X, Y: y, Button: uv.MouseLeft}))

	require.NotNil(t, u.agentViewer)
	require.Equal(t, "nested:msg-a:call-a", u.agentViewer.descriptor.SessionID)
}

func TestSubagentsNonAgentClickDoesNothing(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	u.chat.SetMessages(subagentDispatchItem(u, "msg-a", "call-a", "alpha-sub"))
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()

	// The first content line is the session title, not a subagent row.
	contentMinY := u.layout.sidebar.Min.Y + lipgloss.Height(u.sidebarDrawLogo)
	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: u.layout.sidebar.Min.X, Y: contentMinY, Button: uv.MouseLeft}))

	require.Nil(t, u.agentViewer)
}

func TestSubagentsClickRespectsScrollOffset(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	items := make([]chat.MessageItem, 40)
	for i := range items {
		items[i] = subagentDispatchItem(u, fmt.Sprintf("msg-%d", i), fmt.Sprintf("call-%d", i), fmt.Sprintf("sub-%02d", i))
	}
	u.chat.SetMessages(items...)
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()
	require.True(t, u.sidebarScrollable)

	line := sidebarAgentLine(t, u, "sub-25")
	u.sidebarOffset = min(line, u.sidebarMaxOffsetVal)
	row := line - u.sidebarOffset
	require.Less(t, row, u.sidebarContentHeight, "subagent row must be visible")
	contentMinY := u.layout.sidebar.Min.Y + lipgloss.Height(u.sidebarDrawLogo)

	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: u.layout.sidebar.Min.X, Y: contentMinY + row, Button: uv.MouseLeft}))

	require.NotNil(t, u.agentViewer)
	require.Equal(t, "nested:msg-25:call-25", u.agentViewer.descriptor.SessionID)
}

// TestSubAgentsInfoExcludesTruncationHint pins that only rendered status rows
// are clickable: when truncateRows drops rows behind an "…and N more" hint,
// the hint line and the dropped rows are not mapped.
func TestSubAgentsInfoExcludesTruncationHint(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	statuses := []chat.DispatchStatus{
		{Label: "one", Running: true, MessageID: "m1", ToolCallID: "c1"},
		{Label: "two", Running: true, MessageID: "m2", ToolCallID: "c2"},
		{Label: "three", Running: true, MessageID: "m3", ToolCallID: "c3"},
	}
	section, hits := u.subAgentsInfo(statuses, 40, 2, true)

	byLine := make(map[int]string, len(hits))
	for _, hit := range hits {
		byLine[hit.line] = hit.descriptor.SessionID
	}
	lines := strings.Split(section, "\n")
	hintSeen := false
	for i, line := range lines {
		switch {
		case strings.Contains(line, "…and 2 more"):
			hintSeen = true
			_, mapped := byLine[i]
			require.Falsef(t, mapped, "the truncation hint line %d must not be clickable", i)
		case strings.Contains(line, "one"):
			require.Equal(t, "nested:m1:c1", byLine[i])
		case strings.Contains(line, "two"), strings.Contains(line, "three"):
			_, mapped := byLine[i]
			require.Falsef(t, mapped, "dropped row %d must not be clickable", i)
		}
	}
	require.True(t, hintSeen, "the section must carry the truncation hint")
}

// TestSubagentsClickWrappedRowSecondLine pins the multi-line mapping: a row
// that spans two lines maps both, so clicking its second line opens the same
// subagent.
func TestSubagentsClickWrappedRowSecondLine(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	u.chat.SetMessages(subagentDispatchItem(u, "msg-w", "call-w", "a-very-long-subagent-name-that-wraps-over"))
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()

	const sessionID = "nested:msg-w:call-w"
	secondLine := -1
	for l, d := range u.sidebarAgentHits {
		if d.SessionID != sessionID {
			continue
		}
		if d2, ok := u.sidebarAgentHits[l+1]; ok && d2.SessionID == sessionID {
			secondLine = l + 1
			break
		}
	}
	require.GreaterOrEqual(t, secondLine, 0, "a wrapped row must map two consecutive lines")

	u.sidebarOffset = min(secondLine, u.sidebarMaxOffsetVal)
	row := secondLine - u.sidebarOffset
	contentMinY := u.layout.sidebar.Min.Y + lipgloss.Height(u.sidebarDrawLogo)

	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: u.layout.sidebar.Min.X, Y: contentMinY + row, Button: uv.MouseLeft}))

	require.NotNil(t, u.agentViewer)
	require.Equal(t, sessionID, u.agentViewer.descriptor.SessionID)
}

// TestSubAgentsInfoNonPositiveMaxItemsHasNoHits pins that a non-positive
// maxItems renders no rows and therefore yields no clickable hits.
func TestSubAgentsInfoNonPositiveMaxItemsHasNoHits(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	statuses := []chat.DispatchStatus{
		{Label: "one", Running: true, MessageID: "m1", ToolCallID: "c1"},
	}
	for _, maxItems := range []int{0, -1} {
		_, hits := u.subAgentsInfo(statuses, 40, maxItems, true)
		require.Emptyf(t, hits, "maxItems %d must yield no hits", maxItems)
	}
}

// TestSubagentsHitsClearedWhenSectionAbsent pins that rebuilding the sidebar
// without subagents leaves no stale subagent entries in the hit-map.
func TestSubagentsHitsClearedWhenSectionAbsent(t *testing.T) {
	t.Parallel()

	u := newSidebarMouseUI(t)
	u.chat.SetMessages(subagentDispatchItem(u, "msg-a", "call-a", "alpha-sub"))
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()

	const sessionID = "nested:msg-a:call-a"
	hasSub := false
	for _, d := range u.sidebarAgentHits {
		if d.SessionID == sessionID {
			hasSub = true
		}
	}
	require.True(t, hasSub, "the subagent must be mapped while present")

	u.chat.SetMessages()
	u.sidebarKeyValid = false
	u.updateSidebarScrollState()
	for _, d := range u.sidebarAgentHits {
		require.NotEqual(t, sessionID, d.SessionID, "stale subagent hit must be cleared on rebuild")
	}
}
