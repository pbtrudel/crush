package agentteam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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

// errWorktreeClaimMoved reports a claim the board changed while git ran, so
// the worktree git created belongs to nobody.
var errWorktreeClaimMoved = errors.New("the claim changed while its git worktree was created")

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

// worktreeReady reports whether a teammate's scoped task can get a worktree.
// It reads the repository repo resolved outside the locks, so callers may
// hold s.mu; it never runs git itself.
func (s *Service) worktreeReady() bool {
	return s.cfg.WorkingDir != "" && s.repoOK
}

// dirExists reports whether path is a directory that exists.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// MemberWorkspace returns the workspace member's tools run in: the worktree
// of the scoped task it works on, or of the task its verification task
// checks. ok is false for the Lead and for a member with no such task; they
// work in the Lead's tree. It holds s.mu only to read the board.
func (s *Service) MemberWorkspace(member string) (Workspace, bool) {
	s.mu.Lock()
	ws, ok := s.memberWorktreeLocked(member)
	s.mu.Unlock()
	if !ok || !dirExists(ws.Root) {
		// A worktree the filesystem no longer holds places nobody: the
		// next placement recreates it first.
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
// that keeps a worktree, or that is in progress and will get one: a claim
// whose worktree is still being created holds it too, so a parallel claim
// never gives the member a second one. A member works in one worktree at a
// time. Callers must hold s.mu.
func (s *Service) holdsWorktreeLocked(memberID, except string) bool {
	// Outside a repository nothing gets a worktree, and a member may hold
	// several scoped tasks, as it could before worktrees existed.
	if !s.worktreeReady() {
		return false
	}
	for _, task := range s.state.Tasks {
		if task.ID == except || task.OwnerID != memberID || !task.wantsWorktree() {
			continue
		}
		if task.Status == TaskInProgress || holdsWorktree(task) {
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

// placementResult says what recording a placed worktree found.
type placementResult int

const (
	// placedRecorded means the task now records the worktree just placed.
	placedRecorded placementResult = iota
	// placedAdopted means the task already recorded its own worktree, so
	// the one just placed belongs to nobody.
	placedAdopted
	// placedLost means the claim moved on, so no task names the worktree.
	placedLost
)

// worktreeAction is what a store does with a task whose claim it matches.
type worktreeAction int

const (
	// worktreeStore writes the placed path and branch onto the task.
	worktreeStore worktreeAction = iota
	// worktreeAdopt keeps the worktree the task already records.
	worktreeAdopt
)

// recordWorktree stores a new worktree on its task in a compare-and-set
// write against the claim it was made for: same task, session and owner,
// still in progress. A task that already recorded a worktree was placed by
// another claim first, so it is adopted. The revision is not part of the
// check, because an edit that lands while git worktree add runs must not
// strand the claim; the worktree is bookkeeping, not a new revision.
func (s *Service) recordWorktree(taskID, sessionID, ownerID, path, branch string) (Task, placementResult) {
	return s.storeWorktree(taskID, sessionID, ownerID, path, branch, func(task Task) worktreeAction {
		if task.Worktree == "" {
			return worktreeStore
		}
		return worktreeAdopt
	})
}

// repointWorktree moves a task's record to the path a restore just wrote,
// under the same claim check, when the path it recorded could not be used.
func (s *Service) repointWorktree(taskID, sessionID, ownerID, oldPath, path, branch string) (Task, placementResult) {
	return s.storeWorktree(taskID, sessionID, ownerID, path, branch, func(task Task) worktreeAction {
		if task.Worktree == oldPath {
			return worktreeStore
		}
		return worktreeAdopt
	})
}

// adoptWorktree records nothing and only checks that the claim still holds,
// so a restore whose path did not change still learns when the board moved
// the task on.
func (s *Service) adoptWorktree(taskID, sessionID, ownerID, path, branch string) (Task, placementResult) {
	return s.storeWorktree(taskID, sessionID, ownerID, path, branch, func(task Task) worktreeAction {
		return worktreeAdopt
	})
}

// storeWorktree points the task's record at path and branch when decide asks
// for it, and otherwise adopts the worktree the record already holds. It
// returns the task as the record now holds it, and whether the placement was
// stored, adopted, or lost to a claim the board moved on. Callers must hold
// no lock.
func (s *Service) storeWorktree(taskID, sessionID, ownerID, path, branch string, decide func(Task) worktreeAction) (Task, placementResult) {
	var (
		stored  Task
		outcome = placedLost
	)
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		for i := range s.state.Tasks {
			task := &s.state.Tasks[i]
			if task.ID != taskID || task.SessionID != sessionID ||
				task.OwnerID != ownerID || task.Status != TaskInProgress {
				continue
			}
			if decide(*task) == worktreeAdopt {
				stored, outcome = *task, placedAdopted
				return false, nil
			}
			task.Worktree, task.Branch = path, branch
			stored, outcome = *task, placedRecorded
			s.notifyLocked()
			return true, nil
		}
		return false, nil
	})
	return stored, outcome
}

// discardWorktree removes a worktree no task names, with its branch. It is
// best effort: a leftover is logged, not noticed, because no task owns it.
func (s *Service) discardWorktree(ctx context.Context, path, branch string) {
	s.removeUnusedWorktree(ctx, path, branch)
}

// discardRestoredWorktree removes a restored worktree no task names. Its
// branch stays: it holds the commits of the work under review.
func (s *Service) discardRestoredWorktree(ctx context.Context, path string) {
	s.removeUnusedWorktree(ctx, path, "")
}

// placedWorktree is the worktree a placement found or made for a task.
type placedWorktree struct {
	path   string
	branch string
	// made is set when this placement created the worktree: opened fresh
	// from the Lead's HEAD, or restored because its directory was gone. A
	// reused worktree was not made here, so it is never removed here.
	made bool
}

// discardUnnamedWorktree removes a worktree this placement made when the
// board does not name it. A worktree the task still records belongs to
// whoever holds the task now, and a reused one was not made here, so neither
// goes. A worktree opened from the Lead's HEAD takes its fresh branch with
// it; a restored one keeps its branch and its commits.
func (s *Service) discardUnnamedWorktree(ctx context.Context, claimed Task, placed placedWorktree) {
	if !placed.made || s.recordsWorktreePath(claimed.ID, claimed.SessionID, placed.path) {
		return
	}
	if claimed.Worktree == "" {
		s.discardWorktree(ctx, placed.path, placed.branch)
		return
	}
	s.discardRestoredWorktree(ctx, placed.path)
}

// recordsWorktreePath reports whether the board records path as the worktree
// of the task, whoever owns it now. It holds s.mu only to read.
func (s *Service) recordsWorktreePath(taskID, sessionID, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.state.Tasks {
		if task.ID == taskID && task.SessionID == sessionID {
			return task.Worktree == path
		}
	}
	return false
}

// removeUnusedWorktree removes a worktree no task names, and its branch when
// branch is not empty. It is best effort: a leftover is logged, not noticed,
// because no task owns it.
func (s *Service) removeUnusedWorktree(ctx context.Context, path, branch string) {
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	if err := gitutil.RemoveWorktree(ctx, s.cfg.WorkingDir, path, true); err != nil {
		slog.Warn("Could not remove an unused team worktree", "path", path, "error", err)
		return
	}
	if branch == "" {
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
// change that ends them landed. A run still working in the worktree, other
// than caller's own, is stopped first and its settle waited for, so no tool
// call of that turn reaches the Lead's tree once the worktree is gone. The
// caller's own run is never stopped: it cannot settle until the tool call it
// is running returns, and its later calls already resolve to the Lead's tree
// once the task leaves. It is best effort: a failure is logged and told to
// the Lead, and the state change stands. Callers must hold no lock.
func (s *Service) removeTaskWorktree(ctx context.Context, r worktreeRemoval, caller string) {
	if owner := s.taskOwnerName(r.taskID, r.sessionID); owner != "" && owner != caller {
		s.stopMemberRun(owner, false)
	}
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
// its member starts. A task that has a usable one keeps it, a task whose
// worktree went away gets it back, and a task without write scopes, or
// outside a git repository, starts as before. The worktree is prepared with
// no lock held and recorded against the claim. It reports whether the start
// goes ahead, with its brief rendered for the worktree; a claim the board
// changed meanwhile is handed back like a failed creation.
func (s *Service) placeStart(start dispatchStart) (dispatchStart, bool) {
	if !start.task.wantsWorktree() {
		return start, true
	}
	if start.task.Worktree != "" && dirExists(start.task.Worktree) {
		return start, true
	}
	ctx, cancel := gitContext()
	defer cancel()
	placed, err := s.placeWorktree(ctx, start.task)
	if err != nil {
		s.worktreeFailed(start, err)
		return start, false
	}
	if placed.path == "" {
		return start, true
	}
	task, outcome := s.recordPlacedWorktree(start.task, placed.path, placed.branch)
	switch outcome {
	case placedLost:
		// The task still records the worktree this placement made only
		// when the board moved on after naming it, and then it belongs to
		// the member that holds the task now.
		s.discardUnnamedWorktree(ctx, start.task, placed)
		s.worktreeFailed(start, errWorktreeClaimMoved)
		return start, false
	case placedAdopted:
		// Another claim placed this task's worktree first, so the one made
		// here belongs to nobody.
		s.discardUnnamedWorktree(ctx, start.task, placed)
	}
	start.task, start.revision = task, task.Revision
	start.prompt = taskBrief(start.task)
	return start, true
}

// placeWorktree gives a claimed task a usable worktree: it opens one for a
// task that has none, and restores a recorded one whose directory went away,
// from its branch when the branch still exists and from the Lead's HEAD
// otherwise. It returns an empty placed worktree, and no error, when tasks
// share the Lead's tree. Callers must hold no lock.
func (s *Service) placeWorktree(ctx context.Context, claimed Task) (placed placedWorktree, err error) {
	if claimed.Worktree == "" {
		path, branch, err := s.createWorktree(ctx, claimed.ID)
		return placedWorktree{path: path, branch: branch, made: path != ""}, err
	}
	if dirExists(claimed.Worktree) {
		return placedWorktree{path: claimed.Worktree, branch: claimed.Branch}, nil
	}
	if _, _, ok := s.repo(); !ok {
		return placedWorktree{}, fmt.Errorf("task %s records the worktree %s, but the working directory is not in a git repository",
			claimed.ID, claimed.Worktree)
	}
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	restored, err := gitutil.RestoreWorktree(ctx, s.cfg.WorkingDir, claimed.Worktree, claimed.Branch, "HEAD")
	if err != nil {
		return placedWorktree{}, err
	}
	return placedWorktree{path: restored, branch: claimed.Branch, made: true}, nil
}

// recordPlacedWorktree stores the worktree a claim just got against that
// claim, and returns the task as the write left it. A restore whose path did
// not change still checks the claim, so a task the board deleted meanwhile
// is not launched. A task that already records another worktree adopts it.
func (s *Service) recordPlacedWorktree(claimed Task, path, branch string) (Task, placementResult) {
	switch {
	case claimed.Worktree == "":
		return s.recordWorktree(claimed.ID, claimed.SessionID, claimed.OwnerID, path, branch)
	case path != claimed.Worktree || branch != claimed.Branch:
		return s.repointWorktree(claimed.ID, claimed.SessionID, claimed.OwnerID, claimed.Worktree, path, branch)
	default:
		return s.adoptWorktree(claimed.ID, claimed.SessionID, claimed.OwnerID, path, branch)
	}
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
	// revision matches this process's recorded claim, not the task: the
	// task is released by its owner, so an edit cannot strand it.
	revision int
}

// releaseWorktreeClaim hands back a claim whose worktree could not be
// created, in one write that also frees the member it was planned for. No
// attempt is charged, since the work never started. The Lead is told once,
// because the task is remembered as refused at its new revision until the
// Lead changes it. It reports whether the claim was released, and whether
// the Team was shutting down instead of releasing it.
func (s *Service) releaseWorktreeClaim(r worktreeRelease, err error) (released, closed bool) {
	var leadHook func(sessionID string)
	_ = s.write(mustApply, func() (bool, error) {
		if r.member != "" {
			delete(s.starting, r.member)
		}
		if s.closed {
			// Shutdown hands a recorded claim back itself.
			closed = true
			return false, nil
		}
		if r.member != "" {
			// The claim map is this process's own bookkeeping, so it is
			// matched on the revision it recorded; the task itself is not.
			if claim, ok := s.claims[r.member]; ok &&
				claim.taskID == r.taskID && claim.revision == r.revision {
				delete(s.claims, r.member)
			}
			s.idleProvisionedLocked(r.member)
		}
		released = s.releaseForWorktreeLocked(r.taskID, r.sessionID, r.ownerID, err)
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
	return released, closed
}

// worktreeFailed hands back a dispatcher claim whose worktree could not be
// created. No attempt is charged, since the work never started, and the
// member is free again. The Lead is told once: the dispatcher leaves the
// task until the Lead changes it, instead of failing again on every pass.
func (s *Service) worktreeFailed(start dispatchStart, err error) {
	_, _ = s.releaseWorktreeClaim(worktreeRelease{
		member:    start.member.Name,
		taskID:    start.taskID,
		sessionID: start.sessionID,
		ownerID:   start.member.ID,
		revision:  start.revision,
	}, err)
}

// releaseForWorktreeLocked puts a claimed task whose worktree failed back
// to pending, uncharged, remembers it as refused at its new revision, and
// tells the Lead. The claim is matched on its owner, not its revision, so an
// edit that lands while git runs does not leave the task in progress with
// nobody to run it. It reports false when the claim moved on and nothing was
// released. Callers must hold s.mu.
func (s *Service) releaseForWorktreeLocked(taskID, sessionID, ownerID string, err error) bool {
	for i := range s.state.Tasks {
		task := &s.state.Tasks[i]
		if task.ID != taskID || task.SessionID != sessionID {
			continue
		}
		if task.OwnerID != ownerID || task.Status != TaskInProgress {
			return false
		}
		if task.Worktree != "" && dirExists(task.Worktree) {
			// The task already has the worktree its member works in, so
			// this failure belongs to an older claim: releasing it would
			// strand live work.
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
	claimed := Task{
		ID:        view.ID,
		SessionID: view.SessionID,
		OwnerID:   view.OwnerID,
		Worktree:  view.Worktree,
		Branch:    view.Branch,
	}
	ctx, cancel := gitContext()
	defer cancel()
	placed, err := s.placeWorktree(ctx, claimed)
	if err != nil {
		released, closed := s.releaseUnplaced(view, err)
		return TaskView{}, newError(CodeTaskGit, "could not create the git worktree of task %s: %v. %s",
			view.ID, err, worktreeReleaseNote(released, closed))
	}
	if placed.path == "" {
		return view, nil
	}
	_, outcome := s.recordPlacedWorktree(claimed, placed.path, placed.branch)
	switch outcome {
	case placedLost:
		// The board moved the claim on between its write and this one. A
		// worktree the task still records stays: it is the worktree of
		// whoever holds the task now.
		s.discardUnnamedWorktree(ctx, claimed, placed)
		released, closed := s.releaseUnplaced(view, errWorktreeClaimMoved)
		return TaskView{}, newError(CodeTaskGit, "could not record the git worktree of task %s: %v. %s",
			view.ID, errWorktreeClaimMoved, worktreeReleaseNote(released, closed))
	case placedAdopted:
		// Another claim placed this task's worktree first, so the one made
		// here belongs to nobody.
		s.discardUnnamedWorktree(ctx, claimed, placed)
	}
	// The board, not this call, is the truth: an edit that landed while git
	// ran is reported with its revision, and an adopted worktree is the one
	// the task records.
	return s.taskViewNow(view.ID, view.SessionID, view), nil
}

// worktreeReleaseNote says what happened to a claim whose worktree failed,
// so a refusal never claims a release that did not happen.
func worktreeReleaseNote(released, closed bool) string {
	switch {
	case released:
		return "The claim was released and the task is pending again"
	case closed:
		return "The Team is shutting down"
	default:
		return "The claim had already moved on, so the task is left as the board holds it"
	}
}

// taskViewNow returns the board's current view of a task, or fallback when
// the board no longer holds it. It holds s.mu only to read.
func (s *Service) taskViewNow(taskID, sessionID string, fallback TaskView) TaskView {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.state.Tasks {
		if task.ID == taskID && task.SessionID == sessionID {
			return projectTaskView(s.state, task)
		}
	}
	return fallback
}

// releaseUnplaced hands back a claim whose worktree failed.
func (s *Service) releaseUnplaced(view TaskView, err error) (released, closed bool) {
	return s.releaseWorktreeClaim(worktreeRelease{
		taskID:    view.ID,
		sessionID: view.SessionID,
		ownerID:   view.OwnerID,
		revision:  view.Revision,
	}, err)
}

// runSettleWait bounds the wait for a stopped run to settle, so a wedged turn
// cannot hold a tool call forever.
const runSettleWait = 30 * time.Second

// taskByIDLocked finds the copy of the task with id in sessionID. Old files
// may hold one id twice, so the first copy wins. Callers must hold s.mu.
func (s *Service) taskByIDLocked(taskID, sessionID string) (Task, bool) {
	for _, task := range s.state.Tasks {
		if task.ID == taskID && task.SessionID == sessionID {
			return task, true
		}
	}
	return Task{}, false
}

// taskOwnerNameLocked returns the name of the teammate that owns task, or ""
// when the Lead or nobody owns it. Callers must hold s.mu.
func (s *Service) taskOwnerNameLocked(task Task) string {
	if task.OwnerID == "" {
		return ""
	}
	for _, member := range s.state.Members {
		if member.ID != task.OwnerID {
			continue
		}
		if member.Role == RoleLead {
			return ""
		}
		return member.Name
	}
	return ""
}

// taskOwnerName returns the name of the teammate that owns the task, or ""
// when the task is gone or the Lead owns it. It holds s.mu only to read.
func (s *Service) taskOwnerName(taskID, sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.taskByIDLocked(taskID, sessionID)
	if !ok {
		return ""
	}
	return s.taskOwnerNameLocked(task)
}

// stopDeleteRun stops the run of the member that owns the task a delete is
// about to end, when the task is in progress in its own worktree, so the
// run's settle commits the work the turn left there before the delete
// removes the worktree. The member's in-progress tasks are held for that
// settle, which the caller's write then ends. A delete the write would refuse
// stops nothing, so a stale or unauthorized request never interrupts a turn,
// and a member deleting its own task stops nothing either: its own run cannot
// settle until that call returns. It returns the member whose run was
// stopped, so the caller can hand its held work back. Callers must hold no
// lock.
func (s *Service) stopDeleteRun(caller string, req UpdateTaskRequest) (stopped bool, member Member) {
	if req.Action != "delete" {
		return false, Member{}
	}
	s.mu.Lock()
	callerMember, ok := s.memberLocked(caller)
	if !ok {
		s.mu.Unlock()
		return false, Member{}
	}
	_, task, ok := s.taskIndexLocked(s.scopeLocked(caller, req.SessionID), req.TaskID)
	if !ok || task.Revision != req.ExpectedRevision || task.Status == TaskDeleted {
		s.mu.Unlock()
		return false, Member{}
	}
	if callerMember.Role != RoleLead && task.OwnerID != callerMember.ID {
		s.mu.Unlock()
		return false, Member{}
	}
	for _, other := range s.state.Tasks {
		if other.Status != TaskDeleted && other.ID != task.ID && slices.Contains(other.BlockedBy, task.ID) {
			s.mu.Unlock()
			return false, Member{}
		}
	}
	if task.Worktree == "" || task.Status != TaskInProgress {
		s.mu.Unlock()
		return false, Member{}
	}
	name := s.taskOwnerNameLocked(task)
	s.mu.Unlock()
	if name == "" || name == caller {
		return false, Member{}
	}
	stopped, _ = s.stopMemberRun(name, true)
	if !stopped {
		return false, Member{}
	}
	s.mu.Lock()
	member, _ = s.memberLocked(name)
	s.mu.Unlock()
	return stopped, member
}

// releaseStoppedWork hands back the in-progress tasks a stopped run left when
// the write that was meant to end them refused, so they never stay in
// progress under a member nothing runs. Callers must hold no lock.
func (s *Service) releaseStoppedWork(member Member) {
	if member.ID == "" {
		return
	}
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			return false, nil
		}
		held := false
		for _, task := range s.state.Tasks {
			if task.OwnerID == member.ID && task.Status == TaskInProgress {
				held = true
				break
			}
		}
		if !held {
			return false, nil
		}
		s.releaseHeldLocked(member.ID)
		s.notifyLocked()
		return true, nil
	})
}

// stopMemberRun interrupts name's live run, if any, and waits for its settle
// write to land, so nothing of that turn runs any more. With hold set, the
// settle leaves the member's in-progress tasks for its caller, which commits
// their work and then ends them. It reports whether a run was stopped and
// whether its settle landed. Callers must hold no lock.
func (s *Service) stopMemberRun(name string, hold bool) (stopped, settled bool) {
	s.mu.Lock()
	run, live := s.runs[name]
	if !live || s.closed {
		s.mu.Unlock()
		return false, false
	}
	if hold {
		s.holds[name] = true
	}
	s.mu.Unlock()
	run.Cancel()
	settled = s.waitRunSettled(name, run)
	if hold && !settled {
		// No settle took the hold, so one left here would surprise a
		// later turn.
		s.mu.Lock()
		delete(s.holds, name)
		s.mu.Unlock()
	}
	return true, settled
}

// waitRunSettled waits until the settle write that records run landed, so
// the turn can no longer touch its worktree. It reports false when the wait
// ran out or the Team shut down first.
func (s *Service) waitRunSettled(name string, run Run) bool {
	s.mu.Lock()
	settlement, ok := s.runSettled[name]
	s.mu.Unlock()
	if !ok || settlement.run != run {
		// Its settle write already landed.
		return true
	}
	timer := time.NewTimer(runSettleWait)
	defer timer.Stop()
	select {
	case <-settlement.done:
		return true
	case <-s.done:
		slog.Warn("A stopped team run did not settle before shutdown", "member", name)
		return false
	case <-timer.C:
		slog.Warn("A stopped team run did not settle in time", "member", name)
		return false
	}
}
