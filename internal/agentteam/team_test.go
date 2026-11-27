package agentteam

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRun struct {
	done   chan struct{}
	output string
	err    error
	cancel func()
	// cancelled records that the service asked the run to stop.
	cancelled atomic.Bool
}

func (r *fakeRun) SessionID() string { return "session-fake" }
func (r *fakeRun) Done() <-chan struct{} {
	return r.done
}
func (r *fakeRun) Output() string { return r.output }
func (r *fakeRun) Err() error     { return r.err }
func (r *fakeRun) Cancel() {
	r.cancelled.Store(true)
	r.cancel()
}

type fakeRunner struct {
	mu      sync.Mutex
	prompts []string
	members []string
	runs    map[string]*fakeRun
	// turn, when set, is the work of every run: Cancel cancels the turn's
	// context and done closes only when the turn function returns, like a
	// real turn that has to return before it settles. Without it, Cancel
	// settles the run at once.
	turn func(ctx context.Context)
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{runs: make(map[string]*fakeRun)}
}

// setTurn makes every run a realistic turn the context cancels and only the
// turn function ends.
func (r *fakeRunner) setTurn(turn func(ctx context.Context)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turn = turn
}

// turnCancelled reports whether name's run was asked to stop.
func (r *fakeRunner) turnCancelled(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, ok := r.runs[name]
	return ok && run.cancelled.Load()
}

func (r *fakeRunner) Start(_ context.Context, member Member, prompt string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := &fakeRun{done: make(chan struct{})}
	if r.turn != nil {
		// The realistic mode: Cancel cancels the context, and the run
		// settles only once the turn returns.
		ctx, cancel := context.WithCancel(context.Background())
		run.cancel = cancel
		go func(turn func(context.Context)) {
			defer close(run.done)
			turn(ctx)
		}(r.turn)
	} else {
		run.cancel = func() { close(run.done) }
	}
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

func (r *fakeRunner) finishWithOutput(name, output string) {
	r.mu.Lock()
	run := r.runs[name]
	run.output = output
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

// TestSpawnTeammatePersistsRoleName pins that a spawn's role survives the
// roster: the member view names it and team.json stores it, so a resumed
// teammate is rebuilt in its role instead of unrestricted.
func TestSpawnTeammatePersistsRoleName(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	service, err := New(path, Config{}, newFakeRunner())
	require.NoError(t, err)

	view, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name:        "rev",
		Description: "Reviews the diff",
		Prompt:      "Review the current diff.",
		ParentID:    "session-a",
		RoleName:    "reviewer",
	})
	require.NoError(t, err)
	require.Equal(t, "reviewer", view.RoleName)

	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(saved), `"role_name": "reviewer"`)

	members, err := service.ListMembers("lead", "session-a")
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "reviewer", members[1].RoleName)
}

// TestSpawnTeammatePersistsModelOverride pins that a model override is
// durable: a reopened Team still knows the model and reasoning the Lead
// asked for, which is what lets every later turn rebuild it.
func TestSpawnTeammatePersistsModelOverride(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	service, err := New(path, Config{}, newFakeRunner())
	require.NoError(t, err)

	view, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name:          "reviewer",
		Description:   "Reviews the diff",
		Prompt:        "Review the current diff.",
		ParentID:      "session-a",
		Model:         "mock-reasoner",
		ModelProvider: "mock",
		Reasoning:     "high",
	})
	require.NoError(t, err)
	require.Equal(t, "mock-reasoner", view.Model)
	require.Equal(t, "mock", view.ModelProvider)
	require.Equal(t, "high", view.Reasoning)

	reopened, err := New(path, Config{}, nil)
	require.NoError(t, err)
	members, err := reopened.ListMembers("lead", "session-a")
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "mock-reasoner", members[1].Model)
	require.Equal(t, "mock", members[1].ModelProvider)
	require.Equal(t, "high", members[1].Reasoning)
}

// TestLoadTeamWithoutModelOverrideFields pins backward compatibility: a
// team.json written before the override fields existed still loads, and its
// members simply carry no override.
func TestLoadTeamWithoutModelOverrideFields(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	legacy := `{
  "version": 1,
  "team_id": "team-1",
  "members": [
    {"id": "member-1", "name": "reviewer", "role": "teammate",
     "description": "Reviews the diff", "context": "fresh",
     "parent_session_id": "session-a", "status": "inactive"}
  ],
  "next_member": 1
}`
	require.NoError(t, os.WriteFile(path, []byte(legacy), 0o644))

	service, err := New(path, Config{}, nil)
	require.NoError(t, err)
	members, err := service.ListMembers("lead", "session-a")
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Empty(t, members[1].Model)
	require.Empty(t, members[1].ModelProvider)
	require.Empty(t, members[1].Reasoning)
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

	pending := service.DrainInbox("lead", "session-a")
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

	pending := service.DrainInbox("reviewer", "session-a")
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
	pending := service.DrainInbox("lead", "session-a")
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

	pending := service.DrainInbox("lead", "session-a")
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

	pending := service.DrainInbox("lead", "session-a")
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

// reportPathIn returns the file a cut report names, so a test can read the
// rest of the text back.
func reportPathIn(t *testing.T, report string) string {
	t.Helper()
	const marker = "Full report: "
	start := strings.Index(report, marker)
	require.GreaterOrEqual(t, start, 0, "a cut report must name the file holding the rest")
	rest := report[start+len(marker):]
	end := strings.Index(rest, "]")
	require.GreaterOrEqual(t, end, 0, "the cut note must close its bracket")
	return rest[:end]
}

// settleTeammate runs one teammate turn that finishes with output and returns
// the report the Lead's inbox received.
func settleTeammate(t *testing.T, service *Service, runner *fakeRunner, output string) string {
	t.Helper()
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "reviewer", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	runner.finishWithOutput("reviewer", output)

	var pending []Message
	require.Eventually(t, func() bool {
		pending = service.DrainInbox("lead", "session-a")
		return len(pending) == 1
	}, time.Second, 10*time.Millisecond)
	return pending[0].Text
}

// TestSettledTeammateReportSpillsLongOutput pins that a report past the
// inline cap keeps the whole deliverable reachable: a teammate's final
// message is its work product, and cutting it with an ellipsis loses
// everything past 2,000 bytes.
func TestSettledTeammateReportSpillsLongOutput(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)

	long := strings.Repeat("r", 5000)
	report := settleTeammate(t, service, runner, long)

	require.Contains(t, report, "reviewer finished its turn.")
	require.Contains(t, report, long[:2000])
	require.Contains(t, report, "Report cut: 2000 of 5000 bytes shown")

	path := reportPathIn(t, report)
	require.Equal(t, filepath.Join(filepath.Dir(service.path), "reports"), filepath.Dir(path),
		"the file belongs in the report directory next to the team state")
	full, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, long, string(full))
}

// TestSettledTeammateReportKeepsShortOutputInline pins that the normal case is
// unchanged and writes nothing to disk.
func TestSettledTeammateReportKeepsShortOutputInline(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)

	short := strings.Repeat("s", 100)
	report := settleTeammate(t, service, runner, short)

	require.Equal(t, "reviewer finished its turn.\n\n"+short, report)
	_, statErr := os.Stat(filepath.Join(filepath.Dir(service.path), "reports"))
	require.True(t, os.IsNotExist(statErr), "a report under the cap must not write a file")
}

// TestRunReportWithoutFullReportCutsWithEllipsis pins the fallback: with no
// file to point at, a long report is cut exactly as it was before spilling
// existed, so a failed write never hides the head of the text.
func TestRunReportWithoutFullReportCutsWithEllipsis(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("b", 3000)

	report := runReport("reviewer", long, nil, nil, "")

	require.Equal(t, "reviewer finished its turn.\n\n"+long[:2000]+"…", report)
}

// TestSettledTeammateReportCountsTheBytesItActuallyKeeps pins the cut size on
// a multibyte report: the cut backs off to a rune boundary, so a note that
// printed the cap would overstate what the Lead can read inline.
func TestSettledTeammateReportCountsTheBytesItActuallyKeeps(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)

	// One ASCII byte then 3-byte runes puts a rune boundary at 1,999, so a
	// cut at the 2,000-byte cap keeps 1,999 bytes.
	long := "a" + strings.Repeat("€", 1000)
	report := settleTeammate(t, service, runner, long)

	require.Contains(t, report, "Report cut: 1999 of 3001 bytes shown")

	path := reportPathIn(t, report)
	full, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, long, string(full))
}

// TestSettledTeammateReportFallsBackWithoutATeamPath pins the in-memory Team:
// with no team path there is nowhere to write the full report, and the
// process's working directory is not ours to use, so the report is cut as
// before.
func TestSettledTeammateReportFallsBackWithoutATeamPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	runner := newFakeRunner()
	service, err := New("", Config{}, runner)
	require.NoError(t, err)

	long := strings.Repeat("r", 5000)
	report := settleTeammate(t, service, runner, long)

	require.True(t, strings.HasSuffix(report, "…"), "the report is cut as before")
	require.NotContains(t, report, "Full report")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing may be written when the Team has no path")
}

// TestSettledTeammateReportFallsBackWhenTheDirectoryIsUnusable pins the other
// half of the fallback: a write that fails still delivers the head of the
// report, with no note pointing at a file that is not there.
func TestSettledTeammateReportFallsBackWhenTheDirectoryIsUnusable(t *testing.T) {
	t.Parallel()
	service, runner := newTestService(t)

	// A regular file where the report directory belongs makes every write
	// under it fail.
	blocked := filepath.Join(filepath.Dir(service.path), "reports")
	require.NoError(t, os.WriteFile(blocked, []byte("in the way"), 0o600))

	long := strings.Repeat("r", 5000)
	report := settleTeammate(t, service, runner, long)

	require.Contains(t, report, long[:2000])
	require.True(t, strings.HasSuffix(report, "…"), "the cut must survive a failed write")
	require.NotContains(t, report, "Full report")
}

// TestWriteReportSweepsOldReports pins the age sweep: a report outlives the
// turn that wrote it so the Lead can read it later, which leaves age as the
// only bound on what accumulates.
func TestWriteReportSweepsOldReports(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)

	dir := filepath.Join(filepath.Dir(service.path), "reports")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	stale := filepath.Join(dir, "reviewer-stale.md")
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o600))
	expired := time.Now().Add(-reportRetention - time.Hour)
	require.NoError(t, os.Chtimes(stale, expired, expired))
	fresh := filepath.Join(dir, "reviewer-fresh.md")
	require.NoError(t, os.WriteFile(fresh, []byte("new"), 0o600))

	require.NotEmpty(t, service.writeReport("reviewer", strings.Repeat("x", 3000)))

	_, err := os.Stat(stale)
	require.True(t, os.IsNotExist(err), "a report past the retention window is swept")
	_, err = os.Stat(fresh)
	require.NoError(t, err, "a report inside the window is kept")
}

// TestPolicyDiscouragesFanOutWaves pins the cost rule: every teammate is a
// session that resends the whole prompt, so a wave of near-identical
// teammates for a small job multiplies that cost for no gain.
func TestPolicyDiscouragesFanOutWaves(t *testing.T) {
	t.Parallel()

	require.Contains(t, Policy, "One teammate per concrete, self-contained brief, never a wave.")
	require.Contains(t, Policy, "Do not spawn near-identical teammates for a small or routine job: do the work inline when it is small.")
	require.Contains(t, Policy, "Cap a fan-out at what the task actually needs.")
}

// summaryRun is a fakeRun that reports the session summary its fork prefix
// was rendered against.
type summaryRun struct {
	*fakeRun
	summary string
	sent    bool
}

func (r *summaryRun) ContextSummary() (string, bool) { return r.summary, r.sent }

type summaryRunner struct {
	summary string
	sent    bool
}

func (r *summaryRunner) Start(_ context.Context, _ Member, _ string) (Run, error) {
	run := &fakeRun{done: make(chan struct{})}
	var once sync.Once
	run.cancel = func() { once.Do(func() { close(run.done) }) }
	return &summaryRun{fakeRun: run, summary: r.summary, sent: r.sent}, nil
}

// TestLaunchRecordsTheContextSummary pins that the write that records a run
// also records the summary its fork prefix was sent against, and leaves it
// alone when the turn sent no prefix.
func TestLaunchRecordsTheContextSummary(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sent bool
		want string
	}{
		{"sent", true, "message-7"},
		{"not sent", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, &summaryRunner{summary: "message-7", sent: tc.sent})
			require.NoError(t, err)
			t.Cleanup(s.Shutdown)
			_, err = s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
				Name: "forked", Description: "d", Prompt: "p", Context: ContextFork, ParentID: "session-a",
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, memberByID(t, s, "forked").ContextSummary)
		})
	}
}

func TestContextSummaryIsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(Member{ID: "member-1", Name: "a"})
	require.NoError(t, err)
	require.NotContains(t, string(data), "context_summary")
}
