package agentteam

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSpawnAfterShutdownIsRefused pins that a spawn racing a shutdown fails
// instead of leaving a failed teammate in the durable state for a later
// enable to resume.
func TestSpawnAfterShutdownIsRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	runner := newFakeRunner()
	service, err := New(path, Config{}, runner)
	require.NoError(t, err)
	service.Shutdown()

	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.ErrorIs(t, err, errShutdown)
	require.Equal(t, 0, runner.promptCount(), "a shut-down service must not start a run")

	state, err := LoadState(path)
	require.NoError(t, err)
	for _, member := range state.Members {
		require.NotEqual(t, StatusFailed, member.Status, "a refused spawn must not leave a failed member")
		require.False(t, member.Failed, "a refused spawn must not mark its member failed")
	}
}

// gatedRun is a run the test ends on its own, or a shutdown cancels. A
// cancel is reported on the runner so the test can order itself without
// sleeping.
type gatedRun struct {
	done     chan struct{}
	once     sync.Once
	onCancel func()
}

func (r *gatedRun) SessionID() string     { return "session-gated" }
func (r *gatedRun) Done() <-chan struct{} { return r.done }
func (r *gatedRun) Output() string        { return "" }
func (r *gatedRun) Err() error            { return nil }

// finish ends the run as if its turn returned.
func (r *gatedRun) finish() {
	r.once.Do(func() { close(r.done) })
}

// Cancel ends the run and reports the cancellation.
func (r *gatedRun) Cancel() {
	r.once.Do(func() {
		close(r.done)
		r.onCancel()
	})
}

// gatedRunner holds one member's next turn inside Start until the test
// releases it, and reports every cancellation.
type gatedRunner struct {
	mu       sync.Mutex
	prompts  int
	runs     map[string]*gatedRun
	gateName string
	entered  chan struct{}
	release  chan struct{}
	canceled chan string
}

func newGatedRunner() *gatedRunner {
	return &gatedRunner{
		runs:     make(map[string]*gatedRun),
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		canceled: make(chan string, 8),
	}
}

func (r *gatedRunner) Start(_ context.Context, member Member, _ string) (Run, error) {
	r.mu.Lock()
	r.prompts++
	gate := r.gateName == member.Name
	if gate {
		r.gateName = ""
	}
	run := &gatedRun{
		done:     make(chan struct{}),
		onCancel: func() { r.canceled <- member.Name },
	}
	r.runs[member.Name] = run
	r.mu.Unlock()

	if gate {
		close(r.entered)
		<-r.release
	}
	return run, nil
}

func (r *gatedRunner) gate(name string) {
	r.mu.Lock()
	r.gateName = name
	r.mu.Unlock()
}

func (r *gatedRunner) finish(name string) {
	r.mu.Lock()
	run := r.runs[name]
	r.mu.Unlock()
	run.finish()
}

func (r *gatedRunner) promptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prompts
}

// TestSettlingRunRequeuesItsInboxDuringShutdown pins that a turn settling
// while the Team shuts down neither resumes its member nor loses the mail it
// drained: the message is pending again for a later enable to deliver.
func TestSettlingRunRequeuesItsInboxDuringShutdown(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	runner := newGatedRunner()
	service, err := New(path, Config{}, runner)
	require.NoError(t, err)

	ctx := context.Background()
	for _, name := range []string{"alpha", "beta"} {
		_, err := service.SpawnTeammate(ctx, "lead", SpawnRequest{
			Name: name, Description: "d", Prompt: "p", ParentID: "session-a",
		})
		require.NoError(t, err)
	}
	require.NoError(t, service.SendMessage(ctx, "lead", "session-a", SendRequest{
		Target: "alpha", Message: "keep going",
	}))
	require.Equal(t, 2, runner.promptCount())

	// alpha's next turn is the resume of its queued message. Hold it inside
	// the runner, so the shutdown lands between the inbox drain and the
	// resume starting for real.
	runner.gate("alpha")
	runner.finish("alpha")
	select {
	case <-runner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("alpha's resume never reached the runner")
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		service.Shutdown()
	}()
	// Shutdown marks the Team closed before it cancels the live runs, so
	// beta's cancellation proves the flag is set while alpha is held.
	select {
	case name := <-runner.canceled:
		require.Equal(t, "beta", name, "the live run, not the held resume, cancels first")
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown never canceled beta's run")
	}
	close(runner.release)
	select {
	case <-shutdownDone:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not return after the held resume was released")
	}

	state, err := LoadState(path)
	require.NoError(t, err)
	var queued, members int
	for _, message := range state.Messages {
		if message.To != "alpha" || message.Text != "keep going" {
			continue
		}
		queued++
		require.False(t, message.Delivered, "mail drained for a refused resume must be pending again")
	}
	require.Equal(t, 1, queued, "the queued message must still be in the durable state")
	for _, member := range state.Members {
		if member.Name != "alpha" && member.Name != "beta" {
			continue
		}
		members++
		require.Equal(t, StatusInactive, member.Status)
		require.False(t, member.Failed)
	}
	require.Equal(t, 2, members)
	require.Equal(t, 3, runner.promptCount(), "the refused resume is the last turn attempted")
}
