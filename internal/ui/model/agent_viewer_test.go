package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

type agentViewerWorkspace struct {
	workspace.Workspace

	mu       sync.Mutex
	messages map[string][]message.Message
	getErr   error
	listErr  error
	busy     bool

	getCalls  int
	listReads int
	busyCalls int
}

func (w *agentViewerWorkspace) Config() *config.Config {
	return &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
}

func (w *agentViewerWorkspace) WorkingDir() string { return "/tmp/crush-agent-viewer-test" }

func (w *agentViewerWorkspace) GetSession(_ context.Context, sessionID string) (session.Session, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.getCalls++
	if w.getErr != nil {
		return session.Session{}, w.getErr
	}
	return session.Session{ID: sessionID, Title: "child"}, nil
}

func (w *agentViewerWorkspace) ListMessages(_ context.Context, sessionID string) ([]message.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.listReads++
	if w.listErr != nil {
		return nil, w.listErr
	}
	return append([]message.Message(nil), w.messages[sessionID]...), nil
}

func (w *agentViewerWorkspace) AgentIsSessionBusy(string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.busyCalls++
	return w.busy
}

func (w *agentViewerWorkspace) CreateAgentToolSessionID(messageID, toolCallID string) string {
	return fmt.Sprintf("nested:%s:%s", messageID, toolCallID)
}

func (w *agentViewerWorkspace) listCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.listReads
}

func (w *agentViewerWorkspace) busyProbeCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.busyCalls
}

func newAgentViewerTest(t *testing.T, ws *agentViewerWorkspace, generation uint64) (*UI, *agentViewer) {
	t.Helper()
	u := newTestUI()
	u.com.Workspace = ws
	viewer := newAgentViewer(u.com, dialog.AgentDescriptor{
		SessionID: "child",
		Name:      "researcher",
	}, config.ScrollbarNever, generation)
	return u, viewer
}

func runAgentViewerLoad(t *testing.T, viewer *agentViewer) agentViewerLoadMsg {
	t.Helper()
	cmd := viewer.Start()
	require.NotNil(t, cmd)
	msg, ok := cmd().(agentViewerLoadMsg)
	require.True(t, ok)
	viewer.HandleLoad(msg)
	return msg
}

func agentViewerHistoryMessages() []message.Message {
	return []message.Message{
		{ID: "user-1", SessionID: "child", Role: message.User, CreatedAt: 10, Parts: []message.ContentPart{
			message.TextContent{Text: "inspect the repository"},
		}},
		{ID: "assistant-1", SessionID: "child", Role: message.Assistant, CreatedAt: 11, Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "checking the implementation"},
			message.ToolCall{ID: "tool-1", Name: agent.AgentToolName, Input: `{"prompt":"inspect"}`, Finished: true},
		}},
		{ID: "tool-result-1", SessionID: "child", Role: message.Tool, CreatedAt: 12, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "tool-1", Name: agent.AgentToolName, Content: "done"},
		}},
	}
}

func TestAgentViewerLoadHistoryAndNestedTools(t *testing.T) {
	t.Parallel()
	messages := agentViewerHistoryMessages()
	ws := &agentViewerWorkspace{messages: map[string][]message.Message{
		"child": messages,
		"nested:assistant-1:tool-1": {
			{ID: "nested-assistant", SessionID: "nested:assistant-1:tool-1", Role: message.Assistant, Parts: []message.ContentPart{
				message.ToolCall{ID: "nested-tool", Name: "view", Input: `{}`, Finished: true},
			}},
		},
	}}
	u, viewer := newAgentViewerTest(t, ws, 7)
	u.chat.SetMessages(testMessageItem{id: "main-item", text: "main"})

	runAgentViewerLoad(t, viewer)

	require.Equal(t, "researcher", viewer.descriptor.Name)
	require.Equal(t, "child", viewer.session.ID)
	require.Equal(t, uint64(7), viewer.generation)
	require.False(t, viewer.loading)
	require.NoError(t, viewer.loadErr)
	require.NotNil(t, viewer.chat.MessageItem("user-1"))
	require.NotNil(t, viewer.chat.MessageItem("assistant-1"))
	require.Contains(t, ansi.Strip(viewer.chat.MessageItem("assistant-1").RawRender(100)), "checking the implementation")
	toolItem, ok := viewer.chat.MessageItem("tool-1").(chat.ToolMessageItem)
	require.True(t, ok)
	animatable, ok := toolItem.(chat.Animatable)
	require.True(t, ok)
	require.False(t, animatable.Spinning(), "the persisted result is attached to the tool call")
	container, ok := toolItem.(chat.NestedToolContainer)
	require.True(t, ok)
	require.Len(t, container.NestedTools(), 1)
	require.Equal(t, "nested-tool", container.NestedTools()[0].ID())
	require.NotNil(t, viewer.chat.MessageItem("nested-tool"))
	require.NotNil(t, u.chat.MessageItem("main-item"))
	require.Equal(t, 1, u.chat.Len())
	require.Len(t, viewer.messagesByID, len(messages))
	require.True(t, viewer.chat.Follow())
	require.Equal(t, viewer.chat.Len()-1, viewer.chat.Selected())
	require.Equal(t, 1, ws.busyProbeCalls(), "only the asynchronous load command probes busy state")
}

func TestAgentViewerLoadCompletedSnapshotSuppressesGhostSpinner(t *testing.T) {
	t.Parallel()
	messages := agentViewerHistoryMessages()
	messages[1].Parts = []message.ContentPart{message.ReasoningContent{Thinking: "interrupted"}}
	ws := &agentViewerWorkspace{messages: map[string][]message.Message{"child": messages}, busy: false}
	_, viewer := newAgentViewerTest(t, ws, 1)

	runAgentViewerLoad(t, viewer)

	require.False(t, viewer.chat.HasLiveSpinner())
	require.False(t, viewer.chat.animAllowed)
}

func TestAgentViewerLoadRejectsStaleResultsAndDoesNotProbeBusy(t *testing.T) {
	t.Parallel()
	ws := &agentViewerWorkspace{messages: map[string][]message.Message{"child": agentViewerHistoryMessages()}}
	_, viewer := newAgentViewerTest(t, ws, 8)
	projection := projectTranscriptMessages(viewer.com, ws.messages["child"])
	valid := agentViewerLoadMsg{
		generation: 8,
		sessionID:  "child",
		session:    session.Session{ID: "child"},
		messages:   ws.messages["child"],
		projection: projection,
		busy:       true,
	}

	staleGeneration := valid
	staleGeneration.generation = 7
	require.Nil(t, viewer.HandleLoad(staleGeneration))
	staleSession := valid
	staleSession.sessionID = "other"
	require.Nil(t, viewer.HandleLoad(staleSession))
	require.Zero(t, viewer.chat.Len())
	require.Empty(t, viewer.messagesByID)
	require.Zero(t, ws.busyProbeCalls())

	viewer.HandleLoad(valid)
	require.NotZero(t, viewer.chat.Len())
	require.Zero(t, ws.busyProbeCalls(), "HandleLoad must trust the asynchronous busy result")
	require.True(t, viewer.chat.animAllowed)
}

func TestAgentViewerLoadError(t *testing.T) {
	t.Parallel()
	loadErr := errors.New("history unavailable")
	ws := &agentViewerWorkspace{getErr: loadErr}
	_, viewer := newAgentViewerTest(t, ws, 3)
	viewer.loadErr = errors.New("old error")

	msg := runAgentViewerLoad(t, viewer)

	require.ErrorIs(t, msg.err, loadErr)
	require.ErrorIs(t, viewer.loadErr, loadErr)
	require.Zero(t, viewer.chat.Len())
	require.Empty(t, viewer.messagesByID)
	require.Empty(t, viewer.itemIDsByMessage)
	require.False(t, viewer.loading)
	require.Zero(t, ws.busyProbeCalls())
}

func newLoadedAgentViewer(t *testing.T) (*agentViewer, *agentViewerWorkspace) {
	t.Helper()
	ws := &agentViewerWorkspace{messages: map[string][]message.Message{"child": {}}}
	_, viewer := newAgentViewerTest(t, ws, 11)
	runAgentViewerLoad(t, viewer)
	return viewer, ws
}

func agentViewerEvent(eventType pubsub.EventType, msg message.Message) pubsub.Event[message.Message] {
	return pubsub.Event[message.Message]{Type: eventType, Payload: msg}
}

func TestAgentViewerEventCreatedAppendsOnce(t *testing.T) {
	t.Parallel()
	viewer, ws := newLoadedAgentViewer(t)
	user := message.Message{ID: "user-live", SessionID: "child", Role: message.User, CreatedAt: 20, Parts: []message.ContentPart{
		message.TextContent{Text: "live task"},
	}}
	assistant := message.Message{ID: "assistant-live", SessionID: "child", Role: message.Assistant, CreatedAt: 21, Parts: []message.ContentPart{
		message.TextContent{Text: "live response"},
	}}

	for range 2 {
		viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, user))
		viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, assistant))
	}

	require.Len(t, viewer.messagesByID, 2)
	require.Equal(t, 2, viewer.chat.Len())
	require.NotNil(t, viewer.chat.MessageItem(user.ID))
	require.NotNil(t, viewer.chat.MessageItem(assistant.ID))
	require.Equal(t, int64(20), viewer.lastUserMessageTime)
	require.False(t, viewer.reconcileTimerArmed)
	require.Equal(t, 1, ws.listCalls(), "ordinary live events never trigger snapshot reads")
}

func TestAgentViewerEventDuplicateCreateBufferedDuringLoad(t *testing.T) {
	t.Parallel()
	viewer, _ := newLoadedAgentViewer(t)
	assistant := message.Message{ID: "assistant-buffered", SessionID: "child", Role: message.Assistant, Parts: []message.ContentPart{
		message.TextContent{Text: "buffered response"},
	}}
	viewer.loading = true
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, assistant))
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, assistant))
	require.Zero(t, viewer.chat.Len())

	viewer.HandleLoad(agentViewerLoadMsg{
		generation: viewer.generation,
		sessionID:  "child",
		session:    session.Session{ID: "child"},
		projection: projectTranscriptMessages(viewer.com, nil),
	})

	require.Len(t, viewer.messagesByID, 1)
	require.Equal(t, 1, viewer.chat.Len())
	require.NotNil(t, viewer.chat.MessageItem(assistant.ID))
}

func TestAgentViewerEventUpdatedAssistantAndTools(t *testing.T) {
	t.Parallel()
	viewer, _ := newLoadedAgentViewer(t)
	assistant := message.Message{ID: "assistant-update", SessionID: "child", Role: message.Assistant, Parts: []message.ContentPart{
		message.TextContent{Text: "old response"},
	}}
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, assistant))
	updated := assistant
	updated.Parts = []message.ContentPart{
		message.ReasoningContent{Thinking: "new reasoning"},
		message.ToolCall{ID: "tool-update", Name: "view", Input: `{"path":"old"}`, Finished: false},
	}

	viewer.HandleEvent(agentViewerEvent(pubsub.UpdatedEvent, updated))

	rendered := ansi.Strip(viewer.chat.MessageItem(assistant.ID).RawRender(100))
	require.Contains(t, rendered, "new reasoning")
	require.NotContains(t, rendered, "old response")
	toolItem, ok := viewer.chat.MessageItem("tool-update").(chat.ToolMessageItem)
	require.True(t, ok)
	require.Equal(t, `{"path":"old"}`, toolItem.ToolCall().Input)
	require.Contains(t, viewer.itemIDsByMessage[assistant.ID], assistant.ID)
	require.Contains(t, viewer.itemIDsByMessage[assistant.ID], "tool-update")

	updated.Parts = []message.ContentPart{
		message.ReasoningContent{Thinking: "new reasoning"},
		message.ToolCall{ID: "tool-update", Name: "view", Input: `{"path":"new"}`, Finished: true},
	}
	viewer.HandleEvent(agentViewerEvent(pubsub.UpdatedEvent, updated))
	require.Equal(t, `{"path":"new"}`, toolItem.ToolCall().Input)
	require.True(t, toolItem.ToolCall().Finished)

	updated.Parts = append(updated.Parts, message.Finish{Reason: message.FinishReasonEndTurn, Time: 30})
	viewer.HandleEvent(agentViewerEvent(pubsub.UpdatedEvent, updated))
	require.NotNil(t, viewer.chat.MessageItem(chat.AssistantInfoID(assistant.ID)))
	require.Contains(t, viewer.itemIDsByMessage[assistant.ID], chat.AssistantInfoID(assistant.ID))

	updated.Parts = updated.Parts[:len(updated.Parts)-1]
	viewer.HandleEvent(agentViewerEvent(pubsub.UpdatedEvent, updated))
	require.Nil(t, viewer.chat.MessageItem(chat.AssistantInfoID(assistant.ID)))
	require.NotContains(t, viewer.itemIDsByMessage[assistant.ID], chat.AssistantInfoID(assistant.ID))

	missing := message.Message{ID: "assistant-race", SessionID: "child", Role: message.Assistant, Parts: []message.ContentPart{
		message.TextContent{Text: "update won the race"},
	}}
	viewer.HandleEvent(agentViewerEvent(pubsub.UpdatedEvent, missing))
	require.NotNil(t, viewer.chat.MessageItem(missing.ID))
	require.Contains(t, viewer.messagesByID, missing.ID)
}

func TestAgentViewerEventToolResultAttachesWithoutTopLevelItem(t *testing.T) {
	t.Parallel()
	viewer, _ := newLoadedAgentViewer(t)
	assistant := message.Message{ID: "assistant-tool", SessionID: "child", Role: message.Assistant, Parts: []message.ContentPart{
		message.ToolCall{ID: "tool-live", Name: "view", Input: `{}`},
	}}
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, assistant))
	before := viewer.chat.Len()
	result := message.Message{ID: "result-live", SessionID: "child", Role: message.Tool, Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "tool-live", Content: "file contents"},
	}}

	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, result))

	require.Equal(t, before, viewer.chat.Len())
	require.Nil(t, viewer.chat.MessageItem(result.ID))
	toolItem := viewer.chat.MessageItem("tool-live").(chat.ToolMessageItem)
	require.False(t, toolItem.(chat.Animatable).Spinning())
	require.Empty(t, viewer.itemIDsByMessage[result.ID])
	require.Contains(t, viewer.messagesByID, result.ID)
}

func TestAgentViewerEventDeleteRemovesDerivedItems(t *testing.T) {
	t.Parallel()
	viewer, _ := newLoadedAgentViewer(t)
	assistant := message.Message{ID: "assistant-1", SessionID: "child", Role: message.Assistant, Parts: []message.ContentPart{
		message.TextContent{Text: "finished"},
		message.ToolCall{ID: "tool-delete", Name: "view", Input: `{}`, Finished: true},
		message.Finish{Reason: message.FinishReasonEndTurn, Time: 30},
	}}
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, assistant))
	ids := append([]string(nil), viewer.itemIDsByMessage["assistant-1"]...)
	require.Contains(t, ids, "assistant-1")
	require.Contains(t, ids, "tool-delete")
	require.Contains(t, ids, chat.AssistantInfoID("assistant-1"))

	viewer.HandleEvent(pubsub.Event[message.Message]{Type: pubsub.DeletedEvent, Payload: assistant})
	for _, id := range ids {
		require.Nil(t, viewer.chat.MessageItem(id))
	}
	require.NotContains(t, viewer.messagesByID, "assistant-1")
	require.NotContains(t, viewer.itemIDsByMessage, "assistant-1")
}

func TestAgentViewerEventDeleteNestedAgentToolsReindexesRemainingItems(t *testing.T) {
	t.Parallel()
	viewer, _ := newLoadedAgentViewer(t)
	parent := message.Message{ID: "parent-message", SessionID: "child", Role: message.Assistant, Parts: []message.ContentPart{
		message.ToolCall{ID: "parent-agent", Name: agent.AgentToolName, Input: `{}`, Finished: true},
	}}
	sibling := message.Message{ID: "sibling-message", SessionID: "child", Role: message.Assistant, Parts: []message.ContentPart{
		message.ToolCall{ID: "sibling-agent", Name: agent.AgentToolName, Input: `{}`, Finished: true},
	}}
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, parent))
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, sibling))

	parentContainer := viewer.chat.MessageItem("parent-agent").(chat.NestedToolContainer)
	parentContainer.SetNestedTools([]chat.ToolMessageItem{chat.NewToolMessageItem(
		viewer.com.Styles, parent.ID, message.ToolCall{ID: "nested-delete", Name: "view", Input: `{}`, Finished: true}, nil, false, viewer.com.Workspace.WorkingDir(),
	)})
	viewer.chat.UpdateNestedToolIDs("parent-agent")
	siblingContainer := viewer.chat.MessageItem("sibling-agent").(chat.NestedToolContainer)
	siblingContainer.SetNestedTools([]chat.ToolMessageItem{chat.NewToolMessageItem(
		viewer.com.Styles, sibling.ID, message.ToolCall{ID: "nested-keep", Name: "view", Input: `{}`, Finished: true}, nil, false, viewer.com.Workspace.WorkingDir(),
	)})
	viewer.chat.UpdateNestedToolIDs("sibling-agent")
	siblingItem := viewer.chat.MessageItem("sibling-agent")
	require.NotNil(t, viewer.chat.MessageItem("nested-delete"))
	require.NotNil(t, viewer.chat.MessageItem("nested-keep"))

	viewer.HandleEvent(agentViewerEvent(pubsub.DeletedEvent, parent))

	require.Nil(t, viewer.chat.MessageItem("parent-agent"))
	require.Nil(t, viewer.chat.MessageItem("nested-delete"), "deleted containers must unregister every nested tool ID")
	require.Same(t, siblingItem, viewer.chat.MessageItem("sibling-agent"))
	require.NotNil(t, viewer.chat.MessageItem("nested-keep"), "remaining nested IDs must move with their container")
	require.Equal(t, 1, viewer.chat.Len())
}

func TestAgentViewerEventDeleteToolResultClearsLinkedResult(t *testing.T) {
	t.Parallel()
	viewer, _ := newLoadedAgentViewer(t)
	assistant := message.Message{ID: "assistant-tool-delete", SessionID: "child", Role: message.Assistant, Parts: []message.ContentPart{
		message.ToolCall{ID: "tool-result-delete", Name: "view", Input: `{}`},
	}}
	result := message.Message{ID: "result-delete", SessionID: "child", Role: message.Tool, Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "tool-result-delete", Content: "contents"},
	}}
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, assistant))
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, result))
	toolItem := viewer.chat.MessageItem("tool-result-delete").(chat.ToolMessageItem)
	require.False(t, toolItem.(chat.Animatable).Spinning())

	viewer.HandleEvent(agentViewerEvent(pubsub.DeletedEvent, result))

	require.NotNil(t, viewer.chat.MessageItem("tool-result-delete"), "deleting a result must not orphan or remove its call")
	require.True(t, toolItem.(chat.Animatable).Spinning())
	require.NotContains(t, viewer.messagesByID, result.ID)
	require.NotContains(t, viewer.itemIDsByMessage, result.ID)
}

func TestAgentViewerEventIgnoresUnrelatedSession(t *testing.T) {
	t.Parallel()
	viewer, _ := newLoadedAgentViewer(t)
	other := message.Message{ID: "other-message", SessionID: "other", Role: message.User, Parts: []message.ContentPart{
		message.TextContent{Text: "not mine"},
	}}

	require.Nil(t, viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, other)))

	require.Zero(t, viewer.chat.Len())
	require.Empty(t, viewer.messagesByID)
	require.Empty(t, viewer.buffered)
}

func TestAgentViewerEventFollowAndManualScroll(t *testing.T) {
	t.Parallel()
	viewer, _ := newLoadedAgentViewer(t)
	viewer.chat.SetSize(30, 3)
	for i := range 8 {
		viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, message.Message{
			ID:        fmt.Sprintf("user-%d", i),
			SessionID: "child",
			Role:      message.User,
			CreatedAt: int64(i + 1),
			Parts:     []message.ContentPart{message.TextContent{Text: "a sufficiently long live message"}},
		}))
	}
	require.True(t, viewer.chat.Follow())
	require.True(t, viewer.chat.AtBottom())

	viewer.chat.ScrollToTop()
	beforeIdx, beforeLine := viewer.chat.ScrollPosition()
	viewer.HandleEvent(agentViewerEvent(pubsub.CreatedEvent, message.Message{
		ID: "user-manual", SessionID: "child", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "new content"}},
	}))
	afterIdx, afterLine := viewer.chat.ScrollPosition()
	require.False(t, viewer.chat.Follow())
	require.Equal(t, beforeIdx, afterIdx)
	require.Equal(t, beforeLine, afterLine)
}

func TestAgentViewerSetSizeAndLoadingDraw(t *testing.T) {
	t.Parallel()
	viewer, ws := newLoadedAgentViewer(t)
	viewer.Start() // Set loading state without executing the asynchronous read.
	viewer.SetSize(80, 12)

	require.Equal(t, 80, viewer.width)
	require.Equal(t, 12, viewer.height)
	require.Equal(t, 10, viewer.chat.Height())
	scr := uv.NewScreenBuffer(80, 12)
	viewer.Draw(scr, scr.Bounds())
	plain := ansi.Strip(scr.Render())
	require.Contains(t, plain, "Loading researcher…")
	require.Contains(t, plain, "esc back")
	require.Zero(t, ws.listCalls()-1, "Draw performs no message reads")
	require.Zero(t, ws.busyProbeCalls()-1, "Draw performs no busy probes")
}

func TestAgentViewerUpdateDescriptorRetainsTranscriptAndChangesStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []agentrun.Status{agentrun.StatusDone, agentrun.StatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			viewer, _ := newLoadedAgentViewer(t)
			retained := agentViewerTextMessage("retained", "retained transcript")
			installAgentViewerSnapshot(viewer, retained)
			item := viewer.chat.MessageItem(retained.ID)
			viewer.chat.SetAnimationsAllowed(true)
			descriptor := viewer.descriptor
			descriptor.Status = status

			viewer.UpdateDescriptor(descriptor)
			viewer.SetSize(80, 10)
			scr := uv.NewScreenBuffer(80, 10)
			viewer.Draw(scr, scr.Bounds())
			plain := ansi.Strip(scr.Render())
			lines := strings.Split(plain, "\n")

			require.Same(t, item, viewer.chat.MessageItem(retained.ID))
			require.Equal(t, 1, viewer.chat.Len())
			require.False(t, viewer.chat.animAllowed)
			require.Equal(t, descriptor, viewer.descriptor)
			require.Contains(t, lines[0], "researcher")
			require.Contains(t, lines[0], string(status))
			require.Contains(t, lines[0], "esc back")
			require.Empty(t, strings.TrimSpace(lines[1]))
			require.Contains(t, plain, "retained transcript")
			require.NotContains(t, plain, "Loading researcher…")

			viewer.HandleLoad(agentViewerLoadMsg{
				generation: viewer.generation,
				sessionID:  "child",
				reconcile:  true,
				session:    session.Session{ID: "child"},
				messages:   []message.Message{retained},
				projection: projectTranscriptMessages(viewer.com, []message.Message{retained}),
				busy:       true,
			})
			require.False(t, viewer.chat.animAllowed, "a busy snapshot is not a live event and cannot revive a finished viewer")
		})
	}
}
