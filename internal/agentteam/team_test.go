package agentteam

import (
	"context"
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
func (r *fakeRun) Err() error { return r.err }
func (r *fakeRun) Cancel()    { r.cancel() }

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
	})
	require.NoError(t, err)
	require.Equal(t, "reviewer", view.Target)
	require.Equal(t, RoleTeammate, view.Role)
	require.Equal(t, StatusRunning, view.Status)
	require.Equal(t, 1, runner.promptCount())

	members, err := service.ListMembers("lead")
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "lead", members[0].Target)
	require.Equal(t, RoleLead, members[0].Role)

	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead")
		return err == nil && members[1].Status == StatusInactive
	}, time.Second, 10*time.Millisecond)
}

func TestSpawnTeammateRejectsDuplicateName(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p",
	})
	require.NoError(t, err)
	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p",
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
		Name: "one", Description: "d", Prompt: "p",
	})
	require.NoError(t, err)
	_, err = service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "two", Description: "d", Prompt: "p",
	})
	require.Equal(t, CodeMemberLimit, ErrorCode(err))
}

func TestSendMessageResumesInactiveTeammate(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p",
	})
	require.NoError(t, err)
	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead")
		return err == nil && members[1].Status == StatusInactive
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, service.SendMessage(context.Background(), "lead", SendRequest{
		Target: "reviewer", Message: "Check the tests.",
	}))
	require.Equal(t, 2, runner.promptCount())

	pending := service.DrainInbox("lead")
	require.Empty(t, pending)
}

func TestSendMessageToRunningTeammateQueues(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p",
	})
	require.NoError(t, err)

	require.NoError(t, service.SendMessage(context.Background(), "lead", SendRequest{
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
		Name: "reviewer", Description: "d", Prompt: "p",
	})
	require.NoError(t, err)
	require.NoError(t, service.SendMessage(context.Background(), "reviewer", SendRequest{
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
		Name: "reviewer", Description: "d", Prompt: "p",
	})
	require.NoError(t, err)

	previous, err := service.Interrupt("lead", "reviewer")
	require.NoError(t, err)
	require.Equal(t, StatusRunning, previous)

	// Wait for the settlement write before the test's temp dir is removed.
	require.Eventually(t, func() bool {
		members, err := service.ListMembers("lead")
		return err == nil && members[1].Status == StatusInactive
	}, time.Second, 10*time.Millisecond)

	_, err = service.Interrupt("reviewer", "reviewer")
	require.Equal(t, CodeLeadRequired, ErrorCode(err))
}

func TestWaitForChange(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p",
	})
	require.NoError(t, err)

	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = service.SendMessage(context.Background(), "lead", SendRequest{
			Target: "reviewer", Message: "ping",
		})
	}()
	result, err := service.WaitForChange(context.Background(), "lead", time.Second)
	require.NoError(t, err)
	require.False(t, result.TimedOut)

	result, err = service.WaitForChange(context.Background(), "lead", 20*time.Millisecond)
	require.NoError(t, err)
	require.True(t, result.TimedOut)
}

func TestTaskBoardLifecycle(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p",
	})
	require.NoError(t, err)

	task, err := service.CreateTask("lead", CreateTaskRequest{
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
		TaskID: "task-1", ExpectedRevision: 1, Action: "complete",
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
	first, err := service.CreateTask("lead", CreateTaskRequest{
		Subject: "First", Description: "d",
	})
	require.NoError(t, err)
	second, err := service.CreateTask("lead", CreateTaskRequest{
		Subject: "Second", Description: "d", BlockedBy: []string{first.ID},
	})
	require.NoError(t, err)
	require.False(t, second.Ready)

	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		TaskID: second.ID, ExpectedRevision: 1, Action: "claim",
	})
	require.Equal(t, CodeTaskBlocked, ErrorCode(err))

	_, err = service.CreateTask("lead", CreateTaskRequest{
		Subject: "Cycle", Description: "d", BlockedBy: []string{"task-1"},
	})
	require.NoError(t, err)
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		TaskID: "task-1", ExpectedRevision: 1, Action: "set_dependencies",
		BlockedBy: []string{"task-3"},
	})
	require.Equal(t, CodeTaskDependencyCycle, ErrorCode(err))
}

func TestTaskWriteScopeWarnings(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.CreateTask("lead", CreateTaskRequest{
		Subject: "One", Description: "d", WriteScopes: []string{"internal/ui"},
	})
	require.NoError(t, err)
	_, err = service.CreateTask("lead", CreateTaskRequest{
		Subject: "Two", Description: "d", WriteScopes: []string{"internal/ui/chat"},
	})
	require.NoError(t, err)
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		TaskID: "task-1", ExpectedRevision: 1, Action: "claim",
	})
	require.NoError(t, err)
	view, err := service.GetTask("lead", "task-2")
	require.NoError(t, err)
	require.Equal(t, []string{"write scopes overlap with task-1"}, view.WriteScopeWarnings)
}

func TestDeleteTaskWithDependents(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.CreateTask("lead", CreateTaskRequest{Subject: "One", Description: "d"})
	require.NoError(t, err)
	_, err = service.CreateTask("lead", CreateTaskRequest{
		Subject: "Two", Description: "d", BlockedBy: []string{"task-1"},
	})
	require.NoError(t, err)
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		TaskID: "task-1", ExpectedRevision: 1, Action: "delete",
	})
	require.Equal(t, CodeTaskHasDependents, ErrorCode(err))
}

func TestListTasksFilters(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	_, err := service.CreateTask("lead", CreateTaskRequest{Subject: "One", Description: "d"})
	require.NoError(t, err)
	_, err = service.CreateTask("lead", CreateTaskRequest{Subject: "Two", Description: "d"})
	require.NoError(t, err)
	_, err = service.UpdateTask("lead", UpdateTaskRequest{
		TaskID: "task-1", ExpectedRevision: 1, Action: "claim",
	})
	require.NoError(t, err)

	status := TaskInProgress
	list, err := service.ListTasks("lead", TaskFilter{Status: &status})
	require.NoError(t, err)
	require.Len(t, list.Tasks, 1)
	require.Equal(t, "task-1", list.Tasks[0].ID)

	list, err = service.ListTasks("lead", TaskFilter{Owner: "unowned"})
	require.NoError(t, err)
	require.Len(t, list.Tasks, 1)
	require.Equal(t, "task-2", list.Tasks[0].ID)

	list, err = service.ListTasks("lead", TaskFilter{Limit: 1})
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
		Name: "reviewer", Description: "d", Prompt: "p",
	})
	require.NoError(t, err)
	_, err = service.CreateTask("lead", CreateTaskRequest{Subject: "One", Description: "d"})
	require.NoError(t, err)

	reopened, err := New(path, Config{}, nil)
	require.NoError(t, err)
	members, err := reopened.ListMembers("lead")
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, StatusInactive, members[1].Status)
	task, err := reopened.GetTask("lead", "task-1")
	require.NoError(t, err)
	require.Equal(t, "One", task.Subject)
}
