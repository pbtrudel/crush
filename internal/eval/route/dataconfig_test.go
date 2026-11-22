package route

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/stretchr/testify/require"
)

// dataConfigWithSelection is what an interactive `crush login` leaves in the
// eval home's data config: a model selection, recent models and options
// beside the providers that actually matter.
func dataConfigWithSelection() map[string]any {
	return map[string]any{
		"models": map[string]any{
			"main": map[string]any{"provider": "commandcode", "model": "claude-sonnet-5-5"},
		},
		"recent_models": map[string]any{"large": []any{}},
		"options":       map[string]any{"disable_metrics": true},
		"providers": map[string]any{
			"copilot": map[string]any{"api_key": "token"},
		},
		"untouched": "kept",
	}
}

// TestStripDataConfig checks that the model selection a sign-in leaves in the
// eval home's data config is gone after `crush-eval login`, and that the
// providers and every other field survive.
func TestStripDataConfig(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	path := filepath.Join(h.DataDir(), "crush.json")
	writeJSON(t, path, dataConfigWithSelection())

	require.NoError(t, StripDataConfig(h))

	doc := readDataDoc(t, path)
	for _, key := range []string{"models", "recent_models", "options"} {
		require.NotContains(t, doc, key, "%s must be stripped", key)
	}
	require.Contains(t, doc, "providers")
	require.Contains(t, doc, "untouched")
	require.JSONEq(t, `{"copilot":{"api_key":"token"}}`, string(doc["providers"]))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestStripDataConfigWithoutFile checks that a home no sign-in has touched is
// no error, and that stripping does not create the file.
func TestStripDataConfigWithoutFile(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	require.NoError(t, StripDataConfig(h))
	require.NoFileExists(t, filepath.Join(h.DataDir(), "crush.json"))
}

// TestStripDataConfigKeepsAConfigWithNoSelection checks that a data config
// holding only providers is left byte for byte alone.
func TestStripDataConfigKeepsAConfigWithNoSelection(t *testing.T) {
	t.Parallel()

	h := home.At(t.TempDir())
	require.NoError(t, h.Ensure())
	path := filepath.Join(h.DataDir(), "crush.json")
	writeJSON(t, path, map[string]any{"providers": map[string]any{"copilot": map[string]any{"api_key": "token"}}})
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	require.NoError(t, StripDataConfig(h))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

// readDataDoc reads a data config as its top-level fields.
func readDataDoc(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}
