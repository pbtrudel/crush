package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLRUCacheEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	c := newLRUCache[int, string](2)
	c.put(1, "one")
	c.put(2, "two")

	// A hit on 1 makes 2 the least recently used.
	v, ok := c.get(1)
	require.True(t, ok)
	require.Equal(t, "one", v)

	c.put(3, "three")
	_, ok = c.get(2)
	require.False(t, ok, "least recently used entry is evicted")
	_, ok = c.get(1)
	require.True(t, ok)
	_, ok = c.get(3)
	require.True(t, ok)
}

func TestLRUCachePutReportsEvictedValues(t *testing.T) {
	t.Parallel()

	c := newLRUCache[int, string](1)
	require.Empty(t, c.put(1, "one"))
	require.Equal(t, []string{"one"}, c.put(2, "two"))
}

func TestLRUCacheReset(t *testing.T) {
	t.Parallel()

	c := newLRUCache[int, string](2)
	c.put(1, "one")
	c.reset()
	_, ok := c.get(1)
	require.False(t, ok)
	require.Equal(t, 0, len(c.entries))
}
