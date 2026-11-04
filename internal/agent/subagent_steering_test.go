package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestSubagentOutputWaitReturnsOnSteering(t *testing.T) {
	t.Parallel()

	manager := newSubagentManager()
	run := manager.start("child", "parent", func() {})
	coordinator := &coordinator{subagents: manager}

	steering := make(chan struct{})
	close(steering)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, tools.SteeringContextKey, steering)

	input, err := json.Marshal(SubagentOutputParams{SubagentID: run.id, Wait: true})
	require.NoError(t, err)

	start := time.Now()
	resp, err := coordinator.subagentOutputTool().Run(ctx, fantasy.ToolCall{
		ID:    "subagent-output-call",
		Name:  SubagentOutputToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "still running")
	require.Less(t, time.Since(start), time.Second, "steering must release the wait")
}
