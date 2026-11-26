package agentteam

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newPipelineService(t *testing.T, cfg Config) (*Service, *fakeRunner) {
	t.Helper()
	runner := newFakeRunner()
	service, err := New(filepath.Join(t.TempDir(), "team.json"), cfg, runner)
	require.NoError(t, err)
	// A live run outlives the test, and its settle writes into the temp
	// directory the test cleanup is about to remove.
	t.Cleanup(service.Shutdown)
	return service, runner
}

func (r *fakeRunner) memberNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.members...)
}

func (r *fakeRunner) lastPrompt() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.prompts) == 0 {
		return ""
	}
	return r.prompts[len(r.prompts)-1]
}

func memberStatus(s *Service, name string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked(name)
}

func memberByID(t *testing.T, s *Service, name string) Member {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	member, ok := s.memberLocked(name)
	require.True(t, ok, "member %s", name)
	return member
}

func memberID(t *testing.T, s *Service, name string) string {
	t.Helper()
	return memberByID(t, s, name).ID
}

// leadNotices returns the runtime notices written for the Lead of sessionID.
func leadNotices(t *testing.T, s *Service, sessionID string) []string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var texts []string
	for _, message := range s.state.Messages {
		if message.From == noticeSender && message.To == leadName && message.SessionID == sessionID {
			texts = append(texts, message.Text)
		}
	}
	return texts
}

// refusingRunner fails every start, the way a model the config no longer
// serves does.
type refusingRunner struct {
	mu     sync.Mutex
	starts []string
}

func (r *refusingRunner) Start(_ context.Context, member Member, _ string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, member.Name)
	return nil, errors.New("provider unavailable")
}

func (r *refusingRunner) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.starts...)
}

func TestDispatchSpawnsAMemberForARoleTask(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "researcher-1", Description: "Busy", Prompt: "Work.", RoleName: "researcher", ParentID: pipelineSession,
	})
	require.NoError(t, err)

	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "Find the cause.", Role: "researcher", WriteScopes: []string{"docs"}})
	require.Equal(t, []string{"researcher-1", "researcher-2"}, runner.memberNames(),
		"a busy member of the role is not reused; the lowest unused name is")

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, memberID(t, s, "researcher-2"), got.OwnerID)
	require.Equal(t, 2, got.Revision)

	spawned := memberByID(t, s, "researcher-2")
	require.Equal(t, "researcher", spawned.RoleName)
	require.Equal(t, "Auto-started for task "+task.ID, spawned.Description)
	require.Equal(t, pipelineSession, spawned.ParentID)
	require.Equal(t, fmt.Sprintf("Task %s on the shared board was claimed for you: Research\n\nFind the cause.\n\n"+
		"Write scopes: docs\nComplete it with team_task_update (action \"complete\", expected_revision 2) when the acceptance criteria are met.",
		task.ID), runner.lastPrompt())
}

func TestDispatchReusesAnIdleMemberOfTheRole(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "rev", Description: "Researcher", Prompt: "Wait.", RoleName: "researcher", ParentID: pipelineSession,
	})
	require.NoError(t, err)
	runner.finish("rev")
	require.Eventually(t, func() bool { return memberStatus(s, "rev") == StatusInactive }, time.Second, 10*time.Millisecond)

	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Equal(t, []string{"rev", "rev"}, runner.memberNames())
	require.Equal(t, memberID(t, s, "rev"), taskByID(t, s, task.ID).OwnerID)
	require.Contains(t, runner.lastPrompt(), "Write scopes: none")
}

func TestDispatchNeverPicksAnotherSessionsMember(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "other", Description: "Other Lead's", Prompt: "Wait.", RoleName: "researcher", ParentID: "session-b",
	})
	require.NoError(t, err)
	runner.finish("other")
	require.Eventually(t, func() bool { return memberStatus(s, "other") == StatusInactive }, time.Second, 10*time.Millisecond)

	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Equal(t, memberID(t, s, "researcher-1"), taskByID(t, s, task.ID).OwnerID)
	require.Equal(t, []string{"other", "researcher-1"}, runner.memberNames())
}

func TestDispatchLeavesRoleLessTasksAlone(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Manual", Description: "d"})
	require.Empty(t, runner.memberNames())
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Empty(t, got.OwnerID)
}

func TestDispatchRespectsTheMemberCap(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{MaxMembers: 1})
	first := createTask(t, s, CreateTaskRequest{Subject: "One", Description: "d", Role: "researcher"})
	second := createTask(t, s, CreateTaskRequest{Subject: "Two", Description: "d", Role: "researcher"})
	require.Equal(t, []string{"researcher-1"}, runner.memberNames())
	require.Equal(t, TaskPending, taskByID(t, s, second.ID).Status, "at the cap the task waits")

	act(t, s, "researcher-1", first.ID, "complete", nil)
	runner.finish("researcher-1")
	require.Eventually(t, func() bool {
		return taskByID(t, s, second.ID).Status == TaskInProgress
	}, time.Second, 10*time.Millisecond, "a later pass starts the waiting task")
	// The claim lands before the start, so the start is observed on its own.
	require.Eventually(t, func() bool {
		return len(runner.memberNames()) == 2
	}, time.Second, 10*time.Millisecond, "the idle member of the role is reused")
	require.Equal(t, []string{"researcher-1", "researcher-1"}, runner.memberNames())
}

func TestDispatchStartsATaskOnce(t *testing.T) {
	t.Parallel()
	var on atomic.Bool
	s, runner := newPipelineService(t, Config{Autostart: on.Load})
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Empty(t, runner.memberNames(), "autostart off starts nothing")

	on.Store(true)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { s.dispatch(context.Background(), pipelineSession) })
	}
	wg.Wait()
	require.Equal(t, []string{"researcher-1"}, runner.memberNames())
	require.Equal(t, TaskInProgress, taskByID(t, s, task.ID).Status)
}

func TestDispatchOptionOff(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Empty(t, runner.memberNames())
	require.Equal(t, TaskPending, taskByID(t, s, task.ID).Status)
}

func TestDispatchReportsARemovedRoleOnce(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{RoleExists: func(name string) bool { return name != "gone" }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Old", Description: "d", Role: "gone"})
	createTask(t, s, CreateTaskRequest{Subject: "Other", Description: "d"})
	s.dispatch(context.Background(), pipelineSession)

	require.Empty(t, runner.memberNames())
	notes := leadNotices(t, s, pipelineSession)
	require.Equal(t, []string{fmt.Sprintf(
		"Task %s (Old) names role \"gone\", which no longer exists. It is skipped; edit its role to continue.", task.ID,
	)}, notes)
}

// TestDispatchReportsARemovedRoleAgainAfterAnEdit pins that the once-only
// flag follows the role: a task edited to another missing role is reported
// again.
func TestDispatchReportsARemovedRoleAgainAfterAnEdit(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{RoleExists: func(name string) bool { return name == "researcher" }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Old", Description: "d", Role: "gone"})
	require.Len(t, leadNotices(t, s, pipelineSession), 1)

	role := "also-gone"
	act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Role = &role })

	require.Empty(t, runner.memberNames())
	notes := leadNotices(t, s, pipelineSession)
	require.Len(t, notes, 2, "the new role is reported too")
	require.Contains(t, notes[1], `names role "also-gone"`)
}

// TestDispatchEscalatesAfterThreeFailedStarts pins the whole retry chain: one
// task, three failed starts, and the escalation, with no pass driven by hand.
// The task starts again from dispatchFailed, so this terminates only because
// a failed member is never reused and attempts cap at three.
func TestDispatchEscalatesAfterThreeFailedStarts(t *testing.T) {
	t.Parallel()
	runner := &refusingRunner{}
	s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, runner)
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})

	require.Eventually(t, func() bool { return taskByID(t, s, task.ID).Escalated }, 5*time.Second, 10*time.Millisecond,
		"three failed starts escalate without another pass")

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskPending, got.Status)
	require.Empty(t, got.OwnerID)
	require.Equal(t, 3, got.Attempts)
	require.Equal(t, []string{"researcher-1", "researcher-2", "researcher-3"}, runner.names(),
		"a failed member is never picked again, and an escalated task is not started")
	require.Equal(t, StatusFailed, memberStatus(s, "researcher-1"))
	require.Equal(t, []string{fmt.Sprintf(
		"Task %s (Research) could not be started: provider unavailable. It is escalated; edit, reassign or reopen it to continue.", task.ID,
	)}, leadNotices(t, s, pipelineSession))
}

// TestDispatchRetriesAfterABusyStateLock pins that a pass skipped because the
// state lock was busy is not lost: one retry is scheduled and starts the task
// once the lock is free.
func TestDispatchRetriesAfterABusyStateLock(t *testing.T) {
	t.Parallel()
	var autostart atomic.Bool
	path := filepath.Join(t.TempDir(), "team.json")
	runner := newFakeRunner()
	s, err := New(path, Config{Autostart: autostart.Load}, runner)
	require.NoError(t, err)
	s.lockTimeout = 100 * time.Millisecond
	t.Cleanup(s.Shutdown)

	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Empty(t, runner.memberNames())

	// Hold the in-process write slot, so the pass below is refused as busy.
	key, err := filepath.Abs(path)
	require.NoError(t, err)
	value, _ := stateLocks.LoadOrStore(key, make(chan struct{}, 1))
	slot := value.(chan struct{})
	slot <- struct{}{}

	autostart.Store(true)
	s.dispatch(context.Background(), pipelineSession)
	require.Empty(t, runner.memberNames(), "a skipped pass starts nothing")
	<-slot

	require.Eventually(t, func() bool {
		return len(runner.memberNames()) == 1
	}, 5*time.Second, 10*time.Millisecond, "the retry starts the task once the lock is free")
	require.Equal(t, TaskInProgress, taskByID(t, s, task.ID).Status)
}

func TestDispatchFailedTurnsEscalate(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	for round := 1; round <= 3; round++ {
		require.Eventually(t, func() bool { return len(runner.memberNames()) == round }, time.Second, 10*time.Millisecond)
		runner.finishWithErr("researcher-1", errors.New("boom"))
		require.Eventually(t, func() bool { return taskByID(t, s, task.ID).Attempts == round }, time.Second, 10*time.Millisecond)
	}
	s.Shutdown()
	got := taskByID(t, s, task.ID)
	require.True(t, got.Escalated)
	require.Equal(t, TaskPending, got.Status)
	require.Len(t, runner.memberNames(), 3, "an escalated task is not started a fourth time")
}

func TestDispatchInterruptedTurnEscalates(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	runner.finishWithErr("researcher-1", context.Canceled)
	require.Eventually(t, func() bool { return taskByID(t, s, task.ID).Escalated }, time.Second, 10*time.Millisecond)
	s.Shutdown()
	require.Equal(t, []string{"researcher-1"}, runner.memberNames(), "an interrupted task does not restart on its own")
	require.Contains(t, leadNotices(t, s, pipelineSession)[0], "was interrupted")
}

func TestDispatchAfterShutdownStartsNothing(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	s.Shutdown()
	createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Empty(t, runner.memberNames())
}

// blockingFailRunner holds its first start until the test releases it, then
// fails it, so the test can land a shutdown in between.
type blockingFailRunner struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingFailRunner) Start(_ context.Context, _ Member, _ string) (Run, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	return nil, errors.New("provider unavailable")
}

// TestDispatchFailedAfterShutdownKeepsState pins that a start which fails
// after Shutdown neither writes the state nor charges the task: the claim
// Shutdown found is left as it was.
func TestDispatchFailedAfterShutdownKeepsState(t *testing.T) {
	t.Parallel()
	var autostart atomic.Bool
	path := filepath.Join(t.TempDir(), "team.json")
	runner := &blockingFailRunner{entered: make(chan struct{}), release: make(chan struct{})}
	s, err := New(path, Config{Autostart: autostart.Load}, runner)
	require.NoError(t, err)
	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})

	autostart.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.dispatch(context.Background(), pipelineSession)
	}()
	select {
	case <-runner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatched start never reached the runner")
	}

	s.Shutdown()
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	close(runner.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the failed start never returned after Shutdown")
	}

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "a failed start after Shutdown must not write the state")

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status, "the claim Shutdown found stays")
	require.Equal(t, memberID(t, s, "researcher-1"), got.OwnerID)
	require.Zero(t, got.Attempts)
	require.False(t, got.Escalated)
	require.Empty(t, leadNotices(t, s, pipelineSession))
	s.mu.Lock()
	failed := s.stateMemberLocked("researcher-1").Failed
	s.mu.Unlock()
	require.False(t, failed, "a member must not be marked failed after Shutdown")
}

func TestLeadNoticeReachesOnlyItsLead(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{RoleExists: func(string) bool { return false }})
	var woken []string
	var mu sync.Mutex
	s.SetLeadNotifyHook(func(sessionID string) {
		mu.Lock()
		defer mu.Unlock()
		woken = append(woken, sessionID)
	})
	createTask(t, s, CreateTaskRequest{Subject: "Old", Description: "d", Role: "gone"})

	mu.Lock()
	require.Equal(t, []string{pipelineSession}, woken)
	mu.Unlock()
	require.Equal(t, 1, s.Tree(pipelineSession).UnreadByLead, "crush run waits for the Lead to read it")
	require.Empty(t, s.DrainInbox(leadName, "session-b"), "another Lead never sees it")
	pending := s.DrainInbox(leadName, pipelineSession)
	require.Len(t, pending, 1)
	require.Equal(t, noticeSender, pending[0].From)
}

// lockProbeRunner records whether Start ran while the dispatching service
// held its mutex or its write slot.
type lockProbeRunner struct {
	*fakeRunner
	service *Service
	key     string
	held    atomic.Bool
}

func (r *lockProbeRunner) Start(ctx context.Context, member Member, prompt string) (Run, error) {
	if r.service.mu.TryLock() {
		r.service.mu.Unlock()
	} else {
		r.held.Store(true)
	}
	if value, ok := stateLocks.Load(r.key); ok {
		slot := value.(chan struct{})
		select {
		case slot <- struct{}{}:
			<-slot
		default:
			r.held.Store(true)
		}
	}
	return r.fakeRunner.Start(ctx, member, prompt)
}

func TestDispatchStartsOutsideTheLock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "team.json")
	s, err := New(path, Config{}, nil)
	require.NoError(t, err)
	key, err := filepath.Abs(path)
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)
	probe := &lockProbeRunner{fakeRunner: newFakeRunner(), service: s, key: key}
	s.SetRunner(probe)

	createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})
	require.Equal(t, []string{"researcher-1"}, probe.memberNames())
	require.False(t, probe.held.Load(), "Runner.Start must run with no Team lock held")
}

// TestDispatchRetryHappensOnce pins that a retry pass refused again gives up:
// one retry per skipped pass, so a lock held by another process never starts
// a retry loop. The next write runs a fresh pass.
func TestDispatchRetryHappensOnce(t *testing.T) {
	t.Parallel()
	var autostart atomic.Bool
	path := filepath.Join(t.TempDir(), "team.json")
	runner := newFakeRunner()
	s, err := New(path, Config{Autostart: autostart.Load}, runner)
	require.NoError(t, err)
	s.lockTimeout = 50 * time.Millisecond
	t.Cleanup(s.Shutdown)

	task := createTask(t, s, CreateTaskRequest{Subject: "Research", Description: "d", Role: "researcher"})

	key, err := filepath.Abs(path)
	require.NoError(t, err)
	value, _ := stateLocks.LoadOrStore(key, make(chan struct{}, 1))
	slot := value.(chan struct{})
	slot <- struct{}{}

	autostart.Store(true)
	s.dispatch(context.Background(), pipelineSession)
	// Long enough for the one retry to run into the held lock and give up.
	time.Sleep(4 * dispatchRetryDelay)
	<-slot

	require.Never(t, func() bool { return len(runner.memberNames()) > 0 }, 4*dispatchRetryDelay, 10*time.Millisecond,
		"a retry chain must end after one retry")
	require.Equal(t, TaskPending, taskByID(t, s, task.ID).Status)
}
