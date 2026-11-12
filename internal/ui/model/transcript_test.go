package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/stretchr/testify/require"
)

// transcriptItemIDs returns the IDs of the given items, in order.
func transcriptItemIDs(items []chat.MessageItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID()
	}
	return ids
}

func TestProjectTranscriptMessagesTracksDerivedItems(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	// Item construction resolves the working directory through the
	// workspace, which newTestUI leaves nil.
	u.com.Workspace = &testWorkspace{}
	com := u.com
	msgs := []message.Message{
		{ID: "user-1", SessionID: "child", Role: message.User, CreatedAt: 10, Parts: []message.ContentPart{message.TextContent{Text: "inspect"}}},
		{ID: "assistant-1", SessionID: "child", Role: message.Assistant, CreatedAt: 11, Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "checking", FinishedAt: 12},
			message.ToolCall{ID: "tool-1", Name: "view", Input: `{}`, Finished: true},
		}},
		{ID: "tool-result-1", SessionID: "child", Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{ToolCallID: "tool-1", Content: "contents"}}},
	}

	projection := projectTranscriptMessages(com, msgs)

	require.Equal(t, int64(10), projection.lastUserMessageTime)
	require.Contains(t, projection.itemIDsByMessage["assistant-1"], "assistant-1")
	require.Contains(t, projection.itemIDsByMessage["assistant-1"], "tool-1")
	require.NotEmpty(t, projection.items)
}

// TestProjectTranscriptMessagesAssistantInfo pins that a projected transcript
// carries the assistant info footer exactly when ShouldShowAssistantInfo says
// the turn qualifies for one, matching what the live transcript appends.
func TestProjectTranscriptMessagesAssistantInfo(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = &testWorkspace{}
	com := u.com

	finished := message.Message{
		ID:        "assistant-done",
		SessionID: "child",
		Role:      message.Assistant,
		CreatedAt: 11,
		Parts: []message.ContentPart{
			message.TextContent{Text: "done"},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 20},
		},
	}
	unfinished := message.Message{
		ID:        "assistant-running",
		SessionID: "child",
		Role:      message.Assistant,
		CreatedAt: 11,
		Parts:     []message.ContentPart{message.ReasoningContent{Thinking: "checking"}},
	}

	require.True(t, chat.ShouldShowAssistantInfo(&finished))
	require.False(t, chat.ShouldShowAssistantInfo(&unfinished))

	finishedProjection := projectTranscriptMessages(com, []message.Message{finished})
	finishedIDs := transcriptItemIDs(finishedProjection.items)
	infoID := chat.AssistantInfoID(finished.ID)
	require.Contains(t, finishedIDs, infoID)
	require.Contains(t, finishedProjection.itemIDsByMessage[finished.ID], infoID,
		"the assistant info item is owned by the message it footers")

	unfinishedProjection := projectTranscriptMessages(com, []message.Message{unfinished})
	require.NotContains(t, transcriptItemIDs(unfinishedProjection.items), chat.AssistantInfoID(unfinished.ID))
	require.NotContains(t, unfinishedProjection.itemIDsByMessage[unfinished.ID], chat.AssistantInfoID(unfinished.ID))
}

// TestProjectTranscriptMessagesSubagentReportAdvancesLastUser pins main
// reload semantics: every user-role message advances the last-user mark,
// including a message that carries a subagent report. The live path has its
// own report handling, but reloading history must keep the old behavior.
func TestProjectTranscriptMessagesSubagentReportAdvancesLastUser(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.com.Workspace = &testWorkspace{}
	com := u.com

	msgs := []message.Message{
		{ID: "user-1", SessionID: "child", Role: message.User, CreatedAt: 10, Parts: []message.ContentPart{message.TextContent{Text: "inspect"}}},
		{ID: "report-1", SessionID: "child", Role: message.User, CreatedAt: 42, Parts: []message.ContentPart{
			message.SubAgentReport{Label: "review", Output: "done"},
		}},
	}

	projection := projectTranscriptMessages(com, msgs)

	require.Equal(t, int64(42), projection.lastUserMessageTime,
		"a subagent report on a user-role message still advances the last-user mark")
}

func TestChatSelectID(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.chat.SetMessages(
		testMessageItem{id: "first", text: "a"},
		testMessageItem{id: "second", text: "b"},
		testMessageItem{id: "third", text: "c"},
	)

	require.Equal(t, "", u.chat.SelectedID(), "nothing is selected before SelectID")

	u.chat.SelectID("second")
	require.Equal(t, 1, u.chat.Selected())
	require.Equal(t, "second", u.chat.SelectedID())

	u.chat.SelectID("third")
	require.Equal(t, "third", u.chat.SelectedID())

	u.chat.SelectID("missing")
	require.Equal(t, "third", u.chat.SelectedID(), "an unknown ID leaves the selection alone")
}

func TestChatRestoreScrollPosition(t *testing.T) {
	t.Parallel()
	u := newTestUI()
	u.chat.SetMessages(
		&mutableMessageItem{id: "a", lines: 5},
		&mutableMessageItem{id: "b", lines: 5},
	)
	u.chat.SetSize(40, 4)
	u.chat.ScrollToBottom()
	require.True(t, u.chat.Follow())

	u.chat.RestoreScrollPosition(1, 2)

	require.False(t, u.chat.Follow(), "restoring a scroll position must stop auto-scrolling")
	idx, line := u.chat.ScrollPosition()
	require.Equal(t, 1, idx)
	require.Equal(t, 2, line)
}
