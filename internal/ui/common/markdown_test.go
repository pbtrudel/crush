package common

import (
	"testing"

	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// TestMarkdownRendererCacheIsBounded pins that the per-width renderer
// caches (and their per-renderer locks) stay bounded across a resize
// storm. Not parallel: the caches are package-global.
func TestMarkdownRendererCacheIsBounded(t *testing.T) {
	sty := styles.CharmtonePantera()

	rendererLocksMu.Lock()
	locksBefore := len(rendererLocks)
	rendererLocksMu.Unlock()

	for w := 20; w < 40; w++ {
		LockMarkdownRenderer(MarkdownRenderer(&sty, w))
	}

	mdCacheMu.Lock()
	require.LessOrEqual(t, len(mdCache.entries), markdownRendererCacheMax)
	mdCacheMu.Unlock()

	// Per-renderer locks are evicted alongside their renderers.
	rendererLocksMu.Lock()
	require.LessOrEqual(t, len(rendererLocks), locksBefore+markdownRendererCacheMax)
	rendererLocksMu.Unlock()
}
