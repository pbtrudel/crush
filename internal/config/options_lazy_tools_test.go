package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsGetLazyTools(t *testing.T) {
	t.Parallel()

	on := true
	off := false

	tests := []struct {
		name     string
		options  *Options
		expected bool
	}{
		{"nil options", nil, false},
		{"unset", &Options{}, false},
		{"explicitly off", &Options{LazyTools: &off}, false},
		{"explicitly on", &Options{LazyTools: &on}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, tt.options.GetLazyTools())
		})
	}
}

func TestOptionsLazyToolsFromJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{"lazy_tools":true}}`), &cfg))
	require.True(t, cfg.Options.GetLazyTools())

	var empty Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{}}`), &empty))
	require.False(t, empty.Options.GetLazyTools())
}
