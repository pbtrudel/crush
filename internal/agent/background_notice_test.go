package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

func TestBackgroundJobNotice(t *testing.T) {
	t.Parallel()
	notice := backgroundJobNotice(shell.BackgroundJobEvent{
		ShellID:  "abc",
		Command:  "go test ./...",
		Output:   "ok",
		ExitCode: 0,
	})
	require.Contains(t, notice, "abc")
	require.Contains(t, notice, "go test ./...")
	require.Contains(t, notice, "ok")
	require.Contains(t, notice, "job_output")
}

func TestHandleBackgroundJobEnqueuesOnBusySession(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	sess, err := env.sessions.Create(t.Context(), "background")
	require.NoError(t, err)
	agent := &mockSessionAgent{busy: true}
	c := &coordinator{cfg: cfg, sessions: env.sessions, mainAgent: agent}
	c.handleBackgroundJob(t.Context(), shell.BackgroundJobEvent{
		ShellID:   "abc",
		SessionID: sess.ID,
		Command:   "go test ./...",
		Output:    "ok",
	})
	require.Len(t, agent.notices, 1)
	require.Contains(t, agent.notices[0], "abc")
}

func TestTakeWakeSlot(t *testing.T) {
	t.Parallel()
	cfg, err := config.Init(t.TempDir(), "", false)
	require.NoError(t, err)
	limit := 2
	cfg.Config().Options.MaxConsecutiveWakes = &limit
	c := &coordinator{cfg: cfg}
	require.True(t, c.takeWakeSlot("s"))
	require.True(t, c.takeWakeSlot("s"))
	require.False(t, c.takeWakeSlot("s"))
	c.resetWakeCount("s")
	require.True(t, c.takeWakeSlot("s"))
}
