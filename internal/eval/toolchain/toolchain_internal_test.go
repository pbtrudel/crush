package toolchain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoName checks the binary name of each GOOS: Windows names go.exe.
func TestGoName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "go", goName("linux"))
	require.Equal(t, "go", goName("darwin"))
	require.Equal(t, "go.exe", goName("windows"))
}

// TestGoBinaryWindows checks that Windows finds a go.exe without the
// executable bit, which Windows does not keep.
func TestGoBinaryWindows(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "go.exe")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	require.Equal(t, path, goBinary([]string{"PATH=" + dir}, "windows"))
	require.Equal(t, "go.exe", goBinary([]string{"PATH=" + t.TempDir()}, "windows"),
		"a PATH without go.exe leaves the bare name")

	require.Equal(t, "go", goBinary([]string{"PATH=" + dir}, "linux"),
		"another GOOS does not accept go.exe")
}
