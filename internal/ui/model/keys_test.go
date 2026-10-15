package model

import (
	"testing"

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
