package lsp

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/env"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestClient(t *testing.T) {
	// Create a simple config for testing
	cfg := config.LSPConfig{
		Command:   "$THE_CMD", // Use echo as a dummy command that won't fail
		Args:      []string{"hello"},
		FileTypes: []string{"go"},
		Env:       map[string]string{},
	}

	// Test creating a powernap client - this will likely fail with echo
	// but we can still test the basic structure
	client, err := New("test", cfg, config.NewShellVariableResolver(env.NewFromMap(map[string]string{
		"THE_CMD": "echo",
	})), ".", false)
	if err != nil {
		// Expected to fail with echo command, skip the rest
		t.Skipf("Powernap client creation failed as expected with dummy command: %v", err)
		return
	}

	// If we get here, test basic interface methods
	if client.GetName() != "test" {
		t.Errorf("Expected name 'test', got '%s'", client.GetName())
	}

	if !client.HandlesFile("test.go") {
		t.Error("Expected client to handle .go files")
	}

	if client.HandlesFile("test.py") {
		t.Error("Expected client to not handle .py files")
	}

	// Test server state
	client.SetServerState(StateReady)
	if client.GetServerState() != StateReady {
		t.Error("Expected server state to be StateReady")
	}

	// Clean up - expect this to fail with echo command
	if err := client.Close(t.Context()); err != nil {
		// Expected to fail with echo command
		t.Logf("Close failed as expected with dummy command: %v", err)
	}
}

// TestNew_ExpansionFailure_Args pins that a failing $(cmd) in LSP
// args surfaces as a load error prefixed "invalid lsp args:" and that
// no client is returned. Mirrors the MCP contract where expansion
// failure hard-stops transport creation rather than silently running
// with an empty or literal value.
func TestNew_ExpansionFailure_Args(t *testing.T) {
	t.Parallel()

	cfg := config.LSPConfig{
		Command: "echo",
		Args:    []string{"--root", "$(false)"},
	}
	resolver := config.NewShellVariableResolver(env.NewFromMap(map[string]string{}))

	client, err := New("test-args-fail", cfg, resolver, ".", false)
	require.Error(t, err)
	require.Nil(t, client, "client must not start when args expansion fails")
	require.Contains(t, err.Error(), "invalid lsp args")
}

// TestNew_ExpansionFailure_Env pins the same contract for env values.
func TestNew_ExpansionFailure_Env(t *testing.T) {
	t.Parallel()

	cfg := config.LSPConfig{
		Command: "echo",
		Env:     map[string]string{"BAD": "$(false)"},
	}
	resolver := config.NewShellVariableResolver(env.NewFromMap(map[string]string{}))

	client, err := New("test-env-fail", cfg, resolver, ".", false)
	require.Error(t, err)
	require.Nil(t, client, "client must not start when env expansion fails")
	require.Contains(t, err.Error(), "invalid lsp env")
}

func TestNilClient(t *testing.T) {
	t.Parallel()

	var c *Client

	require.False(t, c.HandlesFile("/some/file.go"))
	require.Equal(t, DiagnosticCounts{}, c.GetDiagnosticCounts())
	require.Nil(t, c.GetDiagnostics())
	require.Nil(t, c.OpenFileOnDemand(context.Background(), "/some/file.go"))
	require.Nil(t, c.NotifyChange(context.Background(), "/some/file.go"))
	c.WaitForDiagnostics(context.Background(), time.Second)
}

// newTestClient builds a stopped client. Tests that wait on it run under
// synctest, since real timers stall on CI.
func newTestClient() *Client {
	c := &Client{
		name:        "test",
		diagnostics: csync.NewVersionedMap[protocol.DocumentURI, []protocol.Diagnostic](),
		openFiles:   csync.NewMap[string, *OpenFileInfo](),
	}
	c.serverState.Store(StateStopped)
	return c
}

func TestWaitForDiagnostics_NoChange(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		c := newTestClient()
		start := time.Now()
		c.WaitForDiagnostics(t.Context(), 5*time.Second)
		elapsed := time.Since(start)

		// Should return early via firstChangeDeadline (1s), not the full timeout.
		require.Equal(t, time.Second, elapsed, "should return early when no diagnostics change")
	})
}

func TestWaitForDiagnostics_ImmediateChange(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		c := newTestClient()

		go func() {
			time.Sleep(100 * time.Millisecond)
			c.diagnostics.Set(protocol.DocumentURI("file:///test.go"), nil)
		}()

		start := time.Now()
		c.WaitForDiagnostics(t.Context(), 5*time.Second)
		elapsed := time.Since(start)

		// Should detect the change and then settle for 300ms.
		require.GreaterOrEqual(t, elapsed, 400*time.Millisecond, "should wait for settle duration")
		require.Less(t, elapsed, time.Second, "should return after settling, not full timeout")
	})
}

func TestWaitForDiagnostics_RepeatedChanges(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		c := newTestClient()

		// Simulate an LSP server that publishes diagnostics in bursts.
		go func() {
			for i := range 5 {
				time.Sleep(50 * time.Millisecond)
				c.diagnostics.Set(protocol.DocumentURI("file:///test.go"), []protocol.Diagnostic{
					{Message: fmt.Sprintf("diag-%d", i)},
				})
			}
		}()

		start := time.Now()
		c.WaitForDiagnostics(t.Context(), 5*time.Second)
		elapsed := time.Since(start)

		// Burst lasts 250ms, then a 300ms settle window.
		require.GreaterOrEqual(t, elapsed, 550*time.Millisecond, "should wait for all changes to settle")
		require.Less(t, elapsed, time.Second, "should return after settling, not full timeout")
	})
}

func TestWaitForDiagnostics_ContextCancellation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		c := newTestClient()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go func() {
			time.Sleep(200 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		c.WaitForDiagnostics(ctx, 5*time.Second)
		elapsed := time.Since(start)

		require.Equal(t, 200*time.Millisecond, elapsed, "should return as soon as the context is cancelled")
	})
}

func TestWaitForDiagnostics_NilClient(t *testing.T) {
	t.Parallel()

	var c *Client
	// Should not panic.
	c.WaitForDiagnostics(context.Background(), time.Second)
}

// TestWaitForDiagnostics_WakesPromptlyOnPublish pins push-based waiting:
// a publish must be noticed immediately rather than on the next poll
// tick, so the total wait is the settle window plus the publish delay.
func TestWaitForDiagnostics_WakesPromptlyOnPublish(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		c := newTestClient()

		go func() {
			time.Sleep(10 * time.Millisecond)
			c.diagnostics.Set(protocol.DocumentURI("file:///test.go"), []protocol.Diagnostic{
				{Message: "diag"},
			})
		}()

		start := time.Now()
		c.WaitForDiagnostics(t.Context(), 5*time.Second)
		elapsed := time.Since(start)

		// 10ms until the publish plus the 300ms settle window. Polling at
		// 100ms granularity overshot this by more than a tick.
		require.LessOrEqual(t, elapsed, 320*time.Millisecond, "should wake on publish, not on a poll tick")
	})
}

func TestOpenFileForRead_ReportsNoNotificationWhenAlreadyOpen(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	const path = "/tmp/already-open.go"
	c.openFiles.Set(string(protocol.URIFromPath(path)), &OpenFileInfo{Version: 1})

	// An already-open file gets no didOpen, so nothing new can be
	// published: the caller must be told not to wait.
	sent, err := c.OpenFileForRead(context.Background(), path)
	require.NoError(t, err)
	require.False(t, sent)
}

func TestHasDiagnosticsFor(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	const path = "/tmp/diag.go"
	require.False(t, c.HasDiagnosticsFor(path))

	// Servers can publish a URI that differs in normalization from the
	// one the client generated from the path; matching must still work
	// because both resolve to the same file.
	c.diagnostics.Set(protocol.DocumentURI("file:///tmp/diag.go"), nil)
	require.True(t, c.HasDiagnosticsFor(path))
	require.False(t, c.HasDiagnosticsFor("/tmp/other.go"))
}
