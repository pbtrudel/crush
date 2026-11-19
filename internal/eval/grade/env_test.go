package grade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEnvPutsToolchainFirst checks that grading runs the go of the toolchain
// go.mod selects and not the go its PATH names first, which GOTOOLCHAIN=local
// would otherwise refuse.
func TestEnvPutsToolchainFirst(t *testing.T) {
	t.Parallel()

	parent := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/home/someone"}
	resolve := func(env []string) (string, error) {
		require.Equal(t, parent, env, "the toolchain resolves with the parent environment")
		return "/toolchains/go1.27.2", nil
	}

	graded := env("/scratch/home", parent, resolve)
	require.Equal(t,
		"PATH="+filepath.Join("/toolchains/go1.27.2", "bin")+string(os.PathListSeparator)+"/usr/local/bin:/usr/bin:/bin",
		graded[0])
	require.Contains(t, graded, "HOME=/scratch/home")
	require.Contains(t, graded, "GOTOOLCHAIN=local")
	require.Contains(t, graded, "GOPROXY=off")
}
