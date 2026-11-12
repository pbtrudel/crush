package model

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/attachments"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func (w *agentViewerUIWorkspace) ListSessionHistory(context.Context, string) ([]history.File, error) {
	return nil, nil
}

func (w *agentViewerUIWorkspace) FileTrackerListReadFiles(context.Context, string) ([]string, error) {
	return nil, nil
}

type agentViewerLifecycleConfigWorkspace struct {
	workspace.Workspace
}

func (w agentViewerLifecycleConfigWorkspace) Config() *config.Config {
	return &config.Config{Options: &config.Options{}}
}

type agentViewerLifecycleActionDialog struct {
	action dialog.Action
}

func (d *agentViewerLifecycleActionDialog) ID() string { return dialog.SessionsID }
func (d *agentViewerLifecycleActionDialog) HandleMsg(tea.Msg) dialog.Action {
	return d.action
}
func (d *agentViewerLifecycleActionDialog) Draw(uv.Screen, uv.Rectangle) *tea.Cursor { return nil }

func selectMainSessionForAgentViewerLifecycle(u *UI, sessionID string) tea.Cmd {
	u.dialog.OpenDialog(&agentViewerLifecycleActionDialog{
		action: dialog.ActionSelectSession{Session: session.Session{ID: sessionID}},
	})
	return u.handleDialogMsg(struct{}{})
}

func requireAgentViewerLifecycleClosed(t *testing.T, u *UI, viewer *agentViewer, generation uint64) {
	t.Helper()
	require.Nil(t, u.agentViewer)
	require.Equal(t, generation+1, viewer.generation)
	require.False(t, viewer.loading)
	require.False(t, viewer.reconcileInFlight)
	require.False(t, viewer.reconcileTimerArmed)
	require.Empty(t, viewer.buffered)
}

func TestAgentViewerLifecycleMainNestedToolsAndDispatchContinue(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	parentTool := chat.NewToolMessageItem(u.com.Styles, "parent-message", message.ToolCall{
		ID: "parent-tool", Name: agent.AgentToolName, Input: `{}`, Finished: true,
	}, nil, false, u.com.Workspace.WorkingDir())
	dispatch := chat.NewToolMessageItem(u.com.Styles, "dispatch-message", message.ToolCall{
		ID: "dispatch-tool", Name: agent.AgentDispatchToolName,
		Input: `{"label":"review","prompt":"inspect"}`, Finished: true,
	}, nil, false, u.com.Workspace.WorkingDir()).(*chat.AgentToolMessageItem)
	u.chat.SetMessages(parentTool, dispatch)
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})

	status, ok := dispatch.DispatchStatus()
	require.True(t, ok)
	require.True(t, status.Running)

	nestedMessage := message.Message{
		ID: "nested-message", SessionID: "child", Role: message.Assistant,
		Parts: []message.ContentPart{message.ToolCall{ID: "nested-tool", Name: "view", Input: `{}`}},
	}
	_, _ = u.Update(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: nestedMessage})
	container, ok := parentTool.(chat.NestedToolContainer)
	require.True(t, ok)
	require.Len(t, container.NestedTools(), 1)
	require.False(t, container.NestedTools()[0].ToolCall().Finished)
	require.False(t, viewer.chat.MessageItem("nested-tool").(chat.ToolMessageItem).ToolCall().Finished)

	nestedMessage.Parts = []message.ContentPart{message.ToolCall{
		ID: "nested-tool", Name: "view", Input: `{}`, Finished: true,
	}}
	_, _ = u.Update(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: nestedMessage})
	require.True(t, container.NestedTools()[0].ToolCall().Finished)
	require.True(t, viewer.chat.MessageItem("nested-tool").(chat.ToolMessageItem).ToolCall().Finished)

	report := message.Message{
		ID: "dispatch-report", SessionID: "main", Role: message.User,
		Parts: []message.ContentPart{message.SubAgentReport{Label: "review", Output: "done"}},
	}
	_, _ = u.Update(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: report})
	status, ok = dispatch.DispatchStatus()
	require.True(t, ok)
	require.False(t, status.Running)
	require.False(t, status.Finished.IsZero())
	require.Same(t, viewer, u.agentViewer)
}

func TestAgentViewerLifecycleMainSessionSwitchClosesAndInvalidates(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	mainSession := u.session
	mainItem := &agentViewerUIItem{id: "main", text: "main remains until its own load finishes"}
	u.chat.SetMessages(mainItem)
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	generation := viewer.generation

	cmd := selectMainSessionForAgentViewerLifecycle(u, "other-main")

	require.NotNil(t, cmd)
	requireAgentViewerLifecycleClosed(t, u, viewer, generation)
	require.Same(t, mainSession, u.session)
	require.Same(t, mainItem, u.chat.MessageItem("main"))
}

func TestAgentViewerLifecycleNewSessionClosesAndInvalidates(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	generation := viewer.generation

	cmd := u.newSession()

	require.NotNil(t, cmd)
	requireAgentViewerLifecycleClosed(t, u, viewer, generation)
	require.Nil(t, u.session)
}

func TestAgentViewerLifecycleDeletingCurrentSessionClosesAndInvalidates(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	generation := viewer.generation

	_, cmd := u.Update(pubsub.Event[session.Session]{
		Type:    pubsub.DeletedEvent,
		Payload: session.Session{ID: "main"},
	})

	require.NotNil(t, cmd)
	requireAgentViewerLifecycleClosed(t, u, viewer, generation)
	require.Nil(t, u.session)
}

func TestAgentViewerLifecycleConnectionRecoveryReloadsAndRejectsStaleReplacement(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	u.mcpFetchInFlight = true // Keep this focused on main and viewer reload commands.
	ws.messages["child"] = []message.Message{agentViewerTextMessage("before-recovery", "before recovery")}
	viewerA := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	generationA := viewerA.generation
	viewerA.reconcileTimerArmed = true
	viewerA.reconcileNeeded = true
	armedSequence := viewerA.reconcileSeq

	cmds := u.handleConnectionEvent(workspace.ConnectionEvent{State: workspace.ConnectionRecovered})

	require.Same(t, viewerA, u.agentViewer, "same-session recovery must not close the viewer")
	require.Len(t, cmds, 3, "status, main reload, and viewer reload")
	require.Equal(t, generationA+1, viewerA.generation)
	require.Equal(t, generationA+1, u.agentViewerGeneration)
	require.True(t, viewerA.reconcileInFlight)
	require.False(t, viewerA.reconcileTimerArmed)
	require.False(t, viewerA.reconcileNeeded)
	require.Greater(t, viewerA.reconcileSeq, armedSequence)
	require.NotNil(t, viewerA.chat.MessageItem("before-recovery"), "reload keeps current items visible")

	var mainLoads []loadSessionMsg
	for _, msg := range runCmdCollect(cmds[1]) {
		if load, ok := msg.(loadSessionMsg); ok {
			mainLoads = append(mainLoads, load)
		}
	}
	require.Len(t, mainLoads, 1)
	require.Equal(t, "main", mainLoads[0].session.ID)

	staleA, ok := cmds[2]().(agentViewerLoadMsg)
	require.True(t, ok)
	require.Equal(t, viewerA.generation, staleA.generation)
	require.Equal(t, "child", staleA.sessionID)
	require.True(t, staleA.reconcile)

	ws.messages["child"] = []message.Message{agentViewerTextMessage("replacement-b", "replacement B")}
	cmdB := u.openAgentViewer(dialog.AgentDescriptor{SessionID: "child", Name: "replacement"})
	viewerB := u.agentViewer
	require.NotSame(t, viewerA, viewerB)
	require.Equal(t, staleA.generation+1, viewerB.generation)
	require.Equal(t, viewerB.generation, u.agentViewerGeneration)

	_, _ = u.Update(staleA)
	require.Same(t, viewerB, u.agentViewer)
	require.Zero(t, viewerB.chat.Len(), "A's stale reload cannot populate replacement B")

	currentB, ok := cmdB().(agentViewerLoadMsg)
	require.True(t, ok)
	_, _ = u.Update(currentB)
	require.Nil(t, viewerB.chat.MessageItem("before-recovery"))
	require.NotNil(t, viewerB.chat.MessageItem("replacement-b"))
	require.Equal(t, uint64(3), u.agentViewerGeneration)
}

func TestAgentViewerLifecycleTeamRefreshUpdatesDescriptorWithoutClosing(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	u.com.Workspace = agentViewerLifecycleConfigWorkspace{Workspace: ws}
	ws.messages["child"] = []message.Message{agentViewerTextMessage("persisted", "loaded transcript")}
	u.teamState = agentteam.State{Members: []agentteam.Member{{
		ID: "member", Name: "researcher", SessionID: "child", ParentID: "main", Status: agentteam.StatusRunning,
	}}}
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{
		SessionID: "child", ParentSessionID: "main", Name: "researcher", Kind: agentrun.KindTeammate, Status: agentrun.StatusRunning,
	})

	refreshed := teamStatusMsg{
		ok: true,
		state: agentteam.State{Members: []agentteam.Member{{
			ID: "member", Name: "renamed", SessionID: "child", ParentID: "main", Status: agentteam.StatusRunning,
		}}},
		runs: []agentrun.Run{{
			SessionID: "child", ParentSessionID: "main", Kind: agentrun.KindTeammate, Status: agentrun.StatusDone,
		}},
	}
	_ = u.handleTeamStatus(refreshed)
	require.Same(t, viewer, u.agentViewer)
	require.Equal(t, "renamed", viewer.descriptor.Name)
	require.Equal(t, agentrun.StatusDone, viewer.descriptor.Status)

	viewer.descriptor.Name = "stale local value"
	_ = u.handleTeamStatus(refreshed)
	require.Equal(t, "renamed", viewer.descriptor.Name, "every refresh rebuilds the selected descriptor")

	lastDescriptor := viewer.descriptor
	_ = u.handleTeamStatus(teamStatusMsg{ok: true, state: agentteam.State{}})
	require.Same(t, viewer, u.agentViewer)
	require.Equal(t, lastDescriptor, viewer.descriptor)
	require.NotNil(t, viewer.chat.MessageItem("persisted"))
	require.Contains(t, viewer.messagesByID, "persisted")
}

type agentViewerLifecycleCacheItem struct {
	id      string
	version uint64
	bumps   int
}

func (i *agentViewerLifecycleCacheItem) ID() string           { return i.id }
func (i *agentViewerLifecycleCacheItem) Render(int) string    { return i.id }
func (i *agentViewerLifecycleCacheItem) RawRender(int) string { return i.id }
func (i *agentViewerLifecycleCacheItem) Version() uint64      { return i.version }
func (i *agentViewerLifecycleCacheItem) Finished() bool       { return true }
func (i *agentViewerLifecycleCacheItem) Bump() {
	i.version++
	i.bumps++
}

func TestAgentViewerLifecycleThemeApplyAndPreviewInvalidateCaches(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	u.header = newHeader(u.com)
	u.attachments = attachments.New(attachments.NewRenderer(u.com.Styles.Attachments), attachments.Keymap{})
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	items := make([]chat.MessageItem, 0, 12)
	typed := make([]*agentViewerLifecycleCacheItem, 0, 12)
	for idx := range 12 {
		item := &agentViewerLifecycleCacheItem{id: string(rune('a' + idx))}
		items = append(items, item)
		typed = append(typed, item)
	}
	viewer.chat.SetMessages(items...)
	viewer.chat.SetSize(50, 2)
	viewer.chat.ScrollToTop()

	u.previewTheme(styles.CharmtonePantera())
	require.Positive(t, typed[0].bumps, "visible viewer items re-render for theme preview")
	require.Zero(t, typed[len(typed)-1].bumps, "off-screen viewer items retain their preview caches")

	for _, item := range typed {
		item.bumps = 0
	}
	u.applyTheme(styles.CharmtonePantera())
	for _, item := range typed {
		require.Equal(t, 1, item.bumps, "theme apply invalidates every viewer item")
	}
}

func TestAgentViewerLifecycleResizePreservesIndependentScrollAndFollow(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	mainItems, _ := agentViewerUIItems("main", 30)
	u.chat.SetMessages(mainItems...)
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	viewerItems, _ := agentViewerUIItems("viewer", 30)
	viewer.chat.SetMessages(viewerItems...)
	u.chat.SetSize(50, 5)
	viewer.chat.SetSize(50, 5)
	u.chat.RestoreScrollPosition(2, 0)
	viewer.chat.RestoreScrollPosition(6, 0)
	mainIndex, mainLine := u.chat.ScrollPosition()
	viewerIndex, viewerLine := viewer.chat.ScrollPosition()

	_, _ = u.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	require.False(t, u.chat.Follow())
	require.False(t, viewer.chat.Follow())
	gotMainIndex, gotMainLine := u.chat.ScrollPosition()
	require.Equal(t, mainIndex, gotMainIndex)
	require.Equal(t, mainLine, gotMainLine)
	gotViewerIndex, gotViewerLine := viewer.chat.ScrollPosition()
	require.Equal(t, viewerIndex, gotViewerIndex)
	require.Equal(t, viewerLine, gotViewerLine)

	u.chat.ScrollToBottom()
	viewer.chat.RestoreScrollPosition(4, 0)
	viewerIndex, viewerLine = viewer.chat.ScrollPosition()
	_, _ = u.Update(tea.WindowSizeMsg{Width: 90, Height: 26})
	require.True(t, u.chat.Follow())
	require.False(t, viewer.chat.Follow())
	gotViewerIndex, gotViewerLine = viewer.chat.ScrollPosition()
	require.Equal(t, viewerIndex, gotViewerIndex)
	require.Equal(t, viewerLine, gotViewerLine)
}

func TestAgentViewerLifecycleOpenCloseNeverMutatesMainSessionOrTranscript(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	mainSession := u.session
	mainItems, typed := agentViewerUIItems("main", 20)
	u.chat.SetMessages(mainItems...)
	u.chat.SetSize(60, 5)
	u.chat.ScrollToTop()
	u.chat.SetSelected(3)
	mainIndex, mainLine := u.chat.ScrollPosition()
	mainSelected := u.chat.Selected()

	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	generation := viewer.generation
	_ = u.closeAgentViewer()

	requireAgentViewerLifecycleClosed(t, u, viewer, generation)
	require.Same(t, mainSession, u.session)
	require.Equal(t, len(mainItems), u.chat.Len())
	require.Same(t, typed[0], u.chat.MessageItem("main-0"))
	require.Equal(t, mainSelected, u.chat.Selected())
	gotIndex, gotLine := u.chat.ScrollPosition()
	require.Equal(t, mainIndex, gotIndex)
	require.Equal(t, mainLine, gotLine)
	require.Zero(t, ws.cancelCalls)
	require.Zero(t, ws.setCurrentSessionCalls)
}
