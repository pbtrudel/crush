package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTUIOptionsGetVerbosity guards the option's default: only an explicit
// "low" collapses the transcript, so an existing config keeps rendering
// every tool call the way it always did.
func TestTUIOptionsGetVerbosity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tui  *TUIOptions
		want string
	}{
		{name: "nil options", tui: nil, want: TUIVerbosityHigh},
		{name: "unset", tui: &TUIOptions{}, want: TUIVerbosityHigh},
		{name: "high", tui: &TUIOptions{Verbosity: TUIVerbosityHigh}, want: TUIVerbosityHigh},
		{name: "low", tui: &TUIOptions{Verbosity: TUIVerbosityLow}, want: TUIVerbosityLow},
		{name: "unknown", tui: &TUIOptions{Verbosity: "verbose"}, want: TUIVerbosityHigh},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.tui.GetVerbosity())
		})
	}
}

// TestTUIOptionsVerbosityFromJSON covers the config file spelling, so
// options.tui.verbosity lands on the field.
func TestTUIOptionsVerbosityFromJSON(t *testing.T) {
	t.Parallel()

	var opts Options
	require.NoError(t, json.Unmarshal([]byte(`{"tui":{"verbosity":"low"}}`), &opts))
	require.Equal(t, TUIVerbosityLow, opts.TUI.GetVerbosity())
}
