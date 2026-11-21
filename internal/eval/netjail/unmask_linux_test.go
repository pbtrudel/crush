//go:build linux

package netjail

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUnmaskExecutablesDropsDefaultsOnly checks that a binary inside a
// default mask is hidden from it, while a mask the caller asked for is
// refused instead of being dropped: running a jail without a mask the caller
// asked for is a silent leak.
func TestUnmaskExecutablesDropsDefaultsOnly(t *testing.T) {
	t.Parallel()

	defaults := []string{"/tmp", "/run"}
	masks := []string{"/tmp", "/work/crush", "/run"}
	kept, blocked := unmaskExecutables(masks,
		defaults, "/tmp/go-build/b001/netjail.test", "/work/crush/bin")
	require.Equal(t, []string{"/run"}, kept, "/tmp holds the binary the jail runs")
	require.Equal(t, []string{"/work/crush"}, blocked, "a requested mask is not dropped")
}

// TestUnmaskExecutablesKeepsTheDefaults checks that filtering never rewrites
// the mask slice it is given, in particular the package global.
func TestUnmaskExecutablesKeepsTheDefaults(t *testing.T) {
	t.Parallel()

	before := slices.Clone(DefaultMasks)
	kept, blocked := unmaskExecutables(DefaultMasks,
		DefaultMasks, "/tmp/go-build/b001/netjail.test")
	require.Empty(t, blocked)
	require.NotContains(t, kept, "/tmp")
	require.Equal(t, before, DefaultMasks, "the package global is untouched")
}
