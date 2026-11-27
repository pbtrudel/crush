package agentteam

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/crush/internal/gitutil"
)

// mergeRequest is a task branch to merge into the Lead's tree once a write
// left its task in review for it.
type mergeRequest struct {
	taskID, sessionID, subject string
	worktree, branch           string
	// revision is the task's revision the merge is for. The write that
	// records the outcome applies only while the task still has it.
	revision int
}

// mergeRequestFor builds the merge of a task in review with a worktree.
func mergeRequestFor(task Task) mergeRequest {
	return mergeRequest{
		taskID:    task.ID,
		sessionID: task.SessionID,
		subject:   task.Subject,
		worktree:  task.Worktree,
		branch:    task.Branch,
		revision:  task.Revision,
	}
}

// commitWork commits everything in a task worktree, with the task subject as
// the message. An empty change set makes no commit and is no error.
func (s *Service) commitWork(ctx context.Context, worktree, subject string) error {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	_, err := gitutil.CommitAll(ctx, worktree, subject)
	return err
}

// commitHeldWork commits the worktrees of the tasks name leaves in progress,
// before its settle decides what happens to them, so work that goes to review
// or is merged is on its branch. It runs with no lock held and returns the
// tasks whose commit failed.
func (s *Service) commitHeldWork(name string) map[string]error {
	s.mu.Lock()
	member := s.stateMemberLocked(name)
	var work []Task
	for _, task := range s.state.Tasks {
		if member.ID != "" && task.OwnerID == member.ID && task.Status == TaskInProgress && task.Worktree != "" {
			work = append(work, task)
		}
	}
	s.mu.Unlock()
	if len(work) == 0 {
		return nil
	}
	ctx, cancel := gitContext()
	defer cancel()
	failed := map[string]error{}
	for _, task := range work {
		if err := s.commitWork(ctx, task.Worktree, task.Subject); err != nil {
			failed[task.ID] = fmt.Errorf("could not commit the worktree of task %s: %w", task.ID, err)
		}
	}
	return failed
}

// completePrep is the git work a complete needs before its write.
type completePrep struct {
	// commit is the worktree of in-progress work to commit.
	commit, subject string
	// merge is in-review work the Lead accepts.
	merge *mergeRequest
}

// prepareComplete reads, holding only s.mu, what git work req needs before
// its write. A request the write would refuse needs none, so a stale or
// unauthorized complete never reaches git with a commit or a merge. Committing
// in-progress work is its owner's call or the Lead's, as the write itself
// requires.
func (s *Service) prepareComplete(caller string, req UpdateTaskRequest) completePrep {
	s.mu.Lock()
	defer s.mu.Unlock()
	member, ok := s.memberLocked(caller)
	if !ok {
		return completePrep{}
	}
	_, task, ok := s.taskIndexLocked(s.scopeLocked(caller, req.SessionID), req.TaskID)
	if !ok || task.Revision != req.ExpectedRevision || task.Worktree == "" || task.Verifies != "" {
		return completePrep{}
	}
	switch {
	case task.Status == TaskInProgress && (member.Role == RoleLead || task.OwnerID == member.ID):
		return completePrep{commit: task.Worktree, subject: task.Subject}
	case task.Status == TaskInReview && member.Role == RoleLead:
		m := mergeRequestFor(task)
		return completePrep{merge: &m}
	}
	return completePrep{}
}

// runCompletePrep runs the git work of a complete with no lock held. A failed
// commit refuses the complete with the task unchanged. A refused merge leaves
// the task in review, escalated, and refuses the complete. It returns the
// merge that ran, so a refused write can say the merge stays.
func (s *Service) runCompletePrep(caller string, req UpdateTaskRequest) (*mergeRequest, error) {
	prep := s.prepareComplete(caller, req)
	if prep.commit == "" && prep.merge == nil {
		return nil, nil
	}
	ctx, cancel := gitContext()
	defer cancel()
	if prep.commit != "" {
		if err := s.commitWork(ctx, prep.commit, prep.subject); err != nil {
			return nil, newError(CodeTaskGit, "could not commit the work of task %s in %s: %v. The task is unchanged",
				req.TaskID, prep.commit, err)
		}
		return nil, nil
	}
	if err := s.mergeBranch(ctx, *prep.merge); err != nil {
		if !errors.Is(err, errShutdown) {
			s.settleMerge(*prep.merge, err)
		}
		return nil, newError(CodeTaskGit, "%s", mergeRefusedText(*prep.merge, err))
	}
	return prep.merge, nil
}

// mergeBranch merges m's branch into the Lead's tree with --no-ff. It refuses
// once the Team is shut down, so a merge never lands that no write could
// record.
func (s *Service) mergeBranch(ctx context.Context, m mergeRequest) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return errShutdown
	}
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	return gitutil.MergeNoFF(ctx, s.cfg.WorkingDir, m.branch, fmt.Sprintf("Merge team task %s: %s", m.taskID, m.subject))
}

// mergeTask merges m's branch, records the outcome and, after a merge, removes
// the worktree and starts what the completed task unblocked. caller is the
// member whose tool call or settle runs the merge, so its own run is never
// stopped to remove the worktree. Callers must hold no lock.
func (s *Service) mergeTask(ctx context.Context, m mergeRequest, caller string) {
	err := s.mergeBranch(ctx, m)
	if errors.Is(err, errShutdown) {
		return
	}
	if s.settleMerge(m, err) {
		s.removeTaskWorktree(ctx, worktreeRemoval{taskID: m.taskID, sessionID: m.sessionID, path: m.worktree, branch: m.branch}, caller)
	}
	s.dispatch(context.Background(), m.sessionID)
}

// settleMerge records the outcome of merging m's branch: the task completes,
// or stays in review escalated with a Lead notice. It applies only while the
// task is still the one the merge was for; a merge that happened for a task
// the board moved on stays in place and the Lead is told. It reports whether
// the task completed.
func (s *Service) settleMerge(m mergeRequest, mergeErr error) bool {
	var (
		completed bool
		noted     bool
		hook      func(sessionID, subject string)
		leadHook  func(sessionID string)
	)
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		hook, leadHook = s.taskCompletedHook, s.leadNotifyHook
		matched := false
		for i := range s.state.Tasks {
			task := &s.state.Tasks[i]
			if task.ID != m.taskID || task.SessionID != m.sessionID {
				continue
			}
			if task.Status != TaskInReview || task.Revision != m.revision {
				break
			}
			matched = true
			task.Revision++
			if mergeErr != nil {
				task.Escalated = true
				s.noteLeadLocked(m.sessionID, mergeRefusedText(m, mergeErr))
				noted = true
				break
			}
			task.Status = TaskCompleted
			task.Escalated = false
			task.LastFailure = ""
			task.Worktree, task.Branch = "", ""
			s.closeChecksLocked(task.ID, true)
			completed = true
			break
		}
		if !matched {
			if mergeErr != nil {
				return false, nil
			}
			s.noteLeadLocked(m.sessionID, mergeStaysText(m, "the board moved the task on before the merge was recorded", s.worktreeKeptLocked(m)))
			noted = true
		}
		s.notifyLocked()
		return true, nil
	})
	if completed && hook != nil {
		hook(m.sessionID, m.subject)
	}
	if noted {
		wakeLeads(leadHook, []string{m.sessionID})
	}
	return completed
}

// noteMergeStays tells the Lead that a merge it asked for landed but the
// write recording it was refused.
func (s *Service) noteMergeStays(m mergeRequest, cause error) {
	var (
		leadHook func(sessionID string)
		noted    bool
	)
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		leadHook = s.leadNotifyHook
		s.noteLeadLocked(m.sessionID, mergeStaysText(m, "the task update was refused: "+cause.Error(), s.worktreeKeptLocked(m)))
		s.notifyLocked()
		noted = true
		return true, nil
	})
	if noted {
		wakeLeads(leadHook, []string{m.sessionID})
	}
}

// mergeStaysText is the Lead notice for a merge no write recorded. kept says
// whether the board still holds the worktree, so the notice never claims a
// worktree another write already removed.
func mergeStaysText(m mergeRequest, reason string, kept bool) string {
	if kept {
		return fmt.Sprintf("Task %s (%s) was merged from branch %s, but %s. The merge stays in the Lead's tree, and the worktree at %s is kept.",
			m.taskID, m.subject, m.branch, reason, m.worktree)
	}
	return fmt.Sprintf("Task %s (%s) was merged from branch %s, but %s. The merge stays in the Lead's tree; the task no longer holds the worktree at %s, so nothing else is left behind.",
		m.taskID, m.subject, m.branch, reason, m.worktree)
}

// worktreeKeptLocked reports whether the board still holds m's worktree.
// Callers must hold s.mu.
func (s *Service) worktreeKeptLocked(m mergeRequest) bool {
	task, ok := s.taskByIDLocked(m.taskID, m.sessionID)
	return ok && task.Worktree == m.worktree
}

// mergeRefusedText is the Lead notice, and the Lead's error, for a merge git
// refused or that conflicted. It never claims the Lead's tree is unchanged:
// a merge that started may have left files behind, so the notice says the
// tree is as git left it.
func mergeRefusedText(m mergeRequest, err error) string {
	detail := err.Error()
	cleanup := "The merge was aborted or never started, and the Lead's tree is as git left it."
	var mergeErr *gitutil.MergeError
	if errors.As(err, &mergeErr) {
		detail = mergeErr.Reason
		if mergeErr.Output != "" {
			detail += ":\n\n" + mergeErr.Output
		}
		if mergeErr.AbortFailed {
			cleanup = "The merge could not be aborted, so it is still in progress in the Lead's tree; finish it, or undo it with git merge --abort."
		}
	}
	return fmt.Sprintf("Task %s (%s) could not be merged from branch %s into the Lead's tree: %s\n\n"+
		"%s The task stays in review, escalated, with its worktree at %s. "+
		"Merge the branch by hand and complete the task to accept it, or reopen it.",
		m.taskID, m.subject, m.branch, detail, cleanup, m.worktree)
}

// refreshView returns the current view of the task view shows, or view
// itself when the task is gone.
func (s *Service) refreshView(view TaskView) TaskView {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.state.Tasks {
		if task.ID == view.ID && task.SessionID == view.SessionID {
			return projectTaskView(s.state, task)
		}
	}
	return view
}
