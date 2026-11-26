package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
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
// effective access the role dispatch ran on.
func TestDispatchHistoryRecordsRole(t *testing.T) {
	t.Parallel()
	input, err := json.Marshal(AgentDispatchParams{Prompt: "do", Label: "rev", Role: "reviewer"})
	require.NoError(t, err)
	meta, err := json.Marshal(AgentDispatchResponseMetadata{Label: "rev", Role: "reviewer", Access: accessRead})
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
	require.Equal(t, accessRead, record.Access, "the result records the effective access")

	// Until a result lands the record keeps the raw request, which the role
	// branch of rehydration resolves again.
	require.Empty(t, recordAccess(AgentDispatchParams{Role: "reviewer"}))
	require.Equal(t, accessWrite, recordAccess(AgentDispatchParams{Role: "implementer", Access: accessWrite}))
	require.Equal(t, accessRead, recordAccess(AgentDispatchParams{}))
}

// assertGatedRole checks that an agent runs in role and that every tool it
// carries goes through the role gate.
func assertGatedRole(t *testing.T, agent SessionAgent, role string) {
	t.Helper()
	sa, ok := agent.(*sessionAgent)
	require.True(t, ok, "expected a *sessionAgent")
	require.NotNil(t, sa.role)
	require.Equal(t, role, sa.role.Name)
	got := sa.tools.Copy()
	require.NotEmpty(t, got)
	for _, tool := range got {
		_, gated := tool.(*roleGatedTool)
		require.True(t, gated, "tool %q must be gated", tool.Info().Name)
	}
}

// TestRoleDispatchRehydratesGated pins that a role dispatch rebuilt after a
// restart comes back in its role, not as a plain task subagent.
func TestRoleDispatchRehydratesGated(t *testing.T) {
	t.Parallel()
	c, env := newOverrideTestCoordinator(t)
	ctx := t.Context()

	parent, err := env.sessions.Create(ctx, "parent")
	require.NoError(t, err)
	input, err := json.Marshal(AgentDispatchParams{Prompt: "review", Label: "rev", Role: "reviewer"})
	require.NoError(t, err)
	meta, err := json.Marshal(AgentDispatchResponseMetadata{
		Label:  "rev",
		Role:   "reviewer",
		Access: accessRead,
	})
	require.NoError(t, err)
	assistant, err := env.messages.Create(ctx, parent.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "c1", Name: AgentDispatchToolName, Input: string(input), Finished: true},
		},
	})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, parent.ID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "c1", Name: AgentDispatchToolName, Metadata: string(meta)},
		},
	})
	require.NoError(t, err)
	childID := env.sessions.CreateAgentToolSessionID(assistant.ID, "c1")
	_, err = env.sessions.CreateTaskSession(ctx, childID, parent.ID, "Subagent")
	require.NoError(t, err)

	entry, err := c.rehydrateDispatch(ctx, parent.ID, "rev")
	require.NoError(t, err)
	require.Equal(t, accessRead, entry.Access)
	require.Equal(t, "reviewer", entry.Role)
	assertGatedRole(t, entry.agent, "reviewer")
}

// TestAgentSendModelSwitchKeepsRole pins that moving a role subagent to
// another model rebuilds it inside its role, never ungated.
func TestAgentSendModelSwitchKeepsRole(t *testing.T) {
	t.Parallel()
	c, env := newOverrideTestCoordinator(t)
	ctx := t.Context()
	agents, err := c.taskAgents(ctx, false)
	require.NoError(t, err)
	dispatch, err := c.agentDispatchTool(ctx, config.AgentCoder, agents[accessRead].Model)
	require.NoError(t, err)
	send := c.agentSendTool(agents[accessRead].Model)

	parent, err := env.sessions.Create(ctx, "parent")
	require.NoError(t, err)
	assistant, err := env.messages.Create(ctx, parent.ID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: "dispatching"}},
	})
	require.NoError(t, err)
	callCtx := context.WithValue(ctx, tools.SessionIDContextKey, parent.ID)
	callCtx = context.WithValue(callCtx, tools.MessageIDContextKey, assistant.ID)

	resp, err := dispatch.Run(callCtx, fantasy.ToolCall{ID: "d1", Name: AgentDispatchToolName, Input: `{"prompt":"do","label":"rev","role":"reviewer"}`})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	var dmeta AgentDispatchResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &dmeta))
	require.Equal(t, "reviewer", dmeta.Role)
	require.Equal(t, accessRead, dmeta.Access, "the result records the effective access")

	entry, ok := c.dispatched.Get(dispatchKey(parent.ID, dmeta.Label))
	require.True(t, ok)
	require.Equal(t, "reviewer", entry.Role)

	// Let the first run settle before the switch, so the test does not
	// depend on how fast the closed provider fails.
	entry.stop("test done")
	require.True(t, entry.awaitSettled(10*time.Second))

	resp, err = send.Run(callCtx, fantasy.ToolCall{
		ID:    "s1",
		Name:  AgentSendToolName,
		Input: `{"label":"` + dmeta.Label + `","message":"carry on","model":"other-model","provider":"other"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	entry, ok = c.dispatched.Get(dispatchKey(parent.ID, dmeta.Label))
	require.True(t, ok)
	require.Equal(t, "other-model", entry.runner().Model().ModelCfg.Model)
	assertGatedRole(t, entry.runner(), "reviewer")

	entry.stop("test done")
	require.True(t, entry.awaitSettled(10*time.Second))
}

// TestDispatchHistoryBackgroundedRoleCall pins that a backgrounded agent-tool
// role call keeps its role and effective access in the record.
func TestDispatchHistoryBackgroundedRoleCall(t *testing.T) {
	t.Parallel()
	bgMeta, err := json.Marshal(AgentBackgroundMetadata{
		Label:        "agent-1",
		Backgrounded: true,
		Access:       accessWrite,
		Role:         "implementer",
		Task:         "do",
	})
	require.NoError(t, err)
	records := dispatchHistory([]message.Message{
		{ID: "m1", Role: message.Assistant, Parts: []message.ContentPart{
			message.ToolCall{ID: "c1", Name: AgentToolName, Input: `{"prompt":"do","role":"implementer"}`, Finished: true},
		}},
		{ID: "t1", Role: message.Tool, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "c1", Name: AgentToolName, Metadata: string(bgMeta)},
		}},
	})
	record := records.byLabel["agent-1"]
	require.NotNil(t, record)
	require.Equal(t, "implementer", record.Role)
	require.Equal(t, accessWrite, record.Access, "the background result records the effective access")
	require.Equal(t, "m1", record.MessageID)
	require.Equal(t, "c1", record.CallID)
}

// TestBackgroundedRoleCallRehydratesGated pins that a role call moved to the
// background comes back gated after a restart.
func TestBackgroundedRoleCallRehydratesGated(t *testing.T) {
	t.Parallel()
	c, env := newOverrideTestCoordinator(t)
	ctx := t.Context()

	parent, err := env.sessions.Create(ctx, "parent")
	require.NoError(t, err)
	bgMeta, err := json.Marshal(AgentBackgroundMetadata{
		Label:        "agent-1",
		Backgrounded: true,
		Access:       accessWrite,
		Role:         "implementer",
		Task:         "do",
	})
	require.NoError(t, err)
	assistant, err := env.messages.Create(ctx, parent.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "c1", Name: AgentToolName, Input: `{"prompt":"do","role":"implementer"}`, Finished: true},
		},
	})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, parent.ID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "c1", Name: AgentToolName, Metadata: string(bgMeta)},
		},
	})
	require.NoError(t, err)
	childID := env.sessions.CreateAgentToolSessionID(assistant.ID, "c1")
	_, err = env.sessions.CreateTaskSession(ctx, childID, parent.ID, "Subagent")
	require.NoError(t, err)

	entry, err := c.rehydrateDispatch(ctx, parent.ID, "agent-1")
	require.NoError(t, err)
	require.Equal(t, accessWrite, entry.Access)
	require.Equal(t, "implementer", entry.Role)
	assertGatedRole(t, entry.agent, "implementer")
}

// TestAgentStatusShowsEffectiveRoleAccess pins that agent_status reports the
// access a role dispatch runs on, even when the call asked for none and its
// result never landed.
func TestAgentStatusShowsEffectiveRoleAccess(t *testing.T) {
	t.Parallel()
	c, env := newOverrideTestCoordinator(t)
	ctx := t.Context()

	parent, err := env.sessions.Create(ctx, "parent")
	require.NoError(t, err)
	input, err := json.Marshal(AgentDispatchParams{Prompt: "do", Label: "impl", Role: "implementer"})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, parent.ID, message.CreateMessageParams{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "c1", Name: AgentDispatchToolName, Input: string(input), Finished: true},
		},
	})
	require.NoError(t, err)

	callCtx := context.WithValue(ctx, tools.SessionIDContextKey, parent.ID)
	resp, err := c.agentStatusTool().Run(callCtx, fantasy.ToolCall{ID: "s1", Name: AgentStatusToolName, Input: `{"label":"impl"}`})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	var meta AgentStatusResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Len(t, meta.Agents, 1)
	require.Equal(t, accessWrite, meta.Agents[0].Access, "an implementer runs write even when the call asked for no access")
}

// TestRoleAgentToolRunNameSkipsRoleBlock pins that the sidebar run name of a
// role subagent comes from the task the tool was called with, not from the
// role block prepended to the prompt the model receives.
func TestRoleAgentToolRunNameSkipsRoleBlock(t *testing.T) {
	c, env := newOverrideTestCoordinator(t)
	path := filepath.Join(t.TempDir(), "agents.json")
	c.agentRuns = agentrun.New(path)

	tool, err := c.agentTool(t.Context(), config.AgentCoder)
	require.NoError(t, err)

	parent, err := env.sessions.Create(t.Context(), "parent")
	require.NoError(t, err)
	assistant, err := env.messages.Create(t.Context(), parent.ID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: "dispatching"}},
	})
	require.NoError(t, err)
	callCtx := context.WithValue(t.Context(), tools.SessionIDContextKey, parent.ID)
	callCtx = context.WithValue(callCtx, tools.MessageIDContextKey, assistant.ID)
	// The run is recorded before the model call, which this test does not
	// need; the timeout keeps the unreachable test provider from retrying.
	callCtx, cancel := context.WithTimeout(callCtx, 2*time.Second)
	defer cancel()

	resp, err := tool.Run(callCtx, fantasy.ToolCall{
		ID:    "a1",
		Name:  AgentToolName,
		Input: `{"prompt":"Inspect the diff.","role":"reviewer"}`,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Content)

	runs := agentrun.Load(path)
	require.NotEmpty(t, runs, "the agent tool must record its run")
	require.Equal(t, "Inspect the diff.", runs[0].Name)
	require.NotContains(t, runs[0].Name, "## Your role:")
}

// TestBackgroundedRoleTaskKeepsDisplayPrompt pins the task text stored in the
// background notice: a role block in the prompt must not leak into it.
func TestBackgroundedRoleTaskKeepsDisplayPrompt(t *testing.T) {
	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	coord.interactive = true
	coord.dispatched = csync.NewMap[string, *dispatchedAgent]()
	coord.backgroundAfter = 20 * time.Millisecond

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	agent := newMockAgent(providerID, 4096, func(ctx context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	role := roles.Role{Name: "reviewer", Body: "Review."}

	resp, err := coord.runSubAgentMaybeBackground(t.Context(), AgentToolName, accessRead, subAgentParams{
		Agent:          agent,
		SessionID:      parent.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "toolcall999999",
		Prompt:         roles.Block(role) + "long task",
		DisplayPrompt:  "long task",
		SessionTitle:   "Test",
		Role:           role.Name,
	})
	require.NoError(t, err)

	var meta AgentBackgroundMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.True(t, meta.Backgrounded)
	require.Equal(t, "long task", meta.Task)
	require.NotContains(t, meta.Task, "## Your role:")

	entry, ok := coord.dispatched.Get(dispatchKey(parent.ID, meta.Label))
	require.True(t, ok)
	require.True(t, entry.stop("test stop"))
	require.True(t, entry.awaitSettled(2*time.Second))
}
