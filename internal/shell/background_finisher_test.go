package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBackgroundFinisherNoteLeadsTheOutput(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := SubscribeBackgroundJobs(ctx)

	manager := newBackgroundShellManager()
	job, err := manager.StartWithSession(ctx, nil, t.TempDir(), nil, "sleep 0.3 && echo done", "", "session-finisher", testMaxJobs)
	require.NoError(t, err)
	note, deferred := job.SetFinisher(func() string { return "NOTE" })
	require.True(t, deferred, "a running job runs the finisher when it ends")
	require.Empty(t, note)

	job.Wait()
	stdout, _, done, _ := job.GetOutput()
	require.True(t, done)
	require.Equal(t, "NOTE\n\ndone\n", stdout)
	require.Equal(t, "NOTE", job.Note())

	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Payload.ShellID != job.ID {
				continue
			}
			require.True(t, strings.HasPrefix(event.Payload.Output, "NOTE\n\n"), event.Payload.Output)
			return
		case <-deadline:
			t.Fatal("timed out waiting for the completion notice")
		}
	}
}

func TestBackgroundFinisherRunsAtOnceForAFinishedJob(t *testing.T) {
	t.Parallel()
	manager := newBackgroundShellManager()
	job, err := manager.Start(t.Context(), nil, t.TempDir(), nil, "echo quick", "", testMaxJobs)
	require.NoError(t, err)
	job.Wait()

	note, deferred := job.SetFinisher(func() string { return "LATE" })
	require.False(t, deferred)
	require.Equal(t, "LATE", note)
	stdout, _, _, _ := job.GetOutput()
	require.Equal(t, "LATE\n\nquick\n", stdout)
}
