package agent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/message"
)

// SessionTree is what a session and the runs started from it had in flight
// when it was read.
type SessionTree struct {
	// Busy is true while a run the coordinator started for the session, or
	// for a teammate session in its tree, has not returned, or one of them
	// runs a turn or has prompts queued.
	Busy bool
	// Team is what the Team service reports for the tree. It is zero when
	// Agent Teams is off.
	Team agentteam.TreeStatus
	// Limit is the limit that refused a run of the tree, empty when none
	// did; a refused run writes nothing to the log.
	Limit message.TurnEnd
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

// TreeLimiter is implemented by coordinators that can bound the work of a
// session tree.
type TreeLimiter interface {
	// LimitSessionTree sets the limits that every run in the tree of
	// sessionID draws on from now. When the timeout passes, every run in
	// the tree is cancelled, its teammates' included.
	LimitSessionTree(sessionID string, limits RunLimits)
}

var _ TreeLimiter = (*coordinator)(nil)

// LimitSessionTree implements TreeLimiter. The deadline is counted from
// now. A run of the tree that would start once a limit is reached is
// refused, so a stopped tree goes idle and a wait for it ends.
func (c *coordinator) LimitSessionTree(sessionID string, limits RunLimits) {
	c.limits.set(sessionID, limits)
	if limits.Timeout > 0 {
		time.AfterFunc(limits.Timeout, func() { c.stopSessionTree(sessionID) })
	}
}

// stopSessionTree cancels every run in the tree of sessionID: the
// session's own with its subagents, its teammates' through the Team
// service, which is the only way to reach their detached contexts, and a
// run a background job woke on a teammate's session.
func (c *coordinator) stopSessionTree(sessionID string) {
	slog.Info("Session tree passed its deadline", "session_id", sessionID)
	c.Cancel(sessionID)
	// Read the service once, so the nil check and the calls come from the
	// same one.
	team := c.team
	if team == nil {
		return
	}
	team.CancelTree(sessionID)
	for _, id := range team.Tree(sessionID).Sessions {
		c.Cancel(id)
	}
}

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

// runCounter counts the runs of each session that are in flight. It keeps its
// own lock, so a run's start and end never wait on the lock a tree read
// holds. The zero value is ready to use.
type runCounter struct {
	mu   sync.Mutex
	runs map[string]int
}

// start counts one run of sessionID as in flight.
func (r *runCounter) start(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = make(map[string]int)
	}
	r.runs[sessionID]++
}

// finish ends one run of sessionID, dropping the entry at zero.
func (r *runCounter) finish(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.runs[sessionID] {
	case 0, 1:
		delete(r.runs, sessionID)
	default:
		r.runs[sessionID]--
	}
}

// count reports how many runs of sessionID are in flight.
func (r *runCounter) count(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs[sessionID]
}

// runStarted counts a run of sessionID as in flight until runFinished.
func (c *coordinator) runStarted(sessionID string) {
	c.inFlight.start(sessionID)
}

// runFinished ends one run of sessionID and wakes the tree's waiters.
func (c *coordinator) runFinished(sessionID string) {
	c.inFlight.finish(sessionID)
	c.runEnded.notify()
}

// runsInFlight reports how many runs of sessionID have started and not
// finished.
func (c *coordinator) runsInFlight(sessionID string) int {
	return c.inFlight.count(sessionID)
}

// sessionTree reads what the tree of sessionID has in flight. team is read
// once by the caller, so the status and the change signal come from the same
// service.
func (c *coordinator) sessionTree(team *agentteam.Service, sessionID string) SessionTree {
	var tree SessionTree
	if team != nil {
		tree.Team = team.Tree(sessionID)
	}
	for _, id := range append([]string{sessionID}, tree.Team.Sessions...) {
		if c.runsInFlight(id) > 0 || c.IsSessionBusy(id) || c.QueuedPrompts(id) > 0 {
			tree.Busy = true
			break
		}
	}
	return tree
}

// WaitForSessionTree implements TreeWatcher. A tree only becomes idle through
// runs started by the coordinator, which signal when they finish, and Team
// service changes, so the tree is read once per change instead of on a timer.
// Summarize and Ask mark a session busy without signalling and are not
// part of a run's tree.
func (c *coordinator) WaitForSessionTree(ctx context.Context, sessionID string) (SessionTree, error) {
	// Read the service once, so the change channel and the tree always come
	// from the same one.
	team := c.team
	for {
		// Take the channels before reading the tree, so a change that lands
		// between the read and the select still wakes the loop.
		runEnded := c.runEnded.changed()
		var teamChanged <-chan struct{}
		if team != nil {
			teamChanged = team.Changed()
		}
		tree := c.sessionTree(team, sessionID)
		if tree.Idle() {
			tree.Limit = c.limits.forSession(ctx, c.sessions, sessionID).refusedBy()
			return tree, nil
		}
		select {
		case <-runEnded:
		case <-teamChanged:
		case <-ctx.Done():
			// The root is a key of the registry, so reading the
			// limit after a cancel reads no session.
			tree.Limit = c.limits.forSession(context.WithoutCancel(ctx), c.sessions, sessionID).refusedBy()
			return tree, ctx.Err()
		}
	}
}
