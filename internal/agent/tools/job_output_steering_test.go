package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

func TestJobOutputWaitReturnsOnSteering(t *testing.T) {
	t.Parallel()

	manager := shell.GetBackgroundShellManager()
	bgShell, err := manager.Start(t.Context(), t.TempDir(), nil, "sleep 100", "", testMaxJobs)
	require.NoError(t, err)
	defer manager.Kill(bgShell.ID)

	steering := make(chan struct{})
	close(steering)
	ctx := context.WithValue(t.Context(), SteeringContextKey, steering)

	tool := NewJobOutputTool(t.TempDir())
	input, err := json.Marshal(JobOutputParams{ShellID: bgShell.ID, Wait: true})
	require.NoError(t, err)

	start := time.Now()
	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "job-output-call",
		Name:  JobOutputToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Status: running")
	require.Less(t, time.Since(start), 2*time.Second, "steering must release the wait")
}
