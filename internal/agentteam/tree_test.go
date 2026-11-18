package agentteam

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The tree is one Lead session's team. Another session's teammates live in
// the same workspace Team and share the Lead inbox, but are not counted.
func TestTreeReportsTheSessionsTeam(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	changed := service.Changed()

	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	select {
	case <-changed:
	default:
		t.Fatal("spawning a teammate did not signal a change")
	}
	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "other", Description: "d", Prompt: "p", ParentID: "session-b",
	})
	require.NoError(t, err)

	tree := service.Tree("session-a")
	require.Equal(t, []string{"session-fake"}, tree.Sessions)
	require.Equal(t, 1, tree.Active)
	require.Zero(t, tree.UnreadByLead)

	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		return service.Tree("session-a").Active == 0
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, 1, service.Tree("session-a").UnreadByLead, "the report waits for the Lead")
	require.Equal(t, 1, service.Tree("session-b").Active)
	require.Zero(t, service.Tree("session-b").UnreadByLead)

	service.DrainInbox("lead")
	require.Zero(t, service.Tree("session-a").UnreadByLead)

	runner.finish("other")
	require.Eventually(t, func() bool {
		return service.Tree("session-b").Active == 0
	}, time.Second, 10*time.Millisecond)
}

// A teammate with messages waiting is started again when its turn ends. It
// must read as active from the moment its inbox is taken, or a wait for the
// tree could end while it is being resumed. The completed-task hook runs in
// exactly that moment: after the inbox is drained, before the next turn.
func TestTreeCountsATeammateBeingResumed(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	task, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "Review", Description: "d"})
	require.NoError(t, err)
	_, err = service.UpdateTask("reviewer", UpdateTaskRequest{
		TaskID: task.ID, ExpectedRevision: task.Revision, Action: "claim",
	})
	require.NoError(t, err)
	require.NoError(t, service.SendMessage(context.Background(), "lead", "session-a", SendRequest{
		Target: "reviewer", Message: "Also check the docs.",
	}))
	require.Equal(t, 1, service.Tree("session-a").UnreadByTeammates)

	seen := make(chan TreeStatus, 1)
	service.SetTaskCompletedHook(func(string, string) {
		seen <- service.Tree("session-a")
	})

	runner.finish("reviewer")
	select {
	case tree := <-seen:
		require.Equal(t, 1, tree.Active, "a teammate being resumed is not idle")
		require.Zero(t, tree.UnreadByTeammates)
	case <-time.After(time.Second):
		t.Fatal("task completed hook was not called")
	}
	require.Eventually(t, func() bool {
		return runner.promptCount() == 2
	}, time.Second, 10*time.Millisecond)

	// Settle the resumed turn so no write outlives the test.
	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		return service.Tree("session-a").Active == 0
	}, time.Second, 10*time.Millisecond)
}

// Messages an earlier process left undelivered are never delivered by this
// one, so they must not hold a wait for the tree forever.
func TestTreeIgnoresMessagesAnEarlierProcessLeft(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	runner := newFakeRunner()
	earlier, err := New(path, Config{}, runner)
	require.NoError(t, err)
	_, err = earlier.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	require.NoError(t, earlier.SendMessage(context.Background(), "lead", "session-a", SendRequest{
		Target: "reviewer", Message: "Steer left.",
	}))
	require.Equal(t, 1, earlier.Tree("session-a").UnreadByTeammates)

	later, err := New(path, Config{}, newFakeRunner())
	require.NoError(t, err)
	tree := later.Tree("session-a")
	require.Zero(t, tree.Active)
	require.Zero(t, tree.UnreadByTeammates)

	// Settle the earlier process: the queued message resumes the teammate
	// once, then the resumed turn ends.
	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		return runner.promptCount() == 2
	}, time.Second, 10*time.Millisecond)
	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		return earlier.Tree("session-a").Active == 0
	}, time.Second, 10*time.Millisecond)
}
