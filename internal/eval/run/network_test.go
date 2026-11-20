package run

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/stretchr/testify/require"
)

// TestNetworkEnv checks that only a jailed run gets the proxy variables.
func TestNetworkEnv(t *testing.T) {
	t.Parallel()

	require.Nil(t, networkEnv(netjail.ModeHost, 1234))
	require.Equal(t, netjail.Env(1234), networkEnv(netjail.ModeJail, 1234))
}

// TestProxyPath checks where the proxy's socket lives: in the run directory
// when the path fits a unix address, in the eval home's sock directory when
// it does not.
func TestProxyPath(t *testing.T) {
	t.Parallel()

	home := filepath.Join(string(filepath.Separator), "home", "u", ".local", "state", "crush-eval")
	short := filepath.Join(home, "batches", "b1", "runs", "pr-0010-main@abc12345-fake-r1")
	require.Equal(t, filepath.Join(short, "net.sock"), proxyPath(short, home))

	long := filepath.Join(home, "batches", "2026-11-19-an-experiment-with-a-long-name", "runs", "pr-0010-main@abc12345-deepseek-flash-r1")
	path := proxyPath(long, home)
	require.NotEqual(t, filepath.Join(long, "net.sock"), path)
	require.Contains(t, path, filepath.Join(home, "sock"))
	require.LessOrEqual(t, len(path), maxSocketPath)
	require.True(t, strings.HasSuffix(path, ".sock"), path)

	other := proxyPath(long+"-other", home)
	require.NotEqual(t, path, other, "two runs never share a socket")
}

// TestRunJailNeedsBaseURL checks that a jail run of a route without a
// base_url stops before starting the child, since it has nothing to allow.
func TestRunJailNeedsBaseURL(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	opts := f.options()
	opts.Network = netjail.ModeJail
	_, err := Run(context.Background(), opts)
	require.ErrorContains(t, err, "base_url")
}
