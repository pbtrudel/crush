package model

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestKeyMapHelpCompleteness(t *testing.T) {
	t.Parallel()

	km := DefaultKeyMap()

	// Space expands/collapses sections (Chat.Expand matches first), so
	// PageDown must not also claim it.
	require.NotContains(t, km.Chat.PageDown.Keys(), " ")
	require.NotContains(t, km.Chat.PageDown.Keys(), "space")

	// Bindings listed in the help views must carry help text.
	require.NotEmpty(t, km.Chat.EndFollow.Help().Key)
	require.NotEmpty(t, km.Chat.EndFollow.Help().Desc)
	require.NotEmpty(t, km.Editor.HistoryPrev.Help().Key)
	require.NotEmpty(t, km.Editor.HistoryPrev.Help().Desc)
	require.NotEmpty(t, km.Editor.HistoryNext.Help().Key)
	require.NotEmpty(t, km.Editor.HistoryNext.Help().Desc)
}

// TestPasteImageBindingAcceptsAltV covers Windows Terminal, which consumes
// ctrl+v itself; alt+v is the keystroke that still reaches the TUI there.
func TestPasteImageBindingAcceptsAltV(t *testing.T) {
	t.Parallel()

	km := DefaultKeyMap()

	require.True(t, key.Matches(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt}, km.Editor.PasteImage))
	require.True(t, key.Matches(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}, km.Editor.PasteImage))
}
