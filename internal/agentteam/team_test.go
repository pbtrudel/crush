package agentteam

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRun struct {
	done   chan struct{}
	err    error
	cancel func()
}

func (r *fakeRun) SessionID() string { return "session-fake" }
func (r *fakeRun) Done() <-chan struct{} {
	return r.done
}
func (r *fakeRun) Output() string { return "" }
func (r *fakeRun) Err() error     { return r.err }
func (r *fakeRun) Cancel()        { r.cancel() }

type fakeRunner struct {
	mu      sync.Mutex
	prompts []string
	members []string
	runs    map[string]*fakeRun
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{runs: make(map[string]*fakeRun)}
}

func (r *fakeRunner) Start(_ context.Context, member Member, prompt string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := &fakeRun{done: make(chan struct{})}
	run.cancel = func() { close(run.done) }
	r.runs[member.Name] = run
	r.prompts = append(r.prompts, prompt)
	r.members = append(r.members, member.Name)
	return run, nil
}

func (r *fakeRunner) finish(name string) {
	r.mu.Lock()
	run := r.runs[name]
	r.mu.Unlock()
	run.cancel()
}

func (r *fakeRunner) finishWithErr(name string, err error) {
	r.mu.Lock()
	run := r.runs[name]
	run.err = err
	r.mu.Unlock()
	run.cancel()
}

func (r *fakeRunner) promptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prompts)
}

func newTestService(t *testing.T) (*Service, *fakeRunner) {
	t.Helper()
	runner := newFakeRunner()
	service, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, runner)
	require.NoError(t, err)
	return service, runner
}

func TestValidateMemberName(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateMemberName("reviewer"))
	require.NoError(t, ValidateMemberName("reviewer-2"))
	require.Error(t, ValidateMemberName("Reviewer"))
	require.Error(t, ValidateMemberName("lead"))
	require.Error(t, ValidateMemberName("-reviewer"))
}

func TestSpawnTeammate(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)

	view, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name:        "reviewer",
		Description: "Reviews the diff",
		Prompt:      "Review the current diff.",
		ParentID:    "session-a",
	})
	require.NoError(t, err)
	require.Equal(t, "reviewer", view.Target)
	require.Equal(t, RoleTeammate, view.Role)
	require.Equal(t, StatusRunning, view.Status)
	require.Equal(t, 1, runner.promptCount())

	members, err := service.ListMembers("lead", "session-a")
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "lead", members[0].Target)
	require.Equal(t, RoleLead, members[0].Role)

	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead", "session-a")
		return err == nil && members[1].Status == StatusInactive
	}, time.Second, 10*time.Millisecond)
}

func TestSpawnTeammateRejectsDuplicateName(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.Equal(t, CodeInvalidMemberName, ErrorCode(err))
}

func TestSpawnTeammateRequiresLead(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "reviewer", SpawnRequest{
		Name: "tester", Description: "d", Prompt: "p",
	})
	require.Equal(t, CodeLeadRequired, ErrorCode(err))
}

func TestMemberLimit(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	service, err := New(filepath.Join(t.TempDir(), "team.json"), Config{MaxMembers: 1}, runner)
	require.NoError(t, err)
	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "one", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "two", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.Equal(t, CodeMemberLimit, ErrorCode(err))
}

func TestSendMessageResumesInactiveTeammate(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead", "session-a")
		return err == nil && members[1].Status == StatusInactive
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, service.SendMessage(context.Background(), "lead", "session-a", SendRequest{
		Target: "reviewer", Message: "Check the tests.",
	}))
	require.Equal(t, 2, runner.promptCount())

	pending := service.DrainInbox("lead")
	require.Len(t, pending, 1)
	require.Contains(t, pending[0].Text, "reviewer finished its turn")
}

func TestSendMessageToRunningTeammateQueues(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	require.NoError(t, service.SendMessage(context.Background(), "lead", "session-a", SendRequest{
		Target: "reviewer", Message: "Steer left.",
	}))
	require.Equal(t, 1, runner.promptCount())

	pending := service.DrainInbox("reviewer")
	require.Len(t, pending, 1)
	require.Equal(t, "lead", pending[0].From)
	require.Equal(t, "Steer left.", pending[0].Text)
}

func TestSendMessageToLeadInbox(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	require.NoError(t, service.SendMessage(context.Background(), "reviewer", "session-a", SendRequest{
		Target: "lead", Message: "Done.",
	}))
	pending := service.DrainInbox("lead")
	require.Len(t, pending, 1)
	require.Equal(t, "reviewer", pending[0].From)
}

func TestInterruptTeammate(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	previous, err := service.Interrupt("lead", "session-a", "reviewer")
	require.NoError(t, err)
	require.Equal(t, StatusRunning, previous)

	// Wait for the settlement write before the test's temp dir is removed.
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead", "session-a")
		return err == nil && members[1].Status == StatusInactive
	}, time.Second, 10*time.Millisecond)

	_, err = service.Interrupt("reviewer", "session-a", "reviewer")
	require.Equal(t, CodeLeadRequired, ErrorCode(err))
}

func TestWaitForChange(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = service.SendMessage(context.Background(), "lead", "session-a", SendRequest{
			Target: "reviewer", Message: "ping",
		})
	}()
	result, err := service.WaitForChange(context.Background(), "lead", "session-a", time.Second)
	require.NoError(t, err)
	require.False(t, result.TimedOut)

	result, err = service.WaitForChange(context.Background(), "lead", "session-a", 20*time.Millisecond)
	require.NoError(t, err)
	require.True(t, result.TimedOut)
}

func TestTaskBoardLifecycle(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	task, err := service.CreateTask("lead", "session-a", CreateTaskRequest{
		Subject:     "Add rate limiting",
		Description: "Implement the middleware.",
		WriteScopes: []string{"./internal/middleware/", "internal/middleware"},
	})
	require.NoError(t, err)
	require.Equal(t, "task-1", task.ID)
	require.Equal(t, 1, task.Revision)
	require.True(t, task.Ready)
	require.Equal(t, []string{"internal/middleware"}, task.WriteScopes)

	claimed, err := service.UpdateTask("reviewer", UpdateTaskRequest{
		TaskID: "task-1", ExpectedRevision: 1, Action: "claim",
	})
	require.NoError(t, err)
	require.Equal(t, TaskInProgress, claimed.Status)
	require.Equal(t, "reviewer", claimed.OwnerName)
	require.Equal(t, 2, claimed.Revision)

	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		SessionID: "session-a",
		TaskID:    "task-1", ExpectedRevision: 1, Action: "complete",
	})
	require.Equal(t, CodeTaskStaleRevision, ErrorCode(err))

	completed, err := service.UpdateTask("reviewer", UpdateTaskRequest{
		TaskID: "task-1", ExpectedRevision: 2, Action: "complete",
	})
	require.NoError(t, err)
	require.Equal(t, TaskCompleted, completed.Status)

	reopened, err := service.UpdateTask("reviewer", UpdateTaskRequest{
		TaskID: "task-1", ExpectedRevision: 3, Action: "reopen",
	})
	require.NoError(t, err)
	require.Equal(t, TaskPending, reopened.Status)
	require.Empty(t, reopened.OwnerName)
}

func TestTaskReadinessAndDependencies(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	first, err := service.CreateTask("lead", "session-a", CreateTaskRequest{
		Subject: "First", Description: "d",
	})
	require.NoError(t, err)
	second, err := service.CreateTask("lead", "session-a", CreateTaskRequest{
		Subject: "Second", Description: "d", BlockedBy: []string{first.ID},
	})
	require.NoError(t, err)
	require.False(t, second.Ready)

	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		SessionID: "session-a",
		TaskID:    second.ID, ExpectedRevision: 1, Action: "claim",
	})
	require.Equal(t, CodeTaskBlocked, ErrorCode(err))

	_, err = service.CreateTask("lead", "session-a", CreateTaskRequest{
		Subject: "Cycle", Description: "d", BlockedBy: []string{"task-1"},
	})
	require.NoError(t, err)
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		SessionID: "session-a",
		TaskID:    "task-1", ExpectedRevision: 1, Action: "set_dependencies",
		BlockedBy: []string{"task-3"},
	})
	require.Equal(t, CodeTaskDependencyCycle, ErrorCode(err))
}

func TestTaskWriteScopeWarnings(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.CreateTask("lead", "session-a", CreateTaskRequest{
		Subject: "One", Description: "d", WriteScopes: []string{"internal/ui"},
	})
	require.NoError(t, err)
	_, err = service.CreateTask("lead", "session-a", CreateTaskRequest{
		Subject: "Two", Description: "d", WriteScopes: []string{"internal/ui/chat"},
	})
	require.NoError(t, err)
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		SessionID: "session-a",
		TaskID:    "task-1", ExpectedRevision: 1, Action: "claim",
	})
	require.NoError(t, err)
	view, err := service.GetTask("lead", "session-a", "task-2")
	require.NoError(t, err)
	require.Equal(t, []string{"write scopes overlap with task-1"}, view.WriteScopeWarnings)
}

func TestDeleteTaskWithDependents(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "One", Description: "d"})
	require.NoError(t, err)
	_, err = service.CreateTask("lead", "session-a", CreateTaskRequest{
		Subject: "Two", Description: "d", BlockedBy: []string{"task-1"},
	})
	require.NoError(t, err)
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		SessionID: "session-a",
		TaskID:    "task-1", ExpectedRevision: 1, Action: "delete",
	})
	require.Equal(t, CodeTaskHasDependents, ErrorCode(err))
}

func TestListTasksFilters(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "One", Description: "d"})
	require.NoError(t, err)
	_, err = service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "Two", Description: "d"})
	require.NoError(t, err)
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		SessionID: "session-a",
		TaskID:    "task-1", ExpectedRevision: 1, Action: "claim",
	})
	require.NoError(t, err)

	status := TaskInProgress
	list, err := service.ListTasks("lead", "session-a", TaskFilter{Status: &status})
	require.NoError(t, err)
	require.Len(t, list.Tasks, 1)
	require.Equal(t, "task-1", list.Tasks[0].ID)

	list, err = service.ListTasks("lead", "session-a", TaskFilter{Owner: "unowned"})
	require.NoError(t, err)
	require.Len(t, list.Tasks, 1)
	require.Equal(t, "task-2", list.Tasks[0].ID)

	list, err = service.ListTasks("lead", "session-a", TaskFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, list.Tasks, 1)
	require.Equal(t, 1, list.NextCursor)
}

func TestStatePersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	service, err := New(path, Config{}, newFakeRunner())
	require.NoError(t, err)
	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	_, err = service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "One", Description: "d"})
	require.NoError(t, err)

	reopened, err := New(path, Config{}, nil)
	require.NoError(t, err)
	members, err := reopened.ListMembers("lead", "session-a")
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, StatusInactive, members[1].Status)
	task, err := reopened.GetTask("lead", "session-a", "task-1")
	require.NoError(t, err)
	require.Equal(t, "One", task.Subject)
}

func TestWatchSettlesTasksAndReportsToLead(t *testing.T) {
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

	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		got, err := service.GetTask("lead", "session-a", task.ID)
		return err == nil && got.Status == TaskCompleted
	}, time.Second, 10*time.Millisecond)

	pending := service.DrainInbox("lead")
	require.Len(t, pending, 1)
	require.Contains(t, pending[0].Text, "reviewer finished its turn")
	require.Contains(t, pending[0].Text, task.ID+" completed")
}

func TestWatchReleasesTasksOnFailure(t *testing.T) {
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

	runner.finishWithErr("reviewer", errors.New("boom"))
	require.Eventually(t, func() bool {
		got, err := service.GetTask("lead", "session-a", task.ID)
		return err == nil && got.Status == TaskPending && got.OwnerName == ""
	}, time.Second, 10*time.Millisecond)

	pending := service.DrainInbox("lead")
	require.Len(t, pending, 1)
	require.Contains(t, pending[0].Text, "reviewer failed")
	require.Contains(t, pending[0].Text, task.ID+" released")
}

func TestWaitResultIncludesProgress(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	task, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "Review", Description: "d"})
	require.NoError(t, err)

	result, err := service.WaitForChange(context.Background(), "lead", "session-a", 10*time.Millisecond)
	require.NoError(t, err)
	require.True(t, result.TimedOut)
	require.Len(t, result.Progress.Members, 2)
	require.Len(t, result.Progress.Tasks, 1)
	require.Equal(t, task.ID, result.Progress.Tasks[0].ID)
}

func TestSettledTaskCallsCompletedHook(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	task, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "Review the diff", Description: "d"})
	require.NoError(t, err)
	_, err = service.UpdateTask("reviewer", UpdateTaskRequest{
		TaskID: task.ID, ExpectedRevision: task.Revision, Action: "claim",
	})
	require.NoError(t, err)

	type completed struct{ sessionID, subject string }
	got := make(chan completed, 1)
	service.SetTaskCompletedHook(func(sessionID, subject string) {
		got <- completed{sessionID: sessionID, subject: subject}
	})

	runner.finish("reviewer")
	select {
	case value := <-got:
		require.Equal(t, "session-a", value.sessionID)
		require.Equal(t, "Review the diff", value.subject)
	case <-time.After(time.Second):
		t.Fatal("task completed hook was not called")
	}
}

func TestUpdateTaskCompleteCallsCompletedHook(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	task, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "Review the diff", Description: "d"})
	require.NoError(t, err)
	claimed, err := service.UpdateTask("reviewer", UpdateTaskRequest{
		TaskID: task.ID, ExpectedRevision: task.Revision, Action: "claim",
	})
	require.NoError(t, err)

	got := make(chan string, 1)
	service.SetTaskCompletedHook(func(sessionID, subject string) {
		got <- sessionID + "|" + subject
	})
	_, err = service.UpdateTask("reviewer", UpdateTaskRequest{
		TaskID: claimed.ID, ExpectedRevision: claimed.Revision, Action: "complete",
	})
	require.NoError(t, err)
	select {
	case value := <-got:
		require.Equal(t, "session-a|Review the diff", value)
	case <-time.After(time.Second):
		t.Fatal("task completed hook was not called")
	}
}

func TestUpdateTaskCompleteUsesCallerSessionForLead(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	task, err := service.CreateTask("lead", "lead-session", CreateTaskRequest{Subject: "Review the diff", Description: "d"})
	require.NoError(t, err)
	claimed, err := service.UpdateTask("lead", UpdateTaskRequest{
		SessionID: "lead-session",
		TaskID:    task.ID, ExpectedRevision: task.Revision, Action: "claim",
	})
	require.NoError(t, err)

	got := make(chan string, 1)
	service.SetTaskCompletedHook(func(sessionID, subject string) {
		got <- sessionID + "|" + subject
	})
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		SessionID: "lead-session",
		TaskID:    claimed.ID, ExpectedRevision: claimed.Revision, Action: "complete",
	})
	require.NoError(t, err)
	select {
	case value := <-got:
		require.Equal(t, "lead-session|Review the diff", value)
	case <-time.After(time.Second):
		t.Fatal("task completed hook was not called")
	}
}

// TestIsLead pins the caller check the Lead-only tools gate on. A context
// with no member identity is the Lead's own run, which is what every
// non-Team caller carries.
func TestIsLead(t *testing.T) {
	t.Parallel()
	require.True(t, IsLead(context.Background()), "a context with no member is the Lead")
	require.True(t, IsLead(WithMember(context.Background(), "lead")))
	require.False(t, IsLead(WithMember(context.Background(), "reviewer")))
}

func TestSpawnTeammateCountsOnlyLiveMembers(t *testing.T) {
	t.Parallel()
	runner := newFakeRunner()
	service, err := New(filepath.Join(t.TempDir(), "team.json"), Config{MaxMembers: 1}, runner)
	require.NoError(t, err)

	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "one", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "two", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.Equal(t, CodeMemberLimit, ErrorCode(err), "a live member must still count against the limit")

	runner.finish("one")
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead", "session-a")
		return err == nil && members[1].Status == StatusInactive
	}, time.Second, 10*time.Millisecond)

	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "two", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err, "an inactive member must not consume a live slot")
}

// TestTeamStateIsScopedToItsSession pins the session boundary: a second
// session must not see, message, or interrupt another session's team.
func TestTeamStateIsScopedToItsSession(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)

	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	task, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "Review", Description: "d"})
	require.NoError(t, err)

	members, err := service.ListMembers("lead", "session-b")
	require.NoError(t, err)
	require.Len(t, members, 1, "a new session must not see another session's teammates")
	require.Equal(t, "lead", members[0].Target)

	list, err := service.ListTasks("lead", "session-b", TaskFilter{})
	require.NoError(t, err)
	require.Empty(t, list.Tasks, "a new session must not see another session's tasks")

	progress := service.Progress("lead", "session-b")
	require.Len(t, progress.Members, 1, "a new session's roster is just its own Lead")
	require.Empty(t, progress.Tasks)
	require.False(t, service.HasActivePeer("lead", "session-b"))

	_, err = service.GetTask("lead", "session-b", task.ID)
	require.Equal(t, CodeTaskNotFound, ErrorCode(err))
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		SessionID: "session-b", TaskID: task.ID, ExpectedRevision: task.Revision, Action: "claim",
	})
	require.Equal(t, CodeTaskNotFound, ErrorCode(err))
	require.Equal(t, CodeMemberNotFound, ErrorCode(service.SendMessage(context.Background(), "lead", "session-b", SendRequest{
		Target: "reviewer", Message: "hi",
	})))
	_, err = service.Interrupt("lead", "session-b", "reviewer")
	require.Equal(t, CodeMemberNotFound, ErrorCode(err))

	members, err = service.ListMembers("lead", "session-a")
	require.NoError(t, err)
	require.Len(t, members, 2, "the owning session still sees its team")
	list, err = service.ListTasks("lead", "session-a", TaskFilter{})
	require.NoError(t, err)
	require.Len(t, list.Tasks, 1)
}

// TestScopeLockedFollowsNestedTeammates pins the nested traversal: a teammate
// spawned by a teammate belongs to the same session tree, and a sibling
// session stays out of it.
func TestScopeLockedFollowsNestedTeammates(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	service.state.Members = []Member{
		{ID: "member-1", Name: "one", ParentID: "session-a", SessionID: "team-one"},
		{ID: "member-2", Name: "two", ParentID: "team-one", SessionID: "team-two"},
		{ID: "member-3", Name: "three", ParentID: "session-b", SessionID: "team-three"},
	}

	scope := service.scopeLocked("lead", "session-a")

	require.True(t, scope["session-a"])
	require.True(t, scope["team-one"])
	require.True(t, scope["team-two"], "a teammate spawned by a teammate stays in the tree")
	require.False(t, scope["session-b"])
	require.Nil(t, service.scopeLocked("lead", ""), "a call with no session sees nothing")

	other := service.scopeLocked("three", "session-a")
	require.True(t, other["session-b"], "a teammate is scoped to the session that spawned it")
	require.False(t, other["session-a"])
}

// TestLegacyTasksStayHidden pins that a task written before session scoping
// never appears on a session's board.
func TestLegacyTasksStayHidden(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	service.state.Tasks = []Task{{ID: "task-1", Revision: 1, Subject: "Old", Status: TaskPending}}

	list, err := service.ListTasks("lead", "session-a", TaskFilter{})
	require.NoError(t, err)
	require.Empty(t, list.Tasks)

	_, err = service.GetTask("lead", "session-a", "task-1")
	require.Equal(t, CodeTaskNotFound, ErrorCode(err))
}
