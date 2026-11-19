package toolchain_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/toolchain"
	"github.com/stretchr/testify/require"
)

func TestPathPutsToolchainFirst(t *testing.T) {
	t.Parallel()

	parent := []string{"HOME=/home/someone", "PATH=/usr/local/bin:/usr/bin:/bin"}
	resolve := func(env []string) (string, error) {
		require.Equal(t, parent, env, "the toolchain resolves with the parent environment")
		return "/toolchains/go1.27.2", nil
	}

	got := toolchain.Path(parent, resolve)
	require.Equal(t, "/toolchains/go1.27.2/bin"+string(os.PathListSeparator)+"/usr/local/bin:/usr/bin:/bin", got)
}

func TestPathWithoutResolvedToolchain(t *testing.T) {
	t.Parallel()

	parent := []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	failing := func([]string) (string, error) { return "", errors.New("go is not installed") }
	require.Equal(t, "/usr/local/bin:/usr/bin:/bin", toolchain.Path(parent, failing), "an unresolved toolchain leaves the PATH alone")

	empty := func([]string) (string, error) { return "", nil }
	require.Equal(t, "/usr/local/bin:/usr/bin:/bin", toolchain.Path(parent, empty))
}

func TestPathWithoutParentPath(t *testing.T) {
	t.Parallel()

	parent := []string{"HOME=/home/someone"}
	resolve := func([]string) (string, error) { return "/toolchains/go1.27.2", nil }
	require.Equal(t, filepath.Join("/toolchains/go1.27.2", "bin"), toolchain.Path(parent, resolve))
}

func TestGo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\n"), 0o700))
	require.Equal(t, filepath.Join(dir, "go"), toolchain.Go([]string{"PATH=" + dir}))

	require.Equal(t, "go", toolchain.Go([]string{"PATH=/nonexistent"}), "a PATH without go leaves the bare name")
	require.Equal(t, "go", toolchain.Go(nil), "an environment without PATH leaves the bare name")
}

// TestValue checks that Value takes a value the parent environment sets and,
// when it does not set it, asks the go of the selected toolchain for it. That
// go runs with the parent environment, so a value the user wrote in go's own
// env file is found even though the child gets an empty HOME.
func TestValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fakeGo(t, root)
	resolve := func([]string) (string, error) { return root, nil }
	parent := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/home/someone"}

	require.Equal(t, "/fake/GOCACHE", toolchain.Value(parent, "GOCACHE", resolve))
	require.Equal(t, "/set/by/parent", toolchain.Value(append(parent, "GOCACHE=/set/by/parent"), "GOCACHE", resolve))
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

// TestGOROOTAndVersion run the real go command, which every other test of
// this repository needs anyway.
func TestGOROOTAndVersion(t *testing.T) {
	t.Parallel()

	root, err := toolchain.GOROOT(os.Environ())
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(root), "GOROOT %q is not absolute", root)

	version, err := toolchain.Version(os.Environ())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(version, "go1."), "version %q does not name a go release", version)
}
