package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadToolAliasInDefaultToolLists pins that every default tool set that
// exposes view also exposes its read alias. The agent presets filter tools
// through these lists, so a missing entry would hide the alias from the
// very agents it is meant to rescue.
func TestReadToolAliasInDefaultToolLists(t *testing.T) {
	t.Parallel()

	all := allToolNames()
	require.Contains(t, all, "read")
	require.Contains(t, resolveReadOnlyTools(all), "read")
	require.Contains(t, resolvePlanTools(all), "read")
}
