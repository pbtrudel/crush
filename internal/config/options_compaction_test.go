package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsGetAutoCompactThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		options  *Options
		expected float64
	}{
		{
			name:     "nil options",
			options:  nil,
			expected: DefaultAutoCompactThreshold,
		},
		{
			name:     "unset field",
			options:  &Options{},
			expected: DefaultAutoCompactThreshold,
		},
		{
			name:     "configured fraction",
			options:  &Options{AutoCompactThreshold: ptr(0.5)},
			expected: 0.5,
		},
		{
			name:     "zero disables auto-compaction",
			options:  &Options{AutoCompactThreshold: ptr(0.0)},
			expected: 0,
		},
		{
			name:     "negative disables too",
			options:  &Options{AutoCompactThreshold: ptr(-0.5)},
			expected: 0,
		},
		{
			name:     "values above one are capped",
			options:  &Options{AutoCompactThreshold: ptr(1.5)},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, tt.options.GetAutoCompactThreshold())
		})
	}
}

func TestOptionsGetCompactTailTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		options  *Options
		expected int64
	}{
		{
			name:     "nil options",
			options:  nil,
			expected: DefaultCompactTailTokens,
		},
		{
			name:     "unset field",
			options:  &Options{},
			expected: DefaultCompactTailTokens,
		},
		{
			name:     "configured budget",
			options:  &Options{CompactTailTokens: ptr(16_384)},
			expected: 16_384,
		},
		{
			name:     "zero disables the tail",
			options:  &Options{CompactTailTokens: ptr(0)},
			expected: 0,
		},
		{
			name:     "negative is treated as zero",
			options:  &Options{CompactTailTokens: ptr(-100)},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, tt.options.GetCompactTailTokens())
		})
	}
}

func TestOptionsCompactionFromJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{"auto_compact_threshold":0.5,"compact_tail_tokens":16384}}`), &cfg))
	require.Equal(t, 0.5, cfg.Options.GetAutoCompactThreshold())
	require.Equal(t, int64(16_384), cfg.Options.GetCompactTailTokens())
}
