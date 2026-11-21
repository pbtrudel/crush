package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
)

// TestSiblingTeammatesShareSystemPrompt pins the prefix half of a fan-out. A
// Lead that spawns N teammates in one turn sends N requests whose tools block
// and system message should be the same bytes, so the provider can reuse one
// cached prefix instead of charging the whole prompt cold N times. Anything
// that names the individual member belongs in the task message, which comes
// after every field the siblings share.
func TestSiblingTeammatesShareSystemPrompt(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}

	dbMember := agentteam.Member{Name: "db-layer", Role: agentteam.RoleTeammate}
	authMember := agentteam.Member{Name: "auth-review", Role: agentteam.RoleTeammate}
	dbTask := "Add the index the migration is missing."
	authTask := "Review the login timeout change."

	first, err := runner.teammateAgent(t.Context(), dbMember)
	require.NoError(t, err)
	second, err := runner.teammateAgent(t.Context(), authMember)
	require.NoError(t, err)

	firstPrompt := first.(*sessionAgent).systemPromptForSession("session-db-layer")
	secondPrompt := second.(*sessionAgent).systemPromptForSession("session-auth-review")
	require.Equal(t, firstPrompt, secondPrompt,
		"two teammates spawned in one fan-out must send the same system prompt, or each pays for the whole prompt cold")

	require.Equal(t, agentToolNames(t, first), agentToolNames(t, second),
		"the tools block must serialise identically for siblings")

	require.NotContains(t, firstPrompt, dbTask, "the task text belongs in the user message, not the shared system prompt")
	require.NotContains(t, firstPrompt, dbMember.Name, "a member name in the system prompt makes every sibling's prefix different")

	// The per-sibling text still has to reach the model, which it does in the
	// message the turn starts from.
	firstMessage := teammateTaskMessage(dbMember, dbTask)
	secondMessage := teammateTaskMessage(authMember, authTask)
	require.Contains(t, firstMessage, dbMember.Name)
	require.True(t, strings.HasSuffix(firstMessage, dbTask))
	require.Contains(t, secondMessage, authMember.Name)
	require.True(t, strings.HasSuffix(secondMessage, authTask))
	require.NotEqual(t, firstMessage, secondMessage, "the messages are what differ between siblings")
}

// TestSiblingSubAgentsShareSystemPrompt is the regression test for the other
// fan-out path. The agent and agent_dispatch tools are handed one built
// subagent per access level, whose prompt holds no task text, label, session
// id or member name, so siblings on that path already send the same bytes;
// this pins it so per-task text cannot drift back into the system prompt.
func TestSiblingSubAgentsShareSystemPrompt(t *testing.T) {
	coord := newTeammateTestCoordinator(t)

	first, err := coord.taskAgents(t.Context(), false)
	require.NoError(t, err)
	second, err := coord.taskAgents(t.Context(), false)
	require.NoError(t, err)

	firstRead := first[accessRead].(*sessionAgent)
	secondRead := second[accessRead].(*sessionAgent)

	firstPrompt := firstRead.systemPromptForSession("session-one")
	secondPrompt := secondRead.systemPromptForSession("session-two")
	require.Equal(t, firstPrompt, secondPrompt,
		"two subagents dispatched from one turn must send the same system prompt")
	require.NotContains(t, firstPrompt, "Add the index the migration is missing.",
		"a subagent's task must reach it in the user message, not its system prompt")

	require.Equal(t, agentToolNames(t, firstRead), agentToolNames(t, secondRead),
		"the tools block must serialise identically for sibling subagents")
}
