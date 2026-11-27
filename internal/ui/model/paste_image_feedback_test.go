package model

import (
	"reflect"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/attachments"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

// newNoVisionUI builds an editor-focused UI whose current model has no vision,
// the state in which image paste used to be refused without a word.
func newNoVisionUI(t *testing.T) *UI {
	t.Helper()

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("test-provider", config.ProviderConfig{
		ID: "test-provider",
		Models: []catwalk.Model{
			{ID: "test-model", Capabilities: catwalk.Capabilities{Vision: false}},
		},
	})
	cfg := &config.Config{
		Models: map[config.SelectedModelType]config.SelectedModel{
			config.SelectedModelTypeMain: {Provider: "test-provider", Model: "test-model"},
		},
		Providers: providers,
		Agents: map[string]config.Agent{
			config.AgentCoder: {Model: config.SelectedModelTypeMain},
		},
	}

	sty := styles.CharmtonePantera()
	com := &common.Common{Workspace: &testWorkspace{cfg: cfg}, Styles: &sty}
	keyMap := DefaultKeyMap()
	u := &UI{
		com:      com,
		keyMap:   keyMap,
		state:    uiChat,
		focus:    uiFocusEditor,
		session:  &session.Session{ID: "sess-1"},
		chat:     NewChat(com, config.ScrollbarDefault),
		textarea: textarea.New(),
		dialog:   dialog.NewOverlay(),
		attachments: attachments.New(nil, attachments.Keymap{
			DeleteMode: keyMap.Editor.AttachmentDeleteMode,
			DeleteAll:  keyMap.Editor.DeleteAllAttachments,
			Escape:     keyMap.Editor.Escape,
		}),
		width:  140,
		height: 45,
	}
	u.status = NewStatus(com, u)
	return u
}

// collectCmdMsgs runs a command and flattens any batch/sequence it yields into
// the leaf messages, so a test can inspect what a keystroke reports.
func collectCmdMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	rv := reflect.ValueOf(msg)
	if rv.IsValid() && rv.Kind() == reflect.Slice && rv.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		var out []tea.Msg
		for i := 0; i < rv.Len(); i++ {
			sub, _ := rv.Index(i).Interface().(tea.Cmd)
			out = append(out, collectCmdMsgs(sub)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func warnMessage(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	require.NotNil(t, cmd, "the keystroke must produce a command, not be silently ignored")
	for _, msg := range collectCmdMsgs(cmd) {
		if info, ok := msg.(util.InfoMsg); ok && info.Type == util.InfoTypeWarn {
			return info.Msg
		}
	}
	return ""
}

// TestPasteImageKeyWarnsWhenModelHasNoVision pins that paste-image on a model
// without vision tells the user so, instead of the keystroke doing nothing.
func TestPasteImageKeyWarnsWhenModelHasNoVision(t *testing.T) {
	t.Parallel()

	u := newNoVisionUI(t)
	require.False(t, u.currentModelSupportsImages())

	cmd := u.handleKeyPressMsg(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	require.Contains(t, warnMessage(t, cmd), "does not support image attachments")
}

// TestAddImageKeyWarnsWhenModelHasNoVision pins the same feedback for the
// add-image shortcut.
func TestAddImageKeyWarnsWhenModelHasNoVision(t *testing.T) {
	t.Parallel()

	u := newNoVisionUI(t)
	require.False(t, u.currentModelSupportsImages())

	cmd := u.handleKeyPressMsg(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Contains(t, warnMessage(t, cmd), "does not support image attachments")
}
