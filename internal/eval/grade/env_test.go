package grade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEnvPutsToolchainFirst checks that grading runs the go of the toolchain
// go.mod selects and not the go its PATH names first, which GOTOOLCHAIN=local
// would otherwise refuse. It also checks that both caches come from the
// parent environment, through the selected go's own environment, so an empty
// HOME does not leave grading without them.
func TestEnvPutsToolchainFirst(t *testing.T) {
	t.Parallel()

	parent := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/home/someone"}
	root := t.TempDir()
	fakeGo(t, root)
	resolve := func(env []string) (string, error) {
		require.Equal(t, parent, env, "the toolchain resolves with the parent environment")
		return root, nil
	}

	graded := env("/scratch/home", parent, resolve)
	require.Equal(t,
		"PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+"/usr/local/bin:/usr/bin:/bin",
		graded[0])
	require.Contains(t, graded, "HOME=/scratch/home")
	require.Contains(t, graded, "GOCACHE=/fake/GOCACHE")
	require.Contains(t, graded, "GOMODCACHE=/fake/GOMODCACHE")
	require.Contains(t, graded, "GOTOOLCHAIN=local")
	require.Contains(t, graded, "GOPROXY=off")
}

// fakeGo writes a go in root's bin that reports /fake/<name> for the go env
// name it is asked for.
func fakeGo(t *testing.T, root string) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	script := "#!/bin/sh\necho \"/fake/$2\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o700))
}
