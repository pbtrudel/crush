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
		defaults, "/work/crush/bin/crush", "/tmp/go-build/b001/netjail.test")
	require.Equal(t, []string{"/run"}, kept, "/tmp holds a binary the jail runs")
	require.Equal(t, []string{"/work/crush"}, blocked, "a requested mask is not dropped")
}

// TestUnmaskExecutablesKeepsAMaskOverAnArgument checks that a requested mask
// is refused only for the executable itself. A run passes its own working
// and data directories, which sit under the masked batches directory and
// are bound back, as arguments; they must not block the mask.
func TestUnmaskExecutablesKeepsAMaskOverAnArgument(t *testing.T) {
	t.Parallel()

	masks := []string{"/home/eval/batches"}
	kept, blocked := unmaskExecutables(masks, DefaultMasks,
		"/home/eval/bin/abc/crush", "--cwd", "/home/eval/batches/b1/runs/r1/src",
		"--data-dir", "/home/eval/batches/b1/runs/r1/data")
	require.Empty(t, blocked)
	require.Equal(t, masks, kept)
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
