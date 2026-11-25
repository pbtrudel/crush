package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestRunShellCommandPersistsACancelledRun pins the server side of the
// bang command: a run cancelled mid-flight keeps the output it already
// produced, with the SIGINT code the TUI shows, the same as the in-process
// workspace.
func TestRunShellCommandPersistsACancelledRun(t *testing.T) {
	t.Parallel()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	q := db.New(conn)
	sessions := session.NewService(q, conn)
	messages := message.NewService(q)

	b, _ := newTestBackend(t)
	ws := &Workspace{
		ID:           uuid.New().String(),
		Path:         t.TempDir(),
		resolvedPath: t.TempDir(),
		clients:      make(map[string]*clientState),
		shutdownFn:   func() {},
	}
	ws.App = &app.App{Sessions: sessions, Messages: messages}
	ws.ctx, ws.cancel = context.WithCancel(b.ctx)
	InsertWorkspaceForTest(b, ws)

	sess, err := sessions.Create(t.Context(), "shell")
	require.NoError(t, err)

	// The command touches this file after printing its first line, so the
	// cancel lands while the command is still running.
	marker := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for {
			if _, statErr := os.Stat(marker); statErr == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()

	resp, err := b.RunShellCommand(ctx, ws.ID, proto.ShellCommandRequest{
		SessionID: sess.ID,
		Command:   fmt.Sprintf("echo started; touch %q; sleep 30; echo done", marker),
	})
	require.NoError(t, err, "the capture path reports the cancellation through the exit code")
	require.NotEqual(t, 0, resp.ExitCode)
	require.Contains(t, resp.Output, "started")

	msgs, err := messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 1, "a cancelled run still persists its output")
	var cmd message.ShellCommand
	for _, part := range msgs[0].Parts {
		if shellCmd, ok := part.(message.ShellCommand); ok {
			cmd = shellCmd
		}
	}
	require.Equal(t, 130, cmd.ExitCode, "a cancelled command reads as SIGINT")
	require.Contains(t, cmd.Output, "started")
	require.NotContains(t, cmd.Output, "done")
}
