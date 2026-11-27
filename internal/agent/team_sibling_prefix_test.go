package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/message"
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

// TestForkSiblingsShareInheritedContext pins the ordering inside a fork
// teammate's message. The inherited turns of the Lead are the same bytes for
// every fork sibling of that Lead, so they have to come first: with the
// member's name in front of them, two fork siblings diverged on the first line
// and the block (up to 16 KB) was reused for neither.
func TestForkSiblingsShareInheritedContext(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	_, err = coord.messages.Create(t.Context(), lead.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "fix the failing tests"}},
	})
	require.NoError(t, err)

	dbMember := agentteam.Member{ID: "member-1", Name: "db-layer", Role: agentteam.RoleTeammate, ParentID: lead.ID, Context: agentteam.ContextFork}
	authMember := agentteam.Member{ID: "member-2", Name: "auth-review", Role: agentteam.RoleTeammate, ParentID: lead.ID, Context: agentteam.ContextFork}

	inherited := runner.forkPrefix(t.Context(), dbMember)
	require.Contains(t, inherited, "fix the failing tests", "the fork block must carry the Lead's turns")
	require.Equal(t, inherited, runner.forkPrefix(t.Context(), authMember), "siblings of one Lead inherit the same block")

	first := runner.teammatePrompt(t.Context(), dbMember, "Add the index the migration is missing.", true)
	second := runner.teammatePrompt(t.Context(), authMember, "Review the login timeout change.", true)

	require.True(t, strings.HasPrefix(first, inherited), "the inherited block must be the message's prefix")
	require.True(t, strings.HasPrefix(second, inherited),
		"a sibling's message must start with the same inherited block, or the shared prefix ends on its first line")

	require.Contains(t, strings.TrimPrefix(first, inherited), `You are teammate "db-layer"`)
	require.True(t, strings.HasSuffix(first, "Add the index the migration is missing."))
	require.True(t, strings.HasSuffix(second, "Review the login timeout change."))
}

// TestForkPrefixFirstTurnOnly pins bug C: the inherited Lead transcript is
// already in a fork teammate's history after its first turn, so a later
// wake must not send up to 16 KB of it again.
func TestForkPrefixFirstTurnOnly(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	_, err = coord.messages.Create(t.Context(), lead.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "fix the failing tests"}},
	})
	require.NoError(t, err)
	member := agentteam.Member{ID: "member-1", Name: "db-layer", Role: agentteam.RoleTeammate, ParentID: lead.ID, Context: agentteam.ContextFork}

	first := runner.teammatePrompt(t.Context(), member, "Start.", true)
	require.Contains(t, first, "# Context inherited from the Team Lead")

	later := runner.teammatePrompt(t.Context(), member, "New messages.", false)
	require.Equal(t, teammateTaskMessage(member, "New messages."), later)
	require.NotContains(t, later, "fix the failing tests")
}

// TestForkPrefixResentAfterCompaction pins the follow-up: compaction
// replaces a fork teammate's history, inherited Lead context included, with
// a summary. The first turn after it carries the inherited context again,
// without the role block, and the turn after that does not.
func TestForkPrefixResentAfterCompaction(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	runner := &teamRunner{c: coord}

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	_, err = coord.messages.Create(t.Context(), lead.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "fix the failing tests"}},
	})
	require.NoError(t, err)
	member := agentteam.Member{
		ID: "member-1", Name: "db-layer", Role: agentteam.RoleTeammate,
		ParentID: lead.ID, Context: agentteam.ContextFork, RoleName: "implementer",
	}
	inherited := runner.forkPrefix(t.Context(), member)
	require.Contains(t, inherited, "fix the failing tests")

	first, sent := runner.teammatePromptFor(t.Context(), member, "Start.", true, "")
	require.True(t, sent)
	require.True(t, strings.HasPrefix(first, inherited))

	resent, sent := runner.teammatePromptFor(t.Context(), member, "Next.", false, "message-summary")
	require.True(t, sent, "a new summary means the inherited context is gone from the history")
	require.Equal(t, inherited+teammateTaskMessage(member, "Next."), resent, "the role block is first-turn only")

	member.ContextSummary = "message-summary"
	later, sent := runner.teammatePromptFor(t.Context(), member, "Again.", false, "message-summary")
	require.False(t, sent)
	require.Equal(t, teammateTaskMessage(member, "Again."), later)

	fresh := member
	fresh.Context = agentteam.ContextFresh
	fresh.ContextSummary = ""
	plain, sent := runner.teammatePromptFor(t.Context(), fresh, "x", false, "message-summary")
	require.False(t, sent, "a fresh member inherits nothing")
	require.Equal(t, teammateTaskMessage(fresh, "x"), plain)

	run := &teamRun{contextSummary: "message-summary", forkSent: true}
	summary, ok := run.ContextSummary()
	require.True(t, ok)
	require.Equal(t, "message-summary", summary)
}
