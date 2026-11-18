package agent

import (
	"context"
	"sync"

	"github.com/charmbracelet/crush/internal/agentteam"
)

// SessionTree is what a session and the runs started from it had in flight
// when it was read.
type SessionTree struct {
	// Busy is true while the session, or a teammate session in its tree,
	// runs a turn or has prompts queued.
	Busy bool
	// Team is what the Team service reports for the tree. It is zero when
	// Agent Teams is off.
	Team agentteam.TreeStatus
}

// Idle reports whether nothing in the tree is running or about to run. It
// is the one place the run command's wait is decided, so a limit on the
// tree belongs beside it.
func (t SessionTree) Idle() bool {
	return !t.Busy && t.Team.Active == 0 && t.Team.UnreadByTeammates == 0
}

// TreeWatcher is implemented by coordinators that can tell when a session's
// tree has gone idle.
type TreeWatcher interface {
	// WaitForSessionTree blocks until the tree of sessionID is idle and
	// returns what it left behind. It returns early with ctx's error.
	WaitForSessionTree(ctx context.Context, sessionID string) (SessionTree, error)
}

var _ TreeWatcher = (*coordinator)(nil)

// treeSignal wakes the callers waiting for a session tree to change. Each
// change closes the current channel and drops it, so a caller that took the
// channel before reading the tree cannot miss a change made after the read.
// The zero value is ready to use.
type treeSignal struct {
	mu sync.Mutex
	ch chan struct{}
}

// changed returns the channel the next change closes.
func (s *treeSignal) changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch == nil {
		s.ch = make(chan struct{})
	}
	return s.ch
}

// notify wakes every caller holding the current channel.
func (s *treeSignal) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ch != nil {
		close(s.ch)
		s.ch = nil
	}
}

// sessionTree reads what the tree of sessionID has in flight.
func (c *coordinator) sessionTree(sessionID string) SessionTree {
	var tree SessionTree
	if c.team != nil {
		tree.Team = c.team.Tree(sessionID)
	}
	for _, id := range append([]string{sessionID}, tree.Team.Sessions...) {
		if c.IsSessionBusy(id) || c.QueuedPrompts(id) > 0 {
			tree.Busy = true
			break
		}
	}
	return tree
}

// WaitForSessionTree implements TreeWatcher. A tree only becomes idle when a
// run returns or the Team service changes, and both signal, so the tree is
// read once per change instead of on a timer.
func (c *coordinator) WaitForSessionTree(ctx context.Context, sessionID string) (SessionTree, error) {
	for {
		// Take the channels before reading the tree, so a change that lands
		// between the read and the select still wakes the loop.
		runEnded := c.runEnded.changed()
		var teamChanged <-chan struct{}
		if c.team != nil {
			teamChanged = c.team.Changed()
		}
		tree := c.sessionTree(sessionID)
		if tree.Idle() {
			return tree, nil
		}
		select {
		case <-runEnded:
		case <-teamChanged:
		case <-ctx.Done():
			return tree, ctx.Err()
		}
	}
}
