package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsGetMaxImages(t *testing.T) {
	t.Parallel()

	require.Equal(t, DefaultMaxImages, (*Options)(nil).GetMaxImages())
	require.Equal(t, DefaultMaxImages, (&Options{}).GetMaxImages())

	zero := 0
	require.Zero(t, (&Options{MaxImages: &zero}).GetMaxImages())

	limit := 42
	require.Equal(t, 42, (&Options{MaxImages: &limit}).GetMaxImages())
}

func TestOptionsGetImageRetentionTurns(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, DefaultImageRetentionTurns)
	require.Equal(t, DefaultImageRetentionTurns, (*Options)(nil).GetImageRetentionTurns())
	require.Equal(t, DefaultImageRetentionTurns, (&Options{}).GetImageRetentionTurns())

	zero := 0
	require.Zero(t, (&Options{ImageRetentionTurns: &zero}).GetImageRetentionTurns())

	two := 2
	require.Equal(t, 2, (&Options{ImageRetentionTurns: &two}).GetImageRetentionTurns())

	disabled := -1
	require.Equal(t, -1, (&Options{ImageRetentionTurns: &disabled}).GetImageRetentionTurns(), "negative must pass through as disabled, not clamp to 0")
}
