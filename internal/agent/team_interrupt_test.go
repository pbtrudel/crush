package agent

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
)

// TestTeammateInterruptEscalatesItsTask pins the whole interrupt path on the
// real teamRun, not a fake: Interrupt cancels the run context, the aborted
// turn returns context.Canceled, and the settle escalates the role task at
// once instead of starting it again.
func TestTeammateInterruptEscalatesItsTask(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())

	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, nil)
	require.NoError(t, err)
	service.SetRunner(&teamRunner{c: coord, team: service})
	t.Cleanup(service.Shutdown)
	coord.setTeam(service)

	release := make(chan struct{})
	releaseTeammate := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseTeammate)

	leadModel := coord.mainModels().Large
	leadModel.CatwalkCfg.ContextWindow = 200_000
	teammateModel := leadModel
	teammateModel.Model = &gatedModel{finishStreamModel: finishStreamModel{text: "Report: all done."}, release: release}
	coord.models.Set(coordinatorModels{large: leadModel, subLarge: teammateModel, small: teammateModel})

	sess, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	_, err = service.CreateTask("lead", sess.ID, agentteam.CreateTaskRequest{
		Subject: "Research", Description: "Find the cause.", Role: "implementer",
	})
	require.NoError(t, err)

	running := func() bool {
		members, err := service.ListMembers("lead", sess.ID)
		if err != nil {
			return false
		}
		for _, member := range members {
			if member.Target == "implementer-1" {
				return member.Status == agentteam.StatusRunning
			}
		}
		return false
	}
	require.Eventually(t, running, 10*time.Second, 10*time.Millisecond, "the dispatcher starts the role task")

	status, err := service.Interrupt("lead", sess.ID, "implementer-1")
	require.NoError(t, err)
	require.Equal(t, agentteam.StatusRunning, status)

	view := func() agentteam.TaskView {
		list, err := service.ListTasks("lead", sess.ID, agentteam.TaskFilter{})
		require.NoError(t, err)
		require.Len(t, list.Tasks, 1)
		return list.Tasks[0]
	}
	require.Eventually(t, func() bool { return view().Escalated }, 10*time.Second, 10*time.Millisecond,
		"an interrupted turn escalates its task, so the task's run error must be context.Canceled")

	got := view()
	require.Zero(t, got.Attempts, "an interrupt is not charged as a failed attempt")
	require.Equal(t, agentteam.TaskPending, got.Status)

	var interrupted bool
	for _, message := range service.DrainInbox("lead", sess.ID) {
		if strings.Contains(message.Text, "was interrupted") {
			interrupted = true
		}
	}
	require.True(t, interrupted, "the Lead is told its task was interrupted")

	// The escalated task is not started again: the member runs no second turn.
	members, err := service.ListMembers("lead", sess.ID)
	require.NoError(t, err)
	require.Len(t, members, 2, "the Lead and its one teammate")
	require.Equal(t, agentteam.StatusInactive, members[1].Status)
}
