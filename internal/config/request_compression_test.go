package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/csync"
	"github.com/stretchr/testify/require"
)

func TestValidateRequestCompression(t *testing.T) {
	t.Parallel()

	valid := &Config{Providers: csync.NewMapFrom(map[string]ProviderConfig{
		"ok": {ID: "ok", RequestCompression: []string{"br", "gzip"}},
	})}
	require.NoError(t, valid.ValidateRequestCompression())

	invalid := &Config{Providers: csync.NewMapFrom(map[string]ProviderConfig{
		"bad": {ID: "bad", RequestCompression: []string{"gzip", "zstd"}},
	})}
	err := invalid.ValidateRequestCompression()
	require.EqualError(t, err, `provider bad: request_compression: unknown encoding "zstd" (expected br or gzip)`)
}

func TestInitRejectsUnknownRequestCompression(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	crushJSON := `{
  "options": {"disable_default_providers": true, "disable_provider_auto_update": true},
  "providers": {"bad": {"type": "openai", "base_url": "http://127.0.0.1:9/v1",
    "api_key": "k", "request_compression": ["deflate"]}}
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "crush.json"), []byte(crushJSON), 0o644))

	_, err := Init(t.Context(), dir, "", false)
	require.ErrorContains(t, err, `provider bad: request_compression: unknown encoding "deflate"`)
}
