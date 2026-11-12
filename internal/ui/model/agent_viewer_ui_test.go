package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agent"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/attachments"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

type agentViewerUIWorkspace struct {
	agentViewerWorkspace
	cancelCalls            int
	setCurrentSessionCalls int
}

func (w *agentViewerUIWorkspace) AgentCancel(string) { w.cancelCalls++ }

func (w *agentViewerUIWorkspace) SetCurrentSession(context.Context, string) error {
	w.setCurrentSessionCalls++
	return nil
}

func (w *agentViewerUIWorkspace) ParseAgentToolSessionID(sessionID string) (string, string, bool) {
	if sessionID != "child" {
		return "", "", false
	}
	return "parent-message", "parent-tool", true
}

func newAgentViewerUITest(t *testing.T) (*UI, *agentViewerUIWorkspace) {
	t.Helper()
	ws := &agentViewerUIWorkspace{agentViewerWorkspace: agentViewerWorkspace{
		messages: make(map[string][]message.Message),
	}}
	u := newTestUI()
	u.com.Workspace = ws
	u.session = &session.Session{ID: "main", Title: "MAIN_ONLY_SIDEBAR"}
	u.keyMap = DefaultKeyMap()
	u.dialog = dialog.NewOverlay()
	u.attachments = attachments.New(nil, attachments.Keymap{})
	u.frames = newFrameCache(time.Minute, 8)
	u.focus = uiFocusMain
	u.status = NewStatus(u.com, u)
	warmCaches(u, false)
	u.updateLayoutAndSize()
	return u, ws
}

func openLoadedAgentViewerUI(t *testing.T, u *UI, descriptor dialog.AgentDescriptor) *agentViewer {
	t.Helper()
	cmd := u.openAgentViewer(descriptor)
	require.NotNil(t, cmd)
	messages := runCmdCollect(cmd)
	require.Len(t, messages, 1)
	load, ok := messages[0].(agentViewerLoadMsg)
	require.True(t, ok)
	_, _ = u.Update(load)
	require.NotNil(t, u.agentViewer)
	require.NoError(t, u.agentViewer.loadErr)
	return u.agentViewer
}

func TestAgentViewerUICommandPaletteContainsViewAgents(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	commands, err := dialog.NewCommands(u.com, "main", true, false, false, false, false, nil, nil)
	require.NoError(t, err)
	for _, r := range "View Agents" {
		code := r
		if r == ' ' {
			code = tea.KeySpace
		}
		commands.HandleMsg(tea.KeyPressMsg{Code: code, Text: string(r)})
	}
	action := commands.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, dialog.ActionOpenDialog{DialogID: dialog.AgentsID}, action)
}

func TestAgentViewerUIOpenSelectorAndReplacementGeneration(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	u.chat.SetRevealSecrets(true)
	u.agentRuns = []agentrun.Run{{
		SessionID:       "child",
		ParentSessionID: "main",
		Name:            "researcher",
		Kind:            agentrun.KindSubagent,
		Status:          agentrun.StatusDone,
	}}

	_ = u.openAgentsDialog()
	require.True(t, u.dialog.ContainsDialog(dialog.AgentsID))
	cmd := u.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, u.dialog.ContainsDialog(dialog.AgentsID))
	require.NotNil(t, u.agentViewer)
	require.Equal(t, "child", u.agentViewer.descriptor.SessionID)
	require.Equal(t, uint64(1), u.agentViewerGeneration)
	require.Equal(t, uint64(1), u.agentViewer.generation)
	require.True(t, u.agentViewer.chat.Focused())
	require.True(t, u.agentViewer.chat.RevealSecrets())
	require.NotNil(t, cmd, "selecting an agent starts its asynchronous load")

	first := u.agentViewer
	firstLoad := agentViewerLoadMsg{
		generation: first.generation,
		sessionID:  first.descriptor.SessionID,
		session:    session.Session{ID: "child"},
		messages:   []message.Message{agentViewerTextMessage("stale", "STALE_TRANSCRIPT")},
	}
	_ = u.openAgentViewer(dialog.AgentDescriptor{SessionID: "child-two", Name: "second"})
	require.NotSame(t, first, u.agentViewer)
	require.Equal(t, uint64(2), u.agentViewerGeneration)
	require.Equal(t, uint64(2), u.agentViewer.generation)

	_, _ = u.Update(firstLoad)
	require.Equal(t, "child-two", u.agentViewer.descriptor.SessionID)
	require.Zero(t, u.agentViewer.chat.Len(), "a replaced viewer's result cannot populate its replacement")

	currentMessage := message.Message{
		ID: "current", SessionID: "child-two", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "CURRENT_TRANSCRIPT"}},
	}
	_, _ = u.Update(agentViewerLoadMsg{
		generation: 2,
		sessionID:  "child-two",
		session:    session.Session{ID: "child-two"},
		messages:   []message.Message{currentMessage},
	})
	require.NotNil(t, u.agentViewer.chat.MessageItem("current"))
	require.Nil(t, u.agentViewer.chat.MessageItem("stale"))
}

func TestAgentViewerUIClosePreservesMainAndDialogPriority(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	mainSession := u.session
	mainItems := make([]chat.MessageItem, 0, 20)
	for i := range 20 {
		mainItems = append(mainItems, &focusableTestItem{testMessageItem: testMessageItem{id: fmt.Sprintf("main-%d", i), text: "main"}})
	}
	u.chat.SetMessages(mainItems...)
	u.chat.SetSize(60, 5)
	u.chat.ScrollToTop()
	u.chat.SetSelected(3)
	u.textarea.SetValue("draft stays intact")
	mainIndex, mainLine := u.chat.ScrollPosition()
	mainSelected := u.chat.Selected()
	ws.busy = true
	warmCaches(u, true)

	_ = u.openAgentViewer(dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	_, _ = u.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Nil(t, u.agentViewer)
	require.Same(t, mainSession, u.session)
	require.Equal(t, mainSelected, u.chat.Selected())
	gotIndex, gotLine := u.chat.ScrollPosition()
	require.Equal(t, mainIndex, gotIndex)
	require.Equal(t, mainLine, gotLine)
	require.Equal(t, "draft stays intact", u.textarea.Value())
	require.Equal(t, 20, u.chat.Len())
	require.Zero(t, ws.cancelCalls)
	require.Zero(t, ws.setCurrentSessionCalls)

	_ = u.openAgentViewer(dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	u.dialog.OpenDialog(dialog.NewAgents(u.com, nil))
	_, _ = u.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NotNil(t, u.agentViewer, "esc closes the front dialog before the viewer")
	require.False(t, u.dialog.HasDialogs())
	_, _ = u.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Nil(t, u.agentViewer)
	require.Zero(t, ws.cancelCalls)
	require.Zero(t, ws.setCurrentSessionCalls)
}

func TestAgentViewerUIInitialLoadFailureClosesOnlyViewer(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	loadErr := errors.New("history unavailable")
	ws.getErr = loadErr
	u.chat.SetMessages(testMessageItem{id: "main", text: "main transcript"})
	mainSession := u.session

	cmd := u.openAgentViewer(dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	messages := runCmdCollect(cmd)
	require.Len(t, messages, 1)
	_, reportCmd := u.Update(messages[0])
	require.Nil(t, u.agentViewer)
	require.Same(t, mainSession, u.session)
	require.NotNil(t, u.chat.MessageItem("main"))
	require.Zero(t, ws.cancelCalls)
	require.Zero(t, ws.setCurrentSessionCalls)

	var reported bool
	for _, msg := range runCmdCollect(reportCmd) {
		if info, ok := msg.(util.InfoMsg); ok && info.Type == util.InfoTypeError {
			reported = true
			require.Contains(t, info.Msg, loadErr.Error())
		}
	}
	require.True(t, reported, "initial load failure is reported to the user")
}

func TestAgentViewerUIEventForwardingKeepsMainProcessing(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	parentTool := chat.NewToolMessageItem(u.com.Styles, "parent-message", message.ToolCall{
		ID: "parent-tool", Name: agent.AgentToolName, Input: `{}`, Finished: true,
	}, nil, false, u.com.Workspace.WorkingDir())
	u.chat.SetMessages(parentTool)
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})

	childEvent := pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
		ID: "child-event", SessionID: "child", Role: message.Assistant,
		Parts: []message.ContentPart{message.ToolCall{ID: "nested-tool", Name: "view", Input: `{}`, Finished: true}},
	}}
	_, _ = u.Update(childEvent)
	require.Contains(t, viewer.messagesByID, "child-event")
	require.NotNil(t, viewer.chat.MessageItem("nested-tool"))
	container, ok := parentTool.(chat.NestedToolContainer)
	require.True(t, ok)
	require.Len(t, container.NestedTools(), 1)
	require.Equal(t, "nested-tool", container.NestedTools()[0].ID())

	unrelated := childEvent
	unrelated.Payload.ID = "unrelated"
	unrelated.Payload.SessionID = "other"
	_, _ = u.Update(unrelated)
	require.NotContains(t, viewer.messagesByID, "unrelated")
	require.Len(t, container.NestedTools(), 1)

	mainMessage := message.Message{
		ID: "main-live", SessionID: "main", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "main still updates"}},
	}
	_, _ = u.Update(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: mainMessage})
	require.NotNil(t, u.chat.MessageItem("main-live"))
	require.NotContains(t, viewer.messagesByID, "main-live")
}

func TestAgentViewerUIDrawsFullScreenAndBypassesFrameCache(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	child := message.Message{
		ID: "child-message", SessionID: "child", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "CHILD_ONLY_TRANSCRIPT"}},
	}
	ws.messages["child"] = []message.Message{child}
	u.chat.SetMessages(testMessageItem{id: "main", text: "MAIN_ONLY_TRANSCRIPT"})
	u.textarea.SetValue("MAIN_ONLY_EDITOR")
	openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})

	for _, size := range []struct{ width, height int }{{100, 24}, {28, 8}} {
		u.width, u.height = size.width, size.height
		u.updateLayoutAndSize()
		scr := uv.NewScreenBuffer(size.width, size.height)
		u.Draw(scr, scr.Bounds())
		plain := ansi.Strip(scr.Render())
		require.Contains(t, plain, "researcher")
		require.Contains(t, plain, "CHILD_ONLY_TRANSCRIPT")
		if size.width == 100 {
			require.Contains(t, plain, "esc back")
		}
		require.NotContains(t, plain, "MAIN_ONLY_TRANSCRIPT")
		require.NotContains(t, plain, "MAIN_ONLY_EDITOR")
		require.NotContains(t, plain, "MAIN_ONLY_SIDEBAR")
		require.False(t, u.frameCacheable())
		_, cacheable := u.currentFrameKey()
		require.False(t, cacheable)
	}
}

type agentViewerUIItem struct {
	id        string
	text      string
	focused   bool
	expanded  bool
	copies    int
	clicks    int
	spinning  bool
	advances  int
	version   uint64
	highlight [4]int
}

func (i *agentViewerUIItem) ID() string           { return i.id }
func (i *agentViewerUIItem) Render(int) string    { return i.text }
func (i *agentViewerUIItem) RawRender(int) string { return i.text }
func (i *agentViewerUIItem) Version() uint64      { return i.version }
func (i *agentViewerUIItem) Finished() bool       { return !i.spinning }
func (i *agentViewerUIItem) SetFocused(focused bool) {
	i.focused = focused
}

func (i *agentViewerUIItem) ToggleExpanded() bool {
	i.expanded = !i.expanded
	i.version++
	return true
}

func (i *agentViewerUIItem) HandleKeyEvent(msg tea.KeyMsg) (bool, tea.Cmd) {
	if msg.String() == "c" || msg.String() == "y" {
		i.copies++
		return true, nil
	}
	return false, nil
}

func (i *agentViewerUIItem) HandleMouseClick(ansi.MouseButton, int, int) bool {
	i.clicks++
	return true
}

func (i *agentViewerUIItem) SetHighlight(startLine, startCol, endLine, endCol int) {
	i.highlight = [4]int{startLine, startCol, endLine, endCol}
}

func (i *agentViewerUIItem) Highlight() (int, int, int, int) {
	return i.highlight[0], i.highlight[1], i.highlight[2], i.highlight[3]
}
func (i *agentViewerUIItem) Spinning() bool { return i.spinning }
func (i *agentViewerUIItem) Advance() bool {
	if !i.spinning {
		return false
	}
	i.advances++
	i.version++
	return true
}

func agentViewerUIItems(prefix string, count int) ([]chat.MessageItem, []*agentViewerUIItem) {
	items := make([]chat.MessageItem, 0, count)
	typed := make([]*agentViewerUIItem, 0, count)
	for idx := range count {
		item := &agentViewerUIItem{id: fmt.Sprintf("%s-%d", prefix, idx), text: fmt.Sprintf("%s %d", prefix, idx)}
		items = append(items, item)
		typed = append(typed, item)
	}
	return items, typed
}

func TestAgentViewerUIKeyboardRoutesScrollExpandCopyAndGlobals(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	mainItems, mainTyped := agentViewerUIItems("main", 30)
	u.chat.SetMessages(mainItems...)
	u.chat.SetSize(50, 5)
	u.chat.SetSelected(4)
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	viewerItems, viewerTyped := agentViewerUIItems("viewer", 30)
	viewer.chat.SetMessages(viewerItems...)
	viewer.chat.SetSelected(0)
	u.updateLayoutAndSize()
	mainSelected := u.chat.Selected()
	mainIndex, mainLine := u.chat.ScrollPosition()

	_, _ = u.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	require.Equal(t, 1, viewer.chat.Selected())
	require.Equal(t, mainSelected, u.chat.Selected())
	_, _ = u.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	require.True(t, viewerTyped[1].expanded)
	require.False(t, mainTyped[mainSelected].expanded)
	_, _ = u.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	require.Equal(t, 1, viewerTyped[1].copies)
	require.Zero(t, mainTyped[mainSelected].copies)
	_, _ = u.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	require.NotZero(t, viewer.chat.Offset())
	gotMainIndex, gotMainLine := u.chat.ScrollPosition()
	require.Equal(t, mainIndex, gotMainIndex)
	require.Equal(t, mainLine, gotMainLine)

	_, _ = u.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.True(t, u.dialog.ContainsDialog(dialog.CommandsID))
	require.NotNil(t, u.agentViewer)
	_, _ = u.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.NotNil(t, u.agentViewer, "closing Commands leaves the viewer open")
	_, _ = u.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.True(t, u.dialog.ContainsDialog(dialog.QuitID))
	require.NotNil(t, u.agentViewer)
	require.Zero(t, ws.cancelCalls)
}

func TestAgentViewerUIMouseDelayedCopyScrollbarAndWarmRouting(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	mainItem := &agentViewerUIItem{id: "main", text: "main text"}
	u.chat.SetMessages(mainItem)
	u.chat.SetSelected(0)
	u.chat.pendingClickID = 1
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	viewerItem := &agentViewerUIItem{id: "viewer", text: "viewer text"}
	viewer.chat.SetMessages(viewerItem)
	viewer.chat.SetSelected(0)
	u.updateLayoutAndSize()

	x := u.layout.agentViewer.Min.X + 4
	y := u.layout.agentViewer.Min.Y + 2
	_, _ = u.Update(tea.MouseClickMsg{X: x, Y: y, Button: uv.MouseLeft})
	require.True(t, viewer.chat.mouseDown)
	require.False(t, u.chat.mouseDown)
	require.Equal(t, 4, viewer.chat.mouseDownX)
	require.Zero(t, viewer.chat.mouseDownY)
	_, _ = u.Update(tea.MouseMotionMsg{X: x + 4, Y: y, Button: uv.MouseLeft})
	require.True(t, viewer.chat.HasHighlight())
	require.False(t, u.chat.HasHighlight())
	_, copyCmd := u.Update(tea.MouseReleaseMsg{X: x + 4, Y: y, Button: uv.MouseLeft})
	require.False(t, viewer.chat.mouseDown)
	require.NotNil(t, copyCmd, "mouse release schedules highlight copying")
	require.Same(t, viewer.chat, u.activeChat())
	_, highlightCmd := u.Update(copyChatHighlightMsg{})
	require.NotNil(t, highlightCmd)
	require.Same(t, viewer.chat, u.activeChat())

	viewer.chat.ClearMouse()
	u.chat.pendingClickID = viewer.chat.pendingClickID
	_, _ = u.Update(DelayedClickMsg{ClickID: viewer.chat.pendingClickID})
	require.Equal(t, 1, viewerItem.clicks)
	require.Zero(t, mainItem.clicks)

	viewer.chat.scrollbarMode = config.ScrollbarDefault
	u.chat.scrollbarMode = config.ScrollbarDefault
	viewer.chat.scrollbarHideSeq = 7
	viewer.chat.scrollbarVisible = true
	u.chat.scrollbarHideSeq = 7
	u.chat.scrollbarVisible = true
	_, _ = u.Update(scrollbarHideMsg{seq: 7})
	require.False(t, viewer.chat.scrollbarVisible)
	require.True(t, u.chat.scrollbarVisible)

	viewer.chat.resizing = true
	viewer.chat.resizeSettleSeq = 9
	u.chat.resizing = true
	u.chat.resizeSettleSeq = 9
	_, _ = u.Update(chatWarmMsg{seq: 9})
	require.False(t, viewer.chat.resizing)
	require.True(t, u.chat.resizing)
}

func TestAgentViewerUIWheelRoutesOnlyToViewer(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	mainItems, _ := agentViewerUIItems("main", 40)
	u.chat.SetMessages(mainItems...)
	u.chat.ScrollToTop()
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	viewerItems, _ := agentViewerUIItems("viewer", 40)
	viewer.chat.SetMessages(viewerItems...)
	viewer.chat.ScrollToTop()
	u.updateLayoutAndSize()
	mainIndex, mainLine := u.chat.ScrollPosition()

	_, _ = u.Update(common.CoalescedWheelMsg{DeltaY: 3})
	require.NotZero(t, viewer.chat.Offset())
	gotMainIndex, gotMainLine := u.chat.ScrollPosition()
	require.Equal(t, mainIndex, gotMainIndex)
	require.Equal(t, mainLine, gotMainLine)
}

func TestAgentViewerUIAnimationOwnsVisibleClockAndRearmsMainOnClose(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	ws.busy = true
	mainSpinner := &agentViewerUIItem{id: "main", text: "main", spinning: true}
	u.chat.SetMessages(mainSpinner)
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{
		SessionID: "child", Name: "researcher", Status: agentrun.StatusRunning,
	})
	viewerSpinner := &agentViewerUIItem{id: "viewer", text: "viewer", spinning: true}
	viewer.chat.SetMessages(viewerSpinner)
	u.updateLayoutAndSize()
	if !u.chat.animRunning {
		require.NotNil(t, u.chat.EnsureAnimating())
	}
	if !viewer.chat.animRunning {
		require.NotNil(t, viewer.chat.EnsureAnimating())
	}
	mainGeneration := u.chat.animGen
	viewerGeneration := viewer.chat.animGen
	require.Equal(t, mainGeneration, viewerGeneration, "the incoming shared message can retire the hidden main clock")

	_, _ = u.Update(animTickMsg{gen: viewerGeneration})
	require.Zero(t, mainSpinner.advances)
	require.Equal(t, 1, viewerSpinner.advances)
	require.False(t, u.chat.animRunning, "the hidden main clock is not re-armed")
	require.True(t, viewer.chat.animRunning)
	require.False(t, u.chat.externalAnimation)
	require.True(t, viewer.chat.externalAnimation)

	_ = u.closeAgentViewer()
	require.True(t, u.chat.animRunning, "closing the viewer rearms the main chat clock")
	_, _ = u.Update(neutralMsg{})
	require.False(t, u.chat.externalAnimation)
	_, _ = u.Update(animTickMsg{gen: u.chat.animGen})
	require.Equal(t, 1, mainSpinner.advances)
}

func TestAgentViewerUIReconciliationErrorKeepsViewerOpen(t *testing.T) {
	t.Parallel()

	u, _ := newAgentViewerUITest(t)
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	viewer.reconcileInFlight = true
	_, cmd := u.Update(agentViewerLoadMsg{
		generation: viewer.generation,
		sessionID:  viewer.descriptor.SessionID,
		reconcile:  true,
		err:        errors.New("refresh failed"),
	})
	require.Same(t, viewer, u.agentViewer)
	require.NotNil(t, cmd)
	require.Error(t, viewer.loadErr)
	require.False(t, viewer.reconcileInFlight)
}

func TestAgentViewerUITextRemainsReadableAtNarrowWidth(t *testing.T) {
	t.Parallel()

	u, ws := newAgentViewerUITest(t)
	ws.messages["child"] = []message.Message{{
		ID: "long", SessionID: "child", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: strings.Repeat("transcript ", 20)}},
	}}
	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "a"})
	u.width, u.height = 12, 6
	u.updateLayoutAndSize()
	require.Equal(t, u.layout.agentViewer.Dx(), viewer.width)
	require.Equal(t, u.layout.agentViewer.Dy(), viewer.height)
	require.Equal(t, max(0, viewer.height-2), viewer.chat.Height())
	scr := uv.NewScreenBuffer(u.width, u.height)
	require.NotPanics(t, func() { u.Draw(scr, scr.Bounds()) })
}
