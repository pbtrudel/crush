package model

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

type agentViewerFakeClock struct{ now time.Time }

func (c *agentViewerFakeClock) Now() time.Time          { return c.now }
func (c *agentViewerFakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func agentViewerTextMessage(id, text string) message.Message {
	return message.Message{
		ID: id, SessionID: "child", Role: message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}
}

func installAgentViewerSnapshot(viewer *agentViewer, messages ...message.Message) tea.Cmd {
	return viewer.HandleLoad(agentViewerLoadMsg{
		generation: viewer.generation,
		sessionID:  viewer.descriptor.SessionID,
		session:    session.Session{ID: viewer.descriptor.SessionID},
		messages:   messages,
		projection: projectTranscriptMessages(viewer.com, messages),
	})
}

func bufferAgentViewerEventDuringLoad(viewer *agentViewer, event pubsub.Event[message.Message], snapshot ...message.Message) tea.Cmd {
	viewer.loading = true
	viewer.HandleEvent(event)
	return installAgentViewerSnapshot(viewer, snapshot...)
}

func runAgentViewerReconcileTimer(t *testing.T, viewer *agentViewer, clock *agentViewerFakeClock, timer tea.Cmd) agentViewerLoadMsg {
	t.Helper()
	require.NotNil(t, timer)
	clock.Advance(agentViewerReconcileDelay)
	msg, ok := timer().(agentViewerReconcileMsg)
	require.True(t, ok)
	loadCmd := viewer.HandleReconcile(msg)
	require.NotNil(t, loadCmd)
	loadMsg, ok := loadCmd().(agentViewerLoadMsg)
	require.True(t, ok)
	return loadMsg
}

func TestAgentViewerReconcileAfterInitialBufferedEvent(t *testing.T) {
	t.Parallel()
	viewer, ws := newLoadedAgentViewer(t)
	clock := &agentViewerFakeClock{now: time.Unix(100, 0)}
	viewer.now = clock.Now
	initialReads := ws.listCalls()
	updated := agentViewerTextMessage("assistant-buffered", "arrived during load")

	timer := bufferAgentViewerEventDuringLoad(viewer, agentViewerEvent(pubsub.CreatedEvent, updated))

	require.NotNil(t, timer)
	require.True(t, viewer.reconcileTimerArmed)
	require.True(t, viewer.reconcileNeeded)
	require.Empty(t, viewer.buffered)
	require.NotNil(t, viewer.chat.MessageItem(updated.ID))
	require.Equal(t, initialReads, ws.listCalls())
	require.Equal(t, clock.Now().Add(agentViewerReconcileDelay), viewer.reconcileDeadline)

	loadMsg := runAgentViewerReconcileTimer(t, viewer, clock, timer)
	require.True(t, loadMsg.reconcile)
	require.True(t, viewer.reconcileInFlight)
	require.False(t, viewer.reconcileTimerArmed)
	require.False(t, viewer.reconcileNeeded)
	require.Equal(t, initialReads+1, ws.listCalls())
}

func TestAgentViewerReconcileTrailingDeadlineAndSingleRead(t *testing.T) {
	t.Parallel()
	viewer, ws := newLoadedAgentViewer(t)
	clock := &agentViewerFakeClock{now: time.Unix(100, 0)}
	viewer.now = clock.Now
	initial := agentViewerTextMessage("assistant-debounce", "initial")
	timer := bufferAgentViewerEventDuringLoad(viewer, agentViewerEvent(pubsub.CreatedEvent, initial))
	firstSnapshot := runAgentViewerReconcileTimer(t, viewer, clock, timer)
	initialReads := ws.listCalls()

	var pending tea.Cmd
	for range 20 {
		updated := agentViewerTextMessage(initial.ID, "updated")
		cmd := viewer.HandleEvent(agentViewerEvent(pubsub.UpdatedEvent, updated))
		if cmd != nil {
			require.Nil(t, pending, "only one timer may be armed")
			pending = cmd
		}
		clock.Advance(50 * time.Millisecond)
		require.Equal(t, initialReads, ws.listCalls())
		pending = viewer.HandleReconcile(agentViewerReconcileMsg{generation: viewer.generation, sequence: viewer.reconcileSeq})
		require.Equal(t, initialReads, ws.listCalls())
	}
	require.True(t, viewer.reconcileInFlight)
	require.Len(t, viewer.buffered, 20)

	// Delivering the outstanding snapshot drains the buffered events and keeps
	// the same trailing timer alive with the latest deadline.
	viewer.HandleLoad(firstSnapshot)
	require.False(t, viewer.reconcileInFlight)
	require.Empty(t, viewer.buffered)
	require.True(t, viewer.reconcileTimerArmed)
	require.Equal(t, initialReads, ws.listCalls())

	secondSnapshot := runAgentViewerReconcileTimer(t, viewer, clock, pending)
	require.True(t, secondSnapshot.reconcile)
	require.Equal(t, initialReads+1, ws.listCalls())
	require.True(t, viewer.reconcileInFlight)
}

func TestAgentViewerReconcileBuffersEventsDuringRead(t *testing.T) {
	t.Parallel()
	viewer, ws := newLoadedAgentViewer(t)
	clock := &agentViewerFakeClock{now: time.Unix(100, 0)}
	viewer.now = clock.Now
	initialReads := ws.listCalls()
	created := agentViewerTextMessage("assistant-read", "created")
	timer := bufferAgentViewerEventDuringLoad(viewer, agentViewerEvent(pubsub.CreatedEvent, created))
	snapshot := runAgentViewerReconcileTimer(t, viewer, clock, timer)
	require.True(t, viewer.reconcileInFlight)

	updated := agentViewerTextMessage(created.ID, "updated while reading")
	trailing := viewer.HandleEvent(agentViewerEvent(pubsub.UpdatedEvent, updated))

	require.NotNil(t, trailing)
	require.Equal(t, initialReads+1, ws.listCalls(), "buffering must not start an immediate second read")
	require.Len(t, viewer.buffered, 1)
	require.True(t, viewer.reconcileTimerArmed)
	viewer.HandleLoad(snapshot)
	require.Contains(t, ansi.Strip(viewer.chat.MessageItem(created.ID).RawRender(100)), "updated while reading")
	require.NotNil(t, trailing)
}

func TestAgentViewerReconcileRestoresNewerSnapshotAfterOlderBufferedUpdate(t *testing.T) {
	t.Parallel()
	persisted := agentViewerTextMessage("assistant-race", "new persisted snapshot")
	ws := &agentViewerWorkspace{messages: map[string][]message.Message{"child": {persisted}}}
	_, viewer := newAgentViewerTest(t, ws, 11)
	clock := &agentViewerFakeClock{now: time.Unix(100, 0)}
	viewer.now = clock.Now
	cmd := viewer.Start()
	require.NotNil(t, cmd)
	loadMsg := cmd().(agentViewerLoadMsg)
	older := agentViewerTextMessage(persisted.ID, "older buffered update")
	viewer.HandleEvent(agentViewerEvent(pubsub.UpdatedEvent, older))
	timer := viewer.HandleLoad(loadMsg)
	require.Contains(t, ansi.Strip(viewer.chat.MessageItem(persisted.ID).RawRender(100)), "older buffered update")

	reconciled := runAgentViewerReconcileTimer(t, viewer, clock, timer)
	viewer.HandleLoad(reconciled)

	rendered := ansi.Strip(viewer.chat.MessageItem(persisted.ID).RawRender(100))
	require.Contains(t, rendered, "new persisted snapshot")
	require.NotContains(t, rendered, "older buffered update")
	require.False(t, viewer.reconcileNeeded)
	require.False(t, viewer.reconcileInFlight)
}

func TestAgentViewerReconcileFailureKeepsItemsAndDoesNotRetry(t *testing.T) {
	t.Parallel()
	viewer, ws := newLoadedAgentViewer(t)
	clock := &agentViewerFakeClock{now: time.Unix(100, 0)}
	viewer.now = clock.Now
	initialReads := ws.listCalls()
	created := agentViewerTextMessage("assistant-failure", "retained content")
	timer := bufferAgentViewerEventDuringLoad(viewer, agentViewerEvent(pubsub.CreatedEvent, created))
	readErr := errors.New("reconciliation failed")
	ws.mu.Lock()
	ws.listErr = readErr
	ws.mu.Unlock()
	failed := runAgentViewerReconcileTimer(t, viewer, clock, timer)

	warning := viewer.HandleLoad(failed)

	require.ErrorIs(t, failed.err, readErr)
	require.NotNil(t, warning)
	warningMsg, ok := warning().(util.InfoMsg)
	require.True(t, ok)
	require.Equal(t, util.InfoTypeWarn, warningMsg.Type)
	require.Contains(t, warningMsg.Msg, readErr.Error())
	require.Contains(t, ansi.Strip(viewer.chat.MessageItem(created.ID).RawRender(100)), "retained content")
	require.False(t, viewer.reconcileInFlight)
	require.False(t, viewer.reconcileNeeded)
	require.False(t, viewer.reconcileTimerArmed)
	require.Equal(t, initialReads+1, ws.listCalls())
	clock.Advance(time.Second)
	require.Nil(t, viewer.HandleReconcile(agentViewerReconcileMsg{generation: viewer.generation, sequence: viewer.reconcileSeq}))
	require.Equal(t, initialReads+1, ws.listCalls())

	ws.mu.Lock()
	ws.listErr = nil
	ws.mu.Unlock()
	explicitReload := viewer.Start()
	require.NotNil(t, explicitReload)
	require.IsType(t, agentViewerLoadMsg{}, explicitReload())
	require.Equal(t, initialReads+2, ws.listCalls())
}

func TestAgentViewerReconcileCloseAndReselectionInvalidatePendingResults(t *testing.T) {
	t.Parallel()
	viewer, ws := newLoadedAgentViewer(t)
	clock := &agentViewerFakeClock{now: time.Unix(100, 0)}
	viewer.now = clock.Now
	created := agentViewerTextMessage("assistant-close", "pending")
	_ = bufferAgentViewerEventDuringLoad(viewer, agentViewerEvent(pubsub.CreatedEvent, created))
	oldGeneration := viewer.generation
	oldSequence := viewer.reconcileSeq
	reads := ws.listCalls()

	viewer.Close()

	require.Equal(t, oldGeneration+1, viewer.generation)
	require.Equal(t, oldSequence+1, viewer.reconcileSeq)
	require.False(t, viewer.loading)
	require.False(t, viewer.reconcileInFlight)
	require.False(t, viewer.reconcileTimerArmed)
	require.False(t, viewer.reconcileNeeded)
	require.Empty(t, viewer.buffered)
	require.Nil(t, viewer.HandleReconcile(agentViewerReconcileMsg{generation: oldGeneration, sequence: oldSequence}))
	require.Nil(t, viewer.HandleLoad(agentViewerLoadMsg{generation: oldGeneration, sessionID: "child"}))
	require.Equal(t, reads, ws.listCalls())

	ws.messages["replacement"] = []message.Message{agentViewerTextMessage("replacement-message", "replacement")}
	_, replacement := newAgentViewerTest(t, ws, viewer.generation+1)
	require.Nil(t, replacement.HandleReconcile(agentViewerReconcileMsg{generation: oldGeneration, sequence: oldSequence}))
	require.Nil(t, replacement.HandleLoad(agentViewerLoadMsg{generation: replacement.generation, sessionID: "child"}))
	require.Zero(t, replacement.chat.Len())
	require.Equal(t, reads, ws.listCalls())
}

func TestAgentViewerReconcilePreservesManualScrollAndSelection(t *testing.T) {
	t.Parallel()
	viewer, _ := newLoadedAgentViewer(t)
	first := agentViewerTextMessage("first", "first\nsecond line\nthird line\nfourth line\nfifth line")
	second := agentViewerTextMessage("second", "second\nsecond line\nthird line\nfourth line\nfifth line")
	installAgentViewerSnapshot(viewer, first, second)
	viewer.chat.SetSize(40, 3)
	viewer.chat.SelectID(first.ID)
	viewer.chat.RestoreScrollPosition(0, 1)

	viewer.reconcileInFlight = true
	viewer.HandleLoad(agentViewerLoadMsg{
		generation: viewer.generation,
		sessionID:  "child",
		reconcile:  true,
		session:    session.Session{ID: "child"},
		messages:   []message.Message{first, second},
		projection: projectTranscriptMessages(viewer.com, []message.Message{first, second}),
	})

	require.False(t, viewer.chat.Follow())
	require.Equal(t, first.ID, viewer.chat.SelectedID())
	idx, line := viewer.chat.ScrollPosition()
	require.Equal(t, 0, idx)
	require.Equal(t, 1, line)
}
