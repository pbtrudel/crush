package batch

import (
	"context"
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
