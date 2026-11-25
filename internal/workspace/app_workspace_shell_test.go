package workspace

import (
	"context"
	"sync"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/logtest"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// shellTestWorkspace builds a real AppWorkspace over a temporary database
// and config, so a test drives the shell path the TUI and the gateway use.
func shellTestWorkspace(t *testing.T) (*AppWorkspace, *app.App, session.Session) {
	t.Helper()
	// An isolated config directory, so the developer's own crush.json
	// cannot decide what this test loads.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Loading a config with no provider warns; the test does not care.
	logtest.Capture(t)

	store, err := config.Load(t.Context(), t.TempDir(), t.TempDir(), false, config.WithoutProviderUpdate())
	require.NoError(t, err)

	a := app.NewForTest(t.Context())
	t.Cleanup(a.ShutdownForTest)
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	q := db.New(conn)
	a.Sessions = session.NewService(q, conn)
	a.Messages = message.NewService(q)
	sess, err := a.Sessions.Create(t.Context(), "shell")
	require.NoError(t, err)
	return NewAppWorkspace(a, store), a, sess
}

// TestAppWorkspacePersistsACancelledShellRun pins that a cancelled bang
// command keeps the output it already produced: the TUI shows a cancelled
// command as SIGINT, so a reloaded transcript must carry the same exit code
// and partial output instead of losing the run.
func TestAppWorkspacePersistsACancelledShellRun(t *testing.T) {
	w, a, sess := shellTestWorkspace(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	printed := make(chan struct{})
	var once sync.Once
	onProgress := func(chunk string) {
		if chunk != "" {
			once.Do(func() { close(printed) })
		}
	}
	go func() {
		select {
		case <-printed:
			cancel()
		case <-ctx.Done():
		}
	}()

	resp, err := w.AgentRunShellCommand(ctx, sess.ID, "echo started; sleep 30; echo done", 80, onProgress, false)
	require.NoError(t, err, "the streaming path reports a cancellation through the exit code")
	require.NotEqual(t, 0, resp.ExitCode)

	msgs, err := a.Messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 1, "a cancelled run still persists its output")
	var shell *message.ShellCommand
	for _, part := range msgs[0].Parts {
		if cmd, ok := part.(message.ShellCommand); ok {
			shell = &cmd
		}
	}
	require.NotNil(t, shell, "the message carries the command output")
	require.Equal(t, 130, shell.ExitCode, "a cancelled command reads as SIGINT")
	require.Contains(t, shell.Output, "started")
	require.NotContains(t, shell.Output, "done")
}
