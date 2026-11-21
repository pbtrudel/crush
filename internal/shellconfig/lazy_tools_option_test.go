package shellconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOption_LazyTools pins the crushrc spelling of options.lazy_tools, so the
// token-saving palette can be enabled without hand-editing crush.json.
func TestOption_LazyTools(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "crushrc")

	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte("option lazy-tools true\n"))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))
	opts := result["options"].(map[string]any)
	require.Equal(t, true, opts["lazy_tools"])
}
