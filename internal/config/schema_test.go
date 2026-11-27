package config

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSchemaHasNoWebDisplay pins the committed schema.json to the structs:
// options.web_display was removed with the old web UI, so the generated
// schema must not offer it. Regenerate with task schema after a change.
func TestSchemaHasNoWebDisplay(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../schema.json")
	require.NoError(t, err)
	for _, name := range []string{"web_display", "WebDisplayOptions"} {
		require.False(t, strings.Contains(string(raw), name), "schema.json still offers %s", name)
	}
}
