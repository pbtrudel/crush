package dialog

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func newCommandsMouseDialog(t *testing.T) *Commands {
	t.Helper()
	sty := styles.CharmtonePantera()
	c, err := NewCommands(&common.Common{
		Workspace: &rcCommandsWorkspace{},
		Styles:    &sty,
	}, "", false, false, false, false, false, nil, nil)
	require.NoError(t, err)
	return c
}

func selectedCommandID(c *Commands) string {
	if item, ok := c.list.SelectedItem().(*CommandItem); ok && item != nil {
		return item.ID()
	}
	return ""
}

// TestCommandMouseClickRunsItem pins that a single left click runs the command
// under the pointer and selects it, the way a menu item behaves.
func TestCommandMouseClickRunsItem(t *testing.T) {
	t.Parallel()

	c := newCommandsMouseDialog(t)
	scr := uv.NewScreenBuffer(80, 30)
	c.Draw(scr, image.Rect(0, 0, 80, 30))
	area := c.commandListArea()
	require.False(t, area.Empty())

	action := c.HandleMsg(tea.MouseClickMsg(tea.Mouse{
		X:      area.Min.X,
		Y:      area.Min.Y + 1,
		Button: tea.MouseLeft,
	}))

	_, ok := action.(ActionOpenWeb)
	require.True(t, ok, "clicking Open Web Mode must run it")
	require.Equal(t, "open_web", selectedCommandID(c))
}

// TestCommandMouseClickRunsEveryVisibleItem pins the fix: every command in the
// list runs on a single click, not only a few, so the whole palette responds to
// the mouse.
func TestCommandMouseClickRunsEveryVisibleItem(t *testing.T) {
	t.Parallel()

	rows := func() int {
		c := newCommandsMouseDialog(t)
		scr := uv.NewScreenBuffer(80, 30)
		c.Draw(scr, image.Rect(0, 0, 80, 30))
		return min(c.list.Height(), len(c.list.FilteredItems()))
	}()
	require.Greater(t, rows, 1)

	for row := 0; row < rows; row++ {
		c := newCommandsMouseDialog(t)
		scr := uv.NewScreenBuffer(80, 30)
		c.Draw(scr, image.Rect(0, 0, 80, 30))
		area := c.commandListArea()
		item, ok := c.list.FilteredItems()[row].(*CommandItem)
		require.True(t, ok)

		action := c.HandleMsg(tea.MouseClickMsg(tea.Mouse{
			X:      area.Min.X,
			Y:      area.Min.Y + row,
			Button: tea.MouseLeft,
		}))

		require.Equal(t, item.Action(), action, "row %d (%s) must run on a single click", row, item.ID())
		require.Equal(t, item.ID(), selectedCommandID(c))
	}
}

func TestCommandMouseClickIgnoresUnsupportedClicks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		point  func(image.Rectangle) image.Point
		button tea.MouseButton
	}{
		{
			name:   "outside list",
			point:  func(image.Rectangle) image.Point { return image.Pt(0, 0) },
			button: tea.MouseLeft,
		},
		{
			name:   "right button",
			point:  func(area image.Rectangle) image.Point { return image.Pt(area.Min.X, area.Min.Y+1) },
			button: tea.MouseRight,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newCommandsMouseDialog(t)
			scr := uv.NewScreenBuffer(80, 30)
			c.Draw(scr, image.Rect(0, 0, 80, 30))
			point := tt.point(c.commandListArea())

			action := c.HandleMsg(tea.MouseClickMsg(tea.Mouse{
				X:      point.X,
				Y:      point.Y,
				Button: tt.button,
			}))

			require.Nil(t, action)
			require.Equal(t, "new_session", selectedCommandID(c))
		})
	}
}

// TestCommandMouseClickFilteredItem pins that a click hit-tests the filtered
// list: ItemIndexAtPosition returns a filtered index and ItemAt resolves it to
// the filtered item, not the unfiltered one.
func TestCommandMouseClickFilteredItem(t *testing.T) {
	t.Parallel()

	c := newCommandsMouseDialog(t)
	scr := uv.NewScreenBuffer(80, 30)
	c.Draw(scr, image.Rect(0, 0, 80, 30))
	c.list.SetFilter("web")
	c.list.SetSelected(0)
	c.Draw(scr, image.Rect(0, 0, 80, 30))
	area := c.commandListArea()

	action := c.HandleMsg(tea.MouseClickMsg(tea.Mouse{
		X:      area.Min.X,
		Y:      area.Min.Y,
		Button: tea.MouseLeft,
	}))
	_, ok := action.(ActionOpenWeb)
	require.True(t, ok, "clicking the filtered Open Web Mode item must run it")
	require.Equal(t, "open_web", selectedCommandID(c))
}
