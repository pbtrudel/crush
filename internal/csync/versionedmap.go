package csync

import (
	"iter"
	"sync"
	"sync/atomic"
)

// NewVersionedMap creates a new versioned, thread-safe map.
func NewVersionedMap[K comparable, V any]() *VersionedMap[K, V] {
	return &VersionedMap[K, V]{
		m:      NewMap[K, V](),
		notify: make(chan struct{}),
	}
}

// VersionedMap is a thread-safe map that keeps track of its version.
type VersionedMap[K comparable, V any] struct {
	m *Map[K, V]
	v atomic.Uint64

	// changeMu guards notify, a channel swapped for a fresh one on every
	// mutation. Changed hands back that channel together with the version
	// it belongs to, so a caller that read an older version first can see
	// that a mutation already landed instead of blocking on a stale
	// channel forever.
	changeMu sync.Mutex
	notify   chan struct{}
}

// Changed returns a channel that closes on the next mutation, plus the
// map version at the moment the channel was handed out. Reading
// Version() and then arming on Changed is race-free only because the pair
// is taken together: a mutation between the two calls shows up as a
// version difference rather than a missed wakeup.
func (m *VersionedMap[K, V]) Changed() (<-chan struct{}, uint64) {
	m.changeMu.Lock()
	defer m.changeMu.Unlock()
	return m.notify, m.v.Load()
}

// bump closes the current notify channel and installs a fresh one.
func (m *VersionedMap[K, V]) bump() {
	m.changeMu.Lock()
	close(m.notify)
	m.notify = make(chan struct{})
	m.changeMu.Unlock()
}

// Get gets the value for the specified key from the map.
func (m *VersionedMap[K, V]) Get(key K) (V, bool) {
	return m.m.Get(key)
}

// Set sets the value for the specified key in the map and increments the version.
func (m *VersionedMap[K, V]) Set(key K, value V) {
	m.m.Set(key, value)
	m.v.Add(1)
	m.bump()
}

// Del deletes the specified key from the map and increments the version.
func (m *VersionedMap[K, V]) Del(key K) {
	m.m.Del(key)
	m.v.Add(1)
	m.bump()
}

// Seq2 returns an iter.Seq2 that yields key-value pairs from the map.
func (m *VersionedMap[K, V]) Seq2() iter.Seq2[K, V] {
	return m.m.Seq2()
}

// Copy returns a copy of the inner map.
func (m *VersionedMap[K, V]) Copy() map[K]V {
	return m.m.Copy()
}

// Len returns the number of items in the map.
func (m *VersionedMap[K, V]) Len() int {
	return m.m.Len()
}

// Version returns the current version of the map.
func (m *VersionedMap[K, V]) Version() uint64 {
	return m.v.Load()
}
