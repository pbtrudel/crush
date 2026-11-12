package claudeagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// recordStore persists the mapping between a Crush session and the Claude
// session that holds its native transcript.
//
// It is a small file store rather than a database table because the data is a
// cache: losing it costs a cache miss, never correctness.
type recordStore struct {
	dir string
	mu  sync.Mutex
}

func newRecordStore(dir string) *recordStore {
	return &recordStore{dir: dir}
}

// get returns the saved record for a session, or nil.
func (s *recordStore) get(key string) *Record {
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := os.ReadFile(s.path(key))
	if err != nil {
		return nil
	}
	var record Record
	if err := json.Unmarshal(body, &record); err != nil {
		return nil
	}
	if record.ClaudeSessionID == "" {
		return nil
	}
	return &record
}

// set saves a record.
func (s *recordStore) set(key string, record *Record) {
	if key == "" || record == nil {
		return
	}
	body, err := json.Marshal(record)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return
	}
	path := s.path(key)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// delete removes a record.
func (s *recordStore) delete(key string) {
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.Remove(s.path(key))
}

// path maps a session key to a file name. The key is already a hash, but it is
// hashed again so no caller can steer the path.
func (s *recordStore) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:16])+".json")
}
