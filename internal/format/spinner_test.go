package format

import (
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/ui/anim"
)

// The spinner runs with the terminal in raw mode, so a Ctrl-C arrives as a
// key press and the process never sees SIGINT. The model has to remember the
// key so the caller can end the run the way a signal would, with 130.
func TestModelCtrlCReportsInterrupt(t *testing.T) {
	t.Parallel()

	var interrupted atomic.Bool
	cancelled := false
	m := model{cancel: func() { cancelled = true }, interrupted: &interrupted}

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	require.True(t, cancelled, "ctrl+c must cancel the run")
	require.True(t, interrupted.Load(), "the caller must be able to end with 130")
	require.NotNil(t, cmd, "ctrl+c must quit the spinner")
}

// Esc means the same thing to the spinner, so it must report the interrupt
// too.
func TestModelEscReportsInterrupt(t *testing.T) {
	t.Parallel()

	var interrupted atomic.Bool
	cancelled := false
	m := model{cancel: func() { cancelled = true }, interrupted: &interrupted}

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	require.True(t, cancelled, "esc must cancel the run")
	require.True(t, interrupted.Load(), "the caller must be able to end with 130")
	require.NotNil(t, cmd, "esc must quit the spinner")
}

// A frame tick keeps the spinner going; it must not report the run as
// interrupted.
func TestModelTickIsNotInterrupt(t *testing.T) {
	t.Parallel()

	var interrupted atomic.Bool
	m := model{
		cancel:      func() {},
		anim:        anim.New(anim.Settings{Size: 10, Label: "Generating"}),
		interrupted: &interrupted,
	}

	_, cmd := m.Update(tickMsg{})

	require.False(t, interrupted.Load(), "a tick is not an interrupt")
	require.NotNil(t, cmd, "a tick schedules the next frame")
}
