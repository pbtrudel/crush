package route

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// configDoc is the shape of the generated file, read back for assertions.
type configDoc struct {
	Providers map[string]map[string]any `json:"providers"`
	Options   map[string]any            `json:"options"`
	MCP       any                       `json:"mcp"`
	Hooks     any                       `json:"hooks"`
}

func TestWriteGlobalConfig(t *testing.T) {
	t.Parallel()

	r := &Route{
		Name:           "deepseek-flash",
		Provider:       "commandcode",
		Model:          "deepseek/deepseek-v4.1-flash",
		ProviderConfig: map[string]any{"api_key": "$DEEPSEEK_API_KEY"},
	}
	path := filepath.Join(t.TempDir(), "crush", "crush.json")

	require.NoError(t, WriteGlobalConfig(path, r, nil))

	doc := readDoc(t, path)
	require.Equal(t, map[string]any{"api_key": "$DEEPSEEK_API_KEY"}, doc.Providers["commandcode"])
	require.Equal(t, map[string]any{
		"disable_metrics":              true,
		"disable_provider_auto_update": true,
		"disabled_tools": []any{
			"fetch", "agentic_fetch", "download", "sourcegraph", "web_search", "web_fetch",
		},
	}, doc.Options)
	require.Nil(t, doc.MCP)
	require.Nil(t, doc.Hooks)

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestWriteGlobalConfigOverlay(t *testing.T) {
	t.Parallel()

	r := &Route{Name: "deepseek-flash", Provider: "commandcode", Model: "deepseek/deepseek-v4.1-flash"}
	path := filepath.Join(t.TempDir(), "crush.json")

	require.NoError(t, WriteGlobalConfig(path, r, map[string]any{"agent_teams": false}))

	doc := readDoc(t, path)
	require.Equal(t, false, doc.Options["agent_teams"])
	require.Equal(t, true, doc.Options["disable_metrics"])
	require.Equal(t, true, doc.Options["disable_provider_auto_update"])
	require.Contains(t, doc.Options, "disabled_tools")
}

// TestWriteGlobalConfigKeepsSecretsOut mutates the environment, so it
// cannot run in parallel with the other tests of the package.
func TestWriteGlobalConfigKeepsSecretsOut(t *testing.T) {
	// The value is in the parent environment, the file must hold only the
	// name of the variable.
	t.Setenv("DEEPSEEK_API_KEY", "sk-live-value-that-must-not-leak")

	r := &Route{
		Name:           "deepseek-flash",
		Provider:       "commandcode",
		Model:          "deepseek/deepseek-v4.1-flash",
		ProviderConfig: map[string]any{"api_key": "$DEEPSEEK_API_KEY"},
	}
	path := filepath.Join(t.TempDir(), "crush.json")
	require.NoError(t, WriteGlobalConfig(path, r, nil))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "sk-live-value-that-must-not-leak")
	require.Contains(t, string(raw), "$DEEPSEEK_API_KEY")
}

func readDoc(t *testing.T, path string) configDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc configDoc
	require.NoError(t, json.Unmarshal(raw, &doc), string(raw))
	require.NotEmpty(t, strings.TrimSpace(string(raw)))
	return doc
}
