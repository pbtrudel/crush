package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func TestCacheReadPercent(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, cacheReadPercent(&session.Session{}))
	require.Equal(t, 0, cacheReadPercent(&session.Session{TotalCacheReadTokens: 100}))
	require.Equal(t, 43, cacheReadPercent(&session.Session{
		TotalCacheReadTokens: 430,
		TotalContextTokens:   1000,
	}))
}
