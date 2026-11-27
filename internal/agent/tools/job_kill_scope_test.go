package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/shell"
)

// killJob runs job_kill for shellID.
func killJob(t *testing.T, shellID string) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(JobKillParams{ShellID: shellID})
	require.NoError(t, err)
	resp, err := NewJobKillTool().Run(t.Context(), fantasy.ToolCall{
		ID:    "test-kill",
		Name:  JobKillToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	return resp
}

// TestJobKillReportsTheRestoreNote pins M2: a killed teammate job's scope
// check restores its change, and the kill's own result carries the note, so
// the model does not have to guess or read a job that is gone.
func TestJobKillReportsTheRestoreNote(t *testing.T) {
	t.Parallel()
	job, err := shell.GetBackgroundShellManager().StartWithSession(t.Context(), nil, t.TempDir(), nil, "sleep 30", "", "session-kill-note", 10)
	require.NoError(t, err)
	job.SetFinisher(func() string { return "Restored: stray.txt." })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = shell.GetBackgroundShellManager().Kill(ctx, job.ID)
	})

	resp := killJob(t, job.ID)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "Restored: stray.txt.")
	require.Contains(t, resp.Content, "terminated successfully")

	var meta JobKillResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, job.ID, meta.ShellID)
}

// TestJobKillLeavesANoteFreeKillAlone pins that a job with nothing to
// restore reads exactly as before.
func TestJobKillLeavesANoteFreeKillAlone(t *testing.T) {
	t.Parallel()
	job, err := shell.GetBackgroundShellManager().StartWithSession(t.Context(), nil, t.TempDir(), nil, "sleep 30", "", "session-kill-plain", 10)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = shell.GetBackgroundShellManager().Kill(ctx, job.ID)
	})

	resp := killJob(t, job.ID)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "Background shell "+job.ID+" terminated successfully", resp.Content)
}
