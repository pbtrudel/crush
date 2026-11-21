package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/stretchr/testify/require"
)

func verbosityTestTools(u *UI) []chat.MessageItem {
	specs := []struct {
		name  string
		input string
	}{
		{name: "view", input: `{"file_path":"a.go"}`},
		{name: "view", input: `{"file_path":"b.go"}`},
		{name: "bash", input: `{"command":"go build ./..."}`},
	}
	items := make([]chat.MessageItem, len(specs))
	for i, s := range specs {
		id := s.name + "-" + string(rune('a'+i))
		tc := message.ToolCall{ID: id, Name: s.name, Input: s.input, Finished: true}
		items[i] = chat.NewToolMessageItem(u.com.Styles, "msg-1", tc,
			&message.ToolResult{ToolCallID: id, Name: s.name, Content: "ok"}, false, "/tmp")
	}
	return items
}

// TestChatVerbosityDefaultsToFullTranscript guards the default: with no
// verbosity set, every tool call keeps its own row, exactly as before the
// option existed.
func TestChatVerbosityDefaultsToFullTranscript(t *testing.T) {
	t.Parallel()

	var unset config.TUIOptions
	require.Equal(t, config.TUIVerbosityHigh, unset.GetVerbosity(),
		"an unset option must mean the current behavior")

	u := newTestUI()

	items := verbosityTestTools(u)
	u.chat.SetMessages(items...)
	require.Equal(t, len(items), u.chat.Len(), "high verbosity must not group anything")
}

// TestChatVerbosityLowGroupsToolRuns covers the opt-in: a run of tool calls
// loaded from storage collapses into one row, and every tool call stays
// reachable by id so live results still land on the right item.
func TestChatVerbosityLowGroupsToolRuns(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetVerbosity(config.TUIVerbosityLow)

	items := verbosityTestTools(u)
	u.chat.SetMessages(items...)
	require.Equal(t, 1, u.chat.Len(), "a run of tool calls must collapse into one row")

	for _, item := range items {
		require.Same(t, item, u.chat.MessageItem(item.ID()),
			"a grouped tool call must resolve to its own item, not the group")
	}
}

// TestChatVerbosityLowGroupsAppendedToolCalls covers the live path: tool
// calls appended after the group was created join it instead of opening a
// second row.
func TestChatVerbosityLowGroupsAppendedToolCalls(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetVerbosity(config.TUIVerbosityLow)

	items := verbosityTestTools(u)
	u.chat.AppendMessages(items[0])
	u.chat.AppendMessages(items[1:]...)
	require.Equal(t, 1, u.chat.Len(), "appended tool calls must join the open row")
}

// TestNewChatReadsVerbosityFromConfig covers the wiring: the chat reads the
// option once, when it is built, so no call site has to pass it down.
func TestNewChatReadsVerbosityFromConfig(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Options: &config.Options{
		TUI: &config.TUIOptions{Verbosity: config.TUIVerbosityLow},
	}}
	com := common.DefaultCommon(nil)
	com.Workspace = &testWorkspace{cfg: cfg}

	require.True(t, NewChat(com, config.ScrollbarDefault).groupsActivity())

	cfg.Options.TUI.Verbosity = ""
	require.False(t, NewChat(com, config.ScrollbarDefault).groupsActivity(),
		"an unset option keeps the full transcript")
}
