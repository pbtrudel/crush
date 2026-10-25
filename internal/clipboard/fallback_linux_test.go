package clipboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// withNativeRead replaces the platform clipboard reader for one test so the
// fallback chain can be exercised without a real X11/Wayland clipboard.
func withNativeRead(t *testing.T, fn func(Format) ([]byte, error)) {
	t.Helper()
	original := readNative
	readNative = fn
	t.Cleanup(func() { readNative = original })
}

// TestReadFallsBackToWSLImage covers the reported failure: the native
// clipboard reports no image because WSLg only exposes image/bmp, and the
// Windows clipboard still has the PNG.
func TestReadFallsBackToWSLImage(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfrom-windows")
	installFakePowerShell(t, png, 0)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	withNativeRead(t, func(Format) ([]byte, error) { return nil, ErrEmpty })

	got, err := Read(FormatImage)

	require.NoError(t, err)
	require.Equal(t, png, got)
}

// TestReadPrefersNativeImage makes sure the fallback never shadows a native
// read that already worked.
func TestReadPrefersNativeImage(t *testing.T) {
	native := []byte("\x89PNG\r\n\x1a\nnative")
	installFakePowerShell(t, []byte("\x89PNG\r\n\x1a\nwindows"), 0)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	withNativeRead(t, func(Format) ([]byte, error) { return native, nil })

	got, err := Read(FormatImage)

	require.NoError(t, err)
	require.Equal(t, native, got)
}

// TestReadTextDoesNotFallBackToWSL keeps text reads native: WSLg already
// bridges text, and the fallback only speaks image.
func TestReadTextDoesNotFallBackToWSL(t *testing.T) {
	installFakePowerShell(t, []byte("windows-text"), 0)
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	withNativeRead(t, func(Format) ([]byte, error) { return nil, ErrEmpty })

	_, err := Read(FormatText)

	require.ErrorIs(t, err, ErrEmpty)
}
