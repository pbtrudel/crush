package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSubagentManager(t *testing.T) {
	t.Parallel()
	m := newSubagentManager()
	run := m.start("child-session", "parent-session", func() {})
	require.Equal(t, "parent-session", run.parentSessionID)

	output, err, done, found := m.result(run.id)
	require.True(t, found)
	require.False(t, done)
	require.Empty(t, output)
	require.NoError(t, err)

	m.finish(run, "the result", nil)
	output, err, done, found = m.result(run.id)
	require.True(t, found)
	require.True(t, done)
	require.Equal(t, "the result", output)
	require.NoError(t, err)
}

func TestBackgroundSubagentNotice(t *testing.T) {
	t.Parallel()

	short := backgroundSubagentNotice(SubagentEvent{ID: "agent-1", Output: "found it"})
	require.Contains(t, short, "agent-1")
	require.Contains(t, short, "found it")

	long := backgroundSubagentNotice(SubagentEvent{ID: "agent-2", Output: strings.Repeat("x", 3000)})
	require.LessOrEqual(t, len([]rune(long)), 2400)
	require.Contains(t, long, "subagent_output")

	failed := backgroundSubagentNotice(SubagentEvent{ID: "agent-3", Err: "boom"})
	require.Contains(t, failed, "boom")
}

func TestSubagentManagerCancelAll(t *testing.T) {
	t.Parallel()
	m := newSubagentManager()
	cancelled := make(chan struct{})
	run := m.start("child", "parent", func() { close(cancelled) })
	select {
	case <-run.done:
		t.Fatal("run should still be pending")
	case <-time.After(10 * time.Millisecond):
	}
	m.cancelAll()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancelAll did not call the run cancel func")
	}
}
