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
