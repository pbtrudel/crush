package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
)

type noticeRun struct {
	sessionID string
	done      chan struct{}
}

func (r *noticeRun) SessionID() string     { return r.sessionID }
func (r *noticeRun) Done() <-chan struct{} { return r.done }
func (r *noticeRun) Output() string        { return "" }
func (r *noticeRun) Err() error            { return nil }
func (r *noticeRun) Cancel()               {}

// noticeRunner stands in for the teammate runner, which runs a turn on the
// teammate's own model.
type noticeRunner struct {
	sessionID string
	mu        sync.Mutex
	prompts   []string
	runs      []*noticeRun
}

func (r *noticeRunner) Start(_ context.Context, _ agentteam.Member, prompt string) (agentteam.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := &noticeRun{sessionID: r.sessionID, done: make(chan struct{})}
	r.prompts = append(r.prompts, prompt)
	r.runs = append(r.runs, run)
	return run, nil
}

func (r *noticeRunner) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.prompts...)
}

// TestBackgroundNoticeWakesTeammateOnItsOwnRun pins that a background job
// finishing in an idle teammate's session wakes that teammate through the
// Team runner. Waking it with the Lead's agent ran the teammate's session on
// the Lead's model, next to the teammate's own run.
func TestBackgroundNoticeWakesTeammateOnItsOwnRun(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	sess, err := coord.sessions.CreateTaskSession(t.Context(), "team-member-1-x", lead.ID, "Teammate reviewer")
	require.NoError(t, err)

	runner := &noticeRunner{sessionID: sess.ID}
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, runner)
	require.NoError(t, err)
	coord.team = service

	_, err = service.SpawnTeammate(t.Context(), "lead", agentteam.SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: lead.ID,
	})
	require.NoError(t, err)
	close(runner.runs[0].done)
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead", lead.ID)
		return err == nil && len(members) == 2 && members[1].Status == agentteam.StatusInactive
	}, 2*time.Second, 10*time.Millisecond)

	coord.deliverNotice(t.Context(), sess.ID, "<background_job_notice>job 1 finished</background_job_notice>")

	prompts := runner.snapshot()
	require.Len(t, prompts, 2, "the teammate must be woken on its own run")
	require.Contains(t, prompts[1], "job 1 finished")
	require.False(t, coord.currentAgent().IsSessionBusy(sess.ID), "the Lead's agent must not run a teammate session")
	require.Never(t, func() bool {
		msgs, err := coord.messages.List(context.Background(), sess.ID)
		return err == nil && len(msgs) > 0
	}, 300*time.Millisecond, 20*time.Millisecond, "the Lead's agent wrote a turn into the teammate session")
	close(runner.runs[1].done)
}
