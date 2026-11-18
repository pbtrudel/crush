package logreport

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestWindowCountsMessagesAndSessions(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "first")
	log.session("s2", "second")
	log.add("s1", message.User, message.TextContent{Text: "hello"})
	log.add("s1", message.Assistant, message.TextContent{Text: "hi"}, message.Finish{Reason: message.FinishReasonEndTurn})
	log.add("s2", message.Assistant, message.Finish{Reason: message.FinishReasonEndTurn})
	log.raw("s2", message.Tool, `[{"type":"no_such_part","data":{}}]`)
	log.add("s2", message.User, message.TextContent{Text: "again"})
	// One parts value can exceed half a megabyte.
	log.add("s2", message.Tool, message.ToolResult{ToolCallID: "c1", Name: "bash", Content: strings.Repeat("x", 1<<20)})

	w := log.report(Options{}).Window
	require.Equal(t, 6, w.Messages)
	require.Equal(t, map[string]int{"user": 2, "assistant": 2, "tool": 2}, w.ByRole)
	require.Equal(t, 2, w.Sessions)
	require.Equal(t, 1, w.Skipped, "a row whose parts do not decode is counted and skipped")
	require.Nil(t, w.Since)
	require.Nil(t, w.Until)
}

func TestWindowBounds(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "only")
	start := log.at
	for range 4 {
		log.add("s1", message.User, message.TextContent{Text: "x"})
	}

	// Since is included and Until is excluded.
	w := log.report(Options{Since: start.Add(time.Second), Until: start.Add(3 * time.Second)}).Window
	require.Equal(t, 2, w.Messages)
	require.Equal(t, start.Add(time.Second).Unix(), w.First.Unix())
	require.Equal(t, start.Add(2*time.Second).Unix(), w.Last.Unix())
	require.NotNil(t, w.Since)

	empty := log.report(Options{Since: start.Add(time.Hour)})
	require.Zero(t, empty.Window.Messages)
	require.Zero(t, empty.Window.Sessions)
	require.Nil(t, empty.Window.First)
	var text, js bytes.Buffer
	require.NoError(t, empty.WriteText(&text), "an empty window is still reported")
	require.NoError(t, empty.WriteJSON(&js))
	require.Contains(t, js.String(), `"schema": 1`)
}

func TestParseBound(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 17, 15, 30, 0, 0, time.Local)
	tests := []struct {
		value string
		want  time.Time
	}{
		{value: "", want: time.Time{}},
		{value: "2026-11-09", want: time.Date(2026, 11, 9, 0, 0, 0, 0, time.Local)},
		{value: "7d", want: now.AddDate(0, 0, -7)},
		{value: "12h", want: now.Add(-12 * time.Hour)},
	}
	for _, tt := range tests {
		got, err := ParseBound(tt.value, now)
		require.NoError(t, err, tt.value)
		require.True(t, tt.want.Equal(got), "%s: got %s", tt.value, got)
	}
	for _, bad := range []string{"yesterday", "-3d", "2026-13-01", "-2h"} {
		_, err := ParseBound(bad, now)
		require.Error(t, err, bad)
	}
}
