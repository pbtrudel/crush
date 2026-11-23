// Package webtest provides a scriptable fake workspace for web gateway
// tests, Playwright smoke tests and frontend development.
package webtest

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/charmbracelet/crush/internal/workspace"
)

// ErrSessionNotFound is returned by GetSession for unknown IDs. It wraps
// sql.ErrNoRows, which is what the real session service returns.
var ErrSessionNotFound = fmt.Errorf("session not found: %w", sql.ErrNoRows)

// subscriber is one SubscribeEvents channel. mu serialises Emit's send
// against the cleanup close so an event can never race with close(ch).
type subscriber struct {
	ch     chan any
	done   <-chan struct{}
	mu     sync.Mutex
	closed bool
}

// Fake implements workspace.Workspace for tests. Methods not overridden
// panic through the nil embedded interface, which flags unexpected calls.
type Fake struct {
	workspace.Workspace

	// subsMu guards subs and is never taken by the getters the gateway
	// polls, so a blocked Emit cannot stall CurrentSessionID.
	subsMu sync.Mutex
	subs   map[*subscriber]struct{}

	mu          sync.Mutex
	current     string
	sessions    map[string]session.Session
	messages    map[string][]message.Message
	mode        permission.PermissionMode
	queued      map[string][]string
	busy        map[string]bool
	history     map[string][]history.File
	diagnostics map[string]lsp.DiagnosticCounts
	teamErr     error
	seq         int
	// skillCatalog is the visible skill set; skillContent and
	// skillDisabled back ReadSkill and the toggles.
	skillCatalog  []skills.CatalogEntry
	skillContent  map[string]string
	skillDisabled map[string]bool
	// skillsErr, when set, makes ListSkills fail.
	skillsErr error
	// mcpPrompts is the prompt catalog; mcpPromptText is keyed by
	// "<client>:<prompt>" and holds what GetMCPPrompt returns.
	mcpPrompts    []commands.MCPPrompt
	mcpPromptText map[string]string
	// sessionSeq hands out CreateSession IDs. A monotonic counter keeps
	// them unique even after a session is deleted.
	sessionSeq int

	// decisions and answers carry resolutions to a running scripted
	// agent, so a test or webfake can play the user's part.
	decisions chan permission.PermissionNotification
	answers   chan []question.Answer

	// WorkDir is what WorkingDir returns; webfake points it at the repo
	// so @ completion has real files.
	WorkDir string
	// LSP and MCP are what LSPGetStates and MCPGetStates return. Set them
	// before the gateway starts.
	LSP map[string]workspace.LSPClientInfo
	MCP map[string]mcp.ClientInfo
	// Calls is guarded by mu; read it through CallLog.
	Calls   []string
	Granted []string
	Answers [][]question.Answer
	// AnswerBatches records the batch each answer named.
	AnswerBatches []string
	Resolve       bool
	OnRun         func(ctx context.Context, sessionID, prompt string, att []message.Attachment) error
	// OnSummarize, OnAsk and OnShell play a scripted agent for the browser
	// action routes. A nil OnSummarize succeeds, a nil OnAsk falls back to
	// AskAnswer, and a nil OnShell returns an empty shell response.
	OnSummarize func(ctx context.Context, sessionID string) error
	OnAsk       func(ctx context.Context, sessionID, question string) (string, error)
	OnShell     func(ctx context.Context, sessionID, command string, onProgress func(string), isFirst bool) (proto.ShellCommandResponse, error)
	// AskAnswer is what AgentAsk returns when OnAsk is nil.
	AskAnswer string
	// NoteErr, when set, makes AgentAddNote fail.
	NoteErr error
	// ReadyErr is what AgentReadyErr returns. nil means the agent is
	// ready. Set it before the first request.
	ReadyErr error
}

// NewFake returns a fake with one current session "s1".
func NewFake() *Fake {
	f := &Fake{
		subs:          make(map[*subscriber]struct{}),
		sessions:      map[string]session.Session{"s1": {ID: "s1", Title: "Holodeck test"}},
		messages:      make(map[string][]message.Message),
		queued:        make(map[string][]string),
		busy:          make(map[string]bool),
		history:       make(map[string][]history.File),
		diagnostics:   make(map[string]lsp.DiagnosticCounts),
		skillContent:  make(map[string]string),
		skillDisabled: make(map[string]bool),
		mcpPromptText: make(map[string]string),
		current:       "s1",
		sessionSeq:    1,
		decisions:     make(chan permission.PermissionNotification, 16),
		answers:       make(chan []question.Answer, 16),
		WorkDir:       "/tmp/project",
		Resolve:       true,
	}
	return f
}

func (f *Fake) record(call string) {
	f.mu.Lock()
	f.Calls = append(f.Calls, call)
	f.mu.Unlock()
}

// Emit delivers ev to every SubscribeEvents channel. Subscribers are
// copied under subsMu and then released, so a blocked Emit never holds
// the lock CurrentSessionID and the other getters need. It waits for a
// full subscriber buffer, but gives up on a subscriber whose context
// ends.
func (f *Fake) Emit(ev any) {
	f.subsMu.Lock()
	subs := make([]*subscriber, 0, len(f.subs))
	for s := range f.subs {
		subs = append(subs, s)
	}
	f.subsMu.Unlock()
	for _, s := range subs {
		s.mu.Lock()
		if !s.closed {
			select {
			case s.ch <- ev:
			case <-s.done:
			}
		}
		s.mu.Unlock()
	}
}

// SetCurrent changes the session the fake terminal is viewing.
func (f *Fake) SetCurrent(id string) {
	f.mu.Lock()
	f.current = id
	f.mu.Unlock()
}

// SetCurrentSession is how both the terminal and the gateway report the
// session they are viewing. The fake has one shared view, like the real
// in-process workspace.
func (f *Fake) SetCurrentSession(_ context.Context, sessionID string) error {
	f.SetCurrent(sessionID)
	return nil
}

// AddMessage stores a message so ListMessages returns it.
func (f *Fake) AddMessage(m message.Message) {
	f.mu.Lock()
	f.messages[m.SessionID] = append(f.messages[m.SessionID], m)
	f.mu.Unlock()
}

// AddSession stores a session, such as a child session with a parent.
func (f *Fake) AddSession(s session.Session) {
	f.mu.Lock()
	f.sessions[s.ID] = s
	f.mu.Unlock()
}

// AddHistory stores one file version for ListSessionHistory.
func (f *Fake) AddHistory(file history.File) {
	f.mu.Lock()
	f.history[file.SessionID] = append(f.history[file.SessionID], file)
	f.mu.Unlock()
}

// SubscribeEvents registers a subscriber before it returns, so no event
// emitted after registration can be lost. The channel closes when ctx is
// done.
func (f *Fake) SubscribeEvents(ctx context.Context) <-chan any {
	s := &subscriber{ch: make(chan any, 1024), done: ctx.Done()}
	f.subsMu.Lock()
	f.subs[s] = struct{}{}
	f.subsMu.Unlock()
	go func() {
		<-ctx.Done()
		f.subsMu.Lock()
		delete(f.subs, s)
		f.subsMu.Unlock()
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.ch)
		}
		s.mu.Unlock()
	}()
	return s.ch
}

// CloseSubscribers closes every SubscribeEvents channel, the way a
// workspace that shut down does.
func (f *Fake) CloseSubscribers() {
	f.subsMu.Lock()
	subs := make([]*subscriber, 0, len(f.subs))
	for s := range f.subs {
		subs = append(subs, s)
		delete(f.subs, s)
	}
	f.subsMu.Unlock()
	for _, s := range subs {
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			close(s.ch)
		}
		s.mu.Unlock()
	}
}

func (f *Fake) CurrentSessionID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

func (f *Fake) WorkingDir() string { return f.WorkDir }

// Config returns nil: the fake has no data directory, so the gateway's team
// watcher stays off unless a test sets Options.AgentTeamsDir.
func (f *Fake) Config() *config.Config { return nil }

func (f *Fake) GitBranch(context.Context) (string, error) { return "main", nil }

func (f *Fake) AgentModel() workspace.AgentModel { return workspace.AgentModel{} }

func (f *Fake) AgentReadyErr() error { return f.ReadyErr }

func (f *Fake) LSPGetStates() map[string]workspace.LSPClientInfo { return f.LSP }

// SetDiagnostics sets what LSPDiagnosticsByFile returns.
func (f *Fake) SetDiagnostics(m map[string]lsp.DiagnosticCounts) {
	f.mu.Lock()
	f.diagnostics = maps.Clone(m)
	f.mu.Unlock()
}

func (f *Fake) LSPDiagnosticsByFile(context.Context) map[string]lsp.DiagnosticCounts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.diagnostics)
}

// SetTeamErr sets the error both team calls return.
func (f *Fake) SetTeamErr(err error) {
	f.mu.Lock()
	f.teamErr = err
	f.mu.Unlock()
}

func (f *Fake) TeamSendMessage(_ context.Context, sessionID, member, text string) error {
	f.record("TeamSendMessage:" + sessionID + ":" + member + ":" + text)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.teamErr
}

func (f *Fake) TeamInterrupt(_ context.Context, sessionID, member string) error {
	f.record("TeamInterrupt:" + sessionID + ":" + member)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.teamErr
}

func (f *Fake) MCPGetStates() map[string]mcp.ClientInfo { return f.MCP }

// AddSkill adds a visible skill and the content ReadSkill returns for it.
func (f *Fake) AddSkill(entry skills.CatalogEntry, content string) {
	f.mu.Lock()
	f.skillCatalog = append(f.skillCatalog, entry)
	f.skillContent[entry.ID] = content
	f.mu.Unlock()
}

// SetSkillsErr makes ListSkills fail, the way a broken catalog does.
func (f *Fake) SetSkillsErr(err error) {
	f.mu.Lock()
	f.skillsErr = err
	f.mu.Unlock()
}

func (f *Fake) ListSkills(context.Context) ([]skills.CatalogEntry, error) {
	f.record("ListSkills")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.skillsErr != nil {
		return nil, f.skillsErr
	}
	return append([]skills.CatalogEntry(nil), f.skillCatalog...), nil
}

func (f *Fake) ReadSkill(_ context.Context, skillID string) ([]byte, skills.SkillReadResult, error) {
	f.record("ReadSkill:" + skillID)
	f.mu.Lock()
	defer f.mu.Unlock()
	content, ok := f.skillContent[skillID]
	if !ok {
		return nil, skills.SkillReadResult{}, fmt.Errorf("read skill %s: %w", skillID, skills.ErrSkillNotFound)
	}
	var result skills.SkillReadResult
	for _, entry := range f.skillCatalog {
		if entry.ID == skillID {
			result = skills.SkillReadResult{Name: entry.Name, Description: entry.Description, Source: entry.Source}
			break
		}
	}
	return []byte(content), result, nil
}

func (f *Fake) ReloadSkills(context.Context) error {
	f.record("ReloadSkills")
	return nil
}

func (f *Fake) SkillsDisabled(context.Context) ([]string, error) {
	f.record("SkillsDisabled")
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for name, disabled := range f.skillDisabled {
		if disabled {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (f *Fake) SkillsEnabled(context.Context) ([]string, error) { return nil, nil }

func (f *Fake) SkillSetDisabled(_ context.Context, name string, disabled bool) error {
	f.record("SkillSetDisabled:" + name + ":" + strconv.FormatBool(disabled))
	f.mu.Lock()
	f.skillDisabled[name] = disabled
	f.mu.Unlock()
	return nil
}

// AddMCPPrompt adds a prompt to the catalog and the text GetMCPPrompt
// returns for it.
func (f *Fake) AddMCPPrompt(p commands.MCPPrompt, text string) {
	f.mu.Lock()
	f.mcpPrompts = append(f.mcpPrompts, p)
	f.mcpPromptText[p.ClientID+":"+p.PromptID] = text
	f.mu.Unlock()
}

func (f *Fake) ListMCPPrompts(context.Context) ([]commands.MCPPrompt, error) {
	f.record("ListMCPPrompts")
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]commands.MCPPrompt(nil), f.mcpPrompts...), nil
}

func (f *Fake) GetMCPPrompt(clientID, promptID string, _ map[string]string) (string, error) {
	f.record("GetMCPPrompt:" + clientID + ":" + promptID)
	f.mu.Lock()
	defer f.mu.Unlock()
	text, ok := f.mcpPromptText[clientID+":"+promptID]
	if !ok {
		return "", fmt.Errorf("mcp prompt %s:%s not found", clientID, promptID)
	}
	return text, nil
}

func (f *Fake) PermissionMode() permission.PermissionMode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

func (f *Fake) PermissionSetMode(m permission.PermissionMode) {
	f.mu.Lock()
	f.mode = m
	f.mu.Unlock()
	f.record("PermissionSetMode")
}

func (f *Fake) GetSession(_ context.Context, id string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return session.Session{}, ErrSessionNotFound
	}
	return s, nil
}

// ListSessions answers newest updated first, with the ID breaking ties,
// the same order the real session service and the gateway use.
func (f *Fake) ListSessions(context.Context) ([]session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]session.Session, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b session.Session) int {
		return cmp.Or(cmp.Compare(b.UpdatedAt, a.UpdatedAt), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func (f *Fake) CreateSession(_ context.Context, title string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionSeq++
	id := "s" + strconv.Itoa(f.sessionSeq)
	s := session.Session{ID: id, Title: title}
	f.sessions[id] = s
	return s, nil
}

// SessionRename updates the title only, then publishes the same Updated
// event the real session service publishes so a tab sees the rename.
func (f *Fake) SessionRename(_ context.Context, id, title string) (session.Session, error) {
	f.record("SessionRename:" + id + ":" + title)
	f.mu.Lock()
	s, ok := f.sessions[id]
	if ok {
		s.Title = title
		f.sessions[id] = s
	}
	f.mu.Unlock()
	if !ok {
		return session.Session{}, ErrSessionNotFound
	}
	f.Emit(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: s})
	return s, nil
}

// DeleteSession removes the session and publishes the Deleted event, like
// the real session service.
func (f *Fake) DeleteSession(_ context.Context, id string) error {
	f.record("DeleteSession:" + id)
	f.mu.Lock()
	s, ok := f.sessions[id]
	if ok {
		delete(f.sessions, id)
	}
	f.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	f.Emit(pubsub.Event[session.Session]{Type: pubsub.DeletedEvent, Payload: s})
	return nil
}

// SetSessionBusy makes AgentIsSessionBusy report id as busy, as a running
// agent would.
func (f *Fake) SetSessionBusy(id string, busy bool) {
	f.mu.Lock()
	f.busy[id] = busy
	f.mu.Unlock()
}

func (f *Fake) ListMessages(_ context.Context, id string) ([]message.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]message.Message(nil), f.messages[id]...), nil
}

func (f *Fake) ListChildSessions(_ context.Context, parentID string) ([]session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []session.Session
	for _, s := range f.sessions {
		if s.ParentSessionID == parentID {
			out = append(out, s)
		}
	}
	if out == nil {
		out = []session.Session{}
	}
	slices.SortFunc(out, func(a, b session.Session) int {
		return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

func (f *Fake) ListSessionHistory(_ context.Context, sessionID string) ([]history.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]history.File(nil), f.history[sessionID]...), nil
}

func (f *Fake) ListSessionHistoryMeta(_ context.Context, sessionID string) ([]history.FileMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	files := f.history[sessionID]
	metas := make([]history.FileMeta, len(files))
	for i, file := range files {
		metas[i] = history.FileMeta{
			ID:        file.ID,
			SessionID: file.SessionID,
			Path:      file.Path,
			Version:   file.Version,
			CreatedAt: file.CreatedAt,
			UpdatedAt: file.UpdatedAt,
			Size:      int64(len(file.Content)),
		}
	}
	return metas, nil
}

func (f *Fake) GetHistoryVersion(_ context.Context, id string) (history.File, error) {
	f.record("GetHistoryVersion:" + id)
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, files := range f.history {
		for _, file := range files {
			if file.ID == id {
				return file, nil
			}
		}
	}
	return history.File{}, fmt.Errorf("history version %s: %w", id, sql.ErrNoRows)
}

func (f *Fake) AgentRun(ctx context.Context, sessionID, prompt string, att ...message.Attachment) error {
	f.record("AgentRun:" + sessionID + ":" + prompt)
	if f.OnRun != nil {
		return f.OnRun(ctx, sessionID, prompt, att)
	}
	return nil
}

func (f *Fake) AgentCancel(sessionID string) {
	f.record("AgentCancel:" + sessionID)
}

func (f *Fake) AgentIsSessionBusy(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy[id]
}

func (f *Fake) AgentQueuedPromptsList(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queued[id]...)
}

func (f *Fake) AgentClearQueue(id string) {
	f.mu.Lock()
	delete(f.queued, id)
	f.mu.Unlock()
	f.record("AgentClearQueue:" + id)
}

func (f *Fake) AgentSummarize(ctx context.Context, sessionID string) error {
	f.record("AgentSummarize:" + sessionID)
	if f.OnSummarize != nil {
		return f.OnSummarize(ctx, sessionID)
	}
	return nil
}

func (f *Fake) AgentAsk(ctx context.Context, sessionID, question string) (string, error) {
	f.record("AgentAsk:" + sessionID + ":" + question)
	if f.OnAsk != nil {
		return f.OnAsk(ctx, sessionID, question)
	}
	return f.AskAnswer, nil
}

func (f *Fake) AgentRunShellCommand(ctx context.Context, sessionID, command string, _ int, onProgress func(string), isFirst bool) (proto.ShellCommandResponse, error) {
	f.record("AgentRunShellCommand:" + sessionID + ":" + command + ":" + strconv.FormatBool(isFirst))
	if f.OnShell != nil {
		return f.OnShell(ctx, sessionID, command, onProgress, isFirst)
	}
	return proto.ShellCommandResponse{}, nil
}

func (f *Fake) AgentAddNote(_ context.Context, sessionID, text string) error {
	f.record("AgentAddNote:" + sessionID + ":" + text)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.NoteErr
}

// resolvePermission publishes the outcome the UI would publish and hands
// it to a running scripted agent. It reports whether the fake answered.
func (f *Fake) resolvePermission(p permission.PermissionRequest, granted bool) bool {
	if !f.Resolve {
		return false
	}
	n := permission.PermissionNotification{SessionID: p.SessionID, ToolCallID: p.ToolCallID, Granted: granted, Denied: !granted}
	f.Emit(pubsub.Event[permission.PermissionNotification]{Type: pubsub.UpdatedEvent, Payload: n})
	select {
	case f.decisions <- n:
	default:
	}
	return true
}

func (f *Fake) PermissionGrant(p permission.PermissionRequest) bool {
	f.record("PermissionGrant:" + p.ID)
	return f.resolvePermission(p, true)
}

func (f *Fake) PermissionGrantPersistent(p permission.PermissionRequest) bool {
	f.record("PermissionGrantPersistent:" + p.ID)
	return f.resolvePermission(p, true)
}

func (f *Fake) PermissionDeny(p permission.PermissionRequest) bool {
	f.record("PermissionDeny:" + p.ID)
	return f.resolvePermission(p, false)
}

func (f *Fake) QuestionAnswer(batchID string, a []question.Answer) bool {
	f.mu.Lock()
	f.Answers = append(f.Answers, a)
	f.AnswerBatches = append(f.AnswerBatches, batchID)
	f.mu.Unlock()
	f.record("QuestionAnswer")
	if !f.Resolve {
		return false
	}
	select {
	case f.answers <- a:
	default:
	}
	return true
}

func (f *Fake) QuestionCancel(batchID string) bool {
	f.record("QuestionCancel:" + batchID)
	return f.Resolve
}

// nextID returns a deterministic, per-fake unique ID. The scripted agent
// uses it so fixture and smoke-test output stays stable.
func (f *Fake) nextID(prefix string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	return prefix + "-" + strconv.Itoa(f.seq)
}

// CallLog returns a copy of the recorded calls.
func (f *Fake) CallLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Calls...)
}
