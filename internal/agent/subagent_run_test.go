package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubAgentRunName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Fix the login bug", subAgentRunName("Fix the login bug\n\nMore detail here."))
	require.Equal(t, "subagent", subAgentRunName("   \n\n "))

	long := strings.Repeat("a", 100)
	name := subAgentRunName(long)
	require.Len(t, []rune(name), 61)
	require.True(t, strings.HasSuffix(name, "…"))
}
