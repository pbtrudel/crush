package gitutil

import (
	"sync"
	"testing"
	"time"
)

// TestLockWorktreeSerializes pins the lock a worktree's scope check and its
// commit share: a second holder waits until the first releases.
func TestLockWorktreeSerializes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	inside := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		unlock := LockWorktree(root)
		close(inside)
		<-release
		unlock()
	}()
	<-inside

	acquired := make(chan struct{})
	go func() {
		unlock := LockWorktree(root)
		unlock()
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("a second holder entered the worktree lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-done
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock did not pass to the next holder")
	}
}

// TestLockWorktreeIsPerPath pins that two worktrees do not wait on each
// other.
func TestLockWorktreeIsPerPath(t *testing.T) {
	t.Parallel()
	one, two := t.TempDir(), t.TempDir()
	var wg sync.WaitGroup
	unlockOne := LockWorktree(one)
	defer unlockOne()
	wg.Go(func() {
		unlock := LockWorktree(two)
		unlock()
	})
	wg.Wait()
}
