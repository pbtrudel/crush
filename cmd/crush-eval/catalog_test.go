package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCatalogFreeze copies a stand-in user's Crush catalogs into the eval home
// and prints the sha256 a batch records.
func TestCatalogFreeze(t *testing.T) {
	state := t.TempDir()
	user := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_DATA_HOME", user)
	t.Setenv("CRUSH_GLOBAL_DATA", "")

	source := filepath.Join(user, "crush")
	require.NoError(t, os.MkdirAll(source, 0o700))
	providers := `{"providers":[{"id":"copilot"}]}` + "\n"
	hyper := `{"id":"hyper"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(source, "providers.json"), []byte(providers), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(source, "hyper.json"), []byte(hyper), 0o600))

	var stdout, stderr bytes.Buffer
	code := run([]string{"catalog", "freeze"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	data := filepath.Join(state, "crush-eval", "data")
	got, err := os.ReadFile(filepath.Join(data, "providers.json"))
	require.NoError(t, err)
	require.Equal(t, providers, string(got))
	info, err := os.Stat(filepath.Join(data, "providers.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.Equal(t, "providers.json sha256 "+sha256Hex(providers)+"\nhyper.json sha256 "+sha256Hex(hyper)+"\n", stdout.String())
}

// TestCatalogUsage checks that a missing or unknown subcommand is a usage
// error, before the user's Crush data dir is read.
func TestCatalogUsage(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"catalog"}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), "crush-eval catalog freeze")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"catalog", "thaw"}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), "crush-eval catalog freeze")
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
