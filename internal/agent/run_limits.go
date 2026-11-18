package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// RunLimits bound the work of one session tree for the command that set
// them: a budget of steps that every run in the tree draws on, and a time
// after which no run in the tree goes on. Zero means no limit.
type RunLimits struct {
	MaxSteps int
	Timeout  time.Duration
}

// errTreeLimitReached refuses a run whose session tree has spent its step
// budget or passed its deadline.
var errTreeLimitReached = errors.New("the session tree reached its step or time limit")

// treeLimits is one session tree's limits and the steps its runs took.
// Every method is safe on a nil receiver, which has no limit.
type treeLimits struct {
	maxSteps int64
	deadline time.Time
	steps    atomic.Int64
	// refused is set once a run of the tree was refused because a limit
	// was reached.
	refused atomic.Bool
}

// newTreeLimits starts the limits at now: the deadline is now plus the
// timeout.
func newTreeLimits(limits RunLimits, now time.Time) *treeLimits {
	tree := &treeLimits{maxSteps: int64(limits.MaxSteps)}
	if limits.Timeout > 0 {
		tree.deadline = now.Add(limits.Timeout)
	}
	return tree
}

// takeStep counts one step a run of the tree is about to take.
func (l *treeLimits) takeStep() {
	if l != nil {
		l.steps.Add(1)
	}
}

// stepsSpent reports whether the tree's runs took every step of the budget.
func (l *treeLimits) stepsSpent() bool {
	return l != nil && l.maxSteps > 0 && l.steps.Load() >= l.maxSteps
}

// deadlinePassed reports whether the tree's deadline has passed.
func (l *treeLimits) deadlinePassed() bool {
	return l != nil && !l.deadline.IsZero() && !time.Now().Before(l.deadline)
}

// reached reports whether a limit stops the tree's runs.
func (l *treeLimits) reached() bool {
	return l.stepsSpent() || l.deadlinePassed()
}

// refuse records that a run of the tree was refused because a limit was
// reached.
func (l *treeLimits) refuse() {
	if l != nil {
		l.refused.Store(true)
	}
}

// refusedBy reports the limit that refused a run of the tree, or empty
// when none was refused. The deadline is read first, as in the turn-end
// rules.
func (l *treeLimits) refusedBy() message.TurnEnd {
	if l == nil || !l.refused.Load() {
		return ""
	}
	if l.deadlinePassed() {
		return message.TurnEndTimeLimit
	}
	return message.TurnEndStepLimit
}

// maxTreeDepth bounds the walk up a parent chain, so a cycle in the session
// table cannot hold a run.
const maxTreeDepth = 32

// runLimits holds the limits set on session trees, by the tree's root
// session. The zero value holds none.
type runLimits struct {
	mu    sync.Mutex
	trees map[string]*treeLimits
}

// set gives the tree of sessionID the limits, counted from now.
func (r *runLimits) set(sessionID string, limits RunLimits) *treeLimits {
	tree := newTreeLimits(limits, time.Now())
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.trees == nil {
		r.trees = make(map[string]*treeLimits)
	}
	r.trees[sessionID] = tree
	return tree
}

// forSession returns the limits of the tree sessionID belongs to, or nil
// when it belongs to no tree with limits. A run finds them through its
// session's parent chain, not through its context: a teammate started
// again and a run woken by a background job begin on fresh contexts.
func (r *runLimits) forSession(ctx context.Context, sessions session.Service, sessionID string) *treeLimits {
	if r == nil {
		return nil
	}
	id := sessionID
	for range maxTreeDepth {
		r.mu.Lock()
		tree, limited := r.trees[id], len(r.trees) > 0
		r.mu.Unlock()
		if tree != nil || !limited {
			return tree
		}
		sess, err := sessions.Get(ctx, id)
		if err != nil || sess.ParentSessionID == "" {
			return nil
		}
		id = sess.ParentSessionID
	}
	return nil
}
