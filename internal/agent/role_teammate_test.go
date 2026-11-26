package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/roles"
)

// TestRoleTeammateKeepsLeadPrefix pins the cache rule for teammates: a
// teammate with any role sends the Lead's exact tools block and system
// prompt.
func TestRoleTeammateKeepsLeadPrefix(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	lead := coord.coderAgent()
	require.NotNil(t, lead)

	plain, err := runner.teammateAgent(agentteam.WithMember(t.Context(), "plain"), agentteam.Member{Name: "plain", Role: agentteam.RoleTeammate})
	require.NoError(t, err)

	for _, name := range []string{"reviewer", "implementer"} {
		member := agentteam.Member{Name: "t-" + name, Role: agentteam.RoleTeammate, RoleName: name}
		agent, err := runner.teammateAgent(agentteam.WithMember(t.Context(), member.Name), member)
		require.NoError(t, err)

		require.Equal(t, agentToolNames(t, lead), agentToolNames(t, agent), name)
		leadTools := lead.(*sessionAgent).tools.Copy()
		for i, tool := range agent.(*sessionAgent).tools.Copy() {
			want, err := json.Marshal(leadTools[i].Info())
			require.NoError(t, err)
			have, err := json.Marshal(tool.Info())
			require.NoError(t, err)
			require.Equal(t, string(want), string(have), "%s: tool %q", name, tool.Info().Name)
		}
		require.Equal(t, plain.SystemPrompt(), agent.SystemPrompt(), name)
		require.Equal(t, name, agent.(*sessionAgent).role.Name)
	}
}

func TestRoleTeammateGatedOnRebuild(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	member := agentteam.Member{Name: "rev", Role: agentteam.RoleTeammate, RoleName: "reviewer", SessionID: "existing"}
	agent, err := runner.teammateAgent(agentteam.WithMember(t.Context(), member.Name), member)
	require.NoError(t, err)
	for _, tool := range agent.(*sessionAgent).tools.Copy() {
		_, gated := tool.(*roleGatedTool)
		require.True(t, gated, "tool %q must be gated", tool.Info().Name)
	}
}

func TestRoleTeammateMissingRoleFails(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	member := agentteam.Member{Name: "gone", Role: agentteam.RoleTeammate, RoleName: "no-such-role"}
	_, err := runner.teammateAgent(agentteam.WithMember(t.Context(), member.Name), member)
	require.ErrorContains(t, err, `role "no-such-role" no longer exists`)
}

func TestTeammatePromptRoleBlockFirstTurnOnly(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	role, refusal := resolveRole(coord.cfg.Config(), "implementer")
	require.Empty(t, refusal)

	a := agentteam.Member{Name: "impl-a", Role: agentteam.RoleTeammate, RoleName: "implementer", Context: agentteam.ContextFresh}
	b := agentteam.Member{Name: "impl-b", Role: agentteam.RoleTeammate, RoleName: "implementer", Context: agentteam.ContextFresh}

	first := runner.teammatePrompt(t.Context(), a, "Do task 1.", true)
	require.True(t, strings.HasPrefix(first, roles.Block(*role)+`You are teammate "impl-a".`))

	sibling := runner.teammatePrompt(t.Context(), b, "Do task 2.", true)
	shared := commonPrefixLen(first, sibling)
	require.GreaterOrEqual(t, shared, len(roles.Block(*role)), "siblings with one role share the whole role block")

	later := runner.teammatePrompt(t.Context(), a, "Next.", false)
	require.NotContains(t, later, "## Your role:")

	plain := agentteam.Member{Name: "plain", Role: agentteam.RoleTeammate, Context: agentteam.ContextFresh}
	require.Equal(t, teammateTaskMessage(plain, "x"), runner.teammatePrompt(t.Context(), plain, "x", true), "no role, no change")
}

func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// TestSpawnTeammateRoleWiring pins the spawn tool's half of the role
// parameter: the catalog reaches the description, an unknown name is a
// refusal, and a valid name lands on the roster entry.
func TestSpawnTeammateRoleWiring(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	tool := coord.spawnTeammateTool("lead", func() Model { return coord.mainModels().Large })
	require.Contains(t, tool.Info().Description, "reviewer (builtin)",
		"the spawn tool must offer the roles it accepts")

	params := map[string]any{"name": "rev", "description": "Reviews the diff", "prompt": "Review."}
	params["role"] = "no-such-role"
	response := runTeamToolRaw(t, tool, params)
	require.True(t, response.IsError)
	require.Contains(t, response.Content, `unknown role "no-such-role"`)

	params["role"] = "reviewer"
	response = runTeamToolRaw(t, tool, params)
	require.False(t, response.IsError, response.Content)
	require.Contains(t, response.Content, `"role_name":"reviewer"`)
}
