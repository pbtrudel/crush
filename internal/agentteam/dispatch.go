package agentteam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// maxTaskAttempts is how many failed starts, failed turns or failed
// verifications a task takes before the dispatcher leaves it to the Lead.
const maxTaskAttempts = 3

// dispatchRetryDelay is how long a skipped pass waits before its one retry.
// The refused write already waited the whole lock timeout, so the retry only
// has to come back after the other writer has had time to finish: the delay
// is a short backoff after that wait, not a second copy of it.
const dispatchRetryDelay = 200 * time.Millisecond

// dispatchClaim is the task the dispatcher claimed for a member. It stays
// recorded until the start records a run or settles its failure, so Shutdown
// can hand the exact claim back instead of guessing at a member's tasks.
type dispatchClaim struct {
	taskID   string
	revision int
}

// dispatchStart is one member start a pass planned under the lock.
type dispatchStart struct {
	member    Member
	taskID    string
	sessionID string
	revision  int
	prompt    string
}

// dispatch starts teammates for the ready tasks of root's team that name a
// role. It plans every claim and spawn in one write and starts the members
// after the write returns, so Runner.Start never runs under a lock. A pass
// that cannot get the state lock is skipped and retried once.
func (s *Service) dispatch(ctx context.Context, root string) {
	s.dispatchPass(ctx, root, true)
}

// dispatchPass is dispatch with the retry decision. A retry is one per
// skipped pass, so a pass that is itself a retry never schedules another;
// the next write runs a fresh pass instead, and the retry chain terminates
// even while another process keeps the lock.
func (s *Service) dispatchPass(ctx context.Context, root string, retry bool) {
	if root == "" || !s.cfg.autostart() {
		return
	}
	// Most writes leave nothing to start. A cheap read under s.mu skips the
	// locked write, so a busy board does not take the file lock twice per
	// tool call.
	s.mu.Lock()
	worth := !s.closed && s.runner != nil && s.hasDispatchableLocked(root)
	s.mu.Unlock()
	if !worth {
		return
	}
	ctx = context.WithoutCancel(ctx)
	var (
		starts   []dispatchStart
		leads    []string
		runner   Runner
		leadHook func(sessionID string)
	)
	err := s.write(refusable, func() (bool, error) {
		if s.closed || s.runner == nil {
			return false, nil
		}
		runner = s.runner
		leadHook = s.leadNotifyHook
		var changed bool
		starts, leads, changed = s.planDispatchLocked(root)
		if changed {
			s.notifyLocked()
		}
		return changed, nil
	})
	if err != nil {
		slog.Debug("Skipped a Team dispatch pass", "session", root, "error", err)
		if retry {
			s.retryDispatch(root)
		}
		return
	}
	wakeLeads(leadHook, leads)
	for _, start := range starts {
		if _, err := s.launch(ctx, runner, start.member, start.prompt); err != nil {
			s.dispatchFailed(start, err)
		}
	}
}

// retryDispatch schedules one more pass after a backoff, so a pass skipped
// while another process held the state lock is not lost. The goroutine is
// counted before it starts, under the same lock Shutdown takes before it
// waits, and it ends at once when Shutdown closes the Team.
func (s *Service) retryDispatch(root string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.watchers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.watchers.Done()
		timer := time.NewTimer(dispatchRetryDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-s.done:
			return
		}
		s.dispatchPass(context.Background(), root, false)
	}()
}

// planDispatchLocked claims each dispatchable task of root's team for an
// idle member of its role, spawning one when there is none and the live
// count allows it. Each claimed member is marked starting, so it reads as
// running to every other pass until its start settles. Callers must hold
// s.mu inside write.
func (s *Service) planDispatchLocked(root string) (starts []dispatchStart, leads []string, changed bool) {
	scope := s.scopeLocked(leadName, root)
	for i := range s.state.Tasks {
		task := s.state.Tasks[i]
		if !scope[task.SessionID] || !dispatchable(s.state, task) {
			continue
		}
		if !s.cfg.roleExists(task.Role) {
			if !task.RoleReported {
				s.state.Tasks[i].RoleReported = true
				s.noteLeadLocked(task.SessionID, fmt.Sprintf(
					"Task %s (%s) names role %q, which no longer exists. It is skipped; edit its role to continue.",
					task.ID, task.Subject, task.Role))
				leads = append(leads, task.SessionID)
				changed = true
			}
			continue
		}
		member, ok := s.idleMemberLocked(task.Role, task.SessionID)
		if !ok {
			if s.liveMembersLocked() >= s.cfg.MaxMembers {
				continue
			}
			member = s.addAutoMemberLocked(task)
		}
		claimed := &s.state.Tasks[i]
		// A role that came back after a reload is reported again if it
		// ever goes away again.
		claimed.RoleReported = false
		claimed.OwnerID = member.ID
		claimed.Status = TaskInProgress
		claimed.Revision++
		s.starting[member.Name] = true
		s.claims[member.Name] = dispatchClaim{taskID: claimed.ID, revision: claimed.Revision}
		starts = append(starts, dispatchStart{
			member:    member,
			taskID:    claimed.ID,
			sessionID: claimed.SessionID,
			revision:  claimed.Revision,
			prompt:    taskBrief(*claimed),
		})
		changed = true
	}
	return starts, leads, changed
}

// dispatchable reports whether the dispatcher may start task: pending,
// ready, unowned, naming a role, not escalated, and owned by a session.
func dispatchable(state State, task Task) bool {
	return task.Role != "" &&
		task.Status == TaskPending &&
		task.OwnerID == "" &&
		!task.Escalated &&
		task.SessionID != "" &&
		taskReady(state, task)
}

// hasDispatchableLocked reports whether a pass for root has anything to do:
// a dispatchable task whose role resolves, or whose missing role was not
// reported yet. Callers must hold s.mu.
func (s *Service) hasDispatchableLocked(root string) bool {
	scope := s.scopeLocked(leadName, root)
	for _, task := range s.state.Tasks {
		if !scope[task.SessionID] || !dispatchable(s.state, task) {
			continue
		}
		if !task.RoleReported || s.cfg.roleExists(task.Role) {
			return true
		}
	}
	return false
}

// idleMemberLocked returns the earliest-created member of role, spawned by
// sessionID, that is inactive, not failed, and owns no task in flight.
// Callers must hold s.mu.
func (s *Service) idleMemberLocked(role, sessionID string) (Member, bool) {
	for _, member := range s.state.Members {
		if member.RoleName != role || member.ParentID != sessionID || member.Failed {
			continue
		}
		if s.statusLocked(member.Name) != StatusInactive || s.ownsActiveTaskLocked(member.ID) {
			continue
		}
		return member, true
	}
	return Member{}, false
}

// ownsActiveTaskLocked reports whether memberID owns a task in progress or
// in review. Callers must hold s.mu.
func (s *Service) ownsActiveTaskLocked(memberID string) bool {
	for _, task := range s.state.Tasks {
		if task.OwnerID == memberID && (task.Status == TaskInProgress || task.Status == TaskInReview) {
			return true
		}
	}
	return false
}

// liveMembersLocked counts the members running or being created. The limit
// bounds the teammates working at once, not the durable roster. Callers
// must hold s.mu.
func (s *Service) liveMembersLocked() int {
	live := 0
	for _, member := range s.state.Members {
		switch s.statusLocked(member.Name) {
		case StatusRunning, StatusProvisioning:
			live++
		}
	}
	return live
}

// nextAutoNameLocked returns "<role>-<n>" with the lowest n no member uses.
// Names are permanent, so a failed or settled member keeps its number.
// Callers must hold s.mu.
func (s *Service) nextAutoNameLocked(role string) string {
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-%d", role, n)
		if !s.hasMemberLocked(name) {
			return name
		}
	}
}

// addAutoMemberLocked records a member the dispatcher spawns for task.
// Callers must hold s.mu.
func (s *Service) addAutoMemberLocked(task Task) Member {
	s.state.NextMember++
	member := Member{
		ID:          fmt.Sprintf("member-%d", s.state.NextMember),
		Name:        s.nextAutoNameLocked(task.Role),
		Role:        RoleTeammate,
		Description: "Auto-started for task " + task.ID,
		Provider:    "crush",
		Context:     ContextFresh,
		RoleName:    task.Role,
		ParentID:    task.SessionID,
		Status:      StatusProvisioning,
	}
	s.state.Members = append(s.state.Members, member)
	s.mine[member.Name] = true
	return member
}

// taskBrief is the message a dispatched member starts from. It is per-task
// text in a message, so the role block before it on a first turn stays the
// shared prefix of every sibling with that role.
func taskBrief(task Task) string {
	scopes := "none"
	if len(task.WriteScopes) > 0 {
		scopes = strings.Join(task.WriteScopes, ", ")
	}
	last := fmt.Sprintf("Complete it with team_task_update (action \"complete\", expected_revision %d) when the acceptance criteria are met.", task.Revision)
	if task.Verifies != "" {
		last = `Record your verdict with team_task_update, action "pass", or action "fail" with notes describing what failed.`
	}
	return fmt.Sprintf("Task %s on the shared board was claimed for you: %s\n\n%s\n\nWrite scopes: %s\n%s",
		task.ID, task.Subject, task.Description, scopes, last)
}

// escalationNote is the Lead notice for a task the dispatcher gave up on.
func escalationNote(task Task, reason string) string {
	return fmt.Sprintf("Task %s (%s) could not be started: %s. It is escalated; edit, reassign or reopen it to continue.",
		task.ID, task.Subject, reason)
}

// dispatchFailed releases a claim whose start failed. A failure counts as an
// attempt and escalates the task at the cap; a start refused by shutdown is
// released without counting, so a later Service retries it. The released
// task may be dispatchable for another member, so a failed start runs the
// next pass itself; that chain ends when the attempts cap escalates the task,
// since a failed member is never reused.
func (s *Service) dispatchFailed(start dispatchStart, err error) {
	shutdown := errors.Is(err, errShutdown)
	var (
		leads    []string
		leadHook func(sessionID string)
		closed   bool
	)
	_ = s.write(mustApply, func() (bool, error) {
		if s.closed {
			// Shutdown owns the Team now, and a write here would land
			// after it returned. Shutdown hands the claim back itself.
			closed = true
			return false, nil
		}
		leadHook = s.leadNotifyHook
		// The claim is settled in this write, so Shutdown has nothing left
		// to hand back. A concurrent pass may already hold a newer claim
		// for the member, and that one must survive.
		if claim, ok := s.claims[start.member.Name]; ok &&
			claim.taskID == start.taskID && claim.revision == start.revision {
			delete(s.claims, start.member.Name)
		}
		for i := range s.state.Tasks {
			task := &s.state.Tasks[i]
			if task.ID != start.taskID || task.SessionID != start.sessionID {
				continue
			}
			if task.OwnerID != start.member.ID || task.Status != TaskInProgress || task.Revision != start.revision {
				// Someone moved the task on since the claim; leave it.
				break
			}
			task.Revision++
			task.Status = TaskPending
			task.OwnerID = ""
			if !shutdown {
				task.Attempts++
				if task.Attempts >= maxTaskAttempts {
					task.Escalated = true
					s.noteLeadLocked(task.SessionID, escalationNote(*task, err.Error()))
					leads = append(leads, task.SessionID)
				}
			}
			break
		}
		if !shutdown {
			// The member failed in this write too, so a Shutdown that
			// lands next cannot mark it failed after it returned.
			s.failMemberLocked(start.member.Name, err)
		}
		s.notifyLocked()
		return true, nil
	})
	if shutdown || closed {
		return
	}
	wakeLeads(leadHook, leads)
	s.dispatch(context.Background(), start.sessionID)
}

// noteLeadLocked queues a runtime notice for the Lead of sessionID. The
// session on the message is what scopes it, since the runtime is not a
// member. Callers must hold s.mu and wake the Lead after the write.
func (s *Service) noteLeadLocked(sessionID, text string) {
	if sessionID == "" {
		return
	}
	s.appendMessageLocked(noticeSender, leadName, text)
	s.state.Messages[len(s.state.Messages)-1].SessionID = sessionID
}

// wakeLeads calls hook once per distinct session, outside every lock.
func wakeLeads(hook func(sessionID string), sessions []string) {
	if hook == nil {
		return
	}
	seen := make(map[string]bool, len(sessions))
	for _, id := range sessions {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		hook(id)
	}
}
