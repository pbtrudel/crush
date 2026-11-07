package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
)

// TestTeammateAgentIsReadyWhenBuilt pins the readiness contract Start relies
// on: once teammateAgent returns, the agent must already carry its tool
// palette, because the turn starts immediately afterwards. Building into the
// coordinator's readiness group and returning without waiting left every
// teammate turn with no tools, so the model could only narrate what it was
// about to do and end the turn.
func TestTeammateAgentIsReadyWhenBuilt(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}

	agent, err := runner.teammateAgent(t.Context(), agentteam.Member{
		Name: "reviewer",
		Role: agentteam.RoleTeammate,
	})
	require.NoError(t, err)
	require.NotEmpty(t, agentToolNames(t, agent),
		"a teammate must have its tools before its turn starts")
}

// TestTeammateIdentityOverridesLeadBrief pins that the identity suffix tells a
// teammate the coordination brief above it is the Lead's, not its own. The
// brief is shared for prompt-cache parity, so it cannot be removed from the
// teammate's prompt.
func TestTeammateIdentityOverridesLeadBrief(t *testing.T) {
	identity := teamIdentity(agentteam.Member{Name: "reviewer", Role: agentteam.RoleTeammate})
	require.Contains(t, identity, `You are teammate "reviewer"`)
	require.Contains(t, identity, "You are not the Lead")
	require.Contains(t, identity, "do not apply to you")
}
