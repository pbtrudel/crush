package model

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/pinentry"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

func TestHandlePinentryEvent(t *testing.T) {
	t.Parallel()

	newUI := func() *UI {
		com := &common.Common{
			Workspace: &testWorkspace{},
			Styles:    &styles.Styles{},
		}
		return &UI{
			com:    com,
			status: NewStatus(com, nil),
		}
	}

	t.Run("tracks terminal handover state without a program", func(t *testing.T) {
		t.Parallel()

		ui := newUI()
		ui.handlePinentryEvent(pinentry.Event{Active: true, CtrlLRedraw: true})
		require.True(t, ui.pinentryActive)
		require.True(t, ui.pinentryCtrlL)

		ui.handlePinentryEvent(pinentry.Event{})
		require.False(t, ui.pinentryActive)
	})

	t.Run("shows and clears the security key touch hint", func(t *testing.T) {
		t.Parallel()

		ui := newUI()
		ui.handlePinentryEvent(pinentry.Event{TouchPending: true})
		require.True(t, ui.pinentryTouchPending)
		require.Equal(t, pinentryTouchMessage, ui.status.msg.Msg)

		ui.handlePinentryEvent(pinentry.Event{})
		require.False(t, ui.pinentryTouchPending)
		require.True(t, ui.status.msg.IsEmpty())
	})

	t.Run("clearing the hint schedules the pending update's timer", func(t *testing.T) {
		t.Parallel()

		ui := newUI()
		ui.handlePinentryEvent(pinentry.Event{TouchPending: true})
		require.Nil(t, ui.status.Show(util.InfoMsg{
			Type: util.InfoTypeUpdate, Msg: "development version", TTL: time.Millisecond,
		}), "the update waits behind the touch hint")

		msgs := runCmdCollect(ui.handlePinentryEvent(pinentry.Event{}))
		require.False(t, ui.pinentryTouchPending)
		require.Equal(t, "development version", ui.status.msg.Msg)
		require.Len(t, msgs, 1, "promoting the update must also start its timer")
		clear, ok := msgs[0].(util.ClearStatusMsg)
		require.True(t, ok)
		require.Equal(t, ui.status.msgSeq, clear.Seq)
	})

	t.Run("repeated events are idempotent", func(t *testing.T) {
		t.Parallel()

		ui := newUI()
		ui.handlePinentryEvent(pinentry.Event{TouchPending: true})
		ui.handlePinentryEvent(pinentry.Event{TouchPending: true})
		require.True(t, ui.pinentryTouchPending)
		require.Equal(t, pinentryTouchMessage, ui.status.msg.Msg)
	})
}
