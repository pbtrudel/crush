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
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/lock"
)

// Defaults mirror the experimental DSH Agent Teams bundle.
const (
	DefaultMaxMembers      = 8
	DefaultMaxTasks        = 256
	DefaultMaxMessageBytes = 65536

	leadName = "lead"

	// defaultLockTimeout bounds how long a write waits for another
	// process to finish its own.
	defaultLockTimeout = 5 * time.Second
)

// LeadName is the roster name of the Team Lead. Frontends that steer a
// team on the user's behalf act as the Lead.
const LeadName = leadName

// Policy is the shared coordination guidance every Team member receives. It
// mirrors the experimental DSH Agent Teams policy.
const Policy = `## Agent Teams

Agent Teams is available in this session. Use it by default: the Lead coordinates and delegates, and teammates do the work.

Work solo only for a single trivial action (one command, one file read, a direct factual answer) or when the user explicitly asks you to. Everything else goes to the Team: create shared tasks, spawn at least one teammate, and delegate. Requests with two or more steps, multiple files, investigation, or tests must use the Team. Reuse existing teammates before spawning new ones. When in doubt, delegate.

One teammate per concrete, self-contained brief, never a wave. Do not spawn near-identical teammates for a small or routine job: do the work inline when it is small. Cap a fan-out at what the task actually needs.

The Team Lead and all teammates share the same working directory and filesystem. Edits are immediately visible to every member. Split write work into disjoint scopes, record expected write scopes on shared tasks, and use task dependencies when work must be ordered. Write-scope overlap is advisory, not a lock.

The Lead owns the task worktree. Create it with the worktree tool before delegating, include its absolute path in every task description, and have every teammate work inside that path. Remove it only after the branch is pushed and merged.

Prefer read/edit/write for file changes. If an edit or write is refused because the file changed since you read it, read the current file, rebase your intended change onto the new content, and retry. Bash, formatters, code generators, and scripts are not fully protected by the filesystem version guard; coordinate them explicitly and have the Lead review the final diff and run tests.

Keep the shared task board current at every step, because the user watches it. Create a task for each unit of work before delegating it, claim it with the current revision, and complete it the moment the work is verified. Edit tasks whose scope changed, and delete tasks that are superseded, abandoned, or no longer relevant. Once a completed task's result has been reported, delete it too, so the board holds only live work. Never report work as done while its task is still pending or in progress.

Report progress to the Lead as you work. Send a one-line message when you start a task, when you finish it, and when you are blocked; never end a turn silently. A settled turn also sends the Lead an automatic report, and wait_agent returns the current roster and live task board, so the Lead always knows where things stand. Finish your tasks before ending a turn: the service completes the tasks you leave in progress when your turn succeeds, and releases them when it fails.

Use the target returned by spawn_teammate or list_agents for send_message and interrupt_agent, or as owner when assigning or filtering shared tasks. send_message steers a running target at its nearest step boundary and starts or resumes an inactive target. inactive means no turn is executing; it does not describe task completion, success, failure, or waiting for other agents. provisioning means member creation is in progress; failed means member creation failed. A peer message starts with its sender name. A successful send does not mean the recipient has processed it. Shared-task workflow is list, get, claim with the current revision, perform the work, then complete. Task readiness never starts an owner. Before wait_agent, use list_agents and make sure another required member is running or provisioning; use send_message first when the required member is inactive. wait_agent observes only changes after that call starts, never wakes a member, and returns noProgress immediately when no other member can produce a change; it also returns the current roster and live task board, so re-list only when you need the full task details. The Lead must wait for required teammates before giving the final answer.`

// ToolNames are the tool names that make the Policy apply. A prompt package
// cannot import the agent package that declares them, so they are repeated
// here for the prompt builder to check a palette against.
var ToolNames = []string{
	"spawn_teammate",
	"send_message",
	"list_agents",
	"wait_agent",
	"interrupt_agent",
	"team_task_create",
	"team_task_list",
	"team_task_get",
	"team_task_update",
}

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
	CodeStateBusy             = "TEAM_STATE_BUSY"
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
// Provider is the runtime that hosts the member, not its model provider;
// the optional model override is Model, ModelProvider, and Reasoning.
type Member struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Role          Role        `json:"role"`
	Description   string      `json:"description,omitempty"`
	Provider      string      `json:"provider,omitempty"`
	Context       ContextMode `json:"context,omitempty"`
	Model         string      `json:"model,omitempty"`
	ModelProvider string      `json:"model_provider,omitempty"`
	Reasoning     string      `json:"reasoning,omitempty"`
	SessionID     string      `json:"session_id,omitempty"`
	ParentID      string      `json:"parent_session_id,omitempty"`
	Status        Status      `json:"status,omitempty"`
	Failed        bool        `json:"failed,omitempty"`
	Diagnostics   []string    `json:"diagnostics,omitempty"`
}

// MemberView is the model-facing roster row.
type MemberView struct {
	Target        string      `json:"target"`
	Role          Role        `json:"role"`
	Status        Status      `json:"status"`
	Description   string      `json:"description,omitempty"`
	Provider      string      `json:"provider,omitempty"`
	Context       ContextMode `json:"context,omitempty"`
	Model         string      `json:"model,omitempty"`
	ModelProvider string      `json:"model_provider,omitempty"`
	Reasoning     string      `json:"reasoning,omitempty"`
	Diagnostics   []string    `json:"diagnostics"`
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
	Name          string
	Description   string
	Prompt        string
	Context       ContextMode
	Provider      string
	Model         string
	ModelProvider string
	Reasoning     string
	ParentID      string
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
	// watchers tracks the goroutines settling runs, so Shutdown can wait
	// for the last state write instead of returning while one is pending.
	watchers sync.WaitGroup
	// closed is set by Shutdown. A settling run must not resume its member
	// once it is set, or it would add a watcher after the wait began.
	closed  bool
	changes chan struct{}
	now     func() time.Time
	// stamp is the state file this process last read or wrote. A different
	// stamp means another process wrote since.
	stamp fileStamp
	// unsaved is set while a write failed. The in-memory record is the
	// newest until the next save succeeds, so reloading would drop it.
	unsaved bool
	// mine names the members this process spawned or started. Only it may
	// report their status.
	mine map[string]bool
	// wrote names the messages this process appended. Only it may deliver
	// them.
	wrote map[string]bool
	// lockTimeout bounds the wait for the cross-process state lock.
	lockTimeout time.Duration
	// lockWarned makes an unexpected lock failure warn once per Service.
	lockWarned bool
	// taskCompletedHook is called after a task settles as completed. It
	// runs without the service lock and is best-effort.
	taskCompletedHook func(sessionID, subject string)
}

// New opens (or creates) the Team state at path. A nil runner supports
// read-only use, such as the CLI status view.
func New(path string, cfg Config, runner Runner) (*Service, error) {
	// Stat before loading. A stamp older than the loaded record costs one
	// extra reload; a stamp newer than it would drop another process's
	// write on the first save. The load takes no lock, because the file is
	// replaced by rename and is never read half-written.
	stamp, _ := statState(path)
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
	repairCounters(&state)
	return &Service{
		state:       state,
		path:        path,
		cfg:         cfg.withDefaults(),
		runner:      runner,
		runs:        make(map[string]Run),
		starting:    make(map[string]bool),
		changes:     make(chan struct{}),
		now:         time.Now,
		stamp:       stamp,
		mine:        make(map[string]bool),
		wrote:       make(map[string]bool),
		lockTimeout: defaultLockTimeout,
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

// writeMode says what a write does when another process holds the state lock
// for longer than the timeout.
type writeMode int

const (
	// refusable is a tool-facing write. It refuses with TEAM_STATE_BUSY
	// rather than risk losing another process's update.
	refusable writeMode = iota
	// mustApply records work that already happened, such as a settled run.
	// It falls back to an unlocked write.
	mustApply
)

// stateLocks holds one slot per state file, so Services in this process
// queue on a Go channel instead of spinning on the file lock.
var stateLocks sync.Map

// write runs fn on the freshest durable record. fn runs with s.mu held and
// reports whether it changed the record; a change is saved before the locks
// drop.
//
// The lock order is always the process mutex, then the file lock, then s.mu.
// Never call write with s.mu held, and never start a run, call a hook, write
// a report, or wait on anything else inside fn.
func (s *Service) write(mode writeMode, fn func() (bool, error)) error {
	release, err := s.lockState(mode)
	if err != nil {
		return err
	}
	defer release()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	changed, err := fn()
	if err == nil && changed {
		s.saveLocked()
	}
	return err
}

// lockState takes the in-process write slot and the cross-process state
// lock, and returns the release for both. A refusable write whose wait times
// out fails with TEAM_STATE_BUSY and changes nothing, so a retry is safe.
// Any other failure warns and continues without the file lock, but still
// takes turns with the other Services in this process, which is what Crush
// did before the file lock existed.
func (s *Service) lockState(mode writeMode) (func(), error) {
	if s.path == "" {
		return func() {}, nil
	}
	key := s.path
	if abs, err := filepath.Abs(s.path); err == nil {
		key = abs
	}
	slot, err := s.takeSlot(key, mode)
	if err != nil {
		return nil, err
	}
	release := func() { <-slot }
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		// The save below reports this too, and it must not be skipped:
		// the record stays in memory until a later save succeeds.
		s.warnLockFailed(fmt.Errorf("create team state directory: %w", err))
		return release, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.lockTimeout)
	unlock, err := lock.File(ctx, lockPath(s.path))
	cancel()
	if err == nil {
		return func() {
			unlock()
			release()
		}, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		if mode == refusable {
			release()
			return nil, newError(CodeStateBusy, "Team state is busy in another Crush process; retry")
		}
		slog.Warn("Wrote the Team state without its lock", "path", s.path, "error", err)
		return release, nil
	}
	s.warnLockFailed(err)
	return release, nil
}

// takeSlot waits for the one write slot of the state file at key. The wait
// is bounded by lockTimeout, so no write queues behind another for longer
// than that; a mustApply write waits it out, because the work it records has
// already happened.
func (s *Service) takeSlot(key string, mode writeMode) (chan struct{}, error) {
	value, _ := stateLocks.LoadOrStore(key, make(chan struct{}, 1))
	slot := value.(chan struct{})
	timer := time.NewTimer(s.lockTimeout)
	defer timer.Stop()
	select {
	case slot <- struct{}{}:
		return slot, nil
	case <-timer.C:
	}
	if mode == refusable {
		return nil, newError(CodeStateBusy, "Team state is busy in another Crush process; retry")
	}
	slot <- struct{}{}
	return slot, nil
}

// warnLockFailed reports an unusable state lock once per Service: a
// filesystem without advisory locks would otherwise log on every write.
func (s *Service) warnLockFailed(err error) {
	if s.lockWarned {
		return
	}
	s.lockWarned = true
	slog.Warn("Could not lock the shared Team state", "path", s.path, "error", err)
}

// reloadLocked adopts the record another process wrote since this one last
// read or wrote it. Callers must hold s.mu.
func (s *Service) reloadLocked() {
	if s.path == "" {
		return
	}
	if s.unsaved {
		// The in-memory record is newer than the file. A reload would drop
		// it, and the next successful save overwrites the file.
		return
	}
	stamp, statErr := statState(s.path)
	if statErr == nil && stamp.equal(s.stamp) {
		return
	}
	if statErr != nil && errors.Is(statErr, fs.ErrNotExist) {
		// The file is gone. Memory is the newest record this process has,
		// and the next save recreates the file.
		return
	}
	disk, err := loadState(s.path)
	if err != nil {
		slog.Warn("Could not read the shared Team state", "path", s.path, "error", err)
		return
	}
	if statErr == nil {
		s.stamp = stamp
	}
	s.mergeLocked(disk)
}

// mergeLocked takes disk as the record and puts back what only this process
// knows: the identity of a Team that was never written, and the members it
// runs, with their live status. Callers must hold s.mu.
func (s *Service) mergeLocked(disk State) {
	prev := s.state
	if disk.TeamID == "" {
		disk.TeamID = prev.TeamID
		disk.CreatedAt = prev.CreatedAt
		disk.Version = prev.Version
	}
	s.state = disk
	for name, run := range s.runs {
		for i := range s.state.Members {
			member := &s.state.Members[i]
			if member.Name != name {
				continue
			}
			member.SessionID = run.SessionID()
			member.Status = StatusRunning
			member.Failed = false
		}
	}
	for _, member := range prev.Members {
		if s.hasMemberLocked(member.Name) {
			continue
		}
		if s.runs[member.Name] == nil && !s.starting[member.Name] && !s.mine[member.Name] {
			continue
		}
		s.state.Members = append(s.state.Members, member)
	}
	repairCounters(&s.state)
}

// repairCounters lifts each id counter to the highest suffix its ids already
// use, so a file an older or buggy writer left behind cannot hand out an id
// twice. Ids themselves are never renumbered.
func repairCounters(state *State) {
	for _, member := range state.Members {
		state.NextMember = max(state.NextMember, idSuffix(member.ID, "member-"))
	}
	for _, task := range state.Tasks {
		state.NextTask = max(state.NextTask, idSuffix(task.ID, "task-"))
	}
	for _, message := range state.Messages {
		state.NextMessage = max(state.NextMessage, idSuffix(message.ID, "message-"))
	}
}

// idSuffix is the number in an id like message-12, or zero when id does not
// have that shape.
func idSuffix(id, prefix string) int {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok {
		return 0
	}
	number, err := strconv.Atoi(rest)
	if err != nil || number < 0 {
		return 0
	}
	return number
}

// hasMemberLocked reports whether name is on the durable roster. Callers
// must hold s.mu.
func (s *Service) hasMemberLocked(name string) bool {
	for _, member := range s.state.Members {
		if member.Name == name {
			return true
		}
	}
	return false
}

// saveLocked persists the current state and records the stamp of the file it
// wrote. Callers must hold s.mu.
func (s *Service) saveLocked() {
	if s.path == "" {
		return
	}
	stamp, err := saveState(s.path, s.state)
	if err != nil {
		// Persistence is best effort: a failed write must not fail the tool
		// call that already changed in-memory state, and the record stays in
		// memory until a later save succeeds.
		slog.Warn("Could not save the shared Team state", "path", s.path, "error", err)
		s.unsaved = true
		return
	}
	s.stamp = stamp
	s.unsaved = false
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
	if !s.mine[name] {
		switch member.Status {
		case StatusRunning, StatusProvisioning, "":
			// Only the process that runs a member writes its status, and
			// no run survives a restart, so a busy status this process did
			// not write is stale.
			return StatusInactive
		}
	}
	if member.Status != "" {
		return member.Status
	}
	return StatusInactive
}

func (s *Service) viewLocked(member Member) MemberView {
	view := MemberView{
		Target:        member.Name,
		Role:          member.Role,
		Status:        s.statusLocked(member.Name),
		Description:   member.Description,
		Provider:      member.Provider,
		Context:       member.Context,
		Model:         member.Model,
		ModelProvider: member.ModelProvider,
		Reasoning:     member.Reasoning,
		Diagnostics:   append([]string{}, member.Diagnostics...),
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

	var member Member
	err := s.write(refusable, func() (bool, error) {
		if _, exists := s.memberLocked(req.Name); exists {
			return false, newError(CodeInvalidMemberName, "teammate name %q is already used", req.Name)
		}
		// The limit bounds the teammates working at once, not the durable
		// roster: a workspace keeps every member it ever created, so
		// counting settled members would lock a new team out of the
		// workspace forever.
		live := 0
		for _, existing := range s.state.Members {
			switch s.statusLocked(existing.Name) {
			case StatusRunning, StatusProvisioning:
				live++
			}
		}
		if live >= s.cfg.MaxMembers {
			return false, newError(CodeMemberLimit, "Team member limit %d reached", s.cfg.MaxMembers)
		}
		s.state.NextMember++
		member = Member{
			ID:            fmt.Sprintf("member-%d", s.state.NextMember),
			Name:          req.Name,
			Role:          RoleTeammate,
			Description:   description,
			Provider:      req.Provider,
			Context:       mode,
			Model:         req.Model,
			ModelProvider: req.ModelProvider,
			Reasoning:     req.Reasoning,
			ParentID:      req.ParentID,
			Status:        StatusProvisioning,
		}
		s.state.Members = append(s.state.Members, member)
		s.mine[member.Name] = true
		s.notifyLocked()
		return true, nil
	})
	if err != nil {
		return MemberView{}, err
	}

	view, err := s.startRun(ctx, member, req.Prompt)
	if err != nil {
		if errors.Is(err, errShutdown) {
			// Shutdown owns the Team now. Failing the member here would
			// write after Shutdown returned and leave a teammate a later
			// enable resumes as failed.
			return MemberView{}, err
		}
		s.failMember(member.Name, err)
		return s.viewFor(member.Name), nil
	}
	return view, nil
}

var (
	errNoRunner = errors.New("no teammate runner is configured")
	errShutdown = errors.New("agent team is shut down")
)

// startRun starts one turn and records the live run.
func (s *Service) startRun(ctx context.Context, member Member, prompt string) (MemberView, error) {
	s.mu.Lock()
	runner := s.runner
	if runner == nil {
		s.mu.Unlock()
		return MemberView{}, errNoRunner
	}
	if s.starting[member.Name] {
		view := s.viewLocked(s.stateMemberLocked(member.Name))
		s.mu.Unlock()
		return view, nil
	}
	s.starting[member.Name] = true
	s.mu.Unlock()
	return s.launch(ctx, runner, member, prompt)
}

// launch runs one turn for a member the caller already marked as starting,
// so the member reads as running from that mark until the run settles.
func (s *Service) launch(ctx context.Context, runner Runner, member Member, prompt string) (MemberView, error) {
	// A turned-off Team starts no work. Checking before the start also keeps
	// a refused turn from briefly running only to be cancelled below.
	s.mu.Lock()
	if s.closed {
		delete(s.starting, member.Name)
		s.mu.Unlock()
		return MemberView{}, errShutdown
	}
	s.mu.Unlock()

	var run Run
	err := errNoRunner
	if runner != nil {
		run, err = runner.Start(ctx, member, prompt)
	}
	if err != nil {
		s.mu.Lock()
		delete(s.starting, member.Name)
		s.mu.Unlock()
		return MemberView{}, err
	}
	var view MemberView
	tracked := false
	// The run already started, so this record must land: the member would
	// otherwise read as idle and its inbox would never be drained.
	_ = s.write(mustApply, func() (bool, error) {
		delete(s.starting, member.Name)
		if s.closed {
			// Shutdown took the live runs while this one was starting, so
			// it is not counted and must not outlive the wait below.
			return false, nil
		}
		s.runs[member.Name] = run
		s.mine[member.Name] = true
		for i := range s.state.Members {
			if s.state.Members[i].Name != member.Name {
				continue
			}
			s.state.Members[i].SessionID = run.SessionID()
			s.state.Members[i].Failed = false
			s.state.Members[i].Status = StatusRunning
		}
		view = s.viewLocked(s.stateMemberLocked(member.Name))
		s.notifyLocked()
		// Counted under the lock Shutdown takes before it waits, so the
		// watcher is either counted before the wait starts or never started.
		s.watchers.Add(1)
		tracked = true
		return true, nil
	})
	if !tracked {
		run.Cancel()
		return MemberView{}, errShutdown
	}
	go func() {
		defer s.watchers.Done()
		s.watch(member.Name, run)
	}()
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

	// Write the full deliverable before taking the service lock: writing
	// the file is the only slow part of settling, and every other member's
	// tool call waits on that lock.
	output := run.Output()
	fullReport := ""
	if text := strings.TrimSpace(output); len(text) > maxReportInline {
		fullReport = s.writeReport(name, text)
	}

	runErr := run.Err()
	var (
		member   Member
		pending  []Message
		runner   Runner
		hook     func(sessionID, subject string)
		complete []string
	)
	// The run already happened, so its settlement must land even when
	// another process keeps the state lock.
	_ = s.write(mustApply, func() (bool, error) {
		if s.runs[name] == run {
			delete(s.runs, name)
		}
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
		member = s.stateMemberLocked(name)
		settled, completed := s.settleTasksLocked(member.ID, runErr)
		complete = completed
		s.appendMessageLocked(name, leadName, runReport(name, output, settled, runErr, fullReport))
		s.notifyLocked()
		hook = s.taskCompletedHook
		if s.closed {
			// Shutdown owns the member now. Leave its inbox queued on disk
			// for a later Service instead of starting a turn that would
			// outlive the wait.
			return true, nil
		}
		pending = s.drainLocked(name, member.ParentID)
		runner = s.runner
		if len(pending) > 0 {
			// Marked under the lock that took the messages, so the member
			// never reads as idle between losing its inbox and starting its
			// next turn.
			s.starting[name] = true
		}
		return true, nil
	})
	if hook != nil {
		for _, subject := range complete {
			hook(member.ParentID, subject)
		}
	}
	if len(pending) == 0 {
		return
	}
	if _, err := s.launch(context.WithoutCancel(context.Background()), runner, member, resumePrompt(pending)); err != nil {
		if errors.Is(err, errShutdown) {
			// Shutdown took the member after this turn drained its inbox.
			// Hand the messages back so a later Service delivers them.
			_ = s.write(mustApply, func() (bool, error) {
				for _, message := range pending {
					for i := range s.state.Messages {
						if s.state.Messages[i].ID == message.ID {
							s.state.Messages[i].Delivered = false
						}
					}
				}
				s.notifyLocked()
				return true, nil
			})
			return
		}
		text := fmt.Sprintf("Could not resume %s: %s\n\n%s", name, err, resumePrompt(pending))
		_ = s.write(mustApply, func() (bool, error) {
			s.appendMessageLocked(name, leadName, text)
			s.notifyLocked()
			return true, nil
		})
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

// appendMessageLocked adds one durable inbox message and records it as this
// process's, so only this process delivers it. Callers must hold s.mu.
func (s *Service) appendMessageLocked(from, to, text string) Message {
	text = strings.TrimSpace(text)
	if len(text) > s.cfg.MaxMessageBytes {
		text = truncateText(text, s.cfg.MaxMessageBytes)
	}
	s.state.NextMessage++
	message := Message{
		ID:        fmt.Sprintf("message-%d", s.state.NextMessage),
		From:      from,
		To:        to,
		Text:      text,
		CreatedAt: s.now(),
	}
	s.state.Messages = append(s.state.Messages, message)
	s.wrote[message.ID] = true
	return message
}

// maxReportInline is how much of a teammate's final message the automatic
// report carries. A teammate's final message is its deliverable, so anything
// past this cap is written to a file instead of being dropped.
const maxReportInline = 2000

const (
	// reportSubdir is where full reports are written, next to the team
	// state so the Lead can read one with the view tool.
	reportSubdir = "reports"
	// reportRetention is how long a written report outlives its turn. The
	// Lead may read a report later in the same session, so files are swept
	// by age instead of being deleted when the turn that wrote them ends.
	reportRetention = 7 * 24 * time.Hour
)

// writeReport writes the full report next to the team state and returns its
// path, or "" when it could not be written. The caller then falls back to the
// cut inline text: delivery must never depend on the file.
func (s *Service) writeReport(name, text string) string {
	if s.path == "" {
		// An in-memory Team has nowhere to put a file, and the process's
		// working directory is not ours to write to.
		return ""
	}
	dir := filepath.Join(filepath.Dir(s.path), reportSubdir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("Could not create the teammate report directory", "dir", dir, "error", err)
		return ""
	}
	sweepReports(dir, s.now())

	file, err := os.CreateTemp(dir, name+"-*.md")
	if err != nil {
		slog.Warn("Could not create a teammate report file", "dir", dir, "error", err)
		return ""
	}
	path := file.Name()
	if _, err := file.WriteString(text); err != nil {
		file.Close()    //nolint:errcheck
		os.Remove(path) //nolint:errcheck
		slog.Warn("Could not write a teammate report file", "path", path, "error", err)
		return ""
	}
	if err := file.Close(); err != nil {
		os.Remove(path) //nolint:errcheck
		slog.Warn("Could not close a teammate report file", "path", path, "error", err)
		return ""
	}
	return path
}

// sweepReports deletes reports older than reportRetention. It is best effort:
// a file that cannot be swept is not worth failing a report over.
func sweepReports(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-reportRetention)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			// Only files this package writes are swept; anything else in the
			// directory is not ours to delete.
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil {
			slog.Debug("Could not delete an old teammate report", "path", path, "error", err)
		}
	}
}

// runReport renders the automatic progress report a settled teammate sends
// the Lead. fullReport is the file holding the whole output when it did not
// fit, or empty when nothing was written.
//
// It stays pure so it can be tested on its own: the file is written by the
// caller, which reads the output anyway.
func runReport(
	name, output string,
	settled []string,
	runErr error,
	fullReport string,
) string {
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
		if len(text) > maxReportInline {
			head := truncateText(text, maxReportInline)
			note := ""
			if fullReport != "" {
				// The head is measured, not assumed: a cut that lands
				// mid-rune keeps fewer bytes than the cap.
				note = fmt.Sprintf(
					"\n\n[Report cut: %d of %d bytes shown. Full report: %s]",
					len(head), len(text), fullReport,
				)
			}
			text = head + "…" + note
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
	_ = s.write(mustApply, func() (bool, error) {
		for i := range s.state.Members {
			if s.state.Members[i].Name != name {
				continue
			}
			s.state.Members[i].Failed = true
			s.state.Members[i].Status = StatusFailed
			s.state.Members[i].Diagnostics = append(
				s.state.Members[i].Diagnostics,
				err.Error(),
			)
		}
		s.notifyLocked()
		return true, nil
	})
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
	var (
		target  Member
		running bool
	)
	err := s.write(refusable, func() (bool, error) {
		if _, ok := s.memberLocked(from); !ok {
			return false, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", from)
		}
		if !s.memberVisibleLocked(s.scopeLocked(from, sessionID), req.Target) {
			return false, newError(CodeMemberNotFound, "Team member %q not found", req.Target)
		}
		target, _ = s.memberLocked(req.Target)
		s.appendMessageLocked(from, target.Name, req.Message)
		s.notifyLocked()
		running = s.statusLocked(target.Name) == StatusRunning
		return true, nil
	})
	if err != nil {
		return err
	}

	if running || target.Role == RoleLead {
		return nil
	}
	pending := s.DrainInbox(target.Name, target.ParentID)
	if len(pending) == 0 {
		return nil
	}
	if _, err := s.startRun(ctx, target, resumePrompt(pending)); err != nil {
		// The member is no longer starting, so wake whoever reads it as
		// busy.
		s.mu.Lock()
		s.notifyLocked()
		s.mu.Unlock()
		return err
	}
	return nil
}

// noticeSender names the sender of a notice the runtime itself delivers, such
// as a finished background job, rather than a Team member. It fails
// ValidateMemberName, so no teammate can send mail under it.
const noticeSender = "Crush"

// NotifySession delivers a runtime notice to the teammate that owns
// sessionID and reports whether a teammate owns it. A running teammate gets
// it in its inbox; an inactive one is woken on its own run, and so on its own
// model. The caller must not wake the session itself when this reports true:
// the Lead's agent would run the teammate's session on the Lead's model.
func (s *Service) NotifySession(ctx context.Context, sessionID, text string) (bool, error) {
	if sessionID == "" || strings.TrimSpace(text) == "" {
		return false, nil
	}
	var (
		target  Member
		found   bool
		running bool
	)
	err := s.write(mustApply, func() (bool, error) {
		for _, member := range s.state.Members {
			if member.Role != RoleLead && member.SessionID == sessionID {
				target, found = member, true
				break
			}
		}
		if !found {
			return false, nil
		}
		s.appendMessageLocked(noticeSender, target.Name, text)
		s.notifyLocked()
		running = s.statusLocked(target.Name) == StatusRunning
		return true, nil
	})
	if err != nil || !found {
		return found, err
	}
	if running {
		return true, nil
	}
	pending := s.DrainInbox(target.Name, target.ParentID)
	if len(pending) == 0 {
		return true, nil
	}
	if _, err := s.startRun(ctx, target, resumePrompt(pending)); err != nil {
		s.mu.Lock()
		s.notifyLocked()
		s.mu.Unlock()
		return true, err
	}
	return true, nil
}

// DrainInbox returns and marks delivered every pending message for name that
// belongs to the caller's team.
func (s *Service) DrainInbox(name, sessionID string) []Message {
	// The team tools drain on every response, read-only ones included. With
	// an unchanged file and no mail waiting there is nothing to take and no
	// reason to wait for another process's lock. A missing state file is
	// quiet too: nothing has been saved yet, so there is no mail on disk and
	// no reason to create the directory and the lock file.
	s.mu.Lock()
	stamp, statErr := statState(s.path)
	unchanged := (statErr == nil && stamp.equal(s.stamp)) || errors.Is(statErr, fs.ErrNotExist)
	quiet := unchanged && !s.hasPendingLocked(name, sessionID)
	s.mu.Unlock()
	if quiet {
		return nil
	}
	var pending []Message
	// Mail that is read has already been handed over, so it must reach the
	// file even when another process holds the state lock.
	_ = s.write(mustApply, func() (bool, error) {
		pending = s.drainLocked(name, sessionID)
		return len(pending) > 0, nil
	})
	return pending
}

// hasPendingLocked reports whether any message is waiting for name in the
// caller's scope. Callers must hold s.mu.
func (s *Service) hasPendingLocked(name, sessionID string) bool {
	scope := s.scopeLocked(name, sessionID)
	if len(scope) == 0 {
		return false
	}
	for i := range s.state.Messages {
		if s.inboxPendingLocked(scope, &s.state.Messages[i], name) {
			return true
		}
	}
	return false
}

// drainLocked takes the messages name may read: those a member of the
// caller's scope wrote. Callers must hold s.mu.
func (s *Service) drainLocked(name, sessionID string) []Message {
	scope := s.scopeLocked(name, sessionID)
	if len(scope) == 0 {
		return nil
	}
	var pending []Message
	for i := range s.state.Messages {
		message := &s.state.Messages[i]
		if !s.inboxPendingLocked(scope, message, name) {
			continue
		}
		message.Delivered = true
		pending = append(pending, *message)
	}
	return pending
}

// inboxPendingLocked reports whether message is waiting for name in scope.
// Every Lead session in a workspace answers to "lead", so mail for the Lead
// counts only when a teammate of this scope wrote it: the sender is what says
// which session the message belongs to. Callers must hold s.mu.
func (s *Service) inboxPendingLocked(scope map[string]bool, message *Message, name string) bool {
	if message.To != name || message.Delivered {
		return false
	}
	if name == leadName {
		member, ok := s.memberLocked(message.From)
		return ok && scope[member.ParentID]
	}
	return message.From == noticeSender || s.memberVisibleLocked(scope, message.From)
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

// TreeStatus is what the Team service knows about the team one Lead
// session started.
type TreeStatus struct {
	// Sessions are the sessions of the tree's teammates that have run.
	Sessions []string
	// Active counts the teammates running, being started or about to be
	// started again.
	Active int
	// UnreadByTeammates counts messages written by this process to the
	// tree's teammates that they have not received yet.
	UnreadByTeammates int
	// UnreadByLead counts messages from the tree's teammates, their reports
	// included, that the Lead has not read.
	UnreadByLead int
}

// Tree reports the state of the team that sessionID leads. The run command
// reads it to decide whether delegated work is still in flight.
func (s *Service) Tree(sessionID string) TreeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tree TreeStatus
	scope := s.scopeLocked(leadName, sessionID)
	members := make(map[string]bool)
	for _, member := range s.visibleMembersLocked(scope) {
		members[member.Name] = true
		if member.SessionID != "" {
			tree.Sessions = append(tree.Sessions, member.SessionID)
		}
		switch s.statusLocked(member.Name) {
		case StatusRunning, StatusProvisioning:
			tree.Active++
		}
	}
	for i := range s.state.Messages {
		msg := &s.state.Messages[i]
		switch {
		case msg.Delivered:
		// A failed member never drains its inbox on its own, so its
		// waiting messages would hold the tree busy for good. Sending to
		// it restarts it and drains the inbox, and that sender's own turn
		// is counted while it runs. It stays in members so a report it
		// already wrote still counts below.
		case members[msg.To] && s.wrote[msg.ID] && s.statusLocked(msg.To) != StatusFailed:
			tree.UnreadByTeammates++
		// Mail counts only when this tree's Lead may drain it, so
		// another Lead session reading a report cannot make it vanish
		// from the count of the Lead that never saw it.
		case s.inboxPendingLocked(scope, msg, leadName):
			tree.UnreadByLead++
		}
	}
	return tree
}

// CancelTree cancels the running turns of the team that sessionID leads.
// The members stay on the roster; each turn settles and reports as usual.
func (s *Service) CancelTree(sessionID string) {
	s.mu.Lock()
	var runs []Run
	for _, member := range s.visibleMembersLocked(s.scopeLocked(leadName, sessionID)) {
		if run, ok := s.runs[member.Name]; ok {
			runs = append(runs, run)
		}
	}
	s.mu.Unlock()
	for _, run := range runs {
		run.Cancel()
	}
}

// Changed returns a channel that is closed at the next change to the
// roster, a member's status, a queued message or the task board. Draining
// an inbox does not close it. Take it before reading the state it guards,
// so a change made after the read cannot be missed.
func (s *Service) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changes
}

// Shutdown cancels this process's live teammate runs and marks its members
// inactive. It waits for every settling run to write the state, so no write
// lands after it returns. Other processes run their own members and keep
// doing so, and the durable state is kept so a later enable resumes the same
// Team.
func (s *Service) Shutdown() {
	var runs []Run
	_ = s.write(mustApply, func() (bool, error) {
		s.closed = true
		changed := false
		for name, run := range s.runs {
			runs = append(runs, run)
			delete(s.runs, name)
			changed = true
		}
		for i := range s.state.Members {
			if !s.mine[s.state.Members[i].Name] {
				continue
			}
			if s.state.Members[i].Status != StatusInactive {
				changed = true
			}
			s.state.Members[i].Status = StatusInactive
		}
		s.notifyLocked()
		return changed, nil
	})
	for _, run := range runs {
		run.Cancel()
	}
	s.watchers.Wait()
}

// CreateTask adds one unowned pending task to the caller's team board.
func (s *Service) CreateTask(caller, sessionID string, req CreateTaskRequest) (TaskView, error) {
	var view TaskView
	err := s.write(refusable, func() (bool, error) {
		if _, ok := s.memberLocked(caller); !ok {
			return false, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
		}
		root := s.scopeRootLocked(caller, sessionID)
		if root == "" {
			return false, newError(CodeNotMember, "agent %q has no session to own a team task", caller)
		}
		scope := s.scopeLocked(caller, sessionID)
		active := 0
		for _, task := range s.state.Tasks {
			if task.Status != TaskDeleted && taskVisibleLocked(scope, task) {
				active++
			}
		}
		if active >= s.cfg.MaxTasks {
			return false, newError(CodeTaskLimit, "Team task limit %d reached", s.cfg.MaxTasks)
		}
		subject, err := requiredText(req.Subject, "subject", 200)
		if err != nil {
			return false, err
		}
		description, err := requiredText(req.Description, "description", 16384)
		if err != nil {
			return false, err
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
				return false, err
			}
			task.BlockedBy = blockedBy
		}
		if req.WriteScopes != nil {
			scopes, err := normalizeWriteScopes(req.WriteScopes)
			if err != nil {
				s.state.NextTask--
				return false, err
			}
			task.WriteScopes = scopes
		}
		if err := assertTaskGraph(s.state.Tasks, task); err != nil {
			s.state.NextTask--
			return false, err
		}
		s.state.Tasks = append(s.state.Tasks, task)
		view = projectTaskView(s.state, task)
		s.notifyLocked()
		return true, nil
	})
	if err != nil {
		return TaskView{}, err
	}
	return view, nil
}

// GetTask returns one task view from the caller's team board.
func (s *Service) GetTask(caller, sessionID, id string) (TaskView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.memberLocked(caller); !ok {
		return TaskView{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	_, task, ok := s.taskIndexLocked(s.scopeLocked(caller, sessionID), id)
	if !ok {
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

// taskUpdate is one applied task transition and what the caller must
// report once the write lands.
type taskUpdate struct {
	view             TaskView
	parentSessionID  string
	completedSubject string
}

// UpdateTask applies one compare-and-set task transition.
func (s *Service) UpdateTask(caller string, req UpdateTaskRequest) (TaskView, error) {
	var (
		applied taskUpdate
		hook    func(sessionID, subject string)
	)
	err := s.write(refusable, func() (bool, error) {
		next, err := s.applyTaskUpdateLocked(caller, req)
		if err != nil {
			return false, err
		}
		applied = next
		hook = s.taskCompletedHook
		s.notifyLocked()
		return true, nil
	})
	if err != nil {
		return TaskView{}, err
	}
	// The hook runs outside every lock, and only once the record is
	// durable.
	if applied.completedSubject != "" && hook != nil {
		hook(applied.parentSessionID, applied.completedSubject)
	}
	return applied.view, nil
}

// applyTaskUpdateLocked validates and applies one task transition to the
// in-memory record. Callers must hold s.mu and must save the record when it
// returns without an error.
func (s *Service) applyTaskUpdateLocked(caller string, req UpdateTaskRequest) (taskUpdate, error) {
	var completedSubject, parentSessionID string
	member, ok := s.memberLocked(caller)
	if !ok {
		return taskUpdate{}, newError(CodeNotMember, "agent %q is not a member of an active Agent Team", caller)
	}
	parentSessionID = member.ParentID
	if parentSessionID == "" {
		parentSessionID = req.SessionID
	}
	scope := s.scopeLocked(caller, req.SessionID)
	index, current, ok := s.taskIndexLocked(scope, req.TaskID)
	if !ok {
		return taskUpdate{}, newError(CodeTaskNotFound, "team task %q not found", req.TaskID)
	}
	if current.Revision != req.ExpectedRevision {
		return taskUpdate{}, newError(CodeTaskStaleRevision,
			"stale team task %q revision %d; current revision is %d",
			current.ID, req.ExpectedRevision, current.Revision)
	}
	if current.Status == TaskDeleted {
		return taskUpdate{}, newError(CodeTaskDeleted, "team task %q is deleted", current.ID)
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
			return taskUpdate{}, newError(CodeTaskAlreadyClaimed, "team task %q is owned by another member", current.ID)
		}
		if current.Status != TaskPending || !taskReady(s.state, current) {
			return taskUpdate{}, newError(CodeTaskBlocked, "team task %q is not ready to claim", current.ID)
		}
		next.Status = TaskInProgress
		next.OwnerID = member.ID
	case "release":
		if err := authorizeOwner(); err != nil {
			return taskUpdate{}, err
		}
		if current.Status != TaskInProgress {
			return taskUpdate{}, newError(CodeTaskInvalidTransition, "only an in-progress task can be released")
		}
		next.Status = TaskPending
		next.OwnerID = ""
	case "edit":
		if err := authorizeOwner(); err != nil {
			return taskUpdate{}, err
		}
		if req.Subject == "" && req.Description == "" && req.WriteScopes == nil {
			return taskUpdate{}, newError(CodeInvalidArgument, "task edit requires subject, description, or write_scopes")
		}
		if req.Subject != "" {
			subject, err := requiredText(req.Subject, "subject", 200)
			if err != nil {
				return taskUpdate{}, err
			}
			next.Subject = subject
		}
		if req.Description != "" {
			description, err := requiredText(req.Description, "description", 16384)
			if err != nil {
				return taskUpdate{}, err
			}
			next.Description = description
		}
		if req.WriteScopes != nil {
			scopes, err := normalizeWriteScopes(req.WriteScopes)
			if err != nil {
				return taskUpdate{}, err
			}
			next.WriteScopes = scopes
		}
	case "set_dependencies":
		if err := authorizeOwner(); err != nil {
			return taskUpdate{}, err
		}
		if req.BlockedBy == nil {
			return taskUpdate{}, newError(CodeInvalidArgument, "set_dependencies requires blocked_by")
		}
		blockedBy, err := s.dependenciesLocked(scope, req.BlockedBy, current.ID)
		if err != nil {
			return taskUpdate{}, err
		}
		next.BlockedBy = blockedBy
	case "complete":
		if err := authorizeOwner(); err != nil {
			return taskUpdate{}, err
		}
		if current.Status != TaskInProgress {
			return taskUpdate{}, newError(CodeTaskInvalidTransition, "only an in-progress task can complete")
		}
		next.Status = TaskCompleted
		completedSubject = next.Subject
	case "reopen":
		if err := authorizeOwner(); err != nil {
			return taskUpdate{}, err
		}
		if current.Status != TaskCompleted {
			return taskUpdate{}, newError(CodeTaskInvalidTransition, "only a completed task can reopen")
		}
		next.Status = TaskPending
		next.OwnerID = ""
	case "reassign":
		if !lead {
			return taskUpdate{}, newError(CodeLeadRequired, "only the Team Lead can reassign tasks")
		}
		if current.Status != TaskPending && current.Status != TaskInProgress {
			return taskUpdate{}, newError(CodeTaskInvalidTransition, "only a pending or in-progress task can be reassigned")
		}
		if strings.TrimSpace(req.Owner) == "" {
			next.Status = TaskPending
			next.OwnerID = ""
			break
		}
		if !taskReady(s.state, current) {
			return taskUpdate{}, newError(CodeTaskBlocked, "team task %q is blocked", current.ID)
		}
		assignee, ok := s.memberLocked(req.Owner)
		if !ok || !s.memberVisibleLocked(scope, req.Owner) {
			return taskUpdate{}, newError(CodeMemberNotFound, "Team member %q not found", req.Owner)
		}
		next.Status = TaskInProgress
		next.OwnerID = assignee.ID
	case "delete":
		if err := authorizeOwner(); err != nil {
			return taskUpdate{}, err
		}
		for _, task := range s.state.Tasks {
			if task.Status != TaskDeleted && task.ID != current.ID && slices.Contains(task.BlockedBy, current.ID) {
				return taskUpdate{}, newError(CodeTaskHasDependents,
					"team task %q still blocks %q", current.ID, task.ID)
			}
		}
		next.Status = TaskDeleted
	default:
		return taskUpdate{}, newError(CodeInvalidArgument, "unsupported task action %q", req.Action)
	}

	next.Revision = current.Revision + 1
	if err := assertTaskGraph(s.state.Tasks, next); err != nil {
		return taskUpdate{}, err
	}
	// Written by index: an old file may hold the same id twice, and the
	// copy the caller acted on is the one in its scope.
	s.state.Tasks[index] = next
	return taskUpdate{
		view:             projectTaskView(s.state, next),
		parentSessionID:  parentSessionID,
		completedSubject: completedSubject,
	}, nil
}

// taskIndexLocked finds id in the caller's scope and returns its index, so a
// writer can replace that copy. An old file may hold one id twice, and the
// first copy visible in scope is the caller's. Callers must hold s.mu.
func (s *Service) taskIndexLocked(scope map[string]bool, id string) (int, Task, bool) {
	for i, task := range s.state.Tasks {
		if task.ID == id && taskVisibleLocked(scope, task) {
			return i, task, true
		}
	}
	return -1, Task{}, false
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
		_, task, ok := s.taskIndexLocked(scope, id)
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
