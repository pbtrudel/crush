package common

// lruCache is a bounded least-recently-used cache. It is not safe for
// concurrent use; callers hold the guarding mutex.
type lruCache[K comparable, V any] struct {
	entries map[K]V
	order   []K
	max     int
}

func newLRUCache[K comparable, V any](max int) *lruCache[K, V] {
	return &lruCache[K, V]{entries: make(map[K]V), max: max}
}

// get returns the value for key, marking it most recently used.
func (c *lruCache[K, V]) get(key K) (V, bool) {
	v, ok := c.entries[key]
	if !ok {
		return v, false
	}
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	c.order = append(c.order, key)
	return v, true
}

// put stores value for key, evicting the least recently used entries
// when the cache is full. It returns the evicted values in eviction
// order so owners can release side tables keyed by value.
func (c *lruCache[K, V]) put(key K, value V) []V {
	if _, ok := c.entries[key]; ok {
		c.entries[key] = value
		return nil
	}
	c.order = append(c.order, key)
	var evicted []V
	for len(c.order) > c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		evicted = append(evicted, c.entries[oldest])
		delete(c.entries, oldest)
	}
	c.entries[key] = value
	return evicted
}

// reset drops every entry.
func (c *lruCache[K, V]) reset() {
	clear(c.entries)
	c.order = nil
}
