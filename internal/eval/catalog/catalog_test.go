package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/stretchr/testify/require"
)

const (
	providersJSON = `{"providers":[{"id":"copilot"}]}` + "\n"
	hyperJSON     = `{"id":"hyper"}` + "\n"
)

// TestFreeze copies the two catalogs a batch needs from the user's Crush data
// dir into the eval home, byte for byte and readable only by its owner, and
// names them by the sha256 a result line records.
func TestFreeze(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	from := source(t, map[string]string{"providers.json": providersJSON, "hyper.json": hyperJSON})

	fp, err := Freeze(h, from)
	require.NoError(t, err)
	require.Equal(t, sha(providersJSON), fp.Providers)
	require.Equal(t, sha(hyperJSON), fp.Hyper)

	for name, content := range map[string]string{"providers.json": providersJSON, "hyper.json": hyperJSON} {
		path := filepath.Join(h.DataDir(), name)
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, content, string(got))
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

// TestFreezeWithoutHyper checks that a user who never used Hyper leaves no
// hyper catalog behind and no empty fingerprint for one.
func TestFreezeWithoutHyper(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	from := source(t, map[string]string{"providers.json": providersJSON})

	fp, err := Freeze(h, from)
	require.NoError(t, err)
	require.Equal(t, sha(providersJSON), fp.Providers)
	require.Empty(t, fp.Hyper)
	require.NoFileExists(t, filepath.Join(h.DataDir(), "hyper.json"))
}

// TestFreezeNeedsProviders checks that a user data dir with no provider
// catalog is an error, not an empty eval home.
func TestFreezeNeedsProviders(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	from := source(t, map[string]string{"hyper.json": hyperJSON})

	_, err := Freeze(h, from)
	require.ErrorContains(t, err, "providers.json")
	require.NoFileExists(t, filepath.Join(h.DataDir(), "providers.json"))
	require.NoFileExists(t, filepath.Join(h.DataDir(), "hyper.json"))
}

// TestReadNeedsProviders checks that recording a catalog refuses a home that
// has none, and says how to freeze one.
func TestReadNeedsProviders(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())

	_, err := Read(h)
	require.ErrorContains(t, err, "providers.json")
	require.ErrorContains(t, err, "crush-eval catalog freeze")
}

// TestRead checks the fingerprint of the catalogs the eval home serves, with
// no hyper catalog when the user has none.
func TestRead(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	write(t, filepath.Join(h.DataDir(), "providers.json"), providersJSON)

	fp, err := Read(h)
	require.NoError(t, err)
	require.Equal(t, sha(providersJSON), fp.Providers)
	require.Empty(t, fp.Hyper)

	write(t, filepath.Join(h.DataDir(), "hyper.json"), hyperJSON)
	fp, err = Read(h)
	require.NoError(t, err)
	require.Equal(t, sha(hyperJSON), fp.Hyper)
}

// TestUserDataDir checks the resolution of the user's Crush data dir, the way
// Crush resolves it: $CRUSH_GLOBAL_DATA, then $XDG_DATA_HOME, then the home
// directory.
func TestUserDataDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("CRUSH_GLOBAL_DATA", filepath.Join(xdg, "data"))

	dir, err := UserDataDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(xdg, "data"), dir)

	t.Setenv("CRUSH_GLOBAL_DATA", "")
	dir, err = UserDataDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(xdg, "crush"), dir)
}

// source is a stand-in for the user's Crush data dir.
func source(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		write(t, filepath.Join(dir, name), content)
	}
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// sha is the hex sha256 of content, as a fingerprint names it.
func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
