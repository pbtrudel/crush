package agentteam

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/lock"
)

// openSharedTeam opens one Service on a state file that another process may
// write too. Both Services exist before either writes, the way two processes
// that start together do.
func openSharedTeam(t *testing.T, path string, runner Runner) *Service {
	t.Helper()
	service, err := New(path, Config{}, runner)
	require.NoError(t, err)
	return service
}

// sharedState reads the durable record the way a CLI or the TUI does.
func sharedState(t *testing.T, path string) State {
	t.Helper()
	state, err := LoadState(path)
	require.NoError(t, err)
	return state
}

// memoryState reads the in-memory record under the service lock.
func memoryState(t *testing.T, service *Service) State {
	t.Helper()
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.state
}

// requireDistinctIDs fails on the duplicate ids two processes hand out when
// each counts from its own record.
func requireDistinctIDs(t *testing.T, ids []string) {
	t.Helper()
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		require.False(t, seen[id], "duplicate id %q", id)
		seen[id] = true
	}
}

func messageIDs(messages []Message) []string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}

func taskIDs(tasks []Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func memberIDs(members []Member) []string {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.ID)
	}
	return ids
}

// Two Crush processes share one workspace Team. Each Lead must keep the
// members, messages and ids the other wrote.
func TestTwoServicesKeepEachOthersMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	runnerA := newFakeRunner()
	runnerB := newFakeRunner()
	a := openSharedTeam(t, path, runnerA)
	b := openSharedTeam(t, path, runnerB)

	for _, spawn := range []struct {
		service *Service
		name    string
		session string
	}{
		{a, "worker-a", "session-a"},
		{b, "worker-b", "session-b"},
	} {
		_, err := spawn.service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
			Name: spawn.name, Description: "d", Prompt: "p", ParentID: spawn.session,
		})
		require.NoError(t, err)
	}
	require.NoError(t, a.SendMessage(context.Background(), "lead", "session-a", SendRequest{
		Target: "worker-a", Message: "Steer a.",
	}))
	require.NoError(t, b.SendMessage(context.Background(), "lead", "session-b", SendRequest{
		Target: "worker-b", Message: "Steer b.",
	}))
	// A running turn takes its mail at its next step, so the message is
	// delivered before the turn ends and does not resume it.
	require.Len(t, a.DrainInbox("worker-a", "session-a"), 1)
	require.Len(t, b.DrainInbox("worker-b", "session-b"), 1)

	runnerA.finish("worker-a")
	runnerB.finish("worker-b")
	require.Eventually(t, func() bool {
		return len(sharedState(t, path).Messages) == 4
	}, 2*time.Second, 10*time.Millisecond, "2 steers and 2 reports per turn")

	state := sharedState(t, path)
	require.Len(t, state.Members, 2, "each process kept the other's member")
	require.Len(t, state.Messages, 4, "each process kept the other's messages")
	requireDistinctIDs(t, messageIDs(state.Messages))
	requireDistinctIDs(t, memberIDs(state.Members))

	// A fresh process reads the whole team, and each Lead still sees only
	// its own session's teammate.
	fresh := openSharedTeam(t, path, nil)
	members, err := fresh.ListMembers("lead", "session-a")
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "worker-a", members[1].Target)
	members, err = fresh.ListMembers("lead", "session-b")
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "worker-b", members[1].Target)

	pending := a.DrainInbox("lead", "session-a")
	require.Len(t, pending, 1, "worker-a's report")
	require.Equal(t, "worker-a", pending[0].From, "another session's report reached this Lead")
}

// Task ids and revisions are shared, so the compare-and-set precondition
// must hold across processes.
func TestTwoServicesKeepEachOthersTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	a := openSharedTeam(t, path, nil)
	b := openSharedTeam(t, path, nil)

	for i := range 5 {
		_, err := a.CreateTask("lead", "session-a", CreateTaskRequest{
			Subject: fmt.Sprintf("A %d", i), Description: "d",
		})
		require.NoError(t, err)
		_, err = b.CreateTask("lead", "session-b", CreateTaskRequest{
			Subject: fmt.Sprintf("B %d", i), Description: "d",
		})
		require.NoError(t, err)
	}
	state := sharedState(t, path)
	require.Len(t, state.Tasks, 10, "each process kept the other's tasks")
	requireDistinctIDs(t, taskIDs(state.Tasks))

	created, err := a.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "Shared", Description: "d"})
	require.NoError(t, err)

	edited, err := b.UpdateTask("lead", UpdateTaskRequest{
		TaskID: created.ID, ExpectedRevision: 1, Action: "edit",
		Subject: "Edited by b", SessionID: "session-a",
	})
	require.NoError(t, err)
	require.Equal(t, 2, edited.Revision)

	_, err = a.UpdateTask("lead", UpdateTaskRequest{
		TaskID: created.ID, ExpectedRevision: 1, Action: "edit",
		Subject: "Stale edit by a", SessionID: "session-a",
	})
	require.Equal(t, CodeTaskStaleRevision, ErrorCode(err), "the revision b wrote must be current for a")

	view, err := a.GetTask("lead", "session-a", created.ID)
	require.NoError(t, err)
	require.Equal(t, 2, view.Revision)
	require.Equal(t, "Edited by b", view.Subject)
}

// Member ids name teammates across processes, so a second process must not
// renumber from its own record or reuse a name.
func TestTwoServicesKeepEachOthersMembers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	runnerA := newFakeRunner()
	runnerB := newFakeRunner()
	a := openSharedTeam(t, path, runnerA)
	b := openSharedTeam(t, path, runnerB)
	_, err := a.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "worker-a", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	_, err = b.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "worker-b", Description: "d", Prompt: "p", ParentID: "session-b",
	})
	require.NoError(t, err)

	state := sharedState(t, path)
	require.Len(t, state.Members, 2)
	requireDistinctIDs(t, memberIDs(state.Members))

	_, err = b.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "worker-a", Description: "d", Prompt: "p", ParentID: "session-b",
	})
	require.Equal(t, CodeInvalidMemberName, ErrorCode(err), "the name is taken in the shared Team")

	require.Equal(t, a.TeamID(), b.TeamID(), "both processes share one Team id")

	runnerA.finish("worker-a")
	runnerB.finish("worker-b")
	require.Eventually(t, func() bool {
		state := sharedState(t, path)
		return len(state.Messages) == 2
	}, 2*time.Second, 10*time.Millisecond)
}

// Two processes writing at once must lose nothing and hand out no id twice.
func TestSharedStateUnderConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	services := []*Service{openSharedTeam(t, path, nil), openSharedTeam(t, path, nil)}

	const (
		writers = 4
		ops     = 25
	)
	var (
		wg       sync.WaitGroup
		failures []error
		mu       sync.Mutex
	)
	for index, service := range services {
		session := fmt.Sprintf("session-%d", index)
		for writer := range writers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for op := range ops {
					var err error
					if op%2 == 0 {
						_, err = service.CreateTask("lead", session, CreateTaskRequest{
							Subject: fmt.Sprintf("task %d-%d", writer, op), Description: "d",
						})
					} else {
						err = service.SendMessage(context.Background(), "lead", session, SendRequest{
							Target: "lead", Message: fmt.Sprintf("note %d-%d", writer, op),
						})
					}
					if err != nil {
						mu.Lock()
						failures = append(failures, err)
						mu.Unlock()
					}
				}
			}()
		}
	}
	wg.Wait()
	require.Empty(t, failures)

	state := sharedState(t, path)
	require.Len(t, state.Tasks, 8*13, "each task write is one record")
	require.Len(t, state.Messages, 8*12, "each message write is one record")
	requireDistinctIDs(t, taskIDs(state.Tasks))
	requireDistinctIDs(t, messageIDs(state.Messages))
	require.Equal(t, len(state.Tasks), state.NextTask, "the counter matches the ids in use")
	require.Equal(t, len(state.Messages), state.NextMessage, "the counter matches the ids in use")
}

// A write waits for the file lock another process holds instead of
// overwriting what that process is writing.
func TestStateLockContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	service := openSharedTeam(t, path, nil)

	release, err := lock.File(context.Background(), lockPath(path))
	require.NoError(t, err)
	held := true
	defer func() {
		if held {
			release()
		}
	}()

	done := make(chan error, 1)
	go func() {
		_, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "One", Description: "d"})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the write did not wait for the file lock, returned %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	release()
	held = false
	require.NoError(t, <-done)
	require.Len(t, sharedState(t, path).Tasks, 1)
}

// A tool-facing write refuses rather than lose another process's update when
// the state stays locked. Nothing changed, so the caller may retry.
func TestStateLockTimeoutRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	service := openSharedTeam(t, path, nil)
	service.lockTimeout = 100 * time.Millisecond
	_, err := service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "One", Description: "d"})
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	release, err := lock.File(context.Background(), lockPath(path))
	require.NoError(t, err)
	_, err = service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "Two", Description: "d"})
	release()
	require.Equal(t, CodeStateBusy, ErrorCode(err))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "a refused write must not touch the file")
	require.Len(t, memoryState(t, service).Tasks, 1, "a refused write must not change memory")
	require.Equal(t, 1, memoryState(t, service).NextTask, "a refused write must not spend an id")
}

// Settling a run must land even when the state stays locked: the run already
// happened and the tree cannot be left running forever.
func TestStateLockTimeoutStillSettlesRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	runner := newFakeRunner()
	service := openSharedTeam(t, path, runner)
	service.lockTimeout = 100 * time.Millisecond
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "worker", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	release, err := lock.File(context.Background(), lockPath(path))
	require.NoError(t, err)
	runner.finish("worker")
	require.Eventually(t, func() bool {
		return service.Tree("session-a").Active == 0
	}, 2*time.Second, 10*time.Millisecond)
	release()

	require.Eventually(t, func() bool {
		return len(sharedState(t, path).Messages) == 1
	}, 2*time.Second, 10*time.Millisecond, "the report is durable")
}

// Shutdown stops this process's teammates only. Another process is still
// running its own.
func TestShutdownLeavesAnotherProcessesMembers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	runnerA := newFakeRunner()
	a := openSharedTeam(t, path, runnerA)
	_, err := a.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "worker-a", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	b := openSharedTeam(t, path, newFakeRunner())
	b.Shutdown()

	state := sharedState(t, path)
	require.Len(t, state.Members, 1)
	require.Equal(t, StatusRunning, state.Members[0].Status, "another process's member is still running")
	require.Equal(t, 1, a.Tree("session-a").Active)

	runnerA.finish("worker-a")
	require.Eventually(t, func() bool {
		return a.Tree("session-a").Active == 0
	}, 2*time.Second, 10*time.Millisecond)
}

// Another process may rewrite the file without this process's members, and
// they were started here, so they must come back running.
func TestReloadKeepsOwnedRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	runner := newFakeRunner()
	service := openSharedTeam(t, path, runner)
	_, err := service.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "worker", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)
	_, err = saveState(path, State{Version: 1, TeamID: "team-other"})
	require.NoError(t, err)

	_, err = service.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "One", Description: "d"})
	require.NoError(t, err)
	require.Equal(t, 1, service.Tree("session-a").Active, "the run is still this process's")

	runner.finish("worker")
	require.Eventually(t, func() bool {
		return service.Tree("session-a").Active == 0
	}, 2*time.Second, 10*time.Millisecond)
	state := sharedState(t, path)
	require.Len(t, state.Members, 1)
	require.Equal(t, "worker", state.Members[0].Name)
	require.Equal(t, StatusInactive, state.Members[0].Status)
	require.Eventually(t, func() bool {
		return len(sharedState(t, path).Messages) == 1
	}, 2*time.Second, 10*time.Millisecond, "the report is durable")
}

// Another session's work in the same workspace is neither a change to this
// session's wait nor to what its tree reports.
func TestUnrelatedTeamDoesNotWakeOrChangeTree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.json")
	runnerA := newFakeRunner()
	a := openSharedTeam(t, path, runnerA)
	b := openSharedTeam(t, path, newFakeRunner())
	_, err := a.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "worker-a", Description: "d", Prompt: "p", ParentID: "session-a",
	})
	require.NoError(t, err)

	changed := a.Changed()
	before := a.Tree("session-a")

	_, err = b.SpawnTeammate(context.Background(), "lead", SpawnRequest{
		Name: "worker-b", Description: "d", Prompt: "p", ParentID: "session-b",
	})
	require.NoError(t, err)
	_, err = b.CreateTask("lead", "session-b", CreateTaskRequest{Subject: "B", Description: "d"})
	require.NoError(t, err)
	require.NoError(t, b.SendMessage(context.Background(), "lead", "session-b", SendRequest{
		Target: "worker-b", Message: "Steer b.",
	}))
	select {
	case <-changed:
		t.Fatal("another session's write woke this session's waiter")
	default:
	}
	require.Equal(t, before, a.Tree("session-a"))

	_, err = a.CreateTask("lead", "session-a", CreateTaskRequest{Subject: "A", Description: "d"})
	require.NoError(t, err)
	select {
	case <-changed:
	default:
		t.Fatal("this session's write did not wake its waiter")
	}
	require.Equal(t, before, a.Tree("session-a"), "a task does not change the tree counts")

	runnerA.finish("worker-a")
	require.Eventually(t, func() bool {
		return a.Tree("session-a").Active == 0
	}, 2*time.Second, 10*time.Millisecond)
}
