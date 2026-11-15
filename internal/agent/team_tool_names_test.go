package agent

import (
	"slices"
	"testing"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/stretchr/testify/require"
)

// The prompt package decides whether to render the Agent Teams policy by
// checking a palette against agentteam.ToolNames, because it cannot import
// this package. That repetition is the trap: rename a team tool here and the
// check silently stops matching, which costs the policy on every request
// rather than breaking loudly. This pins the two lists together.
func TestTeamToolNamesMatchThePolicyList(t *testing.T) {
	t.Parallel()

	declared := []string{
		SpawnTeammateToolName,
		SendMessageToolName,
		ListAgentsToolName,
		WaitAgentToolName,
		InterruptAgentToolName,
		TeamTaskCreateToolName,
		TeamTaskListToolName,
		TeamTaskGetToolName,
		TeamTaskUpdateToolName,
	}
	expected := slices.Clone(agentteam.ToolNames)
	slices.Sort(expected)
	slices.Sort(declared)

	require.Equal(t, expected, declared)
}
