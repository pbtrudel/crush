package chat

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/stretchr/testify/require"
)

// activityTestItems builds the transcript run a collapsed activity row is
// meant to stand in for: three edits, two reads and two bash calls, all
// finished successfully unless the caller marks one as failed.
func activityTestItems(sty *styles.Styles, failIndex int) []MessageItem {
	type spec struct {
		name   string
		input  string
		result string
	}
	specs := []spec{
		{name: "edit", input: `{"file_path":"main.go","old_string":"a","new_string":"b"}`, result: "ok"},
		{name: "edit", input: `{"file_path":"api.go","old_string":"c","new_string":"d"}`, result: "ok"},
		{name: "edit", input: `{"file_path":"ui.go","old_string":"e","new_string":"f"}`, result: "ok"},
		{name: "view", input: `{"file_path":"README.md"}`, result: "contents"},
		{name: "view", input: `{"file_path":"go.mod"}`, result: "contents"},
		{name: "bash", input: `{"command":"go build ./..."}`, result: "ok"},
		{name: "bash", input: `{"command":"go test ./..."}`, result: "ok"},
	}

	items := make([]MessageItem, len(specs))
	for i, s := range specs {
		id := "tool-" + s.name + "-" + string(rune('a'+i))
		tc := message.ToolCall{ID: id, Name: s.name, Input: s.input, Finished: true}
		result := &message.ToolResult{ToolCallID: id, Name: s.name, Content: s.result}
		if i == failIndex {
			result.IsError = true
			result.Content = "exit status 1"
		}
		items[i] = NewToolMessageItem(sty, "msg-1", tc, result, false, "/tmp")
	}
	return items
}

// TestActivityGroupCollapsedRendersSummaryRow covers the opt-in state: one
// run of tool calls between assistant text becomes a single row.
func TestActivityGroupCollapsedRendersSummaryRow(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	grouped := GroupToolActivity(activityTestItems(&sty, -1))
	require.Len(t, grouped, 1, "consecutive tool calls must collapse into one item")

	group, ok := grouped[0].(*ActivityGroupItem)
	require.True(t, ok, "expected an ActivityGroupItem, got %T", grouped[0])
	require.False(t, group.Expanded(), "a group starts collapsed")

	got := stripANSI(group.Render(80))
	require.Contains(t, got, "Ran 7 tools", "the row must say how many tools ran")
	require.Contains(t, got, "3 edits", "the row must break the run down by tool")
	require.Contains(t, got, "2 reads")
	require.Contains(t, got, "2 bash")
	golden.RequireEqual(t, []byte(got))
}

// TestActivityGroupExpandedRendersChildren covers the expanded state: the
// summary row stays and every tool call renders underneath it.
func TestActivityGroupExpandedRendersChildren(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	grouped := GroupToolActivity(activityTestItems(&sty, -1))
	group, ok := grouped[0].(*ActivityGroupItem)
	require.True(t, ok)

	require.True(t, group.ToggleExpanded())
	require.True(t, group.Expanded())

	got := stripANSI(group.Render(80))
	require.Contains(t, got, "Ran 7 tools")
	require.Contains(t, got, "main.go", "expanding must reveal the individual tool calls")
	require.Contains(t, got, "go test ./...")
	golden.RequireEqual(t, []byte(got))
}

// TestActivityGroupAutoExpandsOnError covers the escape hatch: a failing
// tool call inside a collapsed run must never be hidden.
func TestActivityGroupAutoExpandsOnError(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	grouped := GroupToolActivity(activityTestItems(&sty, 5))
	group, ok := grouped[0].(*ActivityGroupItem)
	require.True(t, ok)
	require.False(t, group.Expanded(), "the stored state stays collapsed")

	got := stripANSI(group.Render(80))
	require.Contains(t, got, "go build ./...", "a failed tool call must stay visible")
	require.Contains(t, got, "1 failed", "the summary row must call out the failure")
	golden.RequireEqual(t, []byte(got))
}

// TestActivityGroupAutoExpandsOnAwaitingPermission is the other escape
// hatch: a tool call blocked on a permission prompt must stay visible, or
// the transcript shows a collapsed run while the agent waits on the user.
func TestActivityGroupAutoExpandsOnAwaitingPermission(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	blocked := NewToolMessageItem(&sty, "msg-1",
		message.ToolCall{ID: "blocked", Name: "bash", Input: `{"command":"rm -rf /tmp/x"}`},
		nil, false, "/tmp")
	blocked.SetStatus(ToolStatusAwaitingPermission)

	items := append([]MessageItem{blocked}, activityTestItems(&sty, -1)...)
	grouped := GroupToolActivity(items)
	group, ok := grouped[0].(*ActivityGroupItem)
	require.True(t, ok)

	got := stripANSI(group.Render(80))
	require.Contains(t, got, "rm -rf /tmp/x", "a tool awaiting permission must stay visible")
}

// TestGroupToolActivityKeepsSurroundingItems guards the boundaries: only
// runs of tool calls collapse, and a lone tool call is left alone.
func TestGroupToolActivityKeepsSurroundingItems(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	tools := activityTestItems(&sty, -1)

	assistant := NewAssistantMessageItem(&sty, &message.Message{
		ID:    "msg-1",
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: "Working on it."}},
	})

	items := []MessageItem{assistant}
	items = append(items, tools...)
	items = append(items, assistant)

	grouped := GroupToolActivity(items)
	require.Len(t, grouped, 3, "assistant text on both sides must stay its own item")
	require.Same(t, assistant, grouped[0])
	require.Same(t, assistant, grouped[2])
	require.IsType(t, &ActivityGroupItem{}, grouped[1])

	lone := GroupToolActivity([]MessageItem{tools[0]})
	require.Len(t, lone, 1)
	require.Same(t, tools[0], lone[0], "a single tool call must not become a group")
}

// TestActivityGroupChildLookup covers the ownership contract the chat index
// relies on to keep updating tool results inside a collapsed run.
func TestActivityGroupChildLookup(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	tools := activityTestItems(&sty, -1)
	group := GroupToolActivity(tools)[0].(*ActivityGroupItem)

	require.Len(t, group.ChildItems(), len(tools))

	// The grouped item answers for its first child, so the list keeps a
	// stable identity across reloads.
	require.Equal(t, ActivityGroupID(tools[0].ID()), group.ID())

	found := false
	for _, child := range group.ChildItems() {
		if child.ID() == tools[3].ID() {
			found = true
		}
	}
	require.True(t, found, "every grouped tool call must stay reachable by id")
}

// TestActivityGroupSummaryDurations guards the timing suffix: a group only
// claims a duration once it has one, so loaded sessions show a bare count.
func TestActivityGroupSummaryDurations(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	group := GroupToolActivity(activityTestItems(&sty, -1))[0].(*ActivityGroupItem)

	require.NotContains(t, stripANSI(group.Render(80)), " in ",
		"a group built from stored messages has no timing to claim")

	group.startedAt = time.Unix(0, 0)
	group.endedAt = time.Unix(41, 0)
	require.Contains(t, stripANSI(group.Render(80)), "in 41s")
}

// TestActivityGroupEnterExpands covers the keyboard affordance from the
// chat's key routing: enter on the selected row toggles it.
func TestActivityGroupEnterExpands(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	group := GroupToolActivity(activityTestItems(&sty, -1))[0].(*ActivityGroupItem)

	handled, _ := group.HandleKeyEvent(tea.KeyPressMsg{Text: "enter", Code: tea.KeyEnter})
	require.True(t, handled, "enter must be handled by the group row")
	require.True(t, group.Expanded())

	handled, _ = group.HandleKeyEvent(tea.KeyPressMsg{Text: "x", Code: 'x'})
	require.False(t, handled, "unrelated keys must not be swallowed")
}
