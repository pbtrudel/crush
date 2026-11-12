package claudeagent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"charm.land/fantasy"
)

const (
	// sessionIdleTimeout is how long a session's process is kept alive without
	// a request. The next message resumes the saved Claude session, so the
	// prompt cache survives the gap.
	sessionIdleTimeout = 5 * time.Minute
	// drainTimeout bounds how long an interrupted turn gets to finish flushing
	// its transcript before the process is replaced.
	drainTimeout = 10 * time.Second
	// utilityMinOutputTokens is the smallest output budget a standalone task
	// gets. Claude Code counts its own overhead against the limit.
	utilityMinOutputTokens = 4096
	// parkedTimeout stops a process that is still waiting for tool results
	// long after the turn that asked for them. It is only a backstop: a
	// cancelled turn releases its tool calls, so this should never fire.
	parkedTimeout = 30 * time.Minute
)

// emitter receives the stream parts produced for one step.
type emitter func(fantasy.StreamPart) error

// turnRequest is one model call, already resolved into everything the CLI
// needs. It is the provider's internal contract; the fantasy adapter builds it.
type turnRequest struct {
	// SessionKey identifies the Crush session. Empty means a utility call that
	// runs in a throwaway process and leaves no transcript.
	SessionKey string
	Dir        string
	Model      string
	Effort     string
	// AppendPrompt is added to Claude Code's own system prompt.
	AppendPrompt string
	// SystemPrompt replaces Claude Code's system prompt. Used by utility calls.
	SystemPrompt    string
	Tools           []toolDef
	ToolNames       []string
	Prompt          fantasy.Prompt
	MaxOutputTokens int64
	Connection      Connection
}

// Manager owns the Claude processes, one per Crush session.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	store    *recordStore
	dir      string

	execOnce sync.Once
	execPath string
	execErr  error

	closed bool

	probeMu   sync.Mutex
	onProbe   func(*initializeResult)
	lastProbe *initializeResult
}

// NewManager creates a manager that stores session records under dir.
func NewManager(dir string) *Manager {
	m := &Manager{
		sessions: make(map[string]*session),
		store:    newRecordStore(dir),
		dir:      dir,
	}
	// Every process start reports the login and the models it can run, so the
	// cached catalog follows a login change without a separate probe.
	m.onProbe = func(result *initializeResult) {
		catalog := catalogFrom(result)
		if catalog == nil {
			return
		}
		if err := SaveCatalog(dir, catalog); err != nil {
			slog.Debug("Could not cache the Claude Agent model list", "error", err)
		}
	}
	return m
}

func (m *Manager) reportProbe(result *initializeResult) {
	if result == nil {
		return
	}
	m.probeMu.Lock()
	m.lastProbe = result
	fn := m.onProbe
	m.probeMu.Unlock()
	if fn != nil {
		fn(result)
	}
}

// Stream runs one step of a session.
func (m *Manager) Stream(ctx context.Context, req turnRequest, emit emitter) error {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return errors.New("claude agent is shutting down")
	}
	if req.SessionKey == "" {
		return m.runUtility(ctx, req, emit)
	}
	s := m.sessionFor(req.SessionKey)
	s.lock.Lock()
	defer s.lock.Unlock()
	s.stopIdle()
	defer s.armIdle()
	return s.run(ctx, req, emit)
}

// Close stops every process. It is called when Crush shuts down, so it is
// bounded: a process that will not die must not hold up the exit.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = make(map[string]*session)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), stopGrace)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Go(func() {
			s.closed = true
			s.stopIdle()
			s.stop(ctx)
		})
	}
	wg.Wait()
}

func (m *Manager) sessionFor(key string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[key]; ok {
		return s
	}
	if m.closed {
		// A request that arrives during shutdown gets a session of its own
		// that is never registered, so Close does not have to race it.
		return &session{key: key, store: m.store, manager: m, closed: true}
	}
	s := &session{key: key, store: m.store, manager: m}
	m.sessions[key] = s
	return s
}

func (m *Manager) executable() (string, error) {
	m.execOnce.Do(func() {
		m.execPath, m.execErr = findExecutable()
	})
	return m.execPath, m.execErr
}

// session is one Crush session's Claude process and sync record.
type session struct {
	key     string
	store   *recordStore
	manager *Manager

	lock sync.Mutex

	proc    *cliProcess
	state   *processState
	pending *pendingTools
	launch  launchConfig
	model   string
	record  *Record

	idle   *time.Timer
	closed bool
}

// prepared is what a step sends to the CLI.
type prepared struct {
	// content is sent as a new user message.
	content []ContentBlock
	// continuation resolves the tool calls the CLI is parked on.
	continuation []toolResultPair
	// steer is added to the turn that is already running.
	steer []ContentBlock
	// start is the prompt index where the new content begins.
	start int
}

type toolResultPair struct {
	id     string
	result toolResult
}

func (s *session) run(ctx context.Context, req turnRequest, emit emitter) error {
	executable, err := s.manager.executable()
	if err != nil {
		return err
	}
	cfg := launchFor(req, executable)
	prep, err := s.prepare(ctx, req, cfg)
	if err != nil {
		return err
	}
	fresh, err := s.open(ctx, req, cfg, len(prep.continuation) > 0)
	if err != nil {
		return err
	}
	if fresh {
		// The saved transcript was gone, so the whole prompt has to be sent.
		prep = &prepared{content: ConvertPrompt(req.Prompt)}
	}
	return s.turn(ctx, req, prep, emit)
}

// prepare decides what to send: a continuation for parked tool calls, a
// catch-up for history Claude has not seen, or the whole prompt.
func (s *session) prepare(ctx context.Context, req turnRequest, cfg launchConfig) (*prepared, error) {
	// Retire a process that died or can no longer serve this request. A process
	// parked on tool results is kept even when the launch settings changed:
	// stopping it would record those calls as refused by the user.
	if s.proc != nil {
		parked := s.pending.waiting() && !s.proc.exited() && len(req.Tools) > 0
		incompatible := s.launch.restartKey() != cfg.restartKey()
		if s.proc.exited() || (incompatible && !parked) {
			s.stop(ctx)
		}
	}

	if s.record == nil {
		s.record = s.store.get(req.SessionKey)
	}

	if s.proc != nil && s.pending.waiting() {
		if results, end, ok := s.collectContinuation(req.Prompt); ok {
			// The ids are now paired with results, so forget them: a stale id
			// would make every later turn look like a continuation.
			s.pending.reset()
			prep := &prepared{continuation: results, start: end}
			if end < len(req.Prompt) {
				prep.steer = ConvertPrompt(req.Prompt[end:])
			}
			return prep, nil
		}
		// The parked turn cannot be continued, so it has to be abandoned.
		s.stop(ctx)
		s.record = nil
	}

	decision := Decide(req.Prompt, s.record)
	if decision.Kind == DecisionChanged {
		// Claude cannot rewind its own transcript without a fork, so the
		// conversation restarts and the whole history is sent as a checkpoint.
		s.stop(ctx)
		s.record = nil
		decision = Decision{Kind: DecisionFresh}
	}
	if decision.Kind == DecisionFresh {
		if s.record != nil {
			s.stop(ctx)
			s.record = nil
		}
		return &prepared{content: ConvertPrompt(req.Prompt)}, nil
	}
	if decision.Start >= len(req.Prompt) {
		// A retry of the same prompt has nothing new to send. Repeating the
		// last user message is what the user asked for, and an empty message
		// would leave the CLI waiting for one.
		return &prepared{content: ConvertPrompt(req.Prompt[lastUserMessage(req.Prompt):])}, nil
	}
	return &prepared{content: CatchUpContent(req.Prompt, decision.Start), start: decision.Start}, nil
}

// lastUserMessage returns the index of the prompt's final user message, or 0
// when there is none.
func lastUserMessage(prompt fantasy.Prompt) int {
	for i := len(prompt) - 1; i >= 0; i-- {
		if prompt[i].Role == fantasy.MessageRoleUser {
			return i
		}
	}
	return 0
}

// collectContinuation pairs the parked tool calls with their results.
//
// The results of the step that just ended are always the trailing run of tool
// messages, so they are found from the end rather than from a saved index: the
// prompt Crush sends carries the whole conversation, not just the new part.
func (s *session) collectContinuation(prompt fantasy.Prompt) ([]toolResultPair, int, bool) {
	wanted := s.pending.ids()
	if len(wanted) == 0 {
		return nil, 0, false
	}
	last := -1
	for i := len(prompt) - 1; i >= 0; i-- {
		if prompt[i].Role == fantasy.MessageRoleTool {
			last = i
			break
		}
	}
	if last < 0 {
		return nil, 0, false
	}
	first := last
	for first > 0 && prompt[first-1].Role == fantasy.MessageRoleTool {
		first--
	}
	results := make(map[string]toolResult, len(wanted))
	for i := first; i <= last; i++ {
		for _, part := range prompt[i].Content {
			result, ok := fantasy.AsContentType[fantasy.ToolResultPart](part)
			if !ok {
				continue
			}
			results[result.ToolCallID] = toolResultFromPart(result)
		}
	}
	pairs := make([]toolResultPair, 0, len(wanted))
	for _, id := range wanted {
		result, ok := results[id]
		if !ok {
			return nil, 0, false
		}
		pairs = append(pairs, toolResultPair{id: id, result: result})
	}
	return pairs, last + 1, true
}

// open makes sure a process is running with the right settings. It reports
// whether the process had to start fresh because its transcript was gone.
func (s *session) open(ctx context.Context, req turnRequest, cfg launchConfig, continuing bool) (bool, error) {
	if s.proc != nil {
		if s.launch.restartKey() != cfg.restartKey() {
			if continuing {
				// The CLI is parked on tool results. Stopping it now would
				// record those calls as refused by the user, so the new
				// settings wait for the next turn.
				if s.model != req.Model {
					if err := s.proc.setModel(ctx, req.Model); err != nil {
						return false, err
					}
					s.model = req.Model
				}
				return false, nil
			}
			s.stop(ctx)
		} else {
			if s.model != req.Model {
				if err := s.proc.setModel(ctx, req.Model); err != nil {
					return false, err
				}
				s.model = req.Model
			}
			return false, nil
		}
	}

	if s.record != nil && s.record.ClaudeSessionID != "" {
		cfg.Resume = s.record.ClaudeSessionID
	} else {
		cfg.SessionID = newUUID()
	}

	pending := newPendingTools()
	server := &mcpServer{tools: cfg.Tools, pending: pending}
	handler := func(_ context.Context, _ string, message json.RawMessage) (json.RawMessage, error) {
		return server.handle(context.Background(), message)
	}
	proc, init, err := startProcess(ctx, cfg, handler)
	if err != nil && cfg.Resume != "" {
		// The saved transcript is gone, or the CLI refuses to resume it. Try a
		// new Claude session; the record is only dropped once that works, so a
		// transient failure does not throw away a resumable conversation.
		slog.Info("Claude session could not be resumed; starting a new one", "error", err)
		cfg.Resume = ""
		cfg.SessionID = newUUID()
		proc, init, err = startProcess(ctx, cfg, handler)
		if err != nil {
			return false, err
		}
		s.record = nil
		s.store.delete(s.key)
		s.install(proc, cfg, pending, server)
		s.manager.reportProbe(init)
		return true, nil
	}
	if err != nil {
		return false, err
	}
	s.install(proc, cfg, pending, server)
	s.manager.reportProbe(init)
	return false, nil
}

// install wires a freshly started process into the session.
func (s *session) install(proc *cliProcess, cfg launchConfig, pending *pendingTools, server *mcpServer) {
	s.proc = proc
	s.launch = cfg
	s.model = cfg.Model
	s.pending = pending
	s.state = newProcessState(proc)
	if cfg.Resume != "" {
		proc.id = cfg.Resume
	} else {
		proc.id = cfg.SessionID
	}
}

// turn sends the prepared content and reads the reply until it ends or pauses
// for a tool call.
func (s *session) turn(ctx context.Context, req turnRequest, prep *prepared, emit emitter) error {
	proc := s.proc
	userID := newUUID()

	if len(prep.continuation) > 0 {
		for _, pair := range prep.continuation {
			s.pending.resolve(pair.id, pair.result)
		}
		if len(prep.steer) > 0 {
			if err := proc.steerUserMessage(userID, prep.steer); err != nil {
				return err
			}
		}
	} else {
		s.pending.reset()
		if err := proc.sendUserMessage(userID, prep.content); err != nil {
			return err
		}
	}

	conv := newStreamConverter(req, s.pending, emit)
	_, err := s.readStep(ctx, conv)
	if err != nil {
		s.handleInterrupt()
	}
	s.saveRecord(req, conv, userID, err)
	return err
}

// readStep consumes messages until the step ends.
func (s *session) readStep(ctx context.Context, conv *streamConverter) (stepOutcome, error) {
	for {
		env, err := s.state.next(ctx)
		if err != nil {
			return stepDone, err
		}
		outcome, err := conv.convert(env)
		if err != nil {
			return stepDone, err
		}
		if outcome != stepPending {
			return outcome, nil
		}
	}
}

// handleInterrupt stops the reply in flight and drains it, so the next turn
// starts from a known point instead of reading the tail of this one.
func (s *session) handleInterrupt() {
	if s.proc == nil || s.proc.exited() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	s.pending.failAll("Cancelled by user.")
	if err := s.proc.interrupt(ctx); err != nil {
		// Without a clean drain the next turn would read this turn's tail as
		// its own, so the process is replaced instead.
		slog.Debug("Claude interrupt failed; replacing the process", "error", err)
		s.stop(ctx)
		s.record = nil
		return
	}
	if !s.drain(ctx) {
		slog.Debug("Claude turn did not drain after an interrupt; replacing the process")
		s.stop(ctx)
		s.record = nil
	}
}

// drain reads and discards messages until the turn's result arrives. It
// reports whether the turn ended cleanly.
func (s *session) drain(ctx context.Context) bool {
	for {
		env, err := s.state.next(ctx)
		if err != nil {
			return false
		}
		if env.Type == msgResult {
			return true
		}
	}
}

// saveRecord persists the mapping between this Crush session and the Claude
// session holding its transcript.
func (s *session) saveRecord(req turnRequest, conv *streamConverter, userID string, stepErr error) {
	if s.proc == nil || !s.proc.persisted.Load() {
		return
	}
	interrupted := stepErr != nil
	var reply []ReplyPart
	var messageID string
	if !interrupted {
		// After a cancellation Claude owns the partial reply, so the record
		// ends at the input it actually received.
		reply = conv.reply()
		messageID = conv.messageUUID()
	}
	history := BuildCheckpoints(req.Prompt, s.record, userID, reply, messageID, conv.toolMessages())
	claudeID := s.proc.id
	if id := conv.sessionID(); id != "" {
		claudeID = id
	}
	s.record = &Record{
		ClaudeSessionID: claudeID,
		History:         history,
		Interrupted:     interrupted,
	}
	s.store.set(req.SessionKey, s.record)
}

// stop shuts down the session's process.
func (s *session) stop(ctx context.Context) {
	if s.proc == nil {
		return
	}
	s.pending.failAll("Cancelled by user.")
	s.proc.stop(ctx)
	s.proc = nil
	s.state = nil
	s.pending = nil
}

func (s *session) stopIdle() {
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
}

// armIdle schedules the process to stop once the session has been quiet for a
// while. A process parked on a tool result is kept: subagents, permission
// prompts and questions can take far longer than the idle timeout, and stopping
// it then would record the waiting calls as cancelled.
func (s *session) armIdle() {
	s.stopIdle()
	if s.closed || s.proc == nil {
		return
	}
	if s.pending.waiting() {
		// The CLI is parked on a tool result and must not be stopped: a
		// subagent or a permission prompt can take far longer than the idle
		// timeout. The parked timer is only a backstop for a turn that was
		// abandoned without releasing its calls.
		s.idle = time.AfterFunc(parkedTimeout, func() {
			s.lock.Lock()
			defer s.lock.Unlock()
			s.stop(context.Background())
		})
		return
	}
	s.idle = time.AfterFunc(sessionIdleTimeout, func() {
		s.lock.Lock()
		defer s.lock.Unlock()
		if s.pending != nil && s.pending.waiting() {
			s.armIdle()
			return
		}
		s.stop(context.Background())
	})
}

// runUtility runs a one-off request in a throwaway process. Titles and
// compaction use this: they are standalone tasks, so they get their own system
// prompt and leave no transcript behind.
func (m *Manager) runUtility(ctx context.Context, req turnRequest, emit emitter) error {
	executable, err := m.executable()
	if err != nil {
		return err
	}
	cfg := launchFor(req, executable)
	cfg.SystemPrompt = req.SystemPrompt
	cfg.AppendPrompt = ""
	cfg.SessionID = newUUID()
	cfg.Persist = false
	cfg.Tools = nil
	// A title or a summary is a small standalone task. Thinking would spend
	// most of its output budget before writing a single word.
	cfg.Thinking = "disabled"
	// Claude Code counts its own overhead against the limit, so a budget that
	// is generous for a plain model can still be too small here.
	cfg.MaxOutputTokens = max(cfg.MaxOutputTokens, utilityMinOutputTokens)

	proc, init, err := startProcess(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer proc.stop(context.WithoutCancel(ctx))
	m.reportProbe(init)

	pending := newPendingTools()
	conv := newStreamConverter(req, pending, emit)
	if err := proc.sendUserMessage(newUUID(), ConvertPrompt(req.Prompt)); err != nil {
		return err
	}
	state := newProcessState(proc)
	for {
		env, err := state.next(ctx)
		if err != nil {
			return err
		}
		outcome, err := conv.convert(env)
		if err != nil {
			return err
		}
		if outcome != stepPending {
			return nil
		}
	}
}

// launchFor builds the process settings for a request.
func launchFor(req turnRequest, executable string) launchConfig {
	return launchConfig{
		Executable:      executable,
		Dir:             req.Dir,
		Model:           req.Model,
		Effort:          req.Effort,
		AppendPrompt:    req.AppendPrompt,
		SystemPrompt:    req.SystemPrompt,
		Tools:           req.Tools,
		Persist:         req.SessionKey != "",
		MaxOutputTokens: req.MaxOutputTokens,
		Connection:      req.Connection,
	}
}

// toolResultFromPart converts a Crush tool result into what the MCP bridge
// hands back to Claude.
func toolResultFromPart(part fantasy.ToolResultPart) toolResult {
	switch output := part.Output.(type) {
	case fantasy.ToolResultOutputContentText:
		return mcpContent(output.Text, "", "", false)
	case fantasy.ToolResultOutputContentError:
		message := "tool failed"
		if output.Error != nil {
			message = output.Error.Error()
		}
		return mcpContent(message, "", "", true)
	case fantasy.ToolResultOutputContentMedia:
		return mcpContent(output.Text, output.Data, output.MediaType, false)
	default:
		body, err := json.Marshal(output)
		if err != nil {
			return mcpContent(fmt.Sprint(output), "", "", false)
		}
		return mcpContent(string(body), "", "", false)
	}
}

// processState queues the messages a process emits so a step can read them one
// at a time.
type processState struct {
	proc    *cliProcess
	inbox   chan *envelope
	done    chan struct{}
	errOnce sync.Once
	err     error
}

func newProcessState(proc *cliProcess) *processState {
	state := &processState{
		proc:  proc,
		inbox: make(chan *envelope, 256),
		done:  make(chan struct{}),
	}
	go state.pump()
	return state
}

func (s *processState) pump() {
	// The inbox is closed last so a reader drains everything the process said
	// before it sees the error.
	defer close(s.inbox)
	for env := range s.proc.events {
		select {
		case s.inbox <- env:
		case <-s.proc.done:
			s.finish()
			return
		}
	}
	s.finish()
}

func (s *processState) finish() {
	s.errOnce.Do(func() {
		s.err = s.proc.processError()
		close(s.done)
	})
}

// next returns the next message, or an error once the process is gone.
func (s *processState) next(ctx context.Context) (*envelope, error) {
	select {
	case env, ok := <-s.inbox:
		if !ok {
			return nil, s.failure()
		}
		return env, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *processState) failure() error {
	select {
	case <-s.done:
		if s.err != nil {
			return s.err
		}
	default:
	}
	return errors.New("claude stopped before finishing its reply")
}

// newUUID returns a random v4 UUID.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
