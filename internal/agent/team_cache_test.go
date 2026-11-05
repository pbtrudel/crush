package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/openrouter"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/session"
)

// newTeammateTestCoordinator builds the gate test coordinator with a live Team
// service, so the palette under test includes the Team tools the Lead gets.
// The Lead's tools are rebuilt afterwards because the gate coordinator builds
// them before the service exists, which is not the production order.
func newTeammateTestCoordinator(t *testing.T) *coordinator {
	t.Helper()
	coord := newGateTestCoordinator(t, true)
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, newTeamFakeRunner())
	require.NoError(t, err)
	coord.team = service

	lead := coord.coderAgent()
	require.NotNil(t, lead)
	leadTools, err := coord.buildTools(t.Context(), coord.cfg.Config().Agents[config.AgentCoder], roleLead)
	require.NoError(t, err)
	lead.SetTools(leadTools)
	require.NoError(t, coord.readyWg.Wait())
	return coord
}

// buildTeammateAgent builds the agent teamRunner.Start builds for member.
func buildTeammateAgent(t *testing.T, coord *coordinator, member agentteam.Member) SessionAgent {
	t.Helper()
	lead := coord.coderAgent()
	require.NotNil(t, lead)
	agent, err := coord.buildAgent(
		agentteam.WithMember(t.Context(), member.Name),
		agentSystemPrompt{Prebuilt: lead.SystemPrompt(), Suffix: teamIdentity(member)},
		coord.cfg.Config().Agents[config.AgentCoder],
		roleTeammate,
	)
	require.NoError(t, err)
	require.NoError(t, coord.readyWg.Wait())
	return agent
}

// agentToolNames returns the tool palette a built agent will send.
func agentToolNames(t *testing.T, agent SessionAgent) []string {
	t.Helper()
	sa, ok := agent.(*sessionAgent)
	require.True(t, ok, "buildAgent must return a *sessionAgent")
	return toolNames(sa.tools.Copy())
}

// TestTeammateToolPaletteMatchesLead pins the tools half of the shared cache
// prefix. Providers serialize tools before the system prompt and messages, so
// a teammate whose palette is missing one tool, or lists them in another
// order, invalidates the whole cached prefix and pays full input price on
// every turn.
func TestTeammateToolPaletteMatchesLead(t *testing.T) {
	coord := newTeammateTestCoordinator(t)

	lead := coord.coderAgent()
	require.NotNil(t, lead)
	teammate := buildTeammateAgent(t, coord, agentteam.Member{Name: "reviewer", Role: agentteam.RoleTeammate})

	leadNames := agentToolNames(t, lead)
	require.Contains(t, leadNames, tools.QuestionToolName, "an interactive Lead has the question tool")
	require.Equal(t, leadNames, agentToolNames(t, teammate), "the teammate palette must match the Lead's name for name, in order")

	leadTools := lead.(*sessionAgent).tools.Copy()
	teammateTools := teammate.(*sessionAgent).tools.Copy()
	for i, tool := range leadTools {
		leadInfo, err := json.Marshal(tool.Info())
		require.NoError(t, err)
		teammateInfo, err := json.Marshal(teammateTools[i].Info())
		require.NoError(t, err)
		require.Equal(t, string(leadInfo), string(teammateInfo),
			"tool %q must serialize identically for the Lead and a teammate", tool.Info().Name)
	}
}

// TestTeammateSystemPromptExtendsLead pins the system-prompt half of the
// shared cache prefix. The teammate's system message must be the Lead's plus
// the identity suffix, byte for byte: re-rendering the template would let the
// date, git state, or context files drift and move the cached prefix.
func TestTeammateSystemPromptExtendsLead(t *testing.T) {
	coord := newTeammateTestCoordinator(t)

	member := agentteam.Member{Name: "reviewer", Role: agentteam.RoleTeammate}
	lead := coord.coderAgent()
	require.NotNil(t, lead)
	require.NotEmpty(t, lead.SystemPrompt(), "the Lead's prompt must be built before a teammate starts")
	teammate := buildTeammateAgent(t, coord, member)

	require.Equal(t, lead.SystemPrompt()+teamIdentity(member), teammate.SystemPrompt())

	leadTurn := lead.(*sessionAgent).systemPromptForSession("session-lead")
	teammateTurn := teammate.(*sessionAgent).systemPromptForSession("session-teammate")
	require.True(t, strings.HasPrefix(teammateTurn, leadTurn),
		"the Lead's whole system message must stay an exact prefix of the teammate's")
	require.True(t, strings.HasSuffix(teammateTurn, teamIdentity(member)),
		"the identity must land after the per-session MCP instructions")
}

// TestAgentToolRefusesInsideTeammate pins the runtime half of tool parity: the
// agent tool stays in a teammate's palette for cache parity but must not let a
// teammate delegate further.
func TestAgentToolRefusesInsideTeammate(t *testing.T) {
	coord := newTeammateTestCoordinator(t)

	tool, err := coord.agentTool(t.Context())
	require.NoError(t, err)

	teammateCtx := agentteam.WithMember(t.Context(), "reviewer")
	response := runAgentTool(t, tool, teammateCtx, AgentParams{Prompt: "do the thing"})
	require.True(t, response.IsError)
	require.Contains(t, response.Content, "nested delegation is not supported inside a teammate")

	// The Lead keeps the tool. An empty prompt stops at the next check, which
	// is as far as this test can go without running a real sub-agent.
	response = runAgentTool(t, tool, t.Context(), AgentParams{})
	require.True(t, response.IsError)
	require.Contains(t, response.Content, "prompt is required")
}

func runAgentTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params AgentParams) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	response, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: tool.Info().Name, Input: string(input)})
	require.NoError(t, err)
	return response
}

// TestSubAgentRunContextCarriesTeamMember pins the other half of the guard
// plumbing: teamRunner.Start puts the member on the context it hands to the
// sub-agent runner, and the runner must deliver it unchanged to agent.Run,
// which is where the tools read it from.
func TestSubAgentRunContextCarriesTeamMember(t *testing.T) {
	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})

	parentSession, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	var seen string
	agent := newMockAgent(providerID, 4096, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		seen = agentteam.MemberFromContext(ctx)
		return agentResultWithText("done"), nil
	})

	ctx := agentteam.WithMember(t.Context(), "reviewer")
	_, err = coord.runSubAgent(ctx, subAgentParams{
		Agent:          agent,
		SessionID:      parentSession.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do something",
		SessionTitle:   "Test Session",
	})
	require.NoError(t, err)
	require.Equal(t, "reviewer", seen, "the member identity must survive into agent.Run")
}

// TestExecuteSubAgentCarriesTeamRouting pins the two request fields that
// decide where a teammate's request goes rather than what it contains: the
// channel that filters channel-scoped MCP tools out of the palette, and the
// provider's sticky-routing session id.
func TestExecuteSubAgentCarriesTeamRouting(t *testing.T) {
	const providerID = string(catwalk.InferenceProviderOpenRouter)
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID, Name: "OpenRouter"})

	lead, err := env.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	lead, err = env.sessions.SetChannel(t.Context(), lead.ID, "discord")
	require.NoError(t, err)
	teammateSession, err := env.sessions.CreateTaskSession(t.Context(), "team-reviewer", lead.ID, "Teammate reviewer")
	require.NoError(t, err)

	var got SessionAgentCall
	agent := newMockAgent(providerID, 4096, func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
		got = call
		return agentResultWithText("done"), nil
	})

	_, err = coord.executeSubAgent(t.Context(), agent, teammateSession, subAgentParams{
		SessionID:       lead.ID,
		AgentMessageID:  "msg-1",
		ToolCallID:      "call-1",
		Prompt:          "go",
		Channel:         lead.Channel,
		StickySessionID: lead.ID,
	})
	require.NoError(t, err)

	require.Equal(t, "discord", got.Channel,
		"a teammate must run in the Lead's channel or channel-scoped MCP tools drop out of its palette")
	opts, ok := got.ProviderOptions[openrouter.Name].(*openrouter.ProviderOptions)
	require.True(t, ok, "the OpenRouter provider options must carry the sticky-routing hint")
	require.Equal(t, session.HashID(lead.ID), opts.ExtraBody["session_id"],
		"a teammate must be pinned to the Lead's upstream provider")
}

// TestTeammateLeadSessionCarriesChannel pins where the teammate's routing
// fields come from: the Lead's session, not the teammate's own.
func TestTeammateLeadSessionCarriesChannel(t *testing.T) {
	coord := newGateTestCoordinator(t, true)

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	lead, err = coord.sessions.SetChannel(t.Context(), lead.ID, "discord")
	require.NoError(t, err)

	runner := &teamRunner{c: coord}
	member := agentteam.Member{Name: "reviewer", Role: agentteam.RoleTeammate, ParentID: lead.ID}
	found := runner.leadSession(t.Context(), member)
	require.Equal(t, lead.ID, found.ID)
	require.Equal(t, "discord", found.Channel)

	// A member without a parent session cannot borrow a channel; the run
	// falls back to the teammate's own (empty) binding.
	require.Equal(t, session.Session{}, runner.leadSession(t.Context(), agentteam.Member{Name: "reviewer"}))
}

// TestTeammateInheritsLeadAdoptedToolOrder pins the last piece of tools-block
// parity. A tool the Lead adopted mid-turn sits at the end of its palette, so
// a teammate that keeps the registry order instead sends a different tools
// block and misses the prefix the Lead already cached.
func TestTeammateInheritsLeadAdoptedToolOrder(t *testing.T) {
	coord := newTeammateTestCoordinator(t)

	lead := coord.coderAgent().(*sessionAgent)
	require.NotNil(t, lead)
	const leadSessionID = "lead-session"
	lead.SetAdoptedToolOrder(leadSessionID, []string{tools.QuestionToolName})

	teammate := buildTeammateAgent(t, coord, agentteam.Member{Name: "reviewer", Role: agentteam.RoleTeammate}).(*sessionAgent)
	const teammateSessionID = "teammate-session"
	teammate.SetAdoptedToolOrder(teammateSessionID, lead.AdoptedToolOrder(leadSessionID))

	leadOrder := toolNames(stableToolOrder(lead.tools.Copy(), lead.adoptedToolOrder(leadSessionID)))
	teammateOrder := toolNames(stableToolOrder(teammate.tools.Copy(), teammate.adoptedToolOrder(teammateSessionID)))
	require.Equal(t, leadOrder, teammateOrder, "the teammate must keep the Lead's adopted tool at the end")
	require.Equal(t, tools.QuestionToolName, leadOrder[len(leadOrder)-1])

	unseeded := toolNames(stableToolOrder(teammate.tools.Copy(), nil))
	require.NotEqual(t, leadOrder, unseeded,
		"without the Lead's adopted order the palettes differ, which is what the seed prevents")
}
