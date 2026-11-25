package grade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/evaltest"
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

// TestEnvOmitsUnsetCaches checks that a cache neither the parent nor the
// selected go sets is left out rather than set empty, which go rejects.
func TestEnvOmitsUnsetCaches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	silentGo(t, root)
	parent := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/home/someone"}
	resolve := func([]string) (string, error) { return root, nil }

	graded := env("/scratch/home", parent, resolve)
	require.NotContains(t, graded, "GOCACHE=")
	require.NotContains(t, graded, "GOMODCACHE=")
}

// TestEnvResolvesToolchainOnce checks that grading resolves the toolchain
// once and then runs go once per cache, instead of resolving the toolchain and
// running go again for every value.
func TestEnvResolvesToolchainOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls")
	countingGo(t, root, log)
	parent := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/home/someone"}
	calls := 0
	resolve := func([]string) (string, error) {
		calls++
		return root, nil
	}

	graded := env("/scratch/home", parent, resolve)
	require.Contains(t, graded, "GOCACHE=/fake/GOCACHE")
	require.Contains(t, graded, "GOMODCACHE=/fake/GOMODCACHE")
	require.Equal(t, 1, calls, "the toolchain resolves once")

	data, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(data), "\n"),
		"go runs once per cache")
}

// countingGo writes a go in root's bin that logs the go env name it is asked
// for and reports /fake/<name>.
func countingGo(t *testing.T, root, log string) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	script := "#!/bin/sh\necho \"$2\" >> " + log + "\necho \"/fake/$2\"\n"
	evaltest.WriteExecutable(t, filepath.Join(bin, "go"), []byte(script))
}

// silentGo writes a go in root's bin that reports no value for any name.
func silentGo(t *testing.T, root string) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	evaltest.WriteExecutable(t, filepath.Join(bin, "go"), []byte("#!/bin/sh\n"))
}

// fakeGo writes a go in root's bin that reports /fake/<name> for the go env
// name it is asked for.
func fakeGo(t *testing.T, root string) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	script := "#!/bin/sh\necho \"/fake/$2\"\n"
	evaltest.WriteExecutable(t, filepath.Join(bin, "go"), []byte(script))
}
