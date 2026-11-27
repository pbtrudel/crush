package agentteam

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/charmbracelet/crush/internal/gitutil"
)

// gitTimeout bounds one git step of a task transition, so a wedged git
// cannot hold a tool call, a dispatch pass or a settle forever.
const gitTimeout = 2 * time.Minute

// gitContext is the context one git step runs under. Git work outlives the
// tool call that asked for it, so it is never cancelled with that call.
func gitContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), gitTimeout)
}

// Workspace is where a teammate's tools run while it holds a task with a git
// worktree: the worktree's root, the directory inside it that matches the
// Lead's working directory, and the task branch.
type Workspace struct {
	Root   string
	Dir    string
	Branch string
}

// repo reports the repository that holds the Lead's working directory, and
// that directory's prefix inside it. It runs git once per Service, outside
// every lock; ok is false outside a git repository or with no working
// directory, which turns worktrees off.
func (s *Service) repo() (root, prefix string, ok bool) {
	if s.cfg.WorkingDir == "" {
		return "", "", false
	}
	s.repoOnce.Do(func() {
		ctx, cancel := gitContext()
		defer cancel()
		top, err := gitutil.RepoRoot(ctx, s.cfg.WorkingDir)
		if err != nil {
			return
		}
		sub, err := gitutil.RepoPrefix(ctx, s.cfg.WorkingDir)
		if err != nil {
			return
		}
		s.repoRoot, s.repoPrefix, s.repoOK = top, sub, true
	})
	return s.repoRoot, s.repoPrefix, s.repoOK
}

// MemberWorkspace returns the workspace member's tools run in: the worktree
// of the scoped task it works on, or of the task its verification task
// checks. ok is false for the Lead and for a member with no such task; they
// work in the Lead's tree. It holds s.mu only to read the board.
func (s *Service) MemberWorkspace(member string) (Workspace, bool) {
	s.mu.Lock()
	ws, ok := s.memberWorktreeLocked(member)
	s.mu.Unlock()
	if !ok {
		return Workspace{}, false
	}
	_, prefix, _ := s.repo()
	ws.Dir = filepath.Join(ws.Root, filepath.FromSlash(prefix))
	return ws, true
}

// holdsWorktree reports whether task keeps a worktree for its owner: it is
// in progress or in review and has one. Work in review keeps its worktree
// until the verdict, so a running teammate never moves mid-run.
func holdsWorktree(task Task) bool {
	return (task.Status == TaskInProgress || task.Status == TaskInReview) && task.Worktree != ""
}

// memberWorktreeLocked finds the worktree of name's task: the scoped task it
// works on, or the work its verification task checks. Callers must hold
// s.mu.
func (s *Service) memberWorktreeLocked(name string) (Workspace, bool) {
	owner, ok := s.memberLocked(name)
	if !ok || owner.Role == RoleLead {
		return Workspace{}, false
	}
	for _, task := range s.state.Tasks {
		if task.OwnerID != owner.ID {
			continue
		}
		if task.Verifies != "" {
			// A verifier runs in the worktree of the work it checks,
			// while its check is in progress.
			if task.Status != TaskInProgress {
				continue
			}
			if original, found := findTask(s.state, task.Verifies); found && original.Worktree != "" {
				return Workspace{Root: original.Worktree, Branch: original.Branch}, true
			}
			continue
		}
		if holdsWorktree(task) {
			return Workspace{Root: task.Worktree, Branch: task.Branch}, true
		}
	}
	return Workspace{}, false
}

// holdsWorktreeLocked reports whether memberID owns a task other than except
// that keeps a worktree. A member works in one worktree at a time. Callers
// must hold s.mu.
func (s *Service) holdsWorktreeLocked(memberID, except string) bool {
	for _, task := range s.state.Tasks {
		if task.ID == except || task.OwnerID != memberID {
			continue
		}
		if task.Verifies == "" && holdsWorktree(task) {
			return true
		}
	}
	return false
}

// refusalKey names a task in worktreeRefused. Task ids repeat across Lead
// sessions in old files, so the session is part of the key.
func refusalKey(sessionID, taskID string) string {
	return sessionID + "/" + taskID
}

// worktreeRefusedLocked reports whether task was handed back for a failed
// worktree at its current revision. Callers must hold s.mu.
func (s *Service) worktreeRefusedLocked(task Task) bool {
	revision, ok := s.worktreeRefused[refusalKey(task.SessionID, task.ID)]
	return ok && revision == task.Revision
}

// createWorktree creates the worktree of taskID from the Lead's HEAD, on
// branch crush/team-<task id>. It returns empty values and no error outside
// a git repository, where tasks share the Lead's tree as before. Callers
// must hold no lock.
func (s *Service) createWorktree(ctx context.Context, taskID string) (path, branch string, err error) {
	if _, _, ok := s.repo(); !ok {
		return "", "", nil
	}
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	return gitutil.CreateWorktreeFrom(ctx, s.cfg.WorkingDir, "team-"+taskID, "HEAD")
}

// recordWorktree stores a new worktree on its task in a compare-and-set
// write against the claim it was made for: same task, owner and revision,
// still in progress, with no worktree yet. The revision does not change, so
// the brief's expected_revision stays valid. It reports whether it stored.
func (s *Service) recordWorktree(taskID, sessionID, ownerID string, revision int, path, branch string) bool {
	recorded := false
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		for i := range s.state.Tasks {
			task := &s.state.Tasks[i]
			if task.ID != taskID || task.SessionID != sessionID {
				continue
			}
			if task.OwnerID != ownerID || task.Status != TaskInProgress || task.Revision != revision || task.Worktree != "" {
				return false, nil
			}
			task.Worktree, task.Branch = path, branch
			recorded = true
			s.notifyLocked()
			return true, nil
		}
		return false, nil
	})
	return recorded
}

// discardWorktree removes a worktree no task names, with its branch. It is
// best effort: a leftover is logged, not noticed, because no task owns it.
func (s *Service) discardWorktree(ctx context.Context, path, branch string) {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	if err := gitutil.RemoveWorktree(ctx, s.cfg.WorkingDir, path, true); err != nil {
		slog.Warn("Could not remove an unused team worktree", "path", path, "error", err)
		return
	}
	if err := gitutil.DeleteBranch(ctx, s.cfg.WorkingDir, branch, true); err != nil {
		slog.Warn("Could not delete an unused team branch", "branch", branch, "error", err)
	}
}

// worktreeRemoval is a task worktree to remove once a write lands.
type worktreeRemoval struct {
	taskID, sessionID string
	path, branch      string
	// forceBranch deletes a branch that was never merged, as a deleted
	// task's is; a merged branch is deleted with the safe form.
	forceBranch bool
}

// removeTaskWorktree removes a task's worktree and branch after the state
// change that ends them landed. It is best effort: a failure is logged and
// told to the Lead, and the state change stands. Callers must hold no lock.
func (s *Service) removeTaskWorktree(ctx context.Context, r worktreeRemoval) {
	s.gitMu.Lock()
	err := gitutil.RemoveWorktree(ctx, s.cfg.WorkingDir, r.path, true)
	if err == nil {
		err = gitutil.DeleteBranch(ctx, s.cfg.WorkingDir, r.branch, r.forceBranch)
	}
	s.gitMu.Unlock()
	if err == nil {
		return
	}
	slog.Warn("Could not remove a team task worktree", "task", r.taskID, "path", r.path, "error", err)
	var (
		leadHook func(sessionID string)
		noted    bool
	)
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		leadHook = s.leadNotifyHook
		s.noteLeadLocked(r.sessionID, fmt.Sprintf(
			"Could not remove the git worktree of task %s at %s (branch %s): %v. Remove it by hand with git worktree remove --force and git branch -D.",
			r.taskID, r.path, r.branch, err))
		s.notifyLocked()
		noted = true
		return true, nil
	})
	if noted {
		wakeLeads(leadHook, []string{r.sessionID})
	}
}

// placeStart gives the scoped task of a planned start its worktree before
// its member starts. A task that has one keeps it, and a task without write
// scopes, or outside a git repository, starts as before. The worktree is
// created with no lock held and recorded against the claim, so a claim the
// Lead changed meanwhile gets nothing and the new worktree is removed. It
// reports whether the start goes ahead, with its brief rendered for the
// worktree.
func (s *Service) placeStart(start dispatchStart) (dispatchStart, bool) {
	if !start.task.wantsWorktree() || start.task.Worktree != "" {
		return start, true
	}
	ctx, cancel := gitContext()
	defer cancel()
	path, branch, err := s.createWorktree(ctx, start.taskID)
	if err != nil {
		s.worktreeFailed(start, err)
		return start, false
	}
	if path == "" {
		return start, true
	}
	if !s.recordWorktree(start.taskID, start.sessionID, start.member.ID, start.revision, path, branch) {
		s.discardWorktree(ctx, path, branch)
		s.abandonStart(start)
		return start, false
	}
	start.task.Worktree, start.task.Branch = path, branch
	start.prompt = taskBrief(start.task)
	return start, true
}

// abandonStart undoes the in-memory marks of a planned start that will not
// run because its claim moved on: the member is no longer starting, and a
// member created for it reads as idle instead of provisioning forever.
func (s *Service) abandonStart(start dispatchStart) {
	_ = s.write(mustApply, func() (bool, error) {
		delete(s.starting, start.member.Name)
		if claim, ok := s.claims[start.member.Name]; ok &&
			claim.taskID == start.taskID && claim.revision == start.revision {
			delete(s.claims, start.member.Name)
		}
		if s.closed {
			return false, nil
		}
		changed := s.idleProvisionedLocked(start.member.Name)
		if changed {
			s.notifyLocked()
		}
		return changed, nil
	})
}

// idleProvisionedLocked marks a member created for a start that never ran
// as inactive. Callers must hold s.mu.
func (s *Service) idleProvisionedLocked(name string) bool {
	for i := range s.state.Members {
		if s.state.Members[i].Name == name && s.state.Members[i].Status == StatusProvisioning {
			s.state.Members[i].Status = StatusInactive
			return true
		}
	}
	return false
}

// worktreeRelease names the claim a failed worktree creation hands back:
// the planned start of a dispatch pass, or the claim a caller just made.
// member is empty for a manual claim, which holds no dispatcher state.
type worktreeRelease struct {
	member    string
	taskID    string
	sessionID string
	ownerID   string
	revision  int
}

// releaseWorktreeClaim hands back a claim whose worktree could not be
// created, in one write that also frees the member it was planned for. No
// attempt is charged, since the work never started. The Lead is told once,
// because the task is remembered as refused at its new revision until the
// Lead changes it. It reports whether the claim was released.
func (s *Service) releaseWorktreeClaim(r worktreeRelease, err error) bool {
	var (
		leadHook func(sessionID string)
		released bool
	)
	_ = s.write(mustApply, func() (bool, error) {
		if r.member != "" {
			delete(s.starting, r.member)
		}
		if s.closed {
			// Shutdown hands a recorded claim back itself.
			return false, nil
		}
		if r.member != "" {
			if claim, ok := s.claims[r.member]; ok &&
				claim.taskID == r.taskID && claim.revision == r.revision {
				delete(s.claims, r.member)
			}
			s.idleProvisionedLocked(r.member)
		}
		released = s.releaseForWorktreeLocked(r.taskID, r.sessionID, r.ownerID, r.revision, err)
		if !released {
			return false, nil
		}
		leadHook = s.leadNotifyHook
		s.notifyLocked()
		return true, nil
	})
	if released {
		wakeLeads(leadHook, []string{r.sessionID})
	}
	return released
}

// worktreeFailed hands back a dispatcher claim whose worktree could not be
// created. No attempt is charged, since the work never started, and the
// member is free again. The Lead is told once: the dispatcher leaves the
// task until the Lead changes it, instead of failing again on every pass.
func (s *Service) worktreeFailed(start dispatchStart, err error) {
	s.releaseWorktreeClaim(worktreeRelease{
		member:    start.member.Name,
		taskID:    start.taskID,
		sessionID: start.sessionID,
		ownerID:   start.member.ID,
		revision:  start.revision,
	}, err)
}

// releaseForWorktreeLocked puts a claimed task whose worktree failed back
// to pending, uncharged, remembers it as refused at its new revision, and
// tells the Lead. It reports false when the claim moved on and nothing was
// released. Callers must hold s.mu.
func (s *Service) releaseForWorktreeLocked(taskID, sessionID, ownerID string, revision int, err error) bool {
	for i := range s.state.Tasks {
		task := &s.state.Tasks[i]
		if task.ID != taskID || task.SessionID != sessionID {
			continue
		}
		if task.OwnerID != ownerID || task.Status != TaskInProgress || task.Revision != revision {
			return false
		}
		task.Status = TaskPending
		task.OwnerID = ""
		task.Revision++
		s.worktreeRefused[refusalKey(sessionID, taskID)] = task.Revision
		s.noteLeadLocked(sessionID, fmt.Sprintf(
			"Task %s (%s) could not get its git worktree: %v. It is pending again with no attempt counted; the dispatcher tries again after you edit the task.",
			task.ID, task.Subject, err))
		return true
	}
	return false
}

// placeClaim gives a task a teammate just claimed, or was assigned, its
// worktree. It runs after the claim's write, with no lock held. A failure
// hands the claim back uncharged and tells the Lead, and the caller gets
// the error, so a teammate never starts the work in the Lead's tree.
func (s *Service) placeClaim(view TaskView) (TaskView, error) {
	ctx, cancel := gitContext()
	defer cancel()
	path, branch, err := s.createWorktree(ctx, view.ID)
	if err != nil {
		s.releaseUnplaced(view, err)
		return TaskView{}, newError(CodeTaskGit,
			"could not create the git worktree for task %s: %v. The claim was released and the task is pending again", view.ID, err)
	}
	if path == "" {
		return view, nil
	}
	if !s.recordWorktree(view.ID, view.SessionID, view.OwnerID, view.Revision, path, branch) {
		// The board moved the claim on between its write and this one, so
		// the worktree belongs to nobody.
		s.discardWorktree(ctx, path, branch)
		return view, nil
	}
	view.Worktree, view.Branch = path, branch
	return view, nil
}

// releaseUnplaced hands back a claim whose worktree failed.
func (s *Service) releaseUnplaced(view TaskView, err error) {
	s.releaseWorktreeClaim(worktreeRelease{
		taskID:    view.ID,
		sessionID: view.SessionID,
		ownerID:   view.OwnerID,
		revision:  view.Revision,
	}, err)
}
