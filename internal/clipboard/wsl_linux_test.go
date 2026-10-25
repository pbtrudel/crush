package clipboard

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// installFakePowerShell puts an executable named powershell.exe on PATH so
// the WSL reader finds it without touching a real Windows clipboard. The
// script prints payload and exits with code.
func installFakePowerShell(t *testing.T, payload []byte, code int) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if payload != nil {
		path := filepath.Join(dir, "payload")
		require.NoError(t, os.WriteFile(path, payload, 0o644))
		script += "exec cat '" + path + "'\n"
	}
	script += "exit " + strconv.Itoa(code) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "powershell.exe"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestReadWSLImageReturnsPNGBytes is the core contract: an image copied in
// Windows comes back as PNG bytes even though WSLg only offers image/bmp.
func TestReadWSLImageReturnsPNGBytes(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nwsl-image")
	installFakePowerShell(t, png, 0)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")

	got, err := readWSL(FormatImage)

	require.NoError(t, err)
	require.Equal(t, png, got)
}

// TestReadWSLImageEmptyClipboard makes sure a clipboard without an image is
// reported as empty instead of returning whatever the shell wrote.
func TestReadWSLImageEmptyClipboard(t *testing.T) {
	installFakePowerShell(t, nil, 1)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")

	_, err := readWSL(FormatImage)

	require.ErrorIs(t, err, ErrEmpty)
}

// TestReadWSLSkipsNonWSL keeps native Linux machines from shelling out to a
// Windows-only command.
func TestReadWSLSkipsNonWSL(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := "#!/bin/sh\n: > '" + marker + "'\nprintf 'x'\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "powershell.exe"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "")

	_, err := readWSL(FormatImage)

	require.ErrorIs(t, err, ErrUnsupported)
	_, statErr := os.Stat(marker)
	require.True(t, os.IsNotExist(statErr), "powershell must not run outside WSL")
}

// TestReadWSLTextUnsupported keeps the fallback limited to images; text
// already crosses the WSLg clipboard.
func TestReadWSLTextUnsupported(t *testing.T) {
	installFakePowerShell(t, []byte("text"), 0)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")

	_, err := readWSL(FormatText)

	require.ErrorIs(t, err, ErrUnsupported)
}

func TestIsWSL(t *testing.T) {
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("WSL_INTEROP", "")
	require.True(t, isWSL())

	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "/run/WSL/1_interop")
	require.True(t, isWSL())

	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "")
	require.False(t, isWSL())
}
