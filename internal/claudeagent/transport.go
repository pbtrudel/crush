package claudeagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// executableEnv overrides where the `claude` CLI is found.
const executableEnv = "CRUSH_CLAUDE_PATH"

// startTimeout bounds how long the CLI may take to answer initialize. MCP
// servers start during initialization, so this is generous.
const startTimeout = 30 * time.Second

// stopGrace is how long a process gets to flush its transcript after stdin
// closes before it is killed.
const stopGrace = 5 * time.Second

// isolationEnv turns off Claude Code features that duplicate Crush's own, so
// the user's Claude Code settings never leak into a Crush session.
//
// A nil value means "remove this variable": the user may have exported it in
// their shell, and leaving it set would change how the CLI behaves under us.
var isolationEnv = map[string]*string{
	// Crush reads AGENTS.md and its own memory files.
	"CLAUDE_CODE_DISABLE_CLAUDE_MDS":  ptr("1"),
	"CLAUDE_CODE_DISABLE_AUTO_MEMORY": ptr("1"),
	// Crush compacts instead.
	"DISABLE_AUTO_COMPACT": ptr("1"),
	// Crush has no away summaries.
	"CLAUDE_CODE_ENABLE_AWAY_SUMMARY": ptr("0"),
	// Every Crush tool reaches Claude as an MCP tool, and tool search would
	// hide all of them.
	"ENABLE_TOOL_SEARCH": ptr("false"),
	// The advisor tool would spend the user's plan on calls Crush cannot show.
	"CLAUDE_CODE_DISABLE_ADVISOR_TOOL": ptr("1"),
	// A non-streaming retry would leave Crush waiting for a step that never ends.
	"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": ptr("1"),
	// A refusal fallback would silently switch models.
	"CLAUDE_CODE_DISABLE_REFUSAL_FALLBACK": ptr("1"),
	// Raised so Crush's own truncation applies instead.
	"MAX_MCP_OUTPUT_TOKENS": ptr("200000"),
	// Skips OAuth, which is the whole point of this provider.
	"CLAUDE_CODE_SIMPLE": nil,
	// A shell-level effort override would win over the effort Crush passes.
	"CLAUDE_CODE_EFFORT_LEVEL": nil,
	// Caps every Crush tool call, failing subagents and permission prompts.
	"MCP_TOOL_TIMEOUT": nil,
}

func ptr[T any](v T) *T { return &v }

// Connection maps Crush provider settings onto the environment variables Claude
// Code reads.
type Connection struct {
	BaseURL       string
	APIKey        string
	AuthToken     string
	HeaderTimeout int64
	ChunkTimeout  int64
	Headers       map[string]string
	Body          map[string]any
}

// launchConfig is everything that requires a new process to change.
type launchConfig struct {
	Executable      string
	Dir             string
	Model           string
	Effort          string
	SystemPrompt    string
	AppendPrompt    string
	Tools           []toolDef
	SessionID       string
	Resume          string
	Persist         bool
	MaxOutputTokens int64
	// Thinking is the CLI's thinking mode: "adaptive" for a chat, "disabled"
	// for a standalone task with a small output budget.
	Thinking   string
	Connection Connection
}

// key returns a stable identity for the settings that cannot change on a
// running process.
func (c launchConfig) key() string {
	body, _ := json.Marshal(c)
	return string(body)
}

// restartKey is the identity that requires a new process. The model is
// excluded because a running process can switch models with a control request,
// and the session IDs are excluded because they name the conversation the
// process serves, not how it was started.
func (c launchConfig) restartKey() string {
	c.Model = ""
	c.SessionID = ""
	c.Resume = ""
	return c.key()
}

// toolDef is one tool offered to Claude through the MCP bridge.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// cliProcess is one running `claude` process.
type cliProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	dir   string

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan *controlResponseBody
	nextID    atomic.Int64

	events   chan *envelope
	done     chan struct{}
	readDone chan struct{}

	errMu sync.Mutex
	err   error

	stderr *tailBuffer

	// id is the Claude session ID this process reads and writes.
	id string
	// persisted reports whether the CLI has written any of this turn to its
	// transcript, so a record is only saved when there is something to resume.
	persisted atomic.Bool

	// mcpHandler serves an inbound MCP JSON-RPC message. It may block for as
	// long as a Crush tool takes to run, so it is called on its own goroutine.
	mcpHandler func(ctx context.Context, server string, message json.RawMessage) (json.RawMessage, error)

	closeOnce sync.Once
	doneOnce  sync.Once
	stopOnce  sync.Once
	closed    chan struct{}
}

// FindExecutable locates the `claude` CLI.
func FindExecutable() (string, error) { return findExecutable() }

// findExecutable locates the `claude` CLI.
func findExecutable() (string, error) {
	if p := os.Getenv(executableEnv); p != "" {
		return p, nil
	}
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("the Claude Code CLI is not installed: install it from https://code.claude.com/docs/en/setup, then run `claude auth login`")
	}
	return p, nil
}

// startProcess spawns the CLI and performs the initialize handshake.
//
// mcpHandler must be set before the handshake: the CLI opens its MCP
// connection while initialize is still in flight, and an unanswered handshake
// leaves Claude running with no tools at all.
func startProcess(ctx context.Context, cfg launchConfig, mcpHandler func(context.Context, string, json.RawMessage) (json.RawMessage, error)) (*cliProcess, *initializeResult, error) {
	args := buildArgs(cfg)
	// The process lifetime is managed by stop, not by the request that started
	// it, so the context is detached from the caller's cancellation.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), cfg.Executable, args...)
	cmd.Dir = cfg.Dir
	cmd.Env = buildEnv(cfg)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("claude stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("claude stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, fmt.Errorf("claude stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting claude: %w", err)
	}

	p := &cliProcess{
		cmd:      cmd,
		stdin:    stdin,
		dir:      cfg.Dir,
		pending:  make(map[string]chan *controlResponseBody),
		events:   make(chan *envelope, 256),
		done:     make(chan struct{}),
		closed:   make(chan struct{}),
		readDone: make(chan struct{}),
		stderr:   newTailBuffer(16 << 10),
	}
	p.mcpHandler = mcpHandler

	go p.readLoop(stdout)
	go p.readStderr(stderr)

	init, err := p.initialize(ctx)
	if err != nil {
		p.stop(context.WithoutCancel(ctx))
		return nil, nil, err
	}
	return p, init, nil
}

// buildArgs assembles the CLI invocation. The shape mirrors what the Claude
// Agent SDK sends, because that is the configuration Anthropic supports.
func buildArgs(cfg launchConfig) []string {
	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--input-format", "stream-json",
		// Claude Code's built-in tools are off; Crush's tools arrive over MCP.
		"--tools", "",
		"--permission-mode", "dontAsk",
		"--allowedTools", mcpToolPrefix + "*",
		"--strict-mcp-config",
		// Ignore the user's Claude Code settings files, hooks and skills.
		"--setting-sources=",
		"--settings", `{"disableAllHooks":true}`,
		"--include-partial-messages",
		// Re-render the system prompt each request so a resumed session picks
		// up the current agent prompt instead of a recorded one.
		"--system-prompt-snapshot", "off",
	}

	thinking := cfg.Thinking
	if thinking == "" {
		thinking = "adaptive"
	}
	args = append(args, "--thinking", thinking)
	if thinking != "disabled" {
		// Crush shows reasoning, so ask for summarized thinking.
		args = append(args, "--thinking-display", "summarized")
	}
	if cfg.AppendPrompt != "" {
		args = append(args, "--append-system-prompt", cfg.AppendPrompt)
	}
	if cfg.SystemPrompt != "" {
		args = append(args, "--system-prompt", cfg.SystemPrompt)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.Effort != "" {
		args = append(args, "--effort", cfg.Effort)
	}
	if len(cfg.Tools) > 0 {
		servers := map[string]any{
			"mcpServers": map[string]any{
				mcpServerName: map[string]any{"type": "sdk", "name": mcpServerName},
			},
		}
		body, _ := json.Marshal(servers)
		args = append(args, "--mcp-config", string(body))
	}
	if cfg.Resume != "" {
		// The equals form keeps a value starting with a dash bound to the flag.
		args = append(args, "--resume="+cfg.Resume)
	} else if cfg.SessionID != "" {
		args = append(args, "--session-id="+cfg.SessionID)
	}
	if !cfg.Persist {
		args = append(args, "--no-session-persistence")
	}
	return args
}

// buildEnv merges the user's environment with the isolation settings and the
// provider's connection settings.
func buildEnv(cfg launchConfig) []string {
	conn := cfg.Connection
	env := make(map[string]string, len(os.Environ())+len(isolationEnv))
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		env[name] = value
	}
	// A Crush process that was itself started from a Claude Code session would
	// otherwise make the child think it is nested.
	delete(env, "CLAUDECODE")
	env["CLAUDE_CODE_ENTRYPOINT"] = "sdk-ts"

	if conn.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = conn.BaseURL
	}
	if conn.APIKey != "" {
		env["ANTHROPIC_API_KEY"] = conn.APIKey
	}
	if conn.AuthToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = conn.AuthToken
	}
	if conn.HeaderTimeout > 0 {
		env["API_TIMEOUT_MS"] = fmt.Sprint(conn.HeaderTimeout)
	}
	if conn.ChunkTimeout > 0 {
		env["CLAUDE_STREAM_IDLE_TIMEOUT_MS"] = fmt.Sprint(conn.ChunkTimeout)
	}
	if len(conn.Headers) > 0 {
		lines := make([]string, 0, len(conn.Headers))
		for name, value := range conn.Headers {
			lines = append(lines, name+": "+value)
		}
		env["ANTHROPIC_CUSTOM_HEADERS"] = strings.Join(lines, "\n")
	}
	if len(conn.Body) > 0 {
		if body, err := json.Marshal(conn.Body); err == nil {
			env["CLAUDE_CODE_EXTRA_BODY"] = string(body)
		}
	}
	if cfg.MaxOutputTokens > 0 {
		env["CLAUDE_CODE_MAX_OUTPUT_TOKENS"] = fmt.Sprint(cfg.MaxOutputTokens)
	}

	for name, value := range isolationEnv {
		if value == nil {
			delete(env, name)
			continue
		}
		env[name] = *value
	}

	out := make([]string, 0, len(env))
	for name, value := range env {
		out = append(out, name+"="+value)
	}
	return out
}

// readLoop decodes the CLI's NDJSON stdout.
func (p *cliProcess) readLoop(r io.Reader) {
	defer close(p.readDone)
	defer close(p.events)
	reader := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			p.handleLine(line)
		}
		if err != nil {
			p.setErr(err)
			return
		}
	}
}

func (p *cliProcess) handleLine(line []byte) {
	line = trimSpace(line)
	if len(line) == 0 {
		return
	}
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		slog.Debug("Claude CLI wrote a non-JSON line", "line", truncate(string(line), 200), "error", err)
		return
	}
	switch env.Type {
	case msgControlResponse:
		p.resolveControl(&env)
	case msgControlRequest:
		go p.handleControlRequest(&env)
	default:
		if env.Type == msgAssistant || env.Type == msgResult {
			p.persisted.Store(true)
		}
		select {
		case p.events <- &env:
		case <-p.done:
		case <-p.closed:
			// The session stopped reading; drop the rest instead of blocking
			// the loop, which would keep the process alive.
			return
		}
	}
}

func (p *cliProcess) readStderr(r io.Reader) {
	reader := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			p.stderr.Write(line)
		}
		if err != nil {
			return
		}
	}
}

// write sends one JSON object to the CLI's stdin.
func (p *cliProcess) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding claude message: %w", err)
	}
	body = append(body, '\n')
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.stdin.Write(body); err != nil {
		return fmt.Errorf("writing to claude: %w", err)
	}
	return nil
}

// sendControl writes a control request and waits for its answer.
func (p *cliProcess) sendControl(ctx context.Context, request any, timeout time.Duration) (json.RawMessage, error) {
	id := fmt.Sprintf("req_%d", p.nextID.Add(1))
	ch := make(chan *controlResponseBody, 1)
	p.pendingMu.Lock()
	p.pending[id] = ch
	p.pendingMu.Unlock()
	defer func() {
		p.pendingMu.Lock()
		delete(p.pending, id)
		p.pendingMu.Unlock()
	}()

	if err := p.write(controlRequest{Type: msgControlRequest, RequestID: id, Request: request}); err != nil {
		return nil, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if resp.Subtype != "success" {
			return nil, fmt.Errorf("claude rejected the request: %s", resp.Error)
		}
		return resp.Response, nil
	case <-timer.C:
		return nil, fmt.Errorf("claude did not answer within %s", timeout)
	case <-p.done:
		return nil, p.processError()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *cliProcess) resolveControl(env *envelope) {
	if env.Response == nil {
		return
	}
	p.pendingMu.Lock()
	ch, ok := p.pending[env.Response.RequestID]
	p.pendingMu.Unlock()
	if ok {
		ch <- env.Response
	}
}

// handleControlRequest answers a request from the CLI. Each one runs on its own
// goroutine because a tool call can block for minutes.
func (p *cliProcess) handleControlRequest(env *envelope) {
	var req controlRequestBody
	if err := json.Unmarshal(env.Request, &req); err != nil {
		p.respondError(env.RequestID, err)
		return
	}
	switch req.Subtype {
	case ctrlMCPMessage:
		if p.mcpHandler == nil {
			p.respondError(env.RequestID, errors.New("no MCP handler"))
			return
		}
		// The handler outlives the request context: a tool call must not be
		// cancelled just because the turn that started it ended.
		resp, err := p.mcpHandler(context.Background(), req.ServerName, req.Message)
		if err != nil {
			p.respondError(env.RequestID, err)
			return
		}
		p.respondSuccess(env.RequestID, map[string]any{"mcp_response": resp})
	case ctrlCanUseTool:
		// Crush's own permission system decides, so every tool the CLI asks
		// about is allowed here.
		p.respondSuccess(env.RequestID, map[string]any{
			"behavior":     "allow",
			"updatedInput": json.RawMessage(env.Request),
		})
	case ctrlHookCallback:
		p.respondSuccess(env.RequestID, map[string]any{})
	default:
		p.respondError(env.RequestID, fmt.Errorf("unsupported control request %q", req.Subtype))
	}
}

func (p *cliProcess) respondSuccess(id string, body any) {
	if err := p.write(controlResponse{
		Type:     msgControlResponse,
		Response: controlResponsePayload{Subtype: "success", RequestID: id, Response: body},
	}); err != nil {
		slog.Debug("Could not answer Claude", "request_id", id, "error", err)
	}
}

func (p *cliProcess) respondError(id string, err error) {
	if writeErr := p.write(controlResponse{
		Type:     msgControlResponse,
		Response: controlResponsePayload{Subtype: "error", RequestID: id, Error: err.Error()},
	}); writeErr != nil {
		slog.Debug("Could not report an error to Claude", "request_id", id, "error", writeErr)
	}
}

// initialize performs the control handshake and returns what the CLI reports
// about the login and the models it can run.
func (p *cliProcess) initialize(ctx context.Context) (*initializeResult, error) {
	raw, err := p.sendControl(ctx, initializeRequest{Subtype: ctrlInitialize}, startTimeout)
	if err != nil {
		return nil, fmt.Errorf("claude did not start: %w", err)
	}
	var result initializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decoding claude initialize result: %w", err)
	}
	if err := checkVersion(result.ClaudeCodeVersion); err != nil {
		return nil, err
	}
	return &result, nil
}

// setModel switches the model on a running process.
func (p *cliProcess) setModel(ctx context.Context, model string) error {
	_, err := p.sendControl(ctx, map[string]any{"subtype": ctrlSetModel, "model": model}, startTimeout)
	return err
}

// interrupt stops the reply in flight.
func (p *cliProcess) interrupt(ctx context.Context) error {
	_, err := p.sendControl(ctx, map[string]any{"subtype": ctrlInterrupt}, startTimeout)
	return err
}

// sendUserMessage starts a turn with a new user message.
func (p *cliProcess) sendUserMessage(uuid string, blocks []ContentBlock) error {
	return p.write(userMessage{
		Type:            msgUser,
		UUID:            uuid,
		ParentToolUseID: nil,
		Message:         wireBody{Role: "user", Content: blocks},
		ClientComposed:  true,
	})
}

// steerUserMessage adds a message to a turn that is already running.
func (p *cliProcess) steerUserMessage(uuid string, blocks []ContentBlock) error {
	return p.write(userMessage{
		Type:            msgUser,
		UUID:            uuid,
		ParentToolUseID: nil,
		Message:         wireBody{Role: "user", Content: blocks},
		ClientComposed:  true,
		Priority:        "next",
	})
}

// events returns the stream of non-control messages.
func (p *cliProcess) eventStream() <-chan *envelope { return p.events }

func (p *cliProcess) setErr(err error) {
	p.errMu.Lock()
	if p.err == nil {
		p.err = err
	}
	p.errMu.Unlock()
	p.doneOnce.Do(func() { close(p.done) })
}

func (p *cliProcess) processError() error {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	if p.err == nil || errors.Is(p.err, io.EOF) {
		msg := p.stderr.String()
		if msg != "" {
			return fmt.Errorf("claude exited: %s", lastLine(msg))
		}
		return errors.New("claude exited")
	}
	return fmt.Errorf("claude exited: %w", p.err)
}

// stop shuts the process down, giving it a moment to flush its transcript.
func (p *cliProcess) stop(ctx context.Context) {
	select {
	case <-p.closed:
		return
	default:
	}
	p.stopOnce.Do(func() {
		close(p.closed)
		_ = p.stdin.Close()
		// Wait must not run while the read loop is still reading: exec.Cmd
		// closes the pipe on Wait, which would cut the transcript short.
		select {
		case <-p.readDone:
		case <-time.After(stopGrace):
			_ = p.cmd.Process.Kill()
			<-p.readDone
		case <-ctx.Done():
			_ = p.cmd.Process.Kill()
			<-p.readDone
		}
		_ = p.cmd.Wait()
		p.doneOnce.Do(func() { close(p.done) })
	})
}

func (p *cliProcess) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// tailBuffer keeps the last n bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 {
		last := b[len(b)-1]
		if last != ' ' && last != '\t' && last != '\r' && last != '\n' {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
