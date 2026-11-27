package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
)

// leadWakeRun is one settled teammate turn with a known deliverable, so the
// report the Lead receives can be recognised in its transcript.
type leadWakeRun struct {
	sessionID  string
	output     string
	done       chan struct{}
	finishOnce sync.Once
}

func (r *leadWakeRun) SessionID() string     { return r.sessionID }
func (r *leadWakeRun) Done() <-chan struct{} { return r.done }
func (r *leadWakeRun) Output() string        { return r.output }
func (r *leadWakeRun) Err() error            { return nil }
func (r *leadWakeRun) Cancel()               { r.finishOnce.Do(func() { close(r.done) }) }

type leadWakeRunner struct {
	sessionID string
	output    string
	mu        sync.Mutex
	runs      []*leadWakeRun
}

func (r *leadWakeRunner) Start(_ context.Context, _ agentteam.Member, _ string) (agentteam.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := &leadWakeRun{sessionID: r.sessionID, output: r.output, done: make(chan struct{})}
	r.runs = append(r.runs, run)
	return run, nil
}

// TestTeammateReportWakesIdleLead pins that a teammate finishing its turn
// wakes an idle Lead so it can act on the report without the user prompting.
// Before the fix the report sat in the Lead's team inbox until a user prompt
// or a team tool call drained it, so the Lead appeared to stop working.
func TestTeammateReportWakesIdleLead(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	mate, err := coord.sessions.CreateTaskSession(t.Context(), "team-member-1-x", lead.ID, "Teammate worker")
	require.NoError(t, err)

	const deliverable = "worker-report-ABC123"
	runner := &leadWakeRunner{sessionID: mate.ID, output: deliverable}
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, runner)
	require.NoError(t, err)
	t.Cleanup(service.Shutdown)
	coord.setTeam(service)
	service.SetLeadNotifyHook(coord.wakeLeadForTeamMessage)

	_, err = service.SpawnTeammate(t.Context(), "lead", agentteam.SpawnRequest{
		Name: "worker", Description: "d", Prompt: "p", ParentID: lead.ID,
	})
	require.NoError(t, err)

	// The Lead is idle: nothing is running on its session.
	require.False(t, coord.currentAgent().HasPendingWork(lead.ID))

	runner.runs[0].finishOnce.Do(func() { close(runner.runs[0].done) })
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead", lead.ID)
		return err == nil && len(members) == 2 && members[1].Status == agentteam.StatusInactive
	}, 2*time.Second, 10*time.Millisecond)

	// The idle Lead must be woken with the report, not left waiting for a
	// user prompt. The wake turn persists the report as a hidden message.
	require.Eventually(t, func() bool {
		msgs, err := coord.messages.List(context.Background(), lead.ID)
		if err != nil {
			return false
		}
		for _, msg := range msgs {
			if strings.Contains(msg.Content().String(), deliverable) {
				return true
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond, "the idle Lead was never woken with the teammate report")
}

// TestTeammateMessageWakesIdleLead pins that a direct message a teammate sends
// the Lead also wakes an idle Lead, not only the automatic settle report.
func TestTeammateMessageWakesIdleLead(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	mate, err := coord.sessions.CreateTaskSession(t.Context(), "team-member-1-x", lead.ID, "Teammate worker")
	require.NoError(t, err)

	runner := &leadWakeRunner{sessionID: mate.ID, output: "done"}
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, runner)
	require.NoError(t, err)
	t.Cleanup(service.Shutdown)
	coord.setTeam(service)

	_, err = service.SpawnTeammate(t.Context(), "lead", agentteam.SpawnRequest{
		Name: "worker", Description: "d", Prompt: "p", ParentID: lead.ID,
	})
	require.NoError(t, err)
	runner.runs[0].finishOnce.Do(func() { close(runner.runs[0].done) })
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead", lead.ID)
		return err == nil && len(members) == 2 && members[1].Status == agentteam.StatusInactive
	}, 2*time.Second, 10*time.Millisecond)

	// Install the hook only after the settle, so the Lead is idle and the
	// direct-message wake is the only turn started on it.
	service.SetLeadNotifyHook(coord.wakeLeadForTeamMessage)

	const note = "worker-note-XYZ789"
	require.NoError(t, service.SendMessage(t.Context(), "worker", lead.ID, agentteam.SendRequest{
		Target: "lead", Message: note,
	}))

	require.Eventually(t, func() bool {
		msgs, err := coord.messages.List(context.Background(), lead.ID)
		if err != nil {
			return false
		}
		for _, msg := range msgs {
			if strings.Contains(msg.Content().String(), note) {
				return true
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond, "the idle Lead was never woken with the teammate message")
}
