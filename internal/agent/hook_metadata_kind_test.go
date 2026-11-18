package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Hooks merge their metadata into the tool's, so a kind the tool recorded
// must survive a hook.
func TestMergeHookMetadataKeepsErrorKind(t *testing.T) {
	t.Parallel()

	merged := mergeHookMetadata(`{"error_kind":"not_read"}`, hooks.AggregateResult{HookCount: 1, Decision: hooks.DecisionAllow})
	require.Equal(t, "not_read", gjson.Get(merged, "error_kind").String())
	require.True(t, gjson.Get(merged, "hook").Exists())
}
