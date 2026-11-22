package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoginStripsTheModelSelection runs `crush-eval login <route>` against a
// stand-in crush that leaves the data config a terminal sign-in leaves. The
// model selection, recent models and options must be gone, and the providers
// kept, or every run would follow the pinned model.
func TestLoginStripsTheModelSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in crush is a shell script")
	}

	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	script := `#!/bin/sh
mkdir -p "$CRUSH_GLOBAL_DATA"
cat > "$CRUSH_GLOBAL_DATA/crush.json" <<'JSON'
{"models":{"main":{"provider":"commandcode","model":"claude-sonnet-5-5"}},"recent_models":{"large":[]},"options":{"disable_metrics":true},"providers":{"copilot":{"api_key":"token"}}}
JSON
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(bin, "crush"), []byte(script), 0o755))

	routes := filepath.Join(state, "crush-eval", "routes")
	require.NoError(t, os.MkdirAll(routes, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(routes, "gpt.yaml"),
		[]byte("name: gpt\nprovider: copilot\nmodel: gpt-6-luna\n"), 0o600))

	var stdout, stderr bytes.Buffer
	code := loginCmd([]string{"gpt"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	path := filepath.Join(state, "crush-eval", "data", "crush.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &doc))
	for _, key := range []string{"models", "recent_models", "options"} {
		require.NotContains(t, doc, key, "%s must be stripped after the sign-in", key)
	}
	require.JSONEq(t, `{"copilot":{"api_key":"token"}}`, string(doc["providers"]))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
