package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/stretchr/testify/require"
)

// expire runs a status expiry command and returns the message it delivers.
func expire(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)
	return cmd()
}

func instant(text string) util.InfoMsg {
	return util.InfoMsg{Type: util.InfoTypeInfo, Msg: text, TTL: time.Millisecond}
}

// TestStatusMessageKeepsItsOwnTime checks that each message is retired by
// its own timer only, including when the same text is shown twice.
func TestStatusMessageKeepsItsOwnTime(t *testing.T) {
	t.Parallel()

	s := NewStatus(common.DefaultCommon(&countingWorkspace{ready: true}), nil)

	first := expire(t, s.Show(instant("same words"))).(util.ClearStatusMsg)
	second := expire(t, s.Show(instant("same words"))).(util.ClearStatusMsg)

	s.ClearInfoMsg(first.Seq)
	require.Equal(t, "same words", s.msg.Msg, "a stale timer must not retire a newer message")

	s.ClearInfoMsg(second.Seq)
	require.True(t, s.msg.IsEmpty(), "a message still expires on its own timer")
}

// TestStaleStatusTimerThroughUpdate covers the same rule on the path the
// UI actually takes: a message reported through Update outlives the timer
// of the one it replaced.
func TestStaleStatusTimerThroughUpdate(t *testing.T) {
	pinTTLs(t)

	m := newBusyUI(&countingWorkspace{ready: true})
	stale := expire(t, m.status.Show(instant("something earlier")))

	m.Update(util.InfoMsg{Type: util.InfoTypeSuccess, Msg: "what the user is here to read"})
	m.Update(stale)

	require.Equal(t, "what the user is here to read", m.status.msg.Msg)
}

// TestStatusDefersUpdateBehindVisibleMessage checks that a version notice
// arriving at startup waits behind the web link notice instead of wiping
// it.
func TestStatusDefersUpdateBehindVisibleMessage(t *testing.T) {
	t.Parallel()

	s := NewStatus(common.DefaultCommon(&countingWorkspace{ready: true}), nil)
	first := expire(t, s.Show(util.InfoMsg{
		Type: util.InfoTypeInfo, Msg: "Opening browser", TTL: time.Millisecond,
	})).(util.ClearStatusMsg)

	require.Nil(t, s.Show(util.InfoMsg{
		Type: util.InfoTypeUpdate, Msg: "older update", TTL: time.Millisecond,
	}), "a deferred update must not start its timer")
	require.Nil(t, s.Show(util.InfoMsg{
		Type: util.InfoTypeUpdate, Msg: "newest update", TTL: time.Millisecond,
	}), "a deferred update must not start its timer")
	require.Equal(t, "Opening browser", s.msg.Msg, "an update must not replace a visible message")

	pending := s.ClearInfoMsg(first.Seq)
	require.NotNil(t, pending, "clearing must start the pending update's timer")
	require.Equal(t, "newest update", s.msg.Msg, "only the latest pending update is kept")

	last := expire(t, pending).(util.ClearStatusMsg)
	s.ClearInfoMsg(last.Seq)
	require.True(t, s.msg.IsEmpty())
}

// TestStatusShowsUpdateWhenIdle checks that an update with nothing on
// screen is shown at once: only a message already on screen defers it.
func TestStatusShowsUpdateWhenIdle(t *testing.T) {
	t.Parallel()

	s := NewStatus(common.DefaultCommon(&countingWorkspace{ready: true}), nil)
	cmd := s.Show(util.InfoMsg{Type: util.InfoTypeUpdate, Msg: "update", TTL: time.Minute})
	require.NotNil(t, cmd)
	require.Equal(t, "update", s.msg.Msg)
}

// TestStatusNonUpdateReplacesVisibleMessage checks that everything but an
// update keeps the replace-now behaviour.
func TestStatusNonUpdateReplacesVisibleMessage(t *testing.T) {
	t.Parallel()

	s := NewStatus(common.DefaultCommon(&countingWorkspace{ready: true}), nil)
	s.Show(util.InfoMsg{Type: util.InfoTypeInfo, Msg: "old", TTL: time.Minute})
	cmd := s.Show(util.InfoMsg{Type: util.InfoTypeWarn, Msg: "new", TTL: time.Minute})
	require.NotNil(t, cmd)
	require.Equal(t, "new", s.msg.Msg)
}

// TestDeferredUpdateShownThroughUpdate covers the path the UI takes: the
// clear message handler shows the pending update and keeps its timer.
func TestDeferredUpdateShownThroughUpdate(t *testing.T) {
	pinTTLs(t)

	m := newBusyUI(&countingWorkspace{ready: true})
	m.Update(util.InfoMsg{Type: util.InfoTypeInfo, Msg: "Opening browser", TTL: time.Minute})
	m.Update(util.InfoMsg{Type: util.InfoTypeUpdate, Msg: "development version", TTL: time.Minute})
	require.Equal(t, "Opening browser", m.status.msg.Msg)

	_, cmd := m.Update(util.ClearStatusMsg{Seq: m.status.msgSeq})
	require.NotNil(t, cmd)
	require.Equal(t, "development version", m.status.msg.Msg)
}
