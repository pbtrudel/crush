package dialog

import (
	"image"
	"testing"

	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestSessionsDialogEmptyMessages(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("no sessions at all", func(t *testing.T) {
		t.Parallel()

		d, err := NewSessions(&common.Common{
			Workspace: &sessionMouseWorkspace{sessions: nil},
			Styles:    &sty,
		}, "")
		require.NoError(t, err)

		scr := uv.NewScreenBuffer(80, 30)
		d.Draw(scr, image.Rect(0, 0, 80, 30))
		require.Contains(t, ansi.Strip(scr.Render()), "No sessions")
	})

	t.Run("filter matches nothing", func(t *testing.T) {
		t.Parallel()

		d, err := NewSessions(&common.Common{
			Workspace: &sessionMouseWorkspace{sessions: []session.Session{
				{ID: "s1", Title: "alpha"},
			}},
			Styles: &sty,
		}, "")
		require.NoError(t, err)
		d.list.SetFilter("zzz-no-match")

		scr := uv.NewScreenBuffer(80, 30)
		d.Draw(scr, image.Rect(0, 0, 80, 30))
		require.Contains(t, ansi.Strip(scr.Render()), "No results")
	})
}

func TestSessionsDialogRefreshSession(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	d, err := NewSessions(&common.Common{
		Workspace: &sessionMouseWorkspace{sessions: []session.Session{
			{ID: "s1", Title: "before"},
		}},
		Styles: &sty,
	}, "")
	require.NoError(t, err)

	d.RefreshSession(session.Session{ID: "s1", Title: "after", Cost: 1.5})

	require.Equal(t, "after", d.sessions[0].Title)
	require.InDelta(t, 1.5, d.sessions[0].Cost, 1e-9)
	item, ok := d.list.SelectedItem().(*SessionItem)
	require.True(t, ok)
	require.Equal(t, "after", item.Title)
}

func TestSessionsDialogFilterMatchesIDAndWorkingDir(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	d, err := NewSessions(&common.Common{
		Workspace: &sessionMouseWorkspace{
			sessions: []session.Session{
				{ID: "ABC-123", Title: "alpha"},
				{ID: "def-456", Title: "beta"},
			},
			workingDir: "/tmp/project",
		},
		Styles: &sty,
	}, "")
	require.NoError(t, err)

	d.list.SetFilter("abc-123")
	require.Len(t, d.list.FilteredItems(), 1)

	d.list.SetFilter("/tmp/project")
	require.Len(t, d.list.FilteredItems(), 2)
}
