package run

import (
	"context"
	"errors"
	"os"
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

// TestProxySocket checks that each run's proxy gets its own 0700 directory
// under the eval home, outside the directories the jail masks, and that the
// socket path fits a unix address.
func TestProxySocket(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	path, cleanup, err := proxySocket(home)
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	require.Equal(t, home, filepath.Dir(filepath.Dir(path)))
	require.LessOrEqual(t, len(path), maxSocketPath)
	require.True(t, strings.HasSuffix(path, ".sock"), path)

	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	// The directory is unique to this run.
	other, otherCleanup, err := proxySocket(home)
	require.NoError(t, err)
	require.NotEqual(t, dir, filepath.Dir(other))
	otherCleanup()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep"), nil, 0o600))
	cleanup()
	require.NoDirExists(t, dir)
}

// TestProxySocketTooLong checks that an eval home too deep for a unix socket
// address is an error, not a silently shortened path.
func TestProxySocketTooLong(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), strings.Repeat("h", 120))
	require.NoError(t, os.MkdirAll(home, 0o700))
	path, cleanup, err := proxySocket(home)
	require.Nil(t, cleanup)
	require.Empty(t, path)
	require.ErrorContains(t, err, "too long")
}

// TestRunJailNeedsBaseURL checks that a jail run of a route without a
// base_url stops before starting the child, since it has nothing to allow.
func TestRunJailNeedsBaseURL(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	opts := f.options()
	opts.Network = netjail.ModeJail
	opts.Preflight = func() error { return nil }
	_, err := Run(context.Background(), opts)
	require.ErrorContains(t, err, "base_url")
}

// TestRunJailPreflightError checks that a machine where user namespaces are
// blocked fails before the checkout and points at --network host.
func TestRunJailPreflightError(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	opts := f.options()
	opts.Network = netjail.ModeJail
	opts.Preflight = func() error { return errors.New("user namespaces are blocked") }
	_, err := Run(context.Background(), opts)
	require.ErrorContains(t, err, "user namespaces are blocked")
	require.Empty(t, f.dirMatches(t), "the run did not start")
}

// dirMatches is the runs directory of the fixture's batch, empty when no run
// directory exists.
func (f *fixture) dirMatches(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(f.home.BatchDir("b1"), "runs", "*"))
	require.NoError(t, err)
	return matches
}
