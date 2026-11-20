package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The Responses reasoning replay is on unless the option is explicitly false,
// so a config that never mentions it gets the transport.
func TestResponsesReasoningReplayDefaultsOn(t *testing.T) {
	t.Parallel()

	var unset Options
	require.True(t, unset.GetResponsesReasoningReplay(), "an unset option means on")
	require.True(t, (&Options{ResponsesReasoningReplay: ptr(true)}).GetResponsesReasoningReplay())
	require.False(t, (&Options{ResponsesReasoningReplay: ptr(false)}).GetResponsesReasoningReplay(), "an explicit false means off")
}
