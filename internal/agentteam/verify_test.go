package agentteam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// verificationTaskFor returns the newest verification task of originalID.
func verificationTaskFor(t *testing.T, s *Service, originalID string) Task {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var found Task
	for _, task := range s.state.Tasks {
		if task.Verifies == originalID {
			found = task
		}
	}
	require.NotEmpty(t, found.ID, "no verification task for %s", originalID)
	return found
}

func ownerName(t *testing.T, s *Service, taskID string) string {
	t.Helper()
	view, err := s.GetTask(leadName, pipelineSession, taskID)
	require.NoError(t, err)
	return view.OwnerName
}

func TestCompleteMovesToReviewAndCreatesTheCheck(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "Make it work.", Role: "implementer", WriteScopes: []string{"internal/x"}})
	act(t, s, leadName, task.ID, "claim", nil)
	view := act(t, s, leadName, task.ID, "complete", nil)
	require.Equal(t, TaskInReview, view.Status)

	check := verificationTaskFor(t, s, task.ID)
	require.Equal(t, "Verify: Build", check.Subject)
	require.Equal(t, "verifier", check.Role)
	require.Equal(t, TaskPending, check.Status)
	require.NotNil(t, check.Verify)
	require.False(t, *check.Verify)
	require.Empty(t, check.WriteScopes)
	require.Equal(t, pipelineSession, check.SessionID)
	require.Equal(t, fmt.Sprintf("Verify task %s: Build\n\nAcceptance criteria and description of the original task:\nMake it work.\n\n"+
		"Implemented by: lead\nWrite scopes: internal/x\nThe change is in the shared working directory; inspect it with git status and git diff.", task.ID),
		check.Description)
}

func TestVerificationSubjectIsTruncated(t *testing.T) {
	t.Parallel()
	s := newBareService(t)
	long := ""
	for range maxTaskSubject {
		long += "é"
	}
	task := createTask(t, s, CreateTaskRequest{Subject: long, Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	require.Len(t, []rune(verificationTaskFor(t, s, task.ID).Subject), maxTaskSubject)
}

func TestHandOffWithoutVerification(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	a := createTask(t, s, CreateTaskRequest{Subject: "A", Description: "d", Role: "researcher"})
	b := createTask(t, s, CreateTaskRequest{Subject: "B", Description: "d", Role: "researcher", BlockedBy: []string{a.ID}})
	require.Equal(t, TaskPending, taskByID(t, s, b.ID).Status)

	act(t, s, "researcher-1", a.ID, "complete", nil)
	require.Equal(t, TaskCompleted, taskByID(t, s, a.ID).Status)
	require.Equal(t, TaskInProgress, taskByID(t, s, b.ID).Status, "completing A starts B")
	require.Equal(t, []string{"researcher-1", "researcher-2"}, runner.memberNames())
}

func TestHandOffWaitsForVerification(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	a := createTask(t, s, CreateTaskRequest{Subject: "A", Description: "d", Role: "implementer"})
	b := createTask(t, s, CreateTaskRequest{Subject: "B", Description: "d", Role: "researcher", BlockedBy: []string{a.ID}})

	act(t, s, "implementer-1", a.ID, "complete", nil)
	require.Equal(t, TaskInReview, taskByID(t, s, a.ID).Status)
	require.Equal(t, TaskPending, taskByID(t, s, b.ID).Status, "an in-review blocker keeps B waiting")
	check := verificationTaskFor(t, s, a.ID)
	require.Equal(t, "verifier-1", ownerName(t, s, check.ID), "the verifier starts on its own")
	require.Contains(t, runner.lastPrompt(), `Record your verdict with team_task_update, action "pass", or action "fail" with notes describing what failed.`)

	act(t, s, "verifier-1", check.ID, "pass", nil)
	require.Equal(t, TaskCompleted, taskByID(t, s, check.ID).Status)
	require.Equal(t, TaskCompleted, taskByID(t, s, a.ID).Status)
	require.Equal(t, TaskInProgress, taskByID(t, s, b.ID).Status, "B starts only after pass")
	require.Equal(t, []string{"implementer-1", "verifier-1", "researcher-1"}, runner.memberNames())
}

func TestFailReopensAndWakesTheOwner(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", task.ID, "complete", nil)
	runner.finish("implementer-1")
	require.Eventually(t, func() bool { return memberStatus(s, "implementer-1") == StatusInactive }, time.Second, 10*time.Millisecond)
	check := verificationTaskFor(t, s, task.ID)
	before := runner.promptCount()

	act(t, s, "verifier-1", check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "TestX fails" })
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, memberID(t, s, "implementer-1"), got.OwnerID)
	require.Equal(t, 1, got.Attempts)
	require.Equal(t, TaskCompleted, taskByID(t, s, check.ID).Status)
	require.Equal(t, before+1, runner.promptCount(), "the owner is woken")
	require.Contains(t, runner.lastPrompt(), fmt.Sprintf(
		"verifier-1: Verification of task %s failed:\n\nTestX fails\n\nFix it and complete the task again.", task.ID))
}

// leadInbox returns every message the Lead has, scoped notices included.
func leadInbox(t *testing.T, s *Service) []string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var texts []string
	for _, message := range s.state.Messages {
		if message.To == leadName {
			texts = append(texts, message.Text)
		}
	}
	return texts
}

// runRecorded reports whether the service holds a run for name.
func runRecorded(s *Service, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.runs[name]
	return ok
}

// memberIDOrEmpty returns the durable ID of name, or "" when the roster has
// no such member.
func memberIDOrEmpty(s *Service, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	member, ok := s.memberLocked(name)
	if !ok {
		return ""
	}
	return member.ID
}

// checksFor counts every verification task ever created for originalID,
// including the deleted and completed ones.
func checksFor(t *testing.T, s *Service, originalID string) int {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, task := range s.state.Tasks {
		if task.Verifies == originalID {
			count++
		}
	}
	return count
}

// TestFailedVerdictWhileTheOwnerTurnRunsKeepsTheTaskInProgress pins the race
// between a fail verdict and the turn that called complete: the owner is
// still running, so the verdict cannot wake it. Its turn must settle with the
// task left in progress, under the notes waiting in its inbox, instead of
// sending the task to review again with a second check before the owner has
// read what failed.
func TestFailedVerdictWhileTheOwnerTurnRunsKeepsTheTaskInProgress(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer", WriteScopes: []string{"internal/x"}})
	require.Equal(t, []string{"implementer-1"}, runner.memberNames())

	act(t, s, "implementer-1", task.ID, "complete", nil)
	first := verificationTaskFor(t, s, task.ID)
	require.Equal(t, "verifier-1", ownerName(t, s, first.ID))
	require.Equal(t, StatusRunning, memberStatus(s, "implementer-1"), "the owner is still in the turn that completed")

	act(t, s, "verifier-1", first.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "TestX fails" })
	require.Equal(t, TaskInProgress, taskByID(t, s, task.ID).Status)
	require.Equal(t, StatusRunning, memberStatus(s, "implementer-1"))

	runner.finish("implementer-1")
	require.Eventually(t, func() bool {
		return strings.Contains(runner.lastPrompt(), "TestX fails")
	}, time.Second, 10*time.Millisecond, "the owner is resumed with the failure notes")

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status, "the resumed turn owns the task")
	require.Equal(t, 1, got.Attempts)
	require.Equal(t, 1, checksFor(t, s, task.ID), "no second check before the owner reads the verdict")
	require.NotContains(t, runner.memberNames(), "verifier-2", "no second verifier is started")
	require.Equal(t, 3, runner.promptCount(), "the resume is the only new turn")
	require.Equal(t, []string{"internal/x"}, s.ClaimedWriteScopes("implementer-1"), "the owner keeps its scopes")

	// The resumed owner fixes the work, completes again, and a fresh check
	// decides the new revision.
	act(t, s, "implementer-1", task.ID, "complete", nil)
	second := verificationTaskFor(t, s, task.ID)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, 2, checksFor(t, s, task.ID))
	act(t, s, ownerName(t, s, second.ID), second.ID, "pass", nil)
	require.Equal(t, TaskCompleted, taskByID(t, s, task.ID).Status)
}

// refusingStartRunner starts every turn until failFor names a member, then
// refuses that member's starts, the way a model the config no longer serves
// does.
type refusingStartRunner struct {
	*fakeRunner
	mu      sync.Mutex
	name    string
	failing bool
}

func (r *refusingStartRunner) Start(ctx context.Context, member Member, prompt string) (Run, error) {
	r.mu.Lock()
	failing := r.failing && member.Name == r.name
	r.mu.Unlock()
	if failing {
		return nil, errors.New("model unavailable")
	}
	return r.fakeRunner.Start(ctx, member, prompt)
}

func (r *refusingStartRunner) failFor(name string) {
	r.mu.Lock()
	r.name, r.failing = name, true
	r.mu.Unlock()
}

// TestHeldResumeFailsReleasesTheTask pins the case a held settle opens: the
// owner's turn ends with the failure notes queued, and the start that would
// deliver them is refused. The held task must go back to the board as a
// failed turn, with the failed resume charged, instead of staying in progress
// under a member that can no longer run. The next failed verification then
// reaches the attempts cap.
func TestHeldResumeFailsReleasesTheTask(t *testing.T) {
	t.Parallel()
	runner := &refusingStartRunner{fakeRunner: newFakeRunner()}
	s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, runner)
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)

	woken := make(chan bool, 32)
	s.SetLeadNotifyHook(func(string) {
		mentioned := false
		for _, note := range leadInbox(t, s) {
			if strings.Contains(note, "Could not resume") {
				mentioned = true
			}
		}
		woken <- mentioned
	})

	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	require.Eventually(t, func() bool {
		got := taskByID(t, s, task.ID)
		return got.Status == TaskInProgress && got.OwnerID != "" &&
			got.OwnerID == memberIDOrEmpty(s, "implementer-1") && runRecorded(s, "implementer-1")
	}, time.Second, 10*time.Millisecond, "implementer-1 owns the task and its turn runs")

	// The implementer completes, and the verdict fails while its turn is
	// still running, so the notes wait in its inbox.
	act(t, s, "implementer-1", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, ownerName(t, s, check.ID), check.ID, "fail", func(r *UpdateTaskRequest) {
		r.Notes = "TestX fails"
	})
	require.Equal(t, TaskInProgress, taskByID(t, s, task.ID).Status, "the verdict hands the task back")

	runner.failFor("implementer-1")
	runner.finish("implementer-1")

	notice := ""
	require.Eventually(t, func() bool {
		for _, note := range leadInbox(t, s) {
			if strings.Contains(note, "Could not resume implementer-1") {
				notice = note
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond, "the Lead is told the resume failed")
	require.Contains(t, notice, "Task(s) it left in progress: "+task.ID+".", "the notice names the stranded task")
	require.Eventually(t, func() bool {
		for {
			select {
			case after := <-woken:
				if after {
					return true
				}
			default:
				return false
			}
		}
	}, time.Second, 10*time.Millisecond, "the Lead is woken after the notice lands")

	// The verdict charged one attempt and the failed resume another, and
	// the released task reaches a fresh implementer because the failed
	// member is never reused.
	require.Eventually(t, func() bool {
		got := taskByID(t, s, task.ID)
		return got.Attempts == 2 && got.Status == TaskInProgress &&
			got.OwnerID != "" && got.OwnerID == memberIDOrEmpty(s, "implementer-2") &&
			runRecorded(s, "implementer-2")
	}, time.Second, 10*time.Millisecond, "the released task reaches a fresh implementer")

	// The next failed verification reaches the cap and escalates.
	act(t, s, "implementer-2", task.ID, "complete", nil)
	next := verificationTaskFor(t, s, task.ID)
	act(t, s, ownerName(t, s, next.ID), next.ID, "fail", func(r *UpdateTaskRequest) {
		r.Notes = "still broken"
	})
	got := taskByID(t, s, task.ID)
	require.True(t, got.Escalated, "the third failure escalates the task")
	require.Equal(t, maxTaskAttempts, got.Attempts)
	require.Equal(t, TaskInReview, got.Status)
	require.NotContains(t, runner.memberNames(), "implementer-3", "an escalated task starts nobody")
}

// TestFailedWakeAfterFailNoticesTheLead pins the stranded case: a fail
// verdict hands the task back to an idle owner, but the start that would
// resume it is refused. The task stays in progress with nobody running it, so
// the Lead gets a notice naming the task and the error instead of a Warn log
// nobody reads.
func TestFailedWakeAfterFailNoticesTheLead(t *testing.T) {
	t.Parallel()
	runner := &refusingStartRunner{fakeRunner: newFakeRunner()}
	s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{Autostart: func() bool { return false }}, runner)
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)

	_, err = s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "imp", Description: "Implementer", Prompt: "Work.", RoleName: "implementer", ParentID: pipelineSession,
	})
	require.NoError(t, err)
	runner.finish("imp")
	require.Eventually(t, func() bool { return memberStatus(s, "imp") == StatusInactive }, time.Second, 10*time.Millisecond)

	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "imp", task.ID, "claim", nil)
	act(t, s, "imp", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	_, err = s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "ver", Description: "Verifier", Prompt: "Check.", RoleName: "verifier", ParentID: pipelineSession,
	})
	require.NoError(t, err)
	act(t, s, "ver", check.ID, "claim", nil)

	runner.failFor("imp")
	act(t, s, "ver", check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "still broken" })

	require.Equal(t, TaskInProgress, taskByID(t, s, task.ID).Status, "the task stays with its owner")
	notes := leadNotices(t, s, pipelineSession)
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], task.ID)
	require.Contains(t, notes[0], "model unavailable")
	require.Contains(t, notes[0], "release it, or reassign it with no owner")
}

func TestThirdFailEscalatesInReview(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	for attempt := 1; attempt <= 3; attempt++ {
		act(t, s, "implementer-1", task.ID, "complete", nil)
		check := verificationTaskFor(t, s, task.ID)
		act(t, s, ownerName(t, s, check.ID), check.ID, "fail", func(r *UpdateTaskRequest) {
			r.Notes = fmt.Sprintf("attempt %d broke", attempt)
		})
	}
	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInReview, got.Status, "an escalated original stays in review")
	require.True(t, got.Escalated)
	require.Equal(t, 3, got.Attempts)
	notes := leadNotices(t, s, pipelineSession)
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], "failed verification 3 times")
	require.Contains(t, notes[0], "To recover, reopen it, complete it to accept it as is, or delete it.")
	require.Contains(t, notes[0], "attempt 3 broke")

	act(t, s, leadName, task.ID, "reopen", nil)
	got = taskByID(t, s, task.ID)
	require.False(t, got.Escalated, "reopen by the Lead clears the escalation")
	require.Zero(t, got.Attempts)
}

func TestVerdictRules(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)

	require.Equal(t, CodeTaskInvalidTransition, ErrorCode(actErr(s, leadName, task.ID, "pass", nil)),
		"pass on a task that verifies nothing")
	require.Equal(t, CodeTaskUnauthorized, ErrorCode(actErr(s, leadName, check.ID, "pass", nil)),
		"only the verification task's owner decides")
	require.Equal(t, CodeInvalidArgument, ErrorCode(actErr(s, "verifier-1", check.ID, "fail", nil)),
		"fail needs notes")
	require.Equal(t, CodeTaskInvalidTransition, ErrorCode(actErr(s, "verifier-1", check.ID, "complete", nil)),
		"a verification task records a verdict instead of completing")
}

func TestNoVerdictReleasesAndEscalates(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "ver", Description: "Verifier", Prompt: "Check.", RoleName: "verifier", ParentID: pipelineSession,
	})
	require.NoError(t, err)

	for round := 1; round <= 3; round++ {
		act(t, s, "ver", check.ID, "claim", nil)
		runner.finish("ver")
		require.Eventually(t, func() bool {
			got := taskByID(t, s, check.ID)
			return got.Status == TaskPending && got.Attempts == round
		}, time.Second, 10*time.Millisecond, "a turn without a verdict releases the check")
		if round < 3 {
			require.NoError(t, s.SendMessage(context.Background(), leadName, pipelineSession, SendRequest{Target: "ver", Message: "Try again."}))
		}
	}
	require.True(t, taskByID(t, s, check.ID).Escalated)
	require.Equal(t, TaskInReview, taskByID(t, s, task.ID).Status, "no verdict never passes the work")
	require.Contains(t, leadNotices(t, s, pipelineSession)[0], "got no verdict")
	require.Contains(t, leadNotices(t, s, pipelineSession)[0],
		"It is escalated; to continue, edit it, or reassign it with no owner.")
}

func TestSettleSendsVerifiedWorkToReview(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	runner.finish("implementer-1")
	require.Eventually(t, func() bool { return taskByID(t, s, task.ID).Status == TaskInReview }, time.Second, 10*time.Millisecond)
	require.Equal(t, task.ID, verificationTaskFor(t, s, task.ID).Verifies)
}

func TestVerdictAfterOriginalMoved(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, task.ID, "reopen", nil)
	reopened := taskByID(t, s, task.ID)

	act(t, s, "verifier-1", check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "late" })
	require.Equal(t, TaskCompleted, taskByID(t, s, check.ID).Status)
	got := taskByID(t, s, task.ID)
	require.Equal(t, reopened.Revision, got.Revision, "a verdict leaves an original that left review alone")
	require.Zero(t, got.Attempts)
}

// TestReopenDeletesAPendingCheck pins the cleanup for a check nobody has
// started: a reopen of the original deletes it, so a verifier is never
// started on work the board has moved on from.
func TestReopenDeletesAPendingCheck(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	require.Equal(t, TaskPending, check.Status)

	act(t, s, leadName, task.ID, "reopen", nil)
	require.Equal(t, TaskDeleted, taskByID(t, s, check.ID).Status)
	require.Equal(t, TaskPending, taskByID(t, s, task.ID).Status)
}

// TestDeleteDeletesAPendingCheck pins the same cleanup for a delete.
func TestDeleteDeletesAPendingCheck(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	require.Equal(t, TaskPending, check.Status)

	act(t, s, leadName, task.ID, "delete", nil)
	require.Equal(t, TaskDeleted, taskByID(t, s, check.ID).Status)
	require.Equal(t, TaskDeleted, taskByID(t, s, task.ID).Status)
}

// TestReopenKeepsAnInProgressCheck pins that the cleanup leaves a check its
// verifier is already reviewing alone: deleting it would refuse the verdict
// that Review Focus 4 requires.
func TestReopenKeepsAnInProgressCheck(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	require.Equal(t, TaskInProgress, check.Status)

	act(t, s, leadName, task.ID, "reopen", nil)
	kept := taskByID(t, s, check.ID)
	require.Equal(t, TaskInProgress, kept.Status)
	require.Equal(t, check.OwnerID, kept.OwnerID)
}

// TestTaskLimitCountsOnlyTheOriginal pins the ruling's case: with a limit of
// one, an implementer task completes into review and its check is still
// created, while the original alone fills the limit.
func TestTaskLimitCountsOnlyTheOriginal(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{MaxTasks: 1, Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	require.Equal(t, TaskInReview, act(t, s, leadName, task.ID, "complete", nil).Status)
	require.Equal(t, TaskPending, verificationTaskFor(t, s, task.ID).Status)

	_, err := s.CreateTask(leadName, pipelineSession, CreateTaskRequest{Subject: "More", Description: "d"})
	require.Equal(t, CodeTaskLimit, ErrorCode(err))
}

// TestVerificationTaskIsExemptFromTheTaskLimit distinguishes the exemption
// from a limit that counts every task: two live tasks fit a limit of two
// only when the verification task is free.
func TestVerificationTaskIsExemptFromTheTaskLimit(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{MaxTasks: 2, Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	require.Equal(t, "verifier", verificationTaskFor(t, s, task.ID).Role)

	createTask(t, s, CreateTaskRequest{Subject: "Next", Description: "d"})
	_, err := s.CreateTask(leadName, pipelineSession, CreateTaskRequest{Subject: "Third", Description: "d"})
	require.Equal(t, CodeTaskLimit, ErrorCode(err), "the original still counts against the limit")
}

// heldRunner holds its first start until the test opens the gate, so the
// test can send a member mail while its turn reads as starting.
type heldRunner struct {
	*fakeRunner
	gate    chan struct{}
	entered chan struct{}
	first   sync.Once
}

func newHeldRunner() *heldRunner {
	return &heldRunner{fakeRunner: newFakeRunner(), gate: make(chan struct{}), entered: make(chan struct{})}
}

func (r *heldRunner) Start(ctx context.Context, member Member, prompt string) (Run, error) {
	r.first.Do(func() {
		close(r.entered)
		<-r.gate
	})
	return r.fakeRunner.Start(ctx, member, prompt)
}

// TestWakeDeliversMailToAStartingMember pins that a message sent to a member
// whose turn is still starting waits for that turn instead of being lost.
func TestWakeDeliversMailToAStartingMember(t *testing.T) {
	t.Parallel()
	runner := newHeldRunner()
	s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, runner)
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)

	spawned := make(chan error, 1)
	go func() {
		_, spawnErr := s.SpawnTeammate(context.Background(), leadName, SpawnRequest{
			Name: "worker", Description: "d", Prompt: "Work.", ParentID: pipelineSession,
		})
		spawned <- spawnErr
	}()
	select {
	case <-runner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the spawn never reached the runner")
	}
	require.Equal(t, StatusRunning, memberStatus(s, "worker"), "the spawn holds the member in starting")

	require.NoError(t, s.SendMessage(context.Background(), leadName, pipelineSession, SendRequest{Target: "worker", Message: "Try again."}))
	require.Zero(t, runner.promptCount(), "the message waits for the turn that is starting")

	close(runner.gate)
	require.NoError(t, <-spawned)
	runner.finish("worker")
	require.Eventually(t, func() bool { return runner.promptCount() == 2 }, time.Second, 10*time.Millisecond,
		"the settled turn takes the queued mail")
	require.Contains(t, runner.lastPrompt(), "Try again.")
}

// TestStartMailKeepsMailForATurnAlreadyStarting pins the race a wake can
// lose: mail it drained while another start claimed the member goes back to
// the queue, so the turn that is starting delivers it.
func TestStartMailKeepsMailForATurnAlreadyStarting(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{Autostart: func() bool { return false }})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "worker", Description: "d", Prompt: "Work.", ParentID: pipelineSession,
	})
	require.NoError(t, err)
	runner.finish("worker")
	require.Eventually(t, func() bool { return memberStatus(s, "worker") == StatusInactive }, time.Second, 10*time.Millisecond)
	before := runner.promptCount()

	require.NoError(t, s.write(mustApply, func() (bool, error) {
		s.appendMessageLocked(leadName, "worker", "Try again.")
		return true, nil
	}))
	pending := s.DrainInbox("worker", pipelineSession)
	require.Len(t, pending, 1)

	// Another start owns the member.
	s.mu.Lock()
	s.starting["worker"] = true
	s.mu.Unlock()

	_, err = s.startMail(context.Background(), memberByID(t, s, "worker"), pending)
	require.NoError(t, err)
	require.Equal(t, before, runner.promptCount(), "a member already starting takes no second turn")

	s.mu.Lock()
	delivered := true
	for _, message := range s.state.Messages {
		if message.To == "worker" && message.Text == "Try again." {
			delivered = message.Delivered
		}
	}
	s.mu.Unlock()
	require.False(t, delivered, "the mail is queued again for the starting turn")
}

// TestFailNoticesTheLeadWhenItOwnsTheOriginal pins the Lead-owner branch: a
// failed verification of the Lead's own work is a notice to the Lead, not
// mail to a member.
func TestFailNoticesTheLeadWhenItOwnsTheOriginal(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "ver", Description: "Verifier", Prompt: "Check.", RoleName: "verifier", ParentID: pipelineSession,
	})
	require.NoError(t, err)
	act(t, s, "ver", check.ID, "claim", nil)
	act(t, s, "ver", check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "broken output" })

	got := taskByID(t, s, task.ID)
	require.Equal(t, TaskInProgress, got.Status)
	require.Equal(t, leadName, got.OwnerID, "the Lead keeps its own task")
	notes := leadNotices(t, s, pipelineSession)
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], "Verification of task "+task.ID+" failed")
	require.Contains(t, notes[0], "broken output")
}

// TestNewerReviewReplacesTheStaleCheck pins that the check of an earlier
// review does not outlive it: an older verifier's pass must not decide a
// revision it never saw.
func TestNewerReviewReplacesTheStaleCheck(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", task.ID, "complete", nil)
	first := verificationTaskFor(t, s, task.ID)
	require.Equal(t, TaskInProgress, first.Status)

	act(t, s, leadName, task.ID, "reopen", nil)
	require.Equal(t, TaskInProgress, taskByID(t, s, first.ID).Status, "the started check stays with its verifier")
	// The dispatcher re-claims the reopened task, so its new owner completes.
	act(t, s, ownerName(t, s, task.ID), task.ID, "complete", nil)
	second := verificationTaskFor(t, s, task.ID)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, TaskDeleted, taskByID(t, s, first.ID).Status, "the first check is stale")
	require.Equal(t, TaskInReview, taskByID(t, s, task.ID).Status)

	require.Equal(t, CodeTaskDeleted, ErrorCode(actErr(s, "verifier-1", first.ID, "pass", nil)),
		"a stale verdict is refused")
	require.Equal(t, TaskInReview, taskByID(t, s, task.ID).Status, "a stale pass never completes the work")
}

// TestAVerdictIsFinal pins that a check cannot reopen: reusing an old check
// for a later revision would let it decide work it never saw.
func TestAVerdictIsFinal(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, "verifier-1", check.ID, "pass", nil)
	require.Equal(t, TaskCompleted, taskByID(t, s, check.ID).Status)

	require.Equal(t, CodeTaskInvalidTransition, ErrorCode(actErr(s, "verifier-1", check.ID, "reopen", nil)))
	require.Equal(t, CodeTaskInvalidTransition, ErrorCode(actErr(s, leadName, check.ID, "reopen", nil)),
		"even the Lead cannot reuse a verdict")
}

// TestAnImplementerCannotVerifyItsOwnWork pins that a teammate cannot check
// its own work: neither the claim nor the verdict is allowed, so the original
// waits for an independent verifier.
func TestAnImplementerCannotVerifyItsOwnWork(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "imp", Description: "Implementer", Prompt: "Work.", RoleName: "implementer", ParentID: pipelineSession,
	})
	require.NoError(t, err)
	a := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	b := createTask(t, s, CreateTaskRequest{Subject: "Next", Description: "d", BlockedBy: []string{a.ID}})

	act(t, s, "imp", a.ID, "claim", nil)
	act(t, s, "imp", a.ID, "complete", nil)
	check := verificationTaskFor(t, s, a.ID)
	require.Equal(t, TaskPending, check.Status)

	require.Equal(t, CodeTaskUnauthorized, ErrorCode(actErr(s, "imp", check.ID, "claim", nil)),
		"an implementer cannot claim the check of its own task")

	// The Lead forces the check onto the implementer.
	act(t, s, leadName, check.ID, "reassign", func(r *UpdateTaskRequest) { r.Owner = "imp" })
	require.Equal(t, CodeTaskUnauthorized, ErrorCode(actErr(s, "imp", check.ID, "pass", nil)),
		"an implementer cannot pass its own work")
	require.Equal(t, CodeTaskUnauthorized, ErrorCode(actErr(s, "imp", check.ID, "fail",
		func(r *UpdateTaskRequest) { r.Notes = "looks fine to me" })),
		"an implementer cannot fail its own work either")

	require.Equal(t, TaskInReview, taskByID(t, s, a.ID).Status, "the original waits for a verdict")
	view, err := s.GetTask(leadName, pipelineSession, b.ID)
	require.NoError(t, err)
	require.False(t, view.Ready, "the dependent waits for an independent verdict")
}

// TestACheckOfAReopenedTaskIsDeletedNotRestarted pins that a check whose
// original left review is not put back on the board: no verifier is started
// again and no false no-verdict escalation reaches the Lead.
func TestACheckOfAReopenedTaskIsDeletedNotRestarted(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{})
	a := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", a.ID, "complete", nil)
	check := verificationTaskFor(t, s, a.ID)
	require.Equal(t, TaskInProgress, check.Status)

	act(t, s, leadName, a.ID, "reopen", nil)
	require.Equal(t, TaskInProgress, taskByID(t, s, check.ID).Status, "the started check stays with its verifier")
	starts := runner.memberNames()

	runner.finish("verifier-1")
	require.Eventually(t, func() bool { return taskByID(t, s, check.ID).Status == TaskDeleted },
		time.Second, 10*time.Millisecond, "a check of a task that left review is deleted")
	got := taskByID(t, s, check.ID)
	require.Zero(t, got.Attempts, "a check nobody needs is not charged an attempt")
	require.False(t, got.Escalated)
	require.Equal(t, starts, runner.memberNames(), "no verifier is started again")
	require.Empty(t, leadNotices(t, s, pipelineSession), "the Lead is not told about a check nobody needs")
}

// TestOnlyTheLeadReopensAnInReviewTask pins that the owner of an escalated
// in-review task cannot reopen it to restart the review loop.
func TestOnlyTheLeadReopensAnInReviewTask(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{})
	a := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, "implementer-1", a.ID, "complete", nil)
	s.mu.Lock()
	for i := range s.state.Tasks {
		if s.state.Tasks[i].ID == a.ID {
			s.state.Tasks[i].Escalated = true
			s.state.Tasks[i].Attempts = maxTaskAttempts
		}
	}
	s.mu.Unlock()
	before := taskByID(t, s, a.ID)
	require.True(t, before.Escalated)

	require.Equal(t, CodeLeadRequired, ErrorCode(actErr(s, "implementer-1", a.ID, "reopen", nil)),
		"only the Lead reopens an in-review task")
	require.Equal(t, before, taskByID(t, s, a.ID), "a refused reopen changes nothing")

	view := act(t, s, leadName, a.ID, "reopen", nil)
	require.Equal(t, TaskPending, view.Status)
	require.False(t, view.Escalated)
	require.Zero(t, view.Attempts)
}

// TestRequeueWakesMailWhenTheStartSettledInTheWindow pins the window a
// requeue opens: when the turn that was starting settles between the start's
// check and the requeue write, the mail is delivered at once instead of
// waiting for a wake that already ran.
// TestLeadCompletesInReviewWork pins that the Lead can accept escalated work
// as is: completing an in-review task finishes it, deletes its open check,
// and readies its dependents. A teammate cannot complete in-review work.
func TestLeadCompletesInReviewWork(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	a := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	b := createTask(t, s, CreateTaskRequest{Subject: "Next", Description: "d", Role: "researcher", BlockedBy: []string{a.ID}})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "imp", Description: "Implementer", Prompt: "Work.", RoleName: "implementer", ParentID: pipelineSession,
	})
	require.NoError(t, err)
	_, err = s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "ver", Description: "Verifier", Prompt: "Check.", RoleName: "verifier", ParentID: pipelineSession,
	})
	require.NoError(t, err)

	act(t, s, "imp", a.ID, "claim", nil)
	act(t, s, "imp", a.ID, "complete", nil)
	check := verificationTaskFor(t, s, a.ID)
	act(t, s, "ver", check.ID, "claim", nil)
	require.Equal(t, TaskInProgress, taskByID(t, s, check.ID).Status)

	// The escalated case is three failed verifications, which leaves the
	// original in review with its check open.
	s.mu.Lock()
	for i := range s.state.Tasks {
		if s.state.Tasks[i].ID == a.ID {
			s.state.Tasks[i].Escalated = true
			s.state.Tasks[i].Attempts = maxTaskAttempts
		}
	}
	s.mu.Unlock()

	require.Equal(t, CodeLeadRequired, ErrorCode(actErr(s, "imp", a.ID, "complete", nil)),
		"a teammate cannot accept in-review work")
	require.Equal(t, TaskInReview, taskByID(t, s, a.ID).Status)

	view := act(t, s, leadName, a.ID, "complete", nil)
	require.Equal(t, TaskCompleted, view.Status)
	require.Equal(t, TaskDeleted, taskByID(t, s, check.ID).Status, "the open check goes with the accepted work")
	require.Equal(t, CodeTaskDeleted, ErrorCode(actErr(s, "ver", check.ID, "fail",
		func(r *UpdateTaskRequest) { r.Notes = "late" })), "a stale verdict is refused")

	dependent, err := s.GetTask(leadName, pipelineSession, b.ID)
	require.NoError(t, err)
	require.True(t, dependent.Ready, "the dependent is ready once the Lead accepts the blocker")
}

func TestRequeueWakesMailWhenTheStartSettledInTheWindow(t *testing.T) {
	t.Parallel()
	s, runner := newPipelineService(t, Config{Autostart: func() bool { return false }})
	_, err := s.SpawnTeammate(t.Context(), leadName, SpawnRequest{
		Name: "worker", Description: "d", Prompt: "Work.", ParentID: pipelineSession,
	})
	require.NoError(t, err)
	runner.finish("worker")
	require.Eventually(t, func() bool { return memberStatus(s, "worker") == StatusInactive }, time.Second, 10*time.Millisecond)
	before := runner.promptCount()

	require.NoError(t, s.write(mustApply, func() (bool, error) {
		s.appendMessageLocked(leadName, "worker", "Try again.")
		return true, nil
	}))
	pending := s.DrainInbox("worker", pipelineSession)
	require.Len(t, pending, 1)

	// The wake found the member starting, and that turn settles in the window
	// before the requeue write lands.
	s.mu.Lock()
	s.starting["worker"] = true
	s.mu.Unlock()
	s.mu.Lock()
	delete(s.starting, "worker")
	s.mu.Unlock()

	require.NoError(t, s.requeueMail(context.Background(), memberByID(t, s, "worker"), pending))
	require.Equal(t, before+1, runner.promptCount(), "the mail is delivered at once")
	require.Contains(t, runner.lastPrompt(), "Try again.")
}

// TestDispatchSkipsACheckOfATaskThatLeftReview pins that a pending check
// whose original is not in review starts nobody, so no verifier is started on
// work the board moved on from.
func TestDispatchSkipsACheckOfATaskThatLeftReview(t *testing.T) {
	t.Parallel()
	var on atomic.Bool
	s, runner := newPipelineService(t, Config{Autostart: on.Load})
	a := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, a.ID, "claim", nil)
	act(t, s, leadName, a.ID, "complete", nil)
	check := verificationTaskFor(t, s, a.ID)
	require.Equal(t, TaskPending, check.Status)

	// The original leaves the board without the check being closed, the way
	// an older Team file can hold it.
	s.mu.Lock()
	for i := range s.state.Tasks {
		if s.state.Tasks[i].ID == a.ID {
			s.state.Tasks[i].Status = TaskDeleted
			s.state.Tasks[i].Revision++
		}
	}
	s.mu.Unlock()

	on.Store(true)
	s.dispatch(context.Background(), pipelineSession)
	require.Empty(t, runner.memberNames(), "a stale check starts nobody")
	require.Equal(t, TaskPending, taskByID(t, s, check.ID).Status)
}

// TestFailVerdictKeepsItsNotesOnTheTask pins that a fail verdict's notes are
// stored on the original, capped, and cleared by a pass and by a Lead edit.
func TestFailVerdictKeepsItsNotesOnTheTask(t *testing.T) {
	t.Parallel()
	s, _ := newPipelineService(t, Config{Autostart: func() bool { return false }})
	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	act(t, s, leadName, task.ID, "claim", nil)
	act(t, s, leadName, task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, check.ID, "claim", nil)
	act(t, s, leadName, check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "TestX fails" })
	got := taskByID(t, s, task.ID)
	require.Equal(t, "TestX fails", got.LastFailure)
	require.Contains(t, taskBrief(got), "\n\nThe last verification failed:\n\nTestX fails")

	act(t, s, leadName, task.ID, "complete", nil)
	check = verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, check.ID, "claim", nil)
	act(t, s, leadName, check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = strings.Repeat("x", 9000) })
	require.Len(t, taskByID(t, s, task.ID).LastFailure, maxFailureNotes, "the notes are capped at 8 KiB")

	act(t, s, leadName, task.ID, "edit", func(r *UpdateTaskRequest) { r.Description = "Build it right." })
	require.Empty(t, taskByID(t, s, task.ID).LastFailure, "a Lead edit clears the notes")

	act(t, s, leadName, task.ID, "complete", nil)
	check = verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, check.ID, "claim", nil)
	act(t, s, leadName, check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "Still failing" })
	act(t, s, leadName, task.ID, "complete", nil)
	check = verificationTaskFor(t, s, task.ID)
	act(t, s, leadName, check.ID, "claim", nil)
	act(t, s, leadName, check.ID, "pass", nil)
	got = taskByID(t, s, task.ID)
	require.Equal(t, TaskCompleted, got.Status)
	require.Empty(t, got.LastFailure, "a pass clears the notes")
	require.NotContains(t, taskBrief(got), "The last verification failed")
}

// TestFailureNotesReachTheNextImplementer pins the follow-up: the owner gets
// the notes as mail, and when its resume fails the mail is gone with it, so
// the implementer the dispatcher starts next must read them in its brief.
func TestFailureNotesReachTheNextImplementer(t *testing.T) {
	t.Parallel()
	runner := &refusingStartRunner{fakeRunner: newFakeRunner()}
	s, err := New(filepath.Join(t.TempDir(), "team.json"), Config{}, runner)
	require.NoError(t, err)
	t.Cleanup(s.Shutdown)

	task := createTask(t, s, CreateTaskRequest{Subject: "Build", Description: "d", Role: "implementer"})
	require.Eventually(t, func() bool { return runRecorded(s, "implementer-1") }, time.Second, 10*time.Millisecond)
	act(t, s, "implementer-1", task.ID, "complete", nil)
	check := verificationTaskFor(t, s, task.ID)
	act(t, s, ownerName(t, s, check.ID), check.ID, "fail", func(r *UpdateTaskRequest) { r.Notes = "TestX fails" })

	runner.failFor("implementer-1")
	runner.finish("implementer-1")
	require.Eventually(t, func() bool {
		got := taskByID(t, s, task.ID)
		return got.OwnerID != "" && got.OwnerID == memberIDOrEmpty(s, "implementer-2") && runRecorded(s, "implementer-2")
	}, time.Second, 10*time.Millisecond, "the released task reaches a fresh implementer")
	require.Contains(t, runner.lastPrompt(), "The last verification failed:\n\nTestX fails")
}

// TestLastFailureIsOmittedWhenEmpty pins that an old team.json and a task
// that never failed serialize the same as before.
func TestLastFailureIsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(Task{ID: "task-1"})
	require.NoError(t, err)
	require.NotContains(t, string(data), "last_failure")
}
