package webtest

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestSeedToolCallsAddsOneMessageOfCalls(t *testing.T) {
	t.Parallel()
	f := NewFake()
	clock := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	SeedToolCalls(f, 7, clock)

	msgs, err := f.ListMessages(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "overflow-a", msgs[0].ID)
	require.Equal(t, clock.Unix(), msgs[0].CreatedAt)
	var calls []message.ToolCall
	for _, part := range msgs[0].Parts {
		call, ok := part.(message.ToolCall)
		if ok {
			calls = append(calls, call)
		}
	}
	require.Len(t, calls, 7)
	require.Equal(t, "overflow-tc-0", calls[0].ID)
	require.Equal(t, "overflow-tc-6", calls[6].ID)
	// The kinds cycle so the strip shows every category.
	require.Equal(t, "grep", calls[0].Name)
	require.Equal(t, "bash", calls[2].Name)
}

func TestSeedToolCallsSeedsNothingForZero(t *testing.T) {
	t.Parallel()
	f := NewFake()
	SeedToolCalls(f, 0, time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	msgs, err := f.ListMessages(t.Context(), "s1")
	require.NoError(t, err)
	require.Empty(t, msgs)
}
