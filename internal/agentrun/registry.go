// Package agentrun tracks live agent runs in a workspace so every frontend
// can show all agents, across sessions and processes.
//
// The registry is a small JSON file written atomically and guarded by a
// cross-process lock. Only runs worth showing live in it: a finished run
// stays visible for a short grace period and is then dropped.
package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/lock"
)

// Status is an agent run's lifecycle state.
type Status string

// Run statuses.
const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Kind identifies what started the run.
type Kind string

// Run kinds.
const (
	KindSubagent Kind = "subagent"
	KindTeammate Kind = "teammate"
)

const (
	// maxRunningAge bounds a run whose process died without finishing.
	// Agent runs time out after 30 minutes, so anything older is stale.
	maxRunningAge = 35 * time.Minute
	// finishedGrace keeps a finished run visible long enough for the user
	// to see it complete.
	finishedGrace = 2 * time.Minute
	// lockTimeout bounds the cross-process write lock wait.
	lockTimeout = 2 * time.Second
)

// Run is one agent run.
type Run struct {
	ID              string    `json:"id"`
	Kind            Kind      `json:"kind"`
	Name            string    `json:"name"`
	SessionID       string    `json:"session_id,omitempty"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	Status          Status    `json:"status"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at,omitzero"`
}

// Registry persists agent runs to a JSON file. It is safe for concurrent
// use and for multiple processes sharing the same path.
type Registry struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

// New returns a registry backed by path. An empty path disables persistence.
func New(path string) *Registry {
	return &Registry{path: path, now: time.Now}
}

// Start records a running agent.
func (r *Registry) Start(run Run) {
	if run.StartedAt.IsZero() {
		run.StartedAt = r.now()
	}
	if run.Status == "" {
		run.Status = StatusRunning
	}
	r.update(func(runs []Run) []Run {
		runs = slices.DeleteFunc(runs, func(existing Run) bool { return existing.ID == run.ID })
		return append(runs, run)
	})
}

// Finish marks a run finished. The entry stays visible for a short grace
// period, then List drops it.
func (r *Registry) Finish(id string, status Status) {
	if status == "" {
		status = StatusDone
	}
	r.update(func(runs []Run) []Run {
		for i := range runs {
			if runs[i].ID != id {
				continue
			}
			runs[i].Status = status
			runs[i].FinishedAt = r.now()
		}
		return runs
	})
}

// List returns the runs worth showing, running first and oldest first.
func (r *Registry) List() []Run {
	runs, err := loadRuns(r.path)
	if err != nil {
		return nil
	}
	return sortRuns(r.prune(runs))
}

// sortRuns orders running runs before finished ones, oldest first.
func sortRuns(runs []Run) []Run {
	slices.SortStableFunc(runs, func(a, b Run) int {
		if a.Status != b.Status {
			switch {
			case a.Status == StatusRunning:
				return -1
			case b.Status == StatusRunning:
				return 1
			}
		}
		return a.StartedAt.Compare(b.StartedAt)
	})
	return runs
}

// update applies fn to the durable run list under the cross-process lock.
func (r *Registry) update(fn func([]Run) []Run) {
	if r == nil || r.path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// The lock file lives beside the registry, so the directory must exist
	// before the first write.
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	release, err := lock.File(ctx, r.path+".lock")
	if err != nil {
		return
	}
	defer release()

	runs, _ := loadRuns(r.path)
	runs = r.prune(runs)
	runs = fn(runs)
	_ = saveRuns(r.path, runs)
}

// prune drops runs that can no longer be trusted or shown.
func (r *Registry) prune(runs []Run) []Run {
	now := r.now()
	return slices.DeleteFunc(runs, func(run Run) bool {
		if run.Status == StatusRunning {
			return now.Sub(run.StartedAt) > maxRunningAge
		}
		return !run.FinishedAt.IsZero() && now.Sub(run.FinishedAt) > finishedGrace
	})
}

// Load reads the runs worth showing from path without taking the write lock.
func Load(path string) []Run {
	runs, err := loadRuns(path)
	if err != nil {
		return nil
	}
	return sortRuns(New(path).prune(runs))
}

type runFile struct {
	Version int   `json:"version"`
	Runs    []Run `json:"runs"`
}

// loadRuns reads the durable run list. A missing file is an empty list.
func loadRuns(path string) ([]Run, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agent runs: %w", err)
	}
	var file runFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode agent runs: %w", err)
	}
	return file.Runs, nil
}

// saveRuns writes the durable run list atomically.
func saveRuns(path string, runs []Run) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create agent runs directory: %w", err)
	}
	data, err := json.MarshalIndent(runFile{Version: 1, Runs: runs}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode agent runs: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".agents-*.json")
	if err != nil {
		return fmt.Errorf("create agent runs temp file: %w", err)
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write agent runs: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close agent runs: %w", err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("chmod agent runs: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace agent runs: %w", err)
	}
	return nil
}
