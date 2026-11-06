// Package agentteam implements the durable Agent Teams domain: one Lead,
// named teammates, direct inbox messages, and a shared task board.
//
// The service is the only owner of Team state. Tools and UI read model-facing
// views from it; they never mutate roster or task state themselves.
package agentteam

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Defaults mirror the experimental DSH Agent Teams bundle.
const (
	DefaultMaxMembers      = 8
	DefaultMaxTasks        = 256
	DefaultMaxMessageBytes = 65536

	leadName = "lead"
)

// Policy is the shared coordination guidance every Team member receives. It
// mirrors the experimental DSH Agent Teams policy.
const Policy = `## Agent Teams

Agent Teams is available in this session. Use it by default: the Lead coordinates and delegates, and teammates do the work.

Work solo only for a single trivial action (one command, one file read, a direct factual answer) or when the user explicitly asks you to. Everything else goes to the Team: create shared tasks, spawn at least one teammate, and delegate. Requests with two or more steps, multiple files, investigation, or tests must use the Team. Reuse existing teammates before spawning new ones. When in doubt, delegate.

The Team Lead and all teammates share the same working directory and filesystem. Edits are immediately visible to every member. Split write work into disjoint scopes, record expected write scopes on shared tasks, and use task dependencies when work must be ordered. Write-scope overlap is advisory, not a lock.

The Lead owns the task worktree. Create it with the worktree tool before delegating, include its absolute path in every task description, and have every teammate work inside that path. Remove it only after the branch is pushed and merged.

Prefer read/edit/write for file changes. If a file operation returns FS_STALE_VERSION, read the current file, rebase your intended change onto the new content, and retry. Bash, formatters, code generators, and scripts are not fully protected by the filesystem version guard; coordinate them explicitly and have the Lead review the final diff and run tests.

Keep the shared task board current at every step, because the user watches it. Create a task for each unit of work before delegating it, claim it with the current revision, and complete it the moment the work is verified. Edit tasks whose scope changed, and delete tasks that are superseded, abandoned, or no longer relevant. Once a completed task's result has been reported, delete it too, so the board holds only live work. Never report work as done while its task is still pending or in progress.

Report progress to the Lead as you work. Send a one-line message when you start a task, when you finish it, and when you are blocked; never end a turn silently. A settled turn also sends the Lead an automatic report, and wait_agent returns the current roster and live task board, so the Lead always knows where things stand. Finish your tasks before ending a turn: the service completes the tasks you leave in progress when your turn succeeds, and releases them when it fails.

Use the target returned by spawn_teammate or list_agents for send_message and interrupt_agent, or as owner when assigning or filtering shared tasks. send_message steers a running target at its nearest step boundary and starts or resumes an inactive target. inactive means no turn is executing; it does not describe task completion, success, failure, or waiting for other agents. provisioning means member creation is in progress; failed means member creation failed. A peer message starts with its sender name. A successful send does not mean the recipient has processed it. Shared-task workflow is list, get, claim with the current revision, perform the work, then complete. Task readiness never starts an owner. Before wait_agent, use list_agents and make sure another required member is running or provisioning; use send_message first when the required member is inactive. wait_agent observes only changes after that call starts, never wakes a member, and returns noProgress immediately when no other member can produce a change; it also returns the current roster and live task board, so re-list only when you need the full task details. The Lead must wait for required teammates before giving the final answer.`

// Role is a Team member role.
type Role string

// Team roles.
const (
	RoleLead     Role = "lead"
	RoleTeammate Role = "teammate"
)

// Status is a member's current availability.
type Status string

// Member statuses. Inactive means no turn is executing; it says nothing
// about task completion or outcome.
const (
	StatusRunning      Status = "running"
	StatusInactive     Status = "inactive"
	StatusProvisioning Status = "provisioning"
	StatusFailed       Status = "failed"
)

// ContextMode selects how a teammate starts.
type ContextMode string

// Teammate context modes.
const (
	ContextFresh ContextMode = "fresh"
	ContextFork  ContextMode = "fork"
)

// TaskStatus is a shared task's lifecycle state.
type TaskStatus string

// Task statuses.
const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskCompleted  TaskStatus = "completed"
	TaskDeleted    TaskStatus = "deleted"
)

// Error codes match the DSH Agent Teams domain so callers can branch on them.
const (
	CodeInvalidMemberName     = "TEAM_INVALID_MEMBER_NAME"
	CodeMemberLimit           = "TEAM_MEMBER_LIMIT"
	CodeMemberNotFound        = "TEAM_MEMBER_NOT_FOUND"
	CodeNotMember             = "TEAM_NOT_MEMBER"
	CodeLeadRequired          = "TEAM_LEAD_REQUIRED"
	CodeMessageTooLarge       = "TEAM_MESSAGE_TOO_LARGE"
	CodeTaskNotFound          = "TEAM_TASK_NOT_FOUND"
	CodeTaskLimit             = "TEAM_TASK_LIMIT"
	CodeTaskStaleRevision     = "TEAM_TASK_STALE_REVISION"
	CodeTaskDeleted           = "TEAM_TASK_DELETED"
	CodeTaskUnauthorized      = "TEAM_TASK_UNAUTHORIZED"
	CodeTaskAlreadyClaimed    = "TEAM_TASK_ALREADY_CLAIMED"
	CodeTaskBlocked           = "TEAM_TASK_BLOCKED"
	CodeTaskInvalidTransition = "TEAM_TASK_INVALID_TRANSITION"
	CodeTaskHasDependents     = "TEAM_TASK_HAS_DEPENDENTS"
	CodeTaskDependencyCycle   = "TEAM_TASK_DEPENDENCY_CYCLE"
	CodeInvalidArgument       = "TEAM_INVALID_ARGUMENT"
	CodeInvalidWriteScope     = "TEAM_INVALID_WRITE_SCOPE"
)

// Error is a typed Team failure.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func newError(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrorCode returns the Team error code carried by err, if any.
func ErrorCode(err error) string {
	var teamErr *Error
	if errors.As(err, &teamErr) {
		return teamErr.Code
	}
	return ""
}

type memberContextKey struct{}

// WithMember records the calling Team member on a context.
func WithMember(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, memberContextKey{}, name)
}

// MemberFromContext returns the calling Team member, or the Lead when the
// context carries no member identity.
func MemberFromContext(ctx context.Context) string {
	if name, ok := ctx.Value(memberContextKey{}).(string); ok && name != "" {
		return name
	}
	return leadName
}

// IsLead reports whether the caller is the Team Lead. A context with no
// member identity is the Lead's own session, so tools that only the Lead may
// run gate on this rather than on the member name.
func IsLead(ctx context.Context) bool {
	return MemberFromContext(ctx) == leadName
}

// Member is a durable roster entry. Names are permanent and never reused.
type Member struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Role        Role        `json:"role"`
	Description string      `json:"description,omitempty"`
	Provider    string      `json:"provider,omitempty"`
	Context     ContextMode `json:"context,omitempty"`
	Model       string      `json:"model,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	ParentID    string      `json:"parent_session_id,omitempty"`
	Status      Status      `json:"status,omitempty"`
	Failed      bool        `json:"failed,omitempty"`
	Diagnostics []string    `json:"diagnostics,omitempty"`
}

// MemberView is the model-facing roster row.
type MemberView struct {
	Target      string      `json:"target"`
	Role        Role        `json:"role"`
	Status      Status      `json:"status"`
	Description string      `json:"description,omitempty"`
	Provider    string      `json:"provider,omitempty"`
	Context     ContextMode `json:"context,omitempty"`
	Model       string      `json:"model,omitempty"`
	Diagnostics []string    `json:"diagnostics"`
}

// Task is a durable shared task.
type Task struct {
	ID          string     `json:"id"`
	Revision    int        `json:"revision"`
	Subject     string     `json:"subject"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	OwnerID     string     `json:"owner_id,omitempty"`
	BlockedBy   []string   `json:"blocked_by"`
	WriteScopes []string   `json:"write_scopes"`
	// SessionID is the Lead session whose team owns the task. Tasks
	// written before this field existed carry no session; they are
	// hidden from every scope so they cannot leak into a new session.
	SessionID string `json:"session_id,omitempty"`
}

// TaskView is the model-facing task row.
type TaskView struct {
	ID                 string     `json:"id"`
	Revision           int        `json:"revision"`
	Subject            string     `json:"subject"`
	Description        string     `json:"description"`
	Status             TaskStatus `json:"status"`
	OwnerID            string     `json:"ownerId,omitempty"`
	OwnerName          string     `json:"ownerName,omitempty"`
	BlockedBy          []string   `json:"blockedBy"`
	WriteScopes        []string   `json:"writeScopes"`
	Ready              bool       `json:"ready"`
	WriteScopeWarnings []string   `json:"writeScopeWarnings"`
	SessionID          string     `json:"sessionId,omitempty"`
}

// Message is one inbox entry.
type Message struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
	Delivered bool      `json:"delivered,omitempty"`
}

// SpawnRequest describes one teammate creation.
type SpawnRequest struct {
	Name        string
	Description string
	Prompt      string
	Context     ContextMode
	Provider    string
	Model       string
	ParentID    string
}

// SendRequest describes one direct message.
type SendRequest struct {
	Target  string
	Message string
}

// CreateTaskRequest describes one new shared task.
type CreateTaskRequest struct {
	Subject     string
	Description string
	BlockedBy   []string
	WriteScopes []string
}

// TaskFilter narrows a task listing.
type TaskFilter struct {
	Status *TaskStatus
	Owner  string
	Ready  *bool
	Cursor int
	Limit  int
}

// TaskList is one page of shared tasks.
type TaskList struct {
	Tasks      []TaskView
	NextCursor int
}

// UpdateTaskRequest is one compare-and-set task transition.
type UpdateTaskRequest struct {
	TaskID           string
	ExpectedRevision int
	Action           string
	Subject          string
	Description      string
	BlockedBy        []string
	WriteScopes      []string
	Owner            string
	// SessionID is the caller's session. It scopes the task to the caller's
	// team and reconciles session todos when the Lead completes a task
	// itself.
	SessionID string
}

// ProgressTask is a compact task row for progress snapshots.
type ProgressTask struct {
	ID        string     `json:"id"`
	Subject   string     `json:"subject"`
	Status    TaskStatus `json:"status"`
	OwnerName string     `json:"ownerName,omitempty"`
	Ready     bool       `json:"ready"`
}

// Progress is a compact roster and live task board snapshot.
type Progress struct {
	Members []MemberView   `json:"members"`
	Tasks   []ProgressTask `json:"tasks"`
}

// WaitResult reports whether a wait timed out and the state it observed.
type WaitResult struct {
	TimedOut bool     `json:"timedOut"`
	Progress Progress `json:"progress"`
}

// Run is one live teammate turn.
type Run interface {
	// SessionID is the Crush session backing the run.
	SessionID() string
	// Done closes when the run settles.
	Done() <-chan struct{}
	// Output is the teammate's final report for this turn.
	Output() string
	// Err reports the run failure, if any.
	Err() error
	// Cancel stops the run.
	Cancel()
}

// Runner starts teammate turns. Implementations own agent construction.
type Runner interface {
	Start(ctx context.Context, member Member, prompt string) (Run, error)
}

// Config bounds a Team.
type Config struct {
	MaxMembers      int
	MaxTasks        int
	MaxMessageBytes int
}

func (c Config) withDefaults() Config {
	if c.MaxMembers <= 0 {
		c.MaxMembers = DefaultMaxMembers
	}
	if c.MaxTasks <= 0 {
		c.MaxTasks = DefaultMaxTasks
	}
	if c.MaxMessageBytes <= 0 {
		c.MaxMessageBytes = DefaultMaxMessageBytes
	}
	return c
}

// State is the durable Team record.
type State struct {
	Version     int       `json:"version"`
	TeamID      string    `json:"team_id"`
	CreatedAt   time.Time `json:"created_at"`
	Members     []Member  `json:"members"`
	Tasks       []Task    `json:"tasks"`
	Messages    []Message `json:"messages"`
	NextMember  int       `json:"next_member"`
	NextTask    int       `json:"next_task"`
	NextMessage int       `json:"next_message"`
}

// Service owns Team state and enforces Lead authority.
type Service struct {
	mu       sync.Mutex
	state    State
	path     string
	cfg      Config
	runner   Runner
	runs     map[string]Run
	starting map[string]bool
	changes  chan struct{}
	now      func() time.Time
	// taskCompletedHook is called after a task settles as completed. It
	// runs without the service lock and is best-effort.
	taskCompletedHook func(sessionID, subject string)
}

// New opens (or creates) the Team state at path. A nil runner supports
// read-only use, such as the CLI status view.
func New(path string, cfg Config, runner Runner) (*Service, error) {
	state, err := loadState(path)
	if err != nil {
		return nil, err
	}
	if state.TeamID == "" {
		state.TeamID = fmt.Sprintf("team-%d", time.Now().UnixNano())
		state.CreatedAt = time.Now()
	}
	if state.Version == 0 {
		state.Version = 1
	}
	// No run survives a process restart, so a persisted running or
	// provisioning status is stale.
	for i := range state.Members {
		switch state.Members[i].Status {
		case StatusRunning, StatusProvisioning, "":
			state.Members[i].Status = StatusInactive
		}
	}
	return &Service{
		state:    state,
		path:     path,
		cfg:      cfg.withDefaults(),
		runner:   runner,
		runs:     make(map[string]Run),
		starting: make(map[string]bool),
		changes:  make(chan struct{}),
		now:      time.Now,
	}, nil
}

// TeamID returns the durable Team identifier.
func (s *Service) TeamID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.TeamID
}

// SetRunner installs the runner after construction.
func (s *Service) SetRunner(runner Runner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runner = runner
}

// SetTaskCompletedHook installs a best-effort callback invoked after a task
// settles as completed. It receives the Lead's session ID and the task
// subject, and runs without the service lock.
func (s *Service) SetTaskCompletedHook(hook func(sessionID, subject string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskCompletedHook = hook
}

// notifyLocked wakes every waiter. Callers must hold s.mu.
func (s *Service) notifyLocked() {
	close(s.changes)
	s.changes = make(chan struct{})
}

// saveLocked persists the current state. Callers must hold s.mu.
func (s *Service) saveLocked() {
	if s.path == "" {
		return
	}
	if err := saveState(s.path, s.state); err != nil {
		// Persistence is best effort: a failed write must not fail the
		// tool call that already changed in-memory state.
		_ = err
	}
}

func (s *Service) memberLocked(name string) (Member, bool) {
	if name == leadName {
		return Member{ID: leadName, Name: leadName, Role: RoleLead}, true
	}
	for _, member := range s.state.Members {
		if member.Name == name {
			return member, true
		}
	}
	return Member{}, false
}

// scopeRootLocked returns the Lead session that owns the caller's team: the
// caller's own session for the Lead, and the session that spawned the caller
// for a teammate. Callers must hold s.mu.
func (s *Service) scopeRootLocked(caller, sessionID string) string {
	if caller == leadName {
		return sessionID
	}
	member, ok := s.memberLocked(caller)
	if !ok {
		return ""
	}
	return member.ParentID
}

// scopeLocked returns the Lead sessions the caller may see. It starts at the
// caller's scope root and follows teammate spawn links, so a teammate spawned
// by a teammate stays in the same tree. An empty root yields a nil scope,
// which matches nothing: a call with no session must not see another
// session's team. Callers must hold s.mu.
func (s *Service) scopeLocked(caller, sessionID string) map[string]bool {
	root := s.scopeRootLocked(caller, sessionID)
	if root == "" {
		return nil
	}
	links := make([][2]string, 0, len(s.state.Members))
	for _, member := range s.state.Members {
		if member.ParentID != "" && member.SessionID != "" {
			links = append(links, [2]string{member.ParentID, member.SessionID})
		}
	}
	scope := map[string]bool{root: true}
	for {
		changed := false
		for _, link := range links {
			if scope[link[0]] && !scope[link[1]] {
				scope[link[1]] = true
				changed = true
			}
		}
		if !changed {
			return scope
		}
	}
}

// visibleMembersLocked returns the roster entries in scope, in creation
// order. Callers must hold s.mu.
func (s *Service) visibleMembersLocked(scope map[string]bool) []Member {
	if len(scope) == 0 {
		return nil
	}
	members := make([]Member, 0, len(s.state.Members))
	for _, member := range s.state.Members {
		if scope[member.ParentID] {
			members = append(members, member)
		}
	}
	return members
}

// memberVisibleLocked reports whether name is addressable from scope. The
// Lead is the root of every scope, so it is visible whenever the scope is
// non-empty. Callers must hold s.mu.
func (s *Service) memberVisibleLocked(scope map[string]bool, name string) bool {
	if len(scope) == 0 {
		return false
	}
	if name == leadName {
		return true
	}
	member, ok := s.memberLocked(name)
	return ok && scope[member.ParentID]
}

// taskVisibleLocked reports whether task belongs to scope. A task with no
// session predates session scoping; it is hidden from every scope rather
// than leaking into a session that did not create it.
func taskVisibleLocked(scope map[string]bool, task Task) bool {
	return task.SessionID != "" && scope[task.SessionID]
}

func (s *Service) statusLocked(name string) Status {
	if _, ok := s.runs[name]; ok {
		return StatusRunning
	}
	if s.starting[name] {
		return StatusRunning
	}
	member, ok := s.memberLocked(name)
	if !ok {
		return StatusFailed
	}
	if member.Failed {
		return StatusFailed
	}
	if member.Status != "" {
		return member.Status
	}
	return StatusInactive
}

func (s *Service) viewLocked(member Member) MemberView {
	view := MemberView{
		Target:      member.Name,
		Role:        member.Role,
		Status:      s.statusLocked(member.Name),
		Description: member.Description,
		Provider:    member.Provider,
		Context:     member.Context,
		Model:       member.Model,
		Diagnostics: append([]string{}, member.Diagnostics...),
	}
	if view.Role == RoleLead {
		view.Status = StatusRunning
		view.Diagnostics = []string{}
	}
	return view
}

// ListMembers returns the Lead first, then the caller's teammates in creation
// order. A session only ever sees the team it spawned.
func (s *Service) ListMembers(caller, sessionID string) ([]MemberView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return nil, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	scope := s.scopeLocked(caller, sessionID)
	if len(scope) == 0 {
		return []MemberView{}, nil
	}
	views := []MemberView{s.viewLocked(Member{ID: leadName, Name: leadName, Role: RoleLead})}
	for _, member := range s.visibleMembersLocked(scope) {
		views = append(views, s.viewLocked(member))
	}
	return views, nil
}

var memberNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidateMemberName enforces the durable teammate naming rules.
func ValidateMemberName(name string) error {
	if !memberNamePattern.MatchString(name) || len(name) > 64 || name == leadName {
		return newError(CodeInvalidMemberName, "teammate name must be lower-kebab-case, at most 64 characters, and not %q", leadName)
	}
	return nil
}

// SpawnTeammate creates one durable teammate and starts its first turn.
func (s *Service) SpawnTeammate(ctx context.Context, caller string, req SpawnRequest) (MemberView, error) {
	if caller != leadName {
		return MemberView{}, newError(CodeLeadRequired, "only the Team Lead can create teammates")
	}
	if err := ValidateMemberName(req.Name); err != nil {
		return MemberView{}, err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return MemberView{}, newError(CodeInvalidArgument, "prompt is required")
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		return MemberView{}, newError(CodeInvalidArgument, "description is required")
	}
	if len(description) > 200 {
		return MemberView{}, newError(CodeInvalidArgument, "description must be at most 200 characters")
	}
	mode := req.Context
	if mode == "" {
		mode = ContextFresh
	}
	if mode != ContextFresh && mode != ContextFork {
		return MemberView{}, newError(CodeInvalidArgument, "context must be fresh or fork")
	}

	s.mu.Lock()
	if _, exists := s.memberLocked(req.Name); exists {
		s.mu.Unlock()
		return MemberView{}, newError(CodeInvalidMemberName, "teammate name %q is already used", req.Name)
	}
	// The limit bounds the teammates working at once, not the durable
	// roster: a workspace keeps every member it ever created, so counting
	// settled members would lock a new team out of the workspace forever.
	live := 0
	for _, existing := range s.state.Members {
		switch s.statusLocked(existing.Name) {
		case StatusRunning, StatusProvisioning:
			live++
		}
	}
	if live >= s.cfg.MaxMembers {
		s.mu.Unlock()
		return MemberView{}, newError(CodeMemberLimit, "Team member limit %d reached", s.cfg.MaxMembers)
	}
	s.state.NextMember++
	member := Member{
		ID:          fmt.Sprintf("member-%d", s.state.NextMember),
		Name:        req.Name,
		Role:        RoleTeammate,
		Description: description,
		Provider:    req.Provider,
		Context:     mode,
		Model:       req.Model,
		ParentID:    req.ParentID,
		Status:      StatusProvisioning,
	}
	s.state.Members = append(s.state.Members, member)
	s.saveLocked()
	s.notifyLocked()
	s.mu.Unlock()

	view, err := s.startRun(ctx, member, req.Prompt)
	if err != nil {
		s.failMember(member.Name, err)
		return s.viewFor(member.Name), nil
	}
	return view, nil
}

// startRun starts one turn and records the live run.
func (s *Service) startRun(ctx context.Context, member Member, prompt string) (MemberView, error) {
	s.mu.Lock()
	runner := s.runner
	if runner == nil {
		s.mu.Unlock()
		return MemberView{}, errors.New("no teammate runner is configured")
	}
	if s.starting[member.Name] {
		view := s.viewLocked(s.stateMemberLocked(member.Name))
		s.mu.Unlock()
		return view, nil
	}
	s.starting[member.Name] = true
	s.mu.Unlock()
	run, err := runner.Start(ctx, member, prompt)
	s.mu.Lock()
	delete(s.starting, member.Name)
	if err != nil {
		s.mu.Unlock()
		return MemberView{}, err
	}
	s.runs[member.Name] = run
	for i := range s.state.Members {
		if s.state.Members[i].Name == member.Name {
			s.state.Members[i].SessionID = run.SessionID()
			s.state.Members[i].Failed = false
			s.state.Members[i].Status = StatusRunning
		}
	}
	s.saveLocked()
	s.notifyLocked()
	view := s.viewLocked(s.stateMemberLocked(member.Name))
	s.mu.Unlock()
	go s.watch(member.Name, run)
	return view, nil
}

func (s *Service) stateMemberLocked(name string) Member {
	for _, member := range s.state.Members {
		if member.Name == name {
			return member
		}
	}
	return Member{Name: name, Role: RoleTeammate}
}

// watch settles one run and resumes the member when messages are queued.
func (s *Service) watch(name string, run Run) {
	<-run.Done()
	s.mu.Lock()
	if s.runs[name] == run {
		delete(s.runs, name)
	}
	runErr := run.Err()
	if runErr != nil {
		for i := range s.state.Members {
			if s.state.Members[i].Name == name {
				s.state.Members[i].Diagnostics = append(
					s.state.Members[i].Diagnostics,
					runErr.Error(),
				)
			}
		}
	}
	for i := range s.state.Members {
		if s.state.Members[i].Name == name {
			s.state.Members[i].Status = StatusInactive
		}
	}
	member := s.stateMemberLocked(name)
	settled, completed := s.settleTasksLocked(member.ID, runErr)
	s.enqueueLocked(name, leadName, runReport(name, run.Output(), settled, runErr))
	s.saveLocked()
	s.notifyLocked()
	pending := s.drainLocked(name)
	hook := s.taskCompletedHook
	s.mu.Unlock()
	if hook != nil {
		for _, subject := range completed {
			hook(member.ParentID, subject)
		}
	}
	if len(pending) == 0 {
		return
	}
	if _, err := s.startRun(context.WithoutCancel(context.Background()), member, resumePrompt(pending)); err != nil {
		s.mu.Lock()
		s.enqueueLocked(name, leadName, fmt.Sprintf("Could not resume %s: %s\n\n%s", name, err, resumePrompt(pending)))
		s.saveLocked()
		s.notifyLocked()
		s.mu.Unlock()
		s.failMember(name, err)
	}
}

// settleTasksLocked closes out the tasks a finished teammate left in flight.
// A successful turn completes them; a failed or interrupted turn releases
// them back to the board so they never stay half-done forever. It returns
// the report labels and the subjects of the completed tasks.
func (s *Service) settleTasksLocked(ownerID string, runErr error) (settled, completed []string) {
	if ownerID == "" || ownerID == leadName {
		return nil, nil
	}
	for i := range s.state.Tasks {
		task := &s.state.Tasks[i]
		if task.OwnerID != ownerID || task.Status != TaskInProgress {
			continue
		}
		task.Revision++
		if runErr == nil {
			task.Status = TaskCompleted
			settled = append(settled, task.ID+" completed")
			completed = append(completed, task.Subject)
		} else {
			task.Status = TaskPending
			task.OwnerID = ""
			settled = append(settled, task.ID+" released")
		}
	}
	return settled, completed
}

// enqueueLocked appends one inbox message without waking the target.
func (s *Service) enqueueLocked(from, to, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if len(text) > s.cfg.MaxMessageBytes {
		text = truncateText(text, s.cfg.MaxMessageBytes)
	}
	s.state.NextMessage++
	s.state.Messages = append(s.state.Messages, Message{
		ID:        fmt.Sprintf("message-%d", s.state.NextMessage),
		From:      from,
		To:        to,
		Text:      text,
		CreatedAt: s.now(),
	})
}

// runReport renders the automatic progress report a settled teammate sends
// the Lead.
func runReport(name, output string, settled []string, runErr error) string {
	var sb strings.Builder
	if runErr != nil {
		fmt.Fprintf(&sb, "%s failed: %s.", name, runErr.Error())
	} else {
		fmt.Fprintf(&sb, "%s finished its turn.", name)
	}
	if len(settled) > 0 {
		fmt.Fprintf(&sb, " Tasks: %s.", strings.Join(settled, ", "))
	}
	if text := strings.TrimSpace(output); text != "" {
		const maxReport = 2000
		if len(text) > maxReport {
			text = truncateText(text, maxReport) + "…"
		}
		sb.WriteString("\n\n")
		sb.WriteString(text)
	}
	return sb.String()
}

// truncateText cuts s to at most n bytes without splitting a rune.
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// failMember records a creation failure.
func (s *Service) failMember(name string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.Members {
		if s.state.Members[i].Name == name {
			s.state.Members[i].Failed = true
			s.state.Members[i].Status = StatusFailed
			s.state.Members[i].Diagnostics = append(
				s.state.Members[i].Diagnostics,
				err.Error(),
			)
		}
	}
	s.saveLocked()
	s.notifyLocked()
}

func (s *Service) viewFor(name string) MemberView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.viewLocked(s.stateMemberLocked(name))
}

// SendMessage queues one direct message and wakes an inactive target. The
// target must belong to the caller's session tree.
func (s *Service) SendMessage(ctx context.Context, from, sessionID string, req SendRequest) error {
	if strings.TrimSpace(req.Message) == "" {
		return newError(CodeInvalidArgument, "message is required")
	}
	if len(req.Message) > s.cfg.MaxMessageBytes {
		return newError(CodeMessageTooLarge, "message exceeds %d bytes", s.cfg.MaxMessageBytes)
	}
	s.mu.Lock()
	if _, ok := s.memberLocked(from); !ok {
		s.mu.Unlock()
		return newError(CodeNotMember, "agent %q is not a member of an active Agent Team", from)
	}
	if !s.memberVisibleLocked(s.scopeLocked(from, sessionID), req.Target) {
		s.mu.Unlock()
		return newError(CodeMemberNotFound, "Team member %q not found", req.Target)
	}
	target, _ := s.memberLocked(req.Target)
	s.state.NextMessage++
	s.state.Messages = append(s.state.Messages, Message{
		ID:        fmt.Sprintf("message-%d", s.state.NextMessage),
		From:      from,
		To:        target.Name,
		Text:      req.Message,
		CreatedAt: s.now(),
	})
	s.saveLocked()
	s.notifyLocked()
	running := s.statusLocked(target.Name) == StatusRunning
	s.mu.Unlock()

	if running || target.Role == RoleLead {
		return nil
	}
	pending := s.DrainInbox(target.Name)
	if len(pending) == 0 {
		return nil
	}
	_, err := s.startRun(ctx, target, resumePrompt(pending))
	return err
}

// DrainInbox returns and marks delivered every pending message for name.
func (s *Service) DrainInbox(name string) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.drainLocked(name)
	if len(pending) > 0 {
		s.saveLocked()
	}
	return pending
}

func (s *Service) drainLocked(name string) []Message {
	var pending []Message
	for i := range s.state.Messages {
		if s.state.Messages[i].To == name && !s.state.Messages[i].Delivered {
			s.state.Messages[i].Delivered = true
			pending = append(pending, s.state.Messages[i])
		}
	}
	return pending
}

// WaitForChange waits for the next roster, message, or task change.
func (s *Service) WaitForChange(ctx context.Context, caller, sessionID string, timeout time.Duration) (WaitResult, error) {
	s.mu.Lock()
	if _, ok := s.memberLocked(caller); !ok {
		s.mu.Unlock()
		return WaitResult{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	ch := s.changes
	s.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
		return s.waitResult(false, caller, sessionID), nil
	case <-timer.C:
		return s.waitResult(true, caller, sessionID), nil
	case <-ctx.Done():
		return s.waitResult(true, caller, sessionID), nil
	}
}

// waitResult snapshots the caller's roster and live task board.
func (s *Service) waitResult(timedOut bool, caller, sessionID string) WaitResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return WaitResult{TimedOut: timedOut, Progress: s.progressLocked(s.scopeLocked(caller, sessionID))}
}

// Progress returns the caller's roster and live task board.
func (s *Service) Progress(caller, sessionID string) Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return Progress{}
	}
	return s.progressLocked(s.scopeLocked(caller, sessionID))
}

// progressLocked builds the progress snapshot. Callers must hold s.mu.
func (s *Service) progressLocked(scope map[string]bool) Progress {
	progress := Progress{Members: []MemberView{}}
	if len(scope) == 0 {
		return progress
	}
	progress.Members = append(progress.Members, s.viewLocked(Member{ID: leadName, Name: leadName, Role: RoleLead}))
	for _, member := range s.visibleMembersLocked(scope) {
		progress.Members = append(progress.Members, s.viewLocked(member))
	}
	const maxTasks = 50
	for _, task := range s.state.Tasks {
		if !taskVisibleLocked(scope, task) {
			continue
		}
		if task.Status == TaskDeleted || task.Status == TaskCompleted {
			continue
		}
		if len(progress.Tasks) == maxTasks {
			break
		}
		view := projectTaskView(s.state, task)
		progress.Tasks = append(progress.Tasks, ProgressTask{
			ID:        view.ID,
			Subject:   view.Subject,
			Status:    view.Status,
			OwnerName: view.OwnerName,
			Ready:     view.Ready,
		})
	}
	return progress
}

// Interrupt stops one teammate's current turn. The Lead may only interrupt a
// teammate in its own session tree.
func (s *Service) Interrupt(caller, sessionID, target string) (Status, error) {
	if caller != leadName {
		return "", newError(CodeLeadRequired, "only the Team Lead can interrupt a teammate")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if target == leadName {
		return "", newError(CodeInvalidArgument, "the Team Lead cannot interrupt itself")
	}
	if !s.memberVisibleLocked(s.scopeLocked(caller, sessionID), target) {
		return "", newError(CodeMemberNotFound, "Team member %q not found", target)
	}
	run, ok := s.runs[target]
	if !ok {
		return StatusInactive, nil
	}
	run.Cancel()
	return StatusRunning, nil
}

// HasActivePeer reports whether another member of the caller's team is running
// or provisioning.
func (s *Service) HasActivePeer(caller, sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, member := range s.visibleMembersLocked(s.scopeLocked(caller, sessionID)) {
		if member.Name == caller {
			continue
		}
		switch s.statusLocked(member.Name) {
		case StatusRunning, StatusProvisioning:
			return true
		}
	}
	return false
}

// Shutdown cancels every live teammate run and marks the roster inactive.
// The durable state is kept so a later enable resumes the same Team.
func (s *Service) Shutdown() {
	s.mu.Lock()
	runs := make([]Run, 0, len(s.runs))
	for _, run := range s.runs {
		runs = append(runs, run)
	}
	s.runs = make(map[string]Run)
	for i := range s.state.Members {
		s.state.Members[i].Status = StatusInactive
	}
	s.saveLocked()
	s.notifyLocked()
	s.mu.Unlock()
	for _, run := range runs {
		run.Cancel()
	}
}

// CreateTask adds one unowned pending task to the caller's team board.
func (s *Service) CreateTask(caller, sessionID string, req CreateTaskRequest) (TaskView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return TaskView{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	root := s.scopeRootLocked(caller, sessionID)
	if root == "" {
		return TaskView{}, newError(CodeNotMember, "agent %q has no session to own a team task", caller)
	}
	scope := s.scopeLocked(caller, sessionID)
	active := 0
	for _, task := range s.state.Tasks {
		if task.Status != TaskDeleted && taskVisibleLocked(scope, task) {
			active++
		}
	}
	if active >= s.cfg.MaxTasks {
		return TaskView{}, newError(CodeTaskLimit, "Team task limit %d reached", s.cfg.MaxTasks)
	}
	subject, err := requiredText(req.Subject, "subject", 200)
	if err != nil {
		return TaskView{}, err
	}
	description, err := requiredText(req.Description, "description", 16384)
	if err != nil {
		return TaskView{}, err
	}
	s.state.NextTask++
	task := Task{
		ID:          fmt.Sprintf("task-%d", s.state.NextTask),
		Revision:    1,
		Subject:     subject,
		Description: description,
		Status:      TaskPending,
		BlockedBy:   []string{},
		WriteScopes: []string{},
		SessionID:   root,
	}
	if req.BlockedBy != nil {
		blockedBy, err := s.dependenciesLocked(scope, req.BlockedBy, task.ID)
		if err != nil {
			s.state.NextTask--
			return TaskView{}, err
		}
		task.BlockedBy = blockedBy
	}
	if req.WriteScopes != nil {
		scopes, err := normalizeWriteScopes(req.WriteScopes)
		if err != nil {
			s.state.NextTask--
			return TaskView{}, err
		}
		task.WriteScopes = scopes
	}
	if err := assertTaskGraph(s.state.Tasks, task); err != nil {
		s.state.NextTask--
		return TaskView{}, err
	}
	s.state.Tasks = append(s.state.Tasks, task)
	s.saveLocked()
	s.notifyLocked()
	return projectTaskView(s.state, task), nil
}

// GetTask returns one task view from the caller's team board.
func (s *Service) GetTask(caller, sessionID, id string) (TaskView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return TaskView{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	task, ok := s.taskLocked(id)
	if !ok || !taskVisibleLocked(s.scopeLocked(caller, sessionID), task) {
		return TaskView{}, newError(CodeTaskNotFound, "team task %q not found", id)
	}
	return projectTaskView(s.state, task), nil
}

// ListTasks returns a filtered page of the caller's shared tasks.
func (s *Service) ListTasks(caller, sessionID string, filter TaskFilter) (TaskList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return TaskList{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	scope := s.scopeLocked(caller, sessionID)
	cursor := filter.Cursor
	limit := filter.Limit
	if limit == 0 {
		limit = 50
	}
	if cursor < 0 {
		return TaskList{}, newError(CodeInvalidArgument, "cursor must be a non-negative safe integer")
	}
	if limit < 1 || limit > 100 {
		return TaskList{}, newError(CodeInvalidArgument, "limit must be an integer from 1 through 100")
	}
	var filtered []TaskView
	for _, task := range s.state.Tasks {
		if !taskVisibleLocked(scope, task) {
			continue
		}
		view := projectTaskView(s.state, task)
		if filter.Status != nil && view.Status != *filter.Status {
			continue
		}
		if filter.Owner != "" {
			if filter.Owner == "unowned" {
				if view.OwnerName != "" {
					continue
				}
			} else if view.OwnerName != filter.Owner {
				continue
			}
		}
		if filter.Ready != nil && view.Ready != *filter.Ready {
			continue
		}
		filtered = append(filtered, view)
	}
	result := TaskList{Tasks: []TaskView{}}
	if cursor < len(filtered) {
		end := min(cursor+limit, len(filtered))
		result.Tasks = filtered[cursor:end]
		if end < len(filtered) {
			result.NextCursor = end
		}
	}
	return result, nil
}

// UpdateTask applies one compare-and-set task transition.
func (s *Service) UpdateTask(caller string, req UpdateTaskRequest) (TaskView, error) {
	s.mu.Lock()
	var completedSubject, parentSessionID string
	defer func() {
		hook := s.taskCompletedHook
		s.mu.Unlock()
		if completedSubject != "" && hook != nil {
			hook(parentSessionID, completedSubject)
		}
	}()
	member, ok := s.memberLocked(caller)
	if !ok {
		return TaskView{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	parentSessionID = member.ParentID
	if parentSessionID == "" {
		parentSessionID = req.SessionID
	}
	scope := s.scopeLocked(caller, req.SessionID)
	current, ok := s.taskLocked(req.TaskID)
	if !ok || !taskVisibleLocked(scope, current) {
		return TaskView{}, newError(CodeTaskNotFound, "team task %q not found", req.TaskID)
	}
	if current.Revision != req.ExpectedRevision {
		return TaskView{}, newError(CodeTaskStaleRevision,
			"stale team task %q revision %d; current revision is %d",
			current.ID, req.ExpectedRevision, current.Revision)
	}
	if current.Status == TaskDeleted {
		return TaskView{}, newError(CodeTaskDeleted, "team task %q is deleted", current.ID)
	}
	lead := member.Role == RoleLead
	owner := current.OwnerID == member.ID
	authorizeOwner := func() error {
		if !lead && !owner {
			return newError(CodeTaskUnauthorized, "task mutation requires its owner or Team Lead")
		}
		return nil
	}

	next := current
	switch req.Action {
	case "claim":
		if current.OwnerID != "" && current.OwnerID != member.ID {
			return TaskView{}, newError(CodeTaskAlreadyClaimed, "team task %q is owned by another member", current.ID)
		}
		if current.Status != TaskPending || !taskReady(s.state, current) {
			return TaskView{}, newError(CodeTaskBlocked, "team task %q is not ready to claim", current.ID)
		}
		next.Status = TaskInProgress
		next.OwnerID = member.ID
	case "release":
		if err := authorizeOwner(); err != nil {
			return TaskView{}, err
		}
		if current.Status != TaskInProgress {
			return TaskView{}, newError(CodeTaskInvalidTransition, "only an in-progress task can be released")
		}
		next.Status = TaskPending
		next.OwnerID = ""
	case "edit":
		if err := authorizeOwner(); err != nil {
			return TaskView{}, err
		}
		if req.Subject == "" && req.Description == "" && req.WriteScopes == nil {
			return TaskView{}, newError(CodeInvalidArgument, "task edit requires subject, description, or write_scopes")
		}
		if req.Subject != "" {
			subject, err := requiredText(req.Subject, "subject", 200)
			if err != nil {
				return TaskView{}, err
			}
			next.Subject = subject
		}
		if req.Description != "" {
			description, err := requiredText(req.Description, "description", 16384)
			if err != nil {
				return TaskView{}, err
			}
			next.Description = description
		}
		if req.WriteScopes != nil {
			scopes, err := normalizeWriteScopes(req.WriteScopes)
			if err != nil {
				return TaskView{}, err
			}
			next.WriteScopes = scopes
		}
	case "set_dependencies":
		if err := authorizeOwner(); err != nil {
			return TaskView{}, err
		}
		if req.BlockedBy == nil {
			return TaskView{}, newError(CodeInvalidArgument, "set_dependencies requires blocked_by")
		}
		blockedBy, err := s.dependenciesLocked(scope, req.BlockedBy, current.ID)
		if err != nil {
			return TaskView{}, err
		}
		next.BlockedBy = blockedBy
	case "complete":
		if err := authorizeOwner(); err != nil {
			return TaskView{}, err
		}
		if current.Status != TaskInProgress {
			return TaskView{}, newError(CodeTaskInvalidTransition, "only an in-progress task can complete")
		}
		next.Status = TaskCompleted
		completedSubject = next.Subject
	case "reopen":
		if err := authorizeOwner(); err != nil {
			return TaskView{}, err
		}
		if current.Status != TaskCompleted {
			return TaskView{}, newError(CodeTaskInvalidTransition, "only a completed task can reopen")
		}
		next.Status = TaskPending
		next.OwnerID = ""
	case "reassign":
		if !lead {
			return TaskView{}, newError(CodeLeadRequired, "only the Team Lead can reassign tasks")
		}
		if current.Status != TaskPending && current.Status != TaskInProgress {
			return TaskView{}, newError(CodeTaskInvalidTransition, "only a pending or in-progress task can be reassigned")
		}
		if strings.TrimSpace(req.Owner) == "" {
			next.Status = TaskPending
			next.OwnerID = ""
			break
		}
		if !taskReady(s.state, current) {
			return TaskView{}, newError(CodeTaskBlocked, "team task %q is blocked", current.ID)
		}
		assignee, ok := s.memberLocked(req.Owner)
		if !ok || !s.memberVisibleLocked(scope, req.Owner) {
			return TaskView{}, newError(CodeMemberNotFound, "Team member %q not found", req.Owner)
		}
		next.Status = TaskInProgress
		next.OwnerID = assignee.ID
	case "delete":
		if err := authorizeOwner(); err != nil {
			return TaskView{}, err
		}
		for _, task := range s.state.Tasks {
			if task.Status != TaskDeleted && task.ID != current.ID && slices.Contains(task.BlockedBy, current.ID) {
				return TaskView{}, newError(CodeTaskHasDependents,
					"team task %q still blocks %q", current.ID, task.ID)
			}
		}
		next.Status = TaskDeleted
	default:
		return TaskView{}, newError(CodeInvalidArgument, "unsupported task action %q", req.Action)
	}

	next.Revision = current.Revision + 1
	if err := assertTaskGraph(s.state.Tasks, next); err != nil {
		return TaskView{}, err
	}
	for i := range s.state.Tasks {
		if s.state.Tasks[i].ID == current.ID {
			s.state.Tasks[i] = next
		}
	}
	s.saveLocked()
	s.notifyLocked()
	return projectTaskView(s.state, next), nil
}

func (s *Service) taskLocked(id string) (Task, bool) {
	for _, task := range s.state.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return Task{}, false
}

func (s *Service) dependenciesLocked(scope map[string]bool, ids []string, self string) ([]string, error) {
	seen := make(map[string]bool, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == self {
			return nil, newError(CodeTaskDependencyCycle, "a team task cannot block itself")
		}
		if seen[id] {
			return nil, newError(CodeInvalidArgument, "duplicate blocker %q", id)
		}
		task, ok := s.taskLocked(id)
		if !ok || task.Status == TaskDeleted || !taskVisibleLocked(scope, task) {
			return nil, newError(CodeTaskNotFound, "blocker task %q not found", id)
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}

func requiredText(value, field string, maxLength int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", newError(CodeInvalidArgument, "%s is required", field)
	}
	if len([]rune(trimmed)) > maxLength {
		return "", newError(CodeInvalidArgument, "%s must be at most %d characters", field, maxLength)
	}
	return trimmed, nil
}

// normalizeWriteScopes validates workspace-relative prefixes.
func normalizeWriteScopes(values []string) ([]string, error) {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.ReplaceAll(value, "\\", "/")
		normalized = strings.TrimPrefix(normalized, "./")
		normalized = strings.TrimRight(normalized, "/")
		segments := strings.Split(normalized, "/")
		if normalized == "" || strings.HasPrefix(normalized, "/") ||
			isWindowsDrive(normalized) ||
			slices.ContainsFunc(segments, func(segment string) bool {
				return segment == "" || segment == "." || segment == ".."
			}) {
			return nil, newError(CodeInvalidWriteScope, "invalid workspace-relative write scope %q", value)
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		result = append(result, normalized)
	}
	return result, nil
}

func isWindowsDrive(path string) bool {
	if len(path) < 2 || path[1] != ':' {
		return false
	}
	first := path[0]
	return first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z'
}

// taskReady reports whether every current blocker completed.
func taskReady(state State, task Task) bool {
	for _, id := range task.BlockedBy {
		blocker, ok := findTask(state, id)
		if !ok || blocker.Status != TaskCompleted {
			return false
		}
	}
	return true
}

func findTask(state State, id string) (Task, bool) {
	for _, task := range state.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return Task{}, false
}

// scopesOverlap reports whether two normalized prefixes overlap on path
// components.
func scopesOverlap(left, right string) bool {
	return left == right ||
		strings.HasPrefix(left, right+"/") ||
		strings.HasPrefix(right, left+"/")
}

// projectTaskView derives owner name, readiness, and write overlaps.
func projectTaskView(state State, task Task) TaskView {
	view := TaskView{
		ID:          task.ID,
		Revision:    task.Revision,
		Subject:     task.Subject,
		Description: task.Description,
		Status:      task.Status,
		OwnerID:     task.OwnerID,
		BlockedBy:   append([]string{}, task.BlockedBy...),
		WriteScopes: append([]string{}, task.WriteScopes...),
		Ready:       task.Status == TaskPending && taskReady(state, task),
		SessionID:   task.SessionID,
	}
	if task.OwnerID != "" {
		if task.OwnerID == leadName {
			view.OwnerName = leadName
		} else {
			for _, member := range state.Members {
				if member.ID == task.OwnerID {
					view.OwnerName = member.Name
				}
			}
		}
	}
	warnings := map[string]bool{}
	for _, other := range state.Tasks {
		if other.ID == task.ID || other.Status != TaskInProgress {
			continue
		}
		for _, left := range task.WriteScopes {
			for _, right := range other.WriteScopes {
				if scopesOverlap(left, right) {
					warnings[fmt.Sprintf("write scopes overlap with %s", other.ID)] = true
				}
			}
		}
	}
	view.WriteScopeWarnings = make([]string, 0, len(warnings))
	for warning := range warnings {
		view.WriteScopeWarnings = append(view.WriteScopeWarnings, warning)
	}
	slices.Sort(view.WriteScopeWarnings)
	return view
}

// TaskViews derives model-facing views for every stored task.
func TaskViews(state State) []TaskView {
	views := make([]TaskView, 0, len(state.Tasks))
	for _, task := range state.Tasks {
		views = append(views, projectTaskView(state, task))
	}
	return views
}

// assertTaskGraph rejects dependency cycles introduced by candidate.
func assertTaskGraph(tasks []Task, candidate Task) error {
	graph := make(map[string][]string, len(tasks)+1)
	for _, task := range tasks {
		if task.ID == candidate.ID {
			continue
		}
		if task.Status == TaskDeleted {
			continue
		}
		graph[task.ID] = task.BlockedBy
	}
	graph[candidate.ID] = candidate.BlockedBy
	state := make(map[string]int, len(graph))
	var visit func(string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			return false
		case 2:
			return true
		}
		state[id] = 1
		for _, next := range graph[id] {
			if _, ok := graph[next]; !ok {
				continue
			}
			if !visit(next) {
				return false
			}
		}
		state[id] = 2
		return true
	}
	for id := range graph {
		if !visit(id) {
			return newError(CodeTaskDependencyCycle, "team task dependency cycle")
		}
	}
	return nil
}

// resumePrompt renders queued peer messages for a teammate turn.
func resumePrompt(messages []Message) string {
	var sb strings.Builder
	sb.WriteString("New messages from your Team:\n")
	for _, message := range messages {
		fmt.Fprintf(&sb, "\n%s: %s\n", message.From, message.Text)
	}
	return sb.String()
}
