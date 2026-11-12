package model

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/attachments"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

type agentViewerThemeBlockingWorkspace struct {
	*agentViewerUIWorkspace
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *agentViewerThemeBlockingWorkspace) ListMessages(ctx context.Context, sessionID string) ([]message.Message, error) {
	if sessionID == "child" {
		w.once.Do(func() { close(w.started) })
		select {
		case <-w.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return w.agentViewerUIWorkspace.ListMessages(ctx, sessionID)
}

func TestAgentViewerLoadThemeChangeDuringReadRetriesWithCurrentStyles(t *testing.T) {
	t.Parallel()
	for _, apply := range []bool{false, true} {
		name := "preview"
		if apply {
			name = "apply"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			u, base := newAgentViewerUITest(t)
			base.messages["child"] = []message.Message{agentViewerTextMessage("theme-message", "theme-safe transcript")}
			ws := &agentViewerThemeBlockingWorkspace{
				agentViewerUIWorkspace: base,
				started:                make(chan struct{}),
				release:                make(chan struct{}),
			}
			u.com.Workspace = ws
			u.header = newHeader(u.com)
			u.attachments = attachments.New(attachments.NewRenderer(u.com.Styles.Attachments), attachments.Keymap{})
			cmd := u.openAgentViewer(dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
			viewer := u.agentViewer
			result := make(chan tea.Msg, 1)
			go func() { result <- cmd() }()
			released := false
			defer func() {
				if !released {
					close(ws.release)
				}
			}()

			select {
			case <-ws.started:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "load did not reach the blocking message read")
			}
			if apply {
				u.applyTheme(styles.CharmtonePantera())
			} else {
				u.previewTheme(styles.CharmtonePantera())
			}
			close(ws.release)
			released = true

			var load agentViewerLoadMsg
			select {
			case msg := <-result:
				var ok bool
				load, ok = msg.(agentViewerLoadMsg)
				require.True(t, ok)
			case <-time.After(5 * time.Second):
				require.FailNow(t, "blocked load did not finish")
			}
			retry := viewer.HandleLoad(load)
			require.True(t, viewer.loading, "a result constructed for the old theme remains pending")
			require.Zero(t, viewer.chat.Len(), "stale-themed items are not installed after invalidation")
			require.NotNil(t, retry)

			refreshed, ok := retry().(agentViewerLoadMsg)
			require.True(t, ok)
			viewer.HandleLoad(refreshed)
			require.False(t, viewer.loading)
			require.NotNil(t, viewer.chat.MessageItem("theme-message"))
		})
	}
}

func TestAgentViewerUIDelayedHighlightCopyUsesOriginAfterClose(t *testing.T) {
	u, _ := newAgentViewerUITest(t)
	mainItem := &agentViewerUIItem{id: "main", text: "main highlight"}
	u.chat.SetMessages(mainItem)
	u.chat.SetSelected(0)
	u.updateLayoutAndSize()
	mainX := u.layout.main.Min.X + 4
	mainY := u.layout.main.Min.Y
	_, _ = u.Update(tea.MouseClickMsg{X: mainX, Y: mainY, Button: uv.MouseLeft})
	_, _ = u.Update(tea.MouseMotionMsg{X: mainX + 4, Y: mainY, Button: uv.MouseLeft})
	_, _ = u.Update(tea.MouseReleaseMsg{X: mainX + 4, Y: mainY, Button: uv.MouseLeft})
	require.True(t, u.chat.HasHighlight())
	mainText := u.chat.HighlightContent()

	viewer := openLoadedAgentViewerUI(t, u, dialog.AgentDescriptor{SessionID: "child", Name: "researcher"})
	viewerItem := &agentViewerUIItem{id: "viewer", text: "viewer highlight"}
	viewer.chat.SetMessages(viewerItem)
	viewer.chat.SetSelected(0)
	u.updateLayoutAndSize()
	viewerX := u.layout.agentViewer.Min.X + 4
	viewerY := u.layout.agentViewer.Min.Y + 2
	_, _ = u.Update(tea.MouseClickMsg{X: viewerX, Y: viewerY, Button: uv.MouseLeft})
	_, _ = u.Update(tea.MouseMotionMsg{X: viewerX + 4, Y: viewerY, Button: uv.MouseLeft})
	_, scheduled := u.Update(tea.MouseReleaseMsg{X: viewerX + 4, Y: viewerY, Button: uv.MouseLeft})
	scr := uv.NewScreenBuffer(u.width, u.height)
	chatArea := u.layout.agentViewer
	chatArea.Min.Y += 2
	viewer.chat.Draw(scr, chatArea)
	require.True(t, viewer.chat.HasHighlight())
	viewerText := viewer.chat.HighlightContent()
	require.NotEmpty(t, mainText)
	require.NotEmpty(t, viewerText)
	require.NotEqual(t, mainText, viewerText)

	var delayed copyChatHighlightMsg
	found := false
	for _, msg := range runCmdCollect(scheduled) {
		if copyMsg, ok := msg.(copyChatHighlightMsg); ok {
			delayed = copyMsg
			found = true
		}
	}
	require.True(t, found, "mouse release schedules delayed highlight copying")
	_ = u.closeAgentViewer()
	require.True(t, u.chat.HasHighlight())

	originalCopy := copyChatHighlightToClipboard
	t.Cleanup(func() { copyChatHighlightToClipboard = originalCopy })
	copied := ""
	copyChatHighlightToClipboard = func(text, _ string, callback tea.Cmd) tea.Cmd {
		copied = text
		return callback
	}
	_, copyCmd := u.Update(delayed)
	runCmdCollect(copyCmd)

	require.Equal(t, viewerText, copied)
	require.True(t, u.chat.HasHighlight(), "copying a closed viewer must not clear the main selection")
	require.False(t, viewer.chat.HasHighlight(), "the originating viewer selection is cleared after copying")
}
