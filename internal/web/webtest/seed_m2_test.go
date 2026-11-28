package webtest

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

var m2Clock = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

// nonEmptyLines counts the lines a text block draws: blank separator lines
// add height to the Markdown but no line of their own.
func nonEmptyLines(text string) int {
	lines := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines++
		}
	}
	return lines
}

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
// fake's settle step adds no interrupted notice to the history, and every
// message tall enough that the 30-row older page is several screens at
// 1280x720.
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
		require.GreaterOrEqual(t, nonEmptyLines(m.Content().Text), 4,
			"history message %s is too short for a multi-screen older page", m.ID)
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
	stream, ok := messageByID(msgs, M2StreamMessage)
	require.True(t, ok, "the stream reply is missing")
	require.Equal(t, len(msgs)-1, stream, "the stream rewrites its reply instead of adding one")
	require.True(t, msgs[stream].IsFinished())
}

// TestSeedM2StreamKeepsItsToolRowAboveOverscan pins the fixture the focus
// spec drives. The bash turn has a clipped output, so its row owns the
// focusable "Show all" control, and it sits far enough above the live end
// that the virtualizer's six-row overscan could not reach it: at 1280x720
// the tall streamed reply fills the window, so at most the reply row and
// the prompt row above it are visible, and six rows of overscan reach the
// prompt's neighbours, not a turn on row 2 of the session.
func TestSeedM2StreamKeepsItsToolRowAboveOverscan(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedM2(f, m2Clock)

	msgs, err := f.ListMessages(t.Context(), M2StreamSession)
	require.NoError(t, err)
	bash, stream := -1, -1
	var bashResult message.ToolResult
	for i := range msgs {
		for _, call := range msgs[i].ToolCalls() {
			if call.Name == "bash" {
				bash = i
			}
		}
		for _, result := range msgs[i].ToolResults() {
			if result.Name == "bash" {
				bashResult = result
			}
		}
		if msgs[i].ID == M2StreamMessage {
			stream = i
		}
	}
	require.NotEqual(t, -1, bash, "the stream session has no bash call")
	require.NotEqual(t, -1, stream, "the stream reply is missing")
	require.NotEmpty(t, bashResult.ToolCallID, "the stream session's bash call has no result")
	require.Equal(t, len(msgs)-1, stream, "the stream reply must be the live end")
	// The live end is sixteen rows below the bash turn: six overscan rows
	// plus the reply and its prompt row cover eight, which still leaves
	// eight rows of margin.
	require.GreaterOrEqual(t, stream-bash, 12, "the bash row sits inside the overscan")

	var meta struct {
		Output string `json:"output"`
	}
	require.NoError(t, json.Unmarshal([]byte(bashResult.Metadata), &meta))
	require.Greater(t, strings.Count(meta.Output, "\n")+1, 200,
		"the bash output must be clipped so the row has a Show all control")
}

// TestSeedM2BashResultsReadLikeTheTool pins the result text bash.go
// builds: the metadata output, then the working directory. The terminal
// reads the metadata, and the model-facing text must still be the tool's.
func TestSeedM2BashResultsReadLikeTheTool(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedM2(f, m2Clock)

	for _, sessionID := range []string{M2ChatSession, M2StreamSession} {
		msgs, err := f.ListMessages(t.Context(), sessionID)
		require.NoError(t, err)
		found := false
		for _, m := range msgs {
			for _, result := range m.ToolResults() {
				if result.Name != "bash" || result.IsError {
					continue
				}
				var meta struct {
					Output string `json:"output"`
				}
				require.NoError(t, json.Unmarshal([]byte(result.Metadata), &meta))
				require.NotEmpty(t, meta.Output)
				require.Equal(t, meta.Output+"\n\n<cwd>/tmp/project</cwd>", result.Content)
				found = true
			}
		}
		require.True(t, found, "%s has no bash result", sessionID)
	}
}

// messageByID finds the index of the message with id in msgs.
func messageByID(msgs []message.Message, id string) (int, bool) {
	for i := range msgs {
		if msgs[i].ID == id {
			return i, true
		}
	}
	return 0, false
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
	stream, ok := messageByID(msgs, M2StreamMessage)
	require.True(t, ok)
	require.False(t, msgs[stream].IsFinished())
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
