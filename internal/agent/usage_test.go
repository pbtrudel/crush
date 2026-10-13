package agent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func TestUpdateSessionTokenCountersAccumulatesCacheTotals(t *testing.T) {
	t.Parallel()

	sess := &session.Session{
		PromptTokens:         150,
		CompletionTokens:     20,
		TotalCacheReadTokens: 50,
		TotalContextTokens:   150,
	}

	updateSessionTokenCounters(sess, fantasy.Usage{
		InputTokens:         10,
		CacheReadTokens:     200,
		CacheCreationTokens: 5,
		OutputTokens:        30,
	})

	// Snapshots reflect the latest step.
	require.Equal(t, int64(215), sess.PromptTokens)
	require.Equal(t, int64(30), sess.CompletionTokens)
	// Cache totals accumulate across steps.
	require.Equal(t, int64(250), sess.TotalCacheReadTokens)
	require.Equal(t, int64(365), sess.TotalContextTokens)

	// A zero-usage step changes neither snapshot nor totals.
	updateSessionTokenCounters(sess, fantasy.Usage{})
	require.Equal(t, int64(215), sess.PromptTokens)
	require.Equal(t, int64(250), sess.TotalCacheReadTokens)
	require.Equal(t, int64(365), sess.TotalContextTokens)
}
