package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

func runSubagentOutputTool(t *testing.T, c *coordinator, input string) fantasy.ToolResponse {
	t.Helper()
	tool := c.subagentOutputTool()
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		ID:    "call-1",
		Name:  SubagentOutputToolName,
		Input: input,
	})
	require.NoError(t, err)
	return resp
}

func TestStartBackgroundSubAgentReturnsImmediately(t *testing.T) {
	const providerID = "test-provider"
	providerCfg := config.ProviderConfig{ID: providerID}

	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, providerCfg)

	parentSession, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	release := make(chan struct{})
	agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
		<-release
		return agentResultWithText("background done"), nil
	})

	resp, err := coord.startBackgroundSubAgent(t.Context(), subAgentParams{
		Agent:          agent,
		SessionID:      parentSession.ID,
		AgentMessageID: "msg-1",
		ToolCallID:     "call-1",
		Prompt:         "do something",
		SessionTitle:   "Test Session",
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "started subagent agent-1")

	close(release)

	require.Eventually(t, func() bool {
		_, done, found, _ := coord.subagents.result("agent-1")
		return found && done
	}, 5*time.Second, 10*time.Millisecond)

	out := runSubagentOutputTool(t, coord, `{"subagent_id":"agent-1"}`)
	require.False(t, out.IsError)
	require.Equal(t, "background done", out.Content)
}

func TestSubagentOutputTool(t *testing.T) {
	t.Parallel()

	t.Run("requires an id", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{subagents: newSubagentManager()}
		resp := runSubagentOutputTool(t, c, `{}`)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "subagent_id is required")
	})

	t.Run("reports a missing manager", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{}
		resp := runSubagentOutputTool(t, c, `{"subagent_id":"agent-1"}`)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "no background subagents")
	})

	t.Run("reports an unknown id", func(t *testing.T) {
		t.Parallel()
		c := &coordinator{subagents: newSubagentManager()}
		resp := runSubagentOutputTool(t, c, `{"subagent_id":"agent-99"}`)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "subagent not found")
	})

	t.Run("reports a running subagent", func(t *testing.T) {
		t.Parallel()
		m := newSubagentManager()
		run := m.start("child", "parent", func() {})
		c := &coordinator{subagents: m}
		resp := runSubagentOutputTool(t, c, `{"subagent_id":"`+run.id+`"}`)
		require.False(t, resp.IsError)
		require.Contains(t, resp.Content, "still running")
		m.finish(run, "done", nil)
	})

	t.Run("returns the finished output", func(t *testing.T) {
		t.Parallel()
		m := newSubagentManager()
		run := m.start("child", "parent", func() {})
		m.finish(run, "the answer", nil)
		c := &coordinator{subagents: m}
		resp := runSubagentOutputTool(t, c, `{"subagent_id":"`+run.id+`"}`)
		require.False(t, resp.IsError)
		require.Equal(t, "the answer", resp.Content)
	})

	t.Run("returns a finished error", func(t *testing.T) {
		t.Parallel()
		m := newSubagentManager()
		run := m.start("child", "parent", func() {})
		m.finish(run, "", errors.New("boom"))
		c := &coordinator{subagents: m}
		resp := runSubagentOutputTool(t, c, `{"subagent_id":"`+run.id+`"}`)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "boom")
	})

	t.Run("wait blocks until the subagent finishes", func(t *testing.T) {
		t.Parallel()
		m := newSubagentManager()
		run := m.start("child", "parent", func() {})
		c := &coordinator{subagents: m}

		type result struct {
			resp fantasy.ToolResponse
			err  error
		}
		done := make(chan result, 1)
		go func() {
			tool := c.subagentOutputTool()
			resp, err := tool.Run(context.Background(), fantasy.ToolCall{
				ID:    "call-2",
				Name:  SubagentOutputToolName,
				Input: `{"subagent_id":"` + run.id + `","wait":true}`,
			})
			done <- result{resp: resp, err: err}
		}()

		select {
		case <-done:
			t.Fatal("wait returned before the subagent finished")
		case <-time.After(10 * time.Millisecond):
		}

		m.finish(run, "after wait", nil)

		select {
		case got := <-done:
			require.NoError(t, got.err)
			require.Equal(t, "after wait", got.resp.Content)
		case <-time.After(time.Second):
			t.Fatal("wait did not return after the subagent finished")
		}
	})
}
