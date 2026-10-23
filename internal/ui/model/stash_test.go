package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func TestTextareaStashAndUnstash(t *testing.T) {
	t.Parallel()

	u := newSelectionTestUI()
	u.keyMap = DefaultKeyMap()

	u.textarea.SetValue("draft prompt")

	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "", u.textarea.Value())
	require.Len(t, u.inputStash, 1)
	require.Equal(t, "draft prompt", u.inputStash[0])

	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "draft prompt", u.textarea.Value())
	require.Empty(t, u.inputStash)
}

func TestTextareaUnstashEmptyStash(t *testing.T) {
	t.Parallel()

	u := newSelectionTestUI()
	u.keyMap = DefaultKeyMap()

	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "", u.textarea.Value())
	require.Empty(t, u.inputStash)
}

func TestTextareaStashIsAStack(t *testing.T) {
	t.Parallel()

	u := newSelectionTestUI()
	u.keyMap = DefaultKeyMap()

	u.textarea.SetValue("first")
	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	u.textarea.SetValue("second")
	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	require.Len(t, u.inputStash, 2)

	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "second", u.textarea.Value())
	require.Len(t, u.inputStash, 1)

	// The input must be empty for the shortcut to pop the next stash.
	u.textarea.SetValue("")
	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "first", u.textarea.Value())

	require.Empty(t, u.inputStash)
}

func TestTextareaStashPreservesBangMode(t *testing.T) {
	t.Parallel()

	u := newSelectionTestUI()
	u.keyMap = DefaultKeyMap()

	u.textarea.SetValue("ls -la")
	u.bangMode = true

	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "", u.textarea.Value())
	require.False(t, u.bangMode)

	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "ls -la", u.textarea.Value())
	require.True(t, u.bangMode)
}

func TestTextareaMouseReleaseCopiesSelection(t *testing.T) {
	t.Parallel()

	u := newSelectionTestUI()

	for _, r := range "hello world" {
		u.textarea.InsertRune(r)
	}
	u.textarea.CursorStart()

	startX := u.layout.editor.Min.X + 2
	y := u.layout.editor.Min.Y + 1

	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: startX, Y: y, Button: uv.MouseLeft}))
	_, _ = u.Update(tea.MouseMotionMsg(tea.Mouse{X: startX + 5, Y: y, Button: uv.MouseLeft}))

	_, cmd := u.Update(tea.MouseReleaseMsg(tea.Mouse{X: startX + 5, Y: y, Button: uv.MouseLeft}))

	require.NotNil(t, cmd)
	require.True(t, u.textarea.HasSelection())
	require.Equal(t, "hello", u.textarea.SelectedText())
}

func TestTextareaMouseClickDoesNotCopy(t *testing.T) {
	t.Parallel()

	u := newSelectionTestUI()

	u.textarea.SetValue("hello")

	x := u.layout.editor.Min.X + 2
	y := u.layout.editor.Min.Y + 1

	_, _ = u.Update(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: uv.MouseLeft}))
	_, cmd := u.Update(tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: uv.MouseLeft}))

	require.Nil(t, cmd)
	require.False(t, u.textarea.HasSelection())
}

func TestTextareaStashCtrlD(t *testing.T) {
	t.Parallel()

	u := newSelectionTestUI()
	u.keyMap = DefaultKeyMap()

	u.textarea.SetValue("draft prompt")

	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "", u.textarea.Value())
	require.Len(t, u.inputStash, 1)

	_, _ = u.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	require.Equal(t, "draft prompt", u.textarea.Value())
	require.Empty(t, u.inputStash)
}
