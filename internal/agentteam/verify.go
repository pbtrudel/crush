package agentteam

import "fmt"

// maxFailureNotes caps the failure notes a task keeps for its next brief.
const maxFailureNotes = 8 << 10

// newVerificationTaskLocked builds the task that checks original, which just
// entered review, and takes its id. The caller appends it in the same write.
// Callers must hold s.mu.
func (s *Service) newVerificationTaskLocked(original Task) Task {
	s.state.NextTask++
	implementer := "none"
	if original.OwnerID == leadName {
		implementer = leadName
	}
	for _, member := range s.state.Members {
		if member.ID == original.OwnerID {
			implementer = member.Name
		}
	}
	return Task{
		ID:       fmt.Sprintf("task-%d", s.state.NextTask),
		Revision: 1,
		Subject:  truncateRunes("Verify: "+original.Subject, maxTaskSubject),
		Description: fmt.Sprintf("Verify task %s: %s\n\nAcceptance criteria and description of the original task:\n%s\n\n"+
			"Implemented by: %s\nWrite scopes: %s\n%s",
			original.ID, original.Subject, original.Description, implementer, scopesText(original), changeLocation(original)),
		Status:      TaskPending,
		BlockedBy:   []string{},
		WriteScopes: []string{},
		SessionID:   original.SessionID,
		Role:        verifierRole,
		Verify:      new(false),
		Verifies:    original.ID,
	}
}

// changeLocation tells a verifier where the work it checks is.
func changeLocation(original Task) string {
	if original.Worktree == "" {
		return "The change is in the shared working directory; inspect it with git status and git diff."
	}
	return fmt.Sprintf("The change is committed on branch %s in the task worktree %s, where your tools run; inspect it with git log and git diff.",
		original.Branch, original.Worktree)
}

// verdictResult is what a verdict leaves for the caller once its write
// lands.
type verdictResult struct {
	completedSubject string
	wake, leads      []string
	wakeTask         string
	// merge is set when a pass leaves the original's branch to merge: the
	// original stays in review until the merge succeeds.
	merge *mergeRequest
}

// applyVerdictLocked applies a verifier's verdict to the task check verifies.
// An original that already left review (reopened, deleted) is left alone, so
// a late verdict never rewrites work the board has moved on from. A pass of
// work with a worktree leaves the original in review for its merge. Callers
// must hold s.mu.
func (s *Service) applyVerdictLocked(scope map[string]bool, check Task, verifier Member, verdict, notes string) verdictResult {
	index, original, ok := s.taskIndexLocked(scope, check.Verifies)
	if !ok || original.Status != TaskInReview {
		return verdictResult{}
	}
	original.Revision++
	if verdict == "pass" {
		original.LastFailure = ""
		if original.Worktree != "" {
			s.state.Tasks[index] = original
			m := mergeRequestFor(original)
			return verdictResult{merge: &m}
		}
		original.Status = TaskCompleted
		s.state.Tasks[index] = original
		return verdictResult{completedSubject: original.Subject}
	}
	original.LastFailure = truncateText(notes, maxFailureNotes)
	original.Attempts++
	if original.Attempts >= maxTaskAttempts {
		original.Escalated = true
		s.state.Tasks[index] = original
		s.noteLeadLocked(original.SessionID, fmt.Sprintf(
			"Task %s (%s) failed verification %d times and stays in review, escalated. To recover, %s. Last notes:\n\n%s",
			original.ID, original.Subject, original.Attempts, taskRecoveryActions(original.Status), notes))
		return verdictResult{leads: []string{original.SessionID}}
	}
	original.Status = TaskInProgress
	s.state.Tasks[index] = original
	text := fmt.Sprintf("Verification of task %s failed:\n\n%s\n\nFix it and complete the task again.", original.ID, notes)
	if original.OwnerID == leadName {
		s.noteLeadLocked(original.SessionID, text)
		return verdictResult{leads: []string{original.SessionID}}
	}
	for _, member := range s.state.Members {
		if member.ID == original.OwnerID {
			s.appendMessageLocked(verifier.Name, member.Name, text)
			return verdictResult{wake: []string{member.Name}, wakeTask: original.ID}
		}
	}
	return verdictResult{}
}

// startReviewLocked sends task to review with a fresh check. Any older check
// of the same task is stale now, so it goes: a verdict on an earlier revision
// must not decide this one. The check is created in the same write that moved
// the task, so no reader ever sees work in review with nothing to review it.
// Callers must hold s.mu.
func (s *Service) startReviewLocked(task Task) {
	s.closeChecksLocked(task.ID, true)
	s.state.Tasks = append(s.state.Tasks, s.newVerificationTaskLocked(task))
}

// closeChecksLocked deletes the open verification tasks of originalID. A
// check in progress is closed only when closeStarted is set: a task that left
// review keeps the check its verifier is reviewing, because that verdict must
// still record (Review Focus 4), while a task that enters review again
// replaces every older check, since an earlier verdict must not decide work
// it never saw. Callers must hold s.mu.
func (s *Service) closeChecksLocked(originalID string, closeStarted bool) {
	for i := range s.state.Tasks {
		check := &s.state.Tasks[i]
		if check.Verifies != originalID || check.Status == TaskDeleted || check.Status == TaskCompleted {
			continue
		}
		if check.Status == TaskInProgress && !closeStarted {
			continue
		}
		check.Revision++
		check.Status = TaskDeleted
		check.OwnerID = ""
	}
}

// verifiesOwnWorkLocked reports whether task is a verification task for work
// member owns. A teammate must not check its own work, so such a claim or
// verdict is refused and the Lead decides instead. An original outside the
// caller's scope decides nothing. Callers must hold s.mu.
func (s *Service) verifiesOwnWorkLocked(scope map[string]bool, task Task, member Member) bool {
	if task.Verifies == "" {
		return false
	}
	_, original, ok := s.taskIndexLocked(scope, task.Verifies)
	return ok && original.OwnerID == member.ID
}

// checkWantedLocked reports whether the verification task check still has a
// task in review to decide. A check whose original moved on is stale. Callers
// must hold s.mu.
func (s *Service) checkWantedLocked(check Task) bool {
	original, ok := findTask(s.state, check.Verifies)
	return ok && original.Status == TaskInReview
}

// truncateRunes cuts s to at most n runes.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
