package model

import (
	"testing"

	"charm.land/bubbles/v2/key"
	"github.com/charmbracelet/crush/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// webHelpKeyMap is an empty help keymap for the status draw test.
type webHelpKeyMap struct{}

func (webHelpKeyMap) ShortHelp() []key.Binding  { return nil }
func (webHelpKeyMap) FullHelp() [][]key.Binding { return nil }

func TestStatusDrawShowsWebBadge(t *testing.T) {
	t.Parallel()

	com := common.DefaultCommon(nil)
	com.Web = &fakeWeb{running: true}
	s := NewStatus(com, webHelpKeyMap{})
	scr := uv.NewScreenBuffer(80, 1)
	s.Draw(scr, scr.Bounds())

	require.Contains(t, ansi.Strip(scr.Render()), "WEB")
}
