package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/roles"
)

func TestRoleAccess(t *testing.T) {
	t.Parallel()
	read := &roles.Role{Name: "reviewer", Tools: []string{"view"}}
	write := &roles.Role{Name: "implementer"}

	access, refusal := roleAccess(read, "")
	require.Equal(t, accessRead, access)
	require.Empty(t, refusal)

	_, refusal = roleAccess(read, accessWrite)
	require.Equal(t, `role "reviewer" is read-only; access "write" is not allowed`, refusal)

	access, _ = roleAccess(write, "")
	require.Equal(t, accessWrite, access)
	access, _ = roleAccess(write, accessRead)
	require.Equal(t, accessRead, access, "access can narrow a write role")
}

// TestRoleSubAgentSharesVariantPrefix pins the cache rule for subagents: a
// role picks one of the two existing variants and changes neither its tools
// block nor its system prompt.
func TestRoleSubAgentSharesVariantPrefix(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	variants, err := coord.taskAgents(t.Context(), false)
	require.NoError(t, err)

	for name, access := range map[string]string{"reviewer": accessRead, "implementer": accessWrite} {
		role, refusal := resolveRole(coord.cfg.Config(), name)
		require.Empty(t, refusal)
		agent, _, _, refusal := coord.roleTaskAgent(t.Context(), role, "", AgentDispatchParams{}, coord.mainModels().Large)
		require.Empty(t, refusal, name)

		base := variants[access].(*sessionAgent)
		got := agent.(*sessionAgent)
		require.Equal(t, agentToolNames(t, base), agentToolNames(t, got), name)
		for i, tool := range base.tools.Copy() {
			want, err := json.Marshal(tool.Info())
			require.NoError(t, err)
			have, err := json.Marshal(got.tools.Copy()[i].Info())
			require.NoError(t, err)
			require.Equal(t, string(want), string(have), "%s: tool %q", name, tool.Info().Name)
		}
		require.Equal(t, base.SystemPrompt(), got.SystemPrompt(), name)
		require.Equal(t, name, got.role.Name)
	}
}

func TestPlanModeRoleRefusal(t *testing.T) {
	t.Parallel()
	write := &roles.Role{Name: "implementer"}
	read := &roles.Role{Name: "reviewer", Tools: []string{"view"}}
	require.Empty(t, planModeRoleRefusal(false, write, ""))
	require.Empty(t, planModeRoleRefusal(true, read, ""))
	require.Empty(t, planModeRoleRefusal(true, write, accessRead))
	require.Contains(t, planModeRoleRefusal(true, write, ""), "plan mode subagents are read-only")
}

// TestResolveAgentToolVariantPicksRole pins both ways the agent tool names
// a role: the role parameter, and a role name in the preset parameter.
func TestResolveAgentToolVariantPicksRole(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	variants, err := coord.taskAgents(t.Context(), false)
	require.NoError(t, err)

	byRole := func(params AgentParams) (SessionAgent, string) {
		return coord.resolveAgentToolVariant(t.Context(), params, variants, false)
	}

	agent, refusal := byRole(AgentParams{Prompt: "do", Role: "reviewer"})
	require.Empty(t, refusal)
	require.Equal(t, "reviewer", agent.(*sessionAgent).role.Name)

	agent, refusal = byRole(AgentParams{Prompt: "do", Agent: "implementer"})
	require.Empty(t, refusal)
	require.Equal(t, "implementer", agent.(*sessionAgent).role.Name)

	_, refusal = byRole(AgentParams{Prompt: "do", Role: "reviewer", Access: accessWrite})
	require.Equal(t, `role "reviewer" is read-only; access "write" is not allowed`, refusal)

	_, refusal = coord.resolveAgentToolVariant(t.Context(), AgentParams{Prompt: "do", Role: "implementer"}, variants, true)
	require.Contains(t, refusal, "plan mode subagents are read-only")
}

// TestDispatchHistoryRecordsRole pins that a resume finds the role and the
// access the role dispatch was asked for.
func TestDispatchHistoryRecordsRole(t *testing.T) {
	t.Parallel()
	input, err := json.Marshal(AgentDispatchParams{Prompt: "do", Label: "rev", Role: "reviewer"})
	require.NoError(t, err)
	meta, err := json.Marshal(AgentDispatchResponseMetadata{Label: "rev", Role: "reviewer"})
	require.NoError(t, err)
	records := dispatchHistory([]message.Message{
		{ID: "m1", Role: message.Assistant, Parts: []message.ContentPart{
			message.ToolCall{ID: "c1", Name: AgentDispatchToolName, Input: string(input), Finished: true},
		}},
		{ID: "t1", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "c1", Name: AgentDispatchToolName, Metadata: string(meta)},
		}},
	})
	record := records.byLabel["rev"]
	require.NotNil(t, record)
	require.Equal(t, "reviewer", record.Role)
	require.Empty(t, record.Access, "a role dispatch keeps its access as asked")

	require.Empty(t, recordAccess(AgentDispatchParams{Role: "reviewer"}))
	require.Equal(t, accessWrite, recordAccess(AgentDispatchParams{Role: "implementer", Access: accessWrite}))
	require.Equal(t, accessRead, recordAccess(AgentDispatchParams{}))
}
