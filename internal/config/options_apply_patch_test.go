package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsGetApplyPatch(t *testing.T) {
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
		{"explicitly off", &Options{ApplyPatch: &off}, false},
		{"explicitly on", &Options{ApplyPatch: &on}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, tt.options.GetApplyPatch())
		})
	}
}

func TestOptionsApplyPatchFromJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{"apply_patch":true}}`), &cfg))
	require.True(t, cfg.Options.GetApplyPatch())

	var empty Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{}}`), &empty))
	require.False(t, empty.Options.GetApplyPatch())
}
