package batch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/stretchr/testify/require"
)

// TestBatchRecordsNetwork checks that a batch records its network mode and
// hands it to every run.
func TestBatchRecordsNetwork(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	f.opts.Network = netjail.ModeJail
	f.opts.Preflight = func() error { return nil }
	_, err := Run(context.Background(), f.opts)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(f.opts.Home.BatchDir("b1"), "batch.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"network": "jail"`)

	require.NotEmpty(t, f.calls)
	for _, call := range f.calls {
		require.Equal(t, netjail.ModeJail, call.Network)
	}
}

// TestBatchRefusesUnknownNetwork checks that a mode the runner does not know
// is refused before anything runs.
func TestBatchRefusesUnknownNetwork(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	f.opts.Network = "vpn"
	_, err := Run(context.Background(), f.opts)
	require.ErrorContains(t, err, "network")
	require.Empty(t, f.builds)
	require.Empty(t, f.calls)
}

// TestBatchPreflightError checks that a batch that cannot start the jail
// fails before it builds or runs anything, and says what to do.
func TestBatchPreflightError(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	f.opts.Network = netjail.ModeJail
	f.opts.Preflight = func() error { return errors.New("user namespaces are blocked") }
	_, err := Run(context.Background(), f.opts)
	require.ErrorContains(t, err, "user namespaces are blocked")
	require.Empty(t, f.builds)
	require.Empty(t, f.calls)
}

// TestBatchResumeTreatsEmptyNetworkAsHost checks that a record written
// before the flag existed keeps its runs on the host.
func TestBatchResumeTreatsEmptyNetworkAsHost(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	require.NoError(t, os.MkdirAll(f.opts.Home.BatchDir("b1"), 0o700))
	require.NoError(t, (&Record{Schema: Schema, ID: "b1", Seed: 7, Suite: f.opts.Suite, Experiment: f.opts.Path}).Write(f.opts.Home))
	f.opts.Network = netjail.ModeJail
	f.opts.Preflight = func() error { return nil }

	_, err := Run(context.Background(), f.opts)
	require.NoError(t, err)
	require.NotEmpty(t, f.calls)
	for _, call := range f.calls {
		require.Equal(t, netjail.ModeHost, call.Network)
	}
}

// TestBatchResumeRefusesAnotherNetwork checks that an explicit --network
// that differs from the record is an error.
func TestBatchResumeRefusesAnotherNetwork(t *testing.T) {
	t.Parallel()

	f := newFixture(t, 1, 1)
	require.NoError(t, os.MkdirAll(f.opts.Home.BatchDir("b1"), 0o700))
	require.NoError(t, (&Record{Schema: Schema, ID: "b1", Seed: 7, Suite: f.opts.Suite, Experiment: f.opts.Path, Network: netjail.ModeHost}).Write(f.opts.Home))
	f.opts.Network = netjail.ModeJail
	f.opts.NetworkSet = true
	f.opts.Preflight = func() error { return nil }

	_, err := Run(context.Background(), f.opts)
	require.ErrorContains(t, err, "network")
	require.Empty(t, f.calls)

	// The mode the record holds is accepted.
	f.opts.Network = netjail.ModeHost
	_, err = Run(context.Background(), f.opts)
	require.NoError(t, err)
	require.NotEmpty(t, f.calls)
}
