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

Agent Teams is available in this session

The Team Lead and all teammates share the same working directory and filesystem. Edits are immediately visible to every member. Split write work into disjoint scopes, record expected write scopes on shared tasks, and use task dependencies when work must be ordered. Write-scope overlap is advisory, not a lock.

Prefer read/edit/write for file changes. If a file operation returns FS_STALE_VERSION, read the current file, rebase your intended change onto the new content, and retry. Bash, formatters, code generators, and scripts are not fully protected by the filesystem version guard; coordinate them explicitly and have the Lead review the final diff and run tests.

Use the target returned by spawn_teammate or list_agents for send_message and interrupt_agent, or as owner when assigning or filtering shared tasks. send_message steers a running target at its nearest step boundary and starts or resumes an inactive target. inactive means no turn is executing; it does not describe task completion, success, failure, or waiting for other agents. provisioning means member creation is in progress; failed means member creation failed. A peer message starts with its sender name. A successful send does not mean the recipient has processed it. Shared-task workflow is list, get, claim with the current revision, perform the work, then complete. Task readiness never starts an owner. Before wait_agent, use list_agents and make sure another required member is running or provisioning; use send_message first when the required member is inactive. wait_agent observes only changes after that call starts, never wakes a member, and returns noProgress immediately when no other member can produce a change. Re-list after wakeup or timeout. The Lead must wait for required teammates before giving the final answer.`

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
}

// TaskView is the model-facing task row.
type TaskView struct {
	ID                 string     `json:"id"`
	Revision           int        `json:"revision"`
	Subject            string     `json:"subject"`
	Description        string     `json:"description"`
	Status             TaskStatus `json:"status"`
	OwnerName          string     `json:"ownerName,omitempty"`
	BlockedBy          []string   `json:"blockedBy"`
	WriteScopes        []string   `json:"writeScopes"`
	Ready              bool       `json:"ready"`
	WriteScopeWarnings []string   `json:"writeScopeWarnings"`
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
}

// WaitResult reports whether a wait timed out.
type WaitResult struct {
	TimedOut bool
}

// Run is one live teammate turn.
type Run interface {
	// SessionID is the Crush session backing the run.
	SessionID() string
	// Done closes when the run settles.
	Done() <-chan struct{}
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

// ListMembers returns the Lead first, then teammates in creation order.
func (s *Service) ListMembers(caller string) ([]MemberView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return nil, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	views := []MemberView{s.viewLocked(Member{ID: leadName, Name: leadName, Role: RoleLead})}
	for _, member := range s.state.Members {
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
	if len(s.state.Members) >= s.cfg.MaxMembers {
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
	if err := run.Err(); err != nil {
		for i := range s.state.Members {
			if s.state.Members[i].Name == name {
				s.state.Members[i].Diagnostics = append(
					s.state.Members[i].Diagnostics,
					err.Error(),
				)
			}
		}
	}
	for i := range s.state.Members {
		if s.state.Members[i].Name == name {
			s.state.Members[i].Status = StatusInactive
		}
	}
	s.saveLocked()
	s.notifyLocked()
	pending := s.drainLocked(name)
	member := s.stateMemberLocked(name)
	s.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	_, _ = s.startRun(context.WithoutCancel(context.Background()), member, resumePrompt(pending))
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

// SendMessage queues one direct message and wakes an inactive target.
func (s *Service) SendMessage(ctx context.Context, from string, req SendRequest) error {
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
	target, ok := s.memberLocked(req.Target)
	if !ok {
		s.mu.Unlock()
		return newError(CodeMemberNotFound, "Team member %q not found", req.Target)
	}
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
func (s *Service) WaitForChange(ctx context.Context, caller string, timeout time.Duration) (WaitResult, error) {
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
		return WaitResult{}, nil
	case <-timer.C:
		return WaitResult{TimedOut: true}, nil
	case <-ctx.Done():
		return WaitResult{TimedOut: true}, nil
	}
}

// Interrupt stops one teammate's current turn.
func (s *Service) Interrupt(caller, target string) (Status, error) {
	if caller != leadName {
		return "", newError(CodeLeadRequired, "only the Team Lead can interrupt a teammate")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if target == leadName {
		return "", newError(CodeInvalidArgument, "the Team Lead cannot interrupt itself")
	}
	if _, ok := s.memberLocked(target); !ok {
		return "", newError(CodeMemberNotFound, "Team member %q not found", target)
	}
	run, ok := s.runs[target]
	if !ok {
		return StatusInactive, nil
	}
	run.Cancel()
	return StatusRunning, nil
}

// HasActivePeer reports whether another member is running or provisioning.
func (s *Service) HasActivePeer(caller string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name := range s.runs {
		if name != caller {
			return true
		}
	}
	for _, member := range s.state.Members {
		if member.Name == caller {
			continue
		}
		if s.statusLocked(member.Name) == StatusProvisioning {
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

// CreateTask adds one unowned pending task.
func (s *Service) CreateTask(caller string, req CreateTaskRequest) (TaskView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return TaskView{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	active := 0
	for _, task := range s.state.Tasks {
		if task.Status != TaskDeleted {
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
	}
	if req.BlockedBy != nil {
		blockedBy, err := s.dependenciesLocked(req.BlockedBy, task.ID)
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

// GetTask returns one task view.
func (s *Service) GetTask(caller, id string) (TaskView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return TaskView{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	task, ok := s.taskLocked(id)
	if !ok {
		return TaskView{}, newError(CodeTaskNotFound, "team task %q not found", id)
	}
	return projectTaskView(s.state, task), nil
}

// ListTasks returns a filtered page of shared tasks.
func (s *Service) ListTasks(caller string, filter TaskFilter) (TaskList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return TaskList{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
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
	defer s.mu.Unlock()
	member, ok := s.memberLocked(caller)
	if !ok {
		return TaskView{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	current, ok := s.taskLocked(req.TaskID)
	if !ok {
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
		blockedBy, err := s.dependenciesLocked(req.BlockedBy, current.ID)
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
		if !ok {
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

func (s *Service) dependenciesLocked(ids []string, self string) ([]string, error) {
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
		if !ok || task.Status == TaskDeleted {
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
		BlockedBy:   append([]string{}, task.BlockedBy...),
		WriteScopes: append([]string{}, task.WriteScopes...),
		Ready:       task.Status == TaskPending && taskReady(state, task),
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
