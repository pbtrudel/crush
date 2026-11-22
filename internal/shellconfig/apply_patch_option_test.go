package shellconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOption_ApplyPatch pins the crushrc spelling of options.apply_patch, so
// the GPT patch format can be turned on without hand-editing crush.json.
func TestOption_ApplyPatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte("option apply-patch true\n"))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))
	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["apply_patch"])
}
