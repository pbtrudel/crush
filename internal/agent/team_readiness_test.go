package agent

import (
	"strings"
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
// teammate's prompt, and the sibling-specific member name cannot enter it for
// the same reason.
func TestTeammateIdentityOverridesLeadBrief(t *testing.T) {
	member := agentteam.Member{Name: "reviewer", Role: agentteam.RoleTeammate}
	identity := teamIdentity(member)
	require.Contains(t, identity, "You are not the Lead")
	require.Contains(t, identity, "do not apply to you")
	require.NotContains(t, identity, "reviewer",
		"the member name is per-sibling text; in the system prompt it would give every sibling a different prefix")

	message := teammateTaskMessage(member, "review the diff")
	require.Contains(t, message, `You are teammate "reviewer"`, "the name must still reach the model")
	require.True(t, strings.HasSuffix(message, "review the diff"), "the task must follow the name")
}
