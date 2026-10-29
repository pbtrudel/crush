package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/crush/internal/pubsub"
)

// SubagentEvent reports a background subagent settlement to the parent
// session.
type SubagentEvent struct {
	ID              string
	SessionID       string
	ParentSessionID string
	Output          string
	Err             string
}

var subagentBroker = pubsub.NewBroker[SubagentEvent]()

// SubscribeSubagents returns a channel of background subagent settlements.
func SubscribeSubagents(ctx context.Context) <-chan pubsub.Event[SubagentEvent] {
	return subagentBroker.Subscribe(ctx)
}

// subagentRun is one background delegation tracked by the coordinator.
type subagentRun struct {
	id              string
	childSessionID  string
	parentSessionID string
	done            chan struct{}
	output          string
	err             error
	cancel          context.CancelFunc
	completedAt     time.Time
}

type subagentManager struct {
	mu     sync.Mutex
	runs   map[string]*subagentRun
	nextID atomic.Uint64
}

func newSubagentManager() *subagentManager {
	return &subagentManager{runs: make(map[string]*subagentRun)}
}

func (m *subagentManager) start(childSessionID, parentSessionID string, cancel context.CancelFunc) *subagentRun {
	id := fmt.Sprintf("agent-%d", m.nextID.Add(1))
	run := &subagentRun{
		id:              id,
		childSessionID:  childSessionID,
		parentSessionID: parentSessionID,
		done:            make(chan struct{}),
		cancel:          cancel,
	}
	m.mu.Lock()
	m.runs[id] = run
	m.pruneLocked()
	m.mu.Unlock()
	return run
}

func (m *subagentManager) get(id string) (*subagentRun, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	return run, ok
}

// result returns a completed run's output and error, or done=false while it
// is still running. found reports whether the id is known.
func (m *subagentManager) result(id string) (output string, done, found bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	if !ok {
		return "", false, false, nil
	}
	select {
	case <-run.done:
		return run.output, true, true, run.err
	default:
		return "", false, true, nil
	}
}

func (m *subagentManager) finish(run *subagentRun, output string, err error) {
	m.mu.Lock()
	run.output = output
	run.err = err
	run.completedAt = time.Now()
	close(run.done)
	m.mu.Unlock()
}

func (m *subagentManager) cancelAll() {
	m.mu.Lock()
	runs := make([]*subagentRun, 0, len(m.runs))
	for _, run := range m.runs {
		runs = append(runs, run)
	}
	m.mu.Unlock()
	for _, run := range runs {
		select {
		case <-run.done:
		default:
			run.cancel()
		}
	}
}

func (m *subagentManager) pruneLocked() {
	if len(m.runs) <= 100 {
		return
	}
	cutoff := time.Now().Add(-time.Hour)
	for id, run := range m.runs {
		if !run.completedAt.IsZero() && run.completedAt.Before(cutoff) {
			delete(m.runs, id)
		}
	}
}

func backgroundSubagentNotice(event SubagentEvent) string {
	if event.Err != "" {
		return fmt.Sprintf("<background_subagent_notice>Subagent %s failed: %s</background_subagent_notice>", event.ID, event.Err)
	}
	output := strings.TrimSpace(event.Output)
	if output == "" {
		output = "No text output."
	}
	if len([]rune(output)) > 2048 {
		output = string([]rune(output)[:2048]) + "\n[... output truncated; use subagent_output with subagent_id=" + event.ID + " to read the full result ...]"
	}
	return fmt.Sprintf("<background_subagent_notice>Subagent %s finished. Result:\n%s\nUse subagent_output with subagent_id=%s if you need the complete result.</background_subagent_notice>", event.ID, output, event.ID)
}
