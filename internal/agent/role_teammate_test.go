package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/roles"
	"github.com/charmbracelet/crush/internal/session"
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

// TestRoleTeammateFirstTurnRetry pins the order of Start and the first-turn
// signal together. A build that fails because the role went away must leave
// no session behind, and once the role is back the retried first turn must
// still carry the role block even though its session already exists.
func TestRoleTeammateFirstTurnRetry(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	agents := coord.cfg.Config().Agents
	implementer, ok := agents["implementer"]
	require.True(t, ok)
	require.NotNil(t, implementer.Role)

	delete(agents, "implementer")
	member := agentteam.Member{
		ID: "member-1", Name: "impl", Role: agentteam.RoleTeammate,
		RoleName: "implementer", Context: agentteam.ContextFresh,
	}

	_, err := runner.Start(t.Context(), member, "Do task.")
	require.ErrorContains(t, err, `role "implementer" no longer exists`)
	_, err = coord.sessions.Get(t.Context(), runner.teammateSessionID(member))
	require.Error(t, err, "a failed build must not leave a session behind")

	agents["implementer"] = implementer
	run, err := runner.Start(t.Context(), member, "Do task.")
	require.NoError(t, err)

	// The first user message is stored before the request goes out, so it
	// shows the exact prompt the model is sent.
	var prompt string
	require.Eventually(t, func() bool {
		messages, err := coord.messages.List(t.Context(), run.SessionID())
		if err != nil || len(messages) == 0 {
			return false
		}
		prompt = messages[0].Content().Text
		return true
	}, 5*time.Second, 10*time.Millisecond)
	run.Cancel()
	<-run.Done()

	require.True(t, strings.HasPrefix(prompt, roles.Block(*implementer.Role)),
		"the retried first turn must carry the role block")
}

// TestTeammateFirstTurn pins what makes a turn the member's first: a new
// session, or one that exists but has no stored message yet.
func TestTeammateFirstTurn(t *testing.T) {
	t.Parallel()
	require.True(t, teammateFirstTurn(true, session.Session{}))
	require.True(t, teammateFirstTurn(false, session.Session{ID: "s1"}),
		"a session saved before its first message still needs the role block")
	require.False(t, teammateFirstTurn(false, session.Session{ID: "s2", MessageCount: 2}))
}

// TestTeammatePromptRoleBlockFollowsHistory pins the role block against the
// history rather than against creation alone.
func TestTeammatePromptRoleBlockFollowsHistory(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}
	role, refusal := resolveRole(coord.cfg.Config(), "reviewer")
	require.Empty(t, refusal)
	member := agentteam.Member{Name: "rev", Role: agentteam.RoleTeammate, RoleName: "reviewer", Context: agentteam.ContextFresh}

	empty := session.Session{ID: "empty"}
	reopened := runner.teammatePrompt(t.Context(), member, "x", teammateFirstTurn(false, empty))
	require.True(t, strings.HasPrefix(reopened, roles.Block(*role)),
		"an existing session with no messages is still a first turn")

	started := session.Session{ID: "started", MessageCount: 1}
	continued := runner.teammatePrompt(t.Context(), member, "x", teammateFirstTurn(false, started))
	require.NotContains(t, continued, "## Your role:")
}
