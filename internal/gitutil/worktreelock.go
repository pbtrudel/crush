package gitutil

import (
	"path/filepath"
	"sync"
)

// worktreeGitLock is one worktree's whole-tree git lock. A scope check and a
// commit both read or write the index and the work tree, so they take turns.
type worktreeGitLock struct {
	mu sync.Mutex
}

// worktreeGitLocks holds one lock per worktree path for the lifetime of the
// process. A session uses one path per task, so the map stays small.
var worktreeGitLocks = struct {
	sync.Mutex
	byRoot map[string]*worktreeGitLock
}{byRoot: map[string]*worktreeGitLock{}}

// LockWorktree blocks until it holds root's whole-tree git lock, and returns
// the function that releases it. A scope check and a commit of the same
// worktree share the lock, so a commit never captures a change a check is
// about to restore, and no two of them race on index.lock.
func LockWorktree(root string) (unlock func()) {
	key := filepath.Clean(root)
	worktreeGitLocks.Lock()
	lock := worktreeGitLocks.byRoot[key]
	if lock == nil {
		lock = &worktreeGitLock{}
		worktreeGitLocks.byRoot[key] = lock
	}
	worktreeGitLocks.Unlock()
	lock.mu.Lock()
	return lock.mu.Unlock
}
