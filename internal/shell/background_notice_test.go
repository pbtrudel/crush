package shell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestBackgroundJobCompletionNotice(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := SubscribeBackgroundJobs(ctx)

	manager := newBackgroundShellManager()
	_, err := manager.StartWithSession(ctx, t.TempDir(), nil, "echo hello-notice", "", "session-notice", testMaxJobs)
	require.NoError(t, err)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Payload.SessionID != "session-notice" {
				continue
			}
			require.Equal(t, pubsub.CreatedEvent, event.Type)
			require.Contains(t, event.Payload.Output, "hello-notice")
			require.Equal(t, 0, event.Payload.ExitCode)
			return
		case <-deadline:
			t.Fatal("timed out waiting for background completion notice")
		}
	}
}

func TestForegroundJobPublishesNoNotice(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := SubscribeBackgroundJobs(ctx)

	manager := newBackgroundShellManager()
	bgShell, err := manager.Start(ctx, t.TempDir(), nil, "echo hello-inline", "", testMaxJobs)
	require.NoError(t, err)
	bgShell.Wait()

	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case event := <-events:
			if event.Payload.ShellID == bgShell.ID {
				t.Fatalf("a job the model waited on must not publish a notice, got %#v", event)
			}
		case <-deadline:
			return
		}
	}
}

func TestBackgroundJobSessionAttachedLateStillNotifies(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := SubscribeBackgroundJobs(ctx)

	manager := newBackgroundShellManager()
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	command := fmt.Sprintf("while [ ! -f %q ]; do sleep 0.01; done; echo late-session", release)
	bgShell, err := manager.Start(ctx, dir, nil, command, "", testMaxJobs)
	require.NoError(t, err)

	// The job outlived the foreground window, so the tool layer attaches the
	// session after the fact and its completion must still be announced.
	bgShell.SetSessionID("session-late")
	require.NoError(t, os.WriteFile(release, nil, 0o644))

	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Payload.SessionID != "session-late" {
				continue
			}
			require.Contains(t, event.Payload.Output, "late-session")
			return
		case <-deadline:
			t.Fatal("timed out waiting for the late-attached session notice")
		}
	}
}

func TestBackgroundJobAwaitedSuppressesNotice(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := SubscribeBackgroundJobs(ctx)

	manager := newBackgroundShellManager()
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	command := fmt.Sprintf("while [ ! -f %q ]; do sleep 0.01; done; echo awaited", release)
	bgShell, err := manager.StartWithSession(ctx, dir, nil, command, "", "session-awaited", testMaxJobs)
	require.NoError(t, err)
	bgShell.MarkAwaited()
	require.NoError(t, os.WriteFile(release, nil, 0o644))
	bgShell.Wait()

	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case event := <-events:
			if event.Payload.SessionID == "session-awaited" {
				t.Fatalf("awaited job should not publish a notice, got %#v", event)
			}
		case <-deadline:
			return
		}
	}
}

func TestBackgroundJobKillSuppressesNotice(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events := SubscribeBackgroundJobs(ctx)

	manager := newBackgroundShellManager()
	bgShell, err := manager.StartWithSession(ctx, t.TempDir(), nil, "sleep 30", "", "session-killed", testMaxJobs)
	require.NoError(t, err)
	bgShell.MarkKilledByModel()
	require.NoError(t, manager.Kill(bgShell.ID))

	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case event := <-events:
			if event.Payload.SessionID == "session-killed" {
				t.Fatalf("model-killed job should not publish a notice, got %#v", event)
			}
		case <-deadline:
			return
		}
	}
}
