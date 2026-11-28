package webtest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

var m2Clock = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

// TestSeedM2GivesTheChatEveryView pins the fixture the M2 browser specs
// drive: one call per tool view, each answered but the canceled one, and a
// subagent session under the agent call.
func TestSeedM2GivesTheChatEveryView(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedM2(f, m2Clock)

	msgs, err := f.ListMessages(t.Context(), M2ChatSession)
	require.NoError(t, err)
	names := make(map[string]bool)
	answered := make(map[string]bool)
	for _, m := range msgs {
		for _, call := range m.ToolCalls() {
			names[call.Name] = true
		}
		for _, result := range m.ToolResults() {
			answered[result.ToolCallID] = true
		}
	}
	for _, name := range []string{"bash", "view", "edit", "grep", "glob", "fetch", "todos", "mcp_github_search_issues", "agent"} {
		require.True(t, names[name], "the chat session has no %s call", name)
	}
	require.True(t, answered["tc-bash"])
	require.False(t, answered["tc-slow"], "the canceled call must stay unanswered")

	child, err := f.GetSession(t.Context(), M2ChildSession)
	require.NoError(t, err)
	require.Equal(t, M2ChatSession, child.ParentSessionID)
	childMsgs, err := f.ListMessages(t.Context(), M2ChildSession)
	require.NoError(t, err)
	require.NotEmpty(t, childMsgs)

	chat, err := f.GetSession(t.Context(), M2ChatSession)
	require.NoError(t, err)
	require.Equal(t, int64(42000), chat.PromptTokens)
	require.InDelta(t, 0.1234, chat.Cost, 1e-9)
}

// TestSeedM2LongSessionPagesAndSettlesNothing pins the long session: more
// messages than the first page, every assistant turn finished, so the
// fake's settle step adds no interrupted notice to the history.
func TestSeedM2LongSessionPagesAndSettlesNothing(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedM2(f, m2Clock)

	page, hasMore, err := f.ListMessagesPage(t.Context(), M2LongSession, "", 100)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, page, 100)
	all, err := f.ListMessages(t.Context(), M2LongSession)
	require.NoError(t, err)
	require.Len(t, all, M2LongMessages)
	for _, m := range all {
		if m.Role == message.Assistant {
			require.True(t, m.IsFinished(), m.ID)
			require.Equal(t, message.FinishReasonEndTurn, m.FinishReason(), m.ID)
		}
	}
}

// TestPlayM2StreamGrowsOneReplyInPlace pins the stream: one message id,
// growing on every frame, then finished, the session idle and the run
// complete event the gateway recomputes the status on.
func TestPlayM2StreamGrowsOneReplyInPlace(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedM2(f, m2Clock)
	require.True(t, f.AgentIsSessionBusy(M2StreamSession))
	sub := f.SubscribeEvents(t.Context())

	PlayM2Stream(t.Context(), f, m2Clock, time.Millisecond)

	var lengths []int
	completed := false
	for len(sub) > 0 {
		switch ev := (<-sub).(type) {
		case pubsub.Event[message.Message]:
			require.Equal(t, M2StreamMessage, ev.Payload.ID)
			lengths = append(lengths, len(ev.Payload.Content().Text))
		case pubsub.Event[notify.RunComplete]:
			completed = true
		}
	}
	require.Len(t, lengths, M2StreamLines+1)
	require.True(t, slices.IsSorted(lengths))
	require.True(t, completed)
	require.False(t, f.AgentIsSessionBusy(M2StreamSession))

	msgs, err := f.ListMessages(t.Context(), M2StreamSession)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "the stream rewrites its reply instead of adding one")
	require.True(t, msgs[1].IsFinished())
}

// TestPlayM2StreamStopsOnCancel pins that a stream stopped by the gateway
// shutdown leaves the reply where it was, never finishes it and never
// blocks the caller.
func TestPlayM2StreamStopsOnCancel(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedM2(f, m2Clock)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	PlayM2Stream(ctx, f, m2Clock, time.Hour)

	msgs, err := f.ListMessages(t.Context(), M2StreamSession)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.False(t, msgs[1].IsFinished())
	require.True(t, f.AgentIsSessionBusy(M2StreamSession))
}

func TestUpsertMessageReplacesByID(t *testing.T) {
	t.Parallel()
	f := NewFake()
	f.AddMessage(message.Message{ID: "x", SessionID: "s1", Parts: []message.ContentPart{message.TextContent{Text: "a"}}})
	f.UpsertMessage(message.Message{ID: "x", SessionID: "s1", Parts: []message.ContentPart{message.TextContent{Text: "ab"}}})
	f.UpsertMessage(message.Message{ID: "y", SessionID: "s1"})
	msgs, err := f.ListMessages(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "ab", msgs[0].Content().Text)
}
