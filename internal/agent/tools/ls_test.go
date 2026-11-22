package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// The depth notice used to overwrite the truncation notice whenever a
// depth limit was configured, so a listing cut by max_items read as if it
// were complete. Both notices have to survive.
func TestListDirectoryTreeKeepsBothNotices(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for d := range 3 {
		dir := filepath.Join(root, fmt.Sprintf("dir%d", d))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		for i := range 3 {
			name := filepath.Join(dir, fmt.Sprintf("file%d.txt", i))
			require.NoError(t, os.WriteFile(name, []byte("x"), 0o644))
		}
	}

	depth, items := 2, 2
	limits := config.ToolLs{MaxDepth: &depth, MaxItems: &items}

	out, meta, err := ListDirectoryTree(root, LSParams{}, limits)
	require.NoError(t, err)
	require.True(t, meta.Truncated, "the listing must be cut by max_items")
	require.Contains(t, out, "There are more than 2 files")
	require.Contains(t, out, "up to a depth of 2")
}
