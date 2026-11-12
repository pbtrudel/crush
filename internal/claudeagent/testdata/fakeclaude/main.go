// Command fakeclaude stands in for the `claude` CLI in the claudeagent
// integration tests. It speaks the same NDJSON control protocol as the real
// binary, but every reply is scripted, so the tests need neither a Claude
// subscription nor network access.
//
// The script is chosen by the text of the user message: a message containing
// CALL_TOOL makes the fake call a Crush tool over MCP and answer with its
// result, and any other message produces a short text reply.
//
// Environment variables:
//
//	FAKECLAUDE_LOG      file the fake appends a JSON log of its traffic to
//	FAKECLAUDE_TOOL     tool name to call for CALL_TOOL (default "echo")
//	FAKECLAUDE_VERSION  version reported to the provider (default "2.1.322")
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// defaultVersion is new enough for every check the provider makes.
	defaultVersion = "2.1.322"
	// controlTimeout bounds how long the fake waits for the provider to
	// answer one of its control requests.
	controlTimeout = 10 * time.Second
	// toolPrefix is what Claude Code prepends to an MCP tool name.
	toolPrefix = "mcp__crush__"
)

// valueFlags are the flags that take a value, either as the next argument or
// after an equals sign. Unknown flags are rejected so provider drift fails the
// test instead of being silently ignored.
var valueFlags = map[string]bool{
	"--output-format":          true,
	"--input-format":           true,
	"--tools":                  true,
	"--permission-mode":        true,
	"--allowedTools":           true,
	"--setting-sources":        true,
	"--settings":               true,
	"--thinking":               true,
	"--thinking-display":       true,
	"--system-prompt-snapshot": true,
	"--append-system-prompt":   true,
	"--system-prompt":          true,
	"--model":                  true,
	"--effort":                 true,
	"--mcp-config":             true,
	"--resume":                 true,
	"--session-id":             true,
}

// boolFlags are the flags that stand alone.
var boolFlags = map[string]bool{
	"--verbose":                  true,
	"--strict-mcp-config":        true,
	"--include-partial-messages": true,
	"--no-session-persistence":   true,
}

// options is the subset of the CLI invocation the fake reacts to.
type options struct {
	args         []string
	model        string
	effort       string
	mcpConfig    string
	resume       string
	sessionID    string
	systemPrompt string
	appendPrompt string
	persist      bool
}

// parseArgs validates the flags the provider passes and keeps the ones that
// change the fake's behaviour.
func parseArgs(args []string) (*options, error) {
	opts := &options{args: args, persist: true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		if boolFlags[name] {
			if hasValue {
				return nil, fmt.Errorf("flag %s does not take a value", name)
			}
			if name == "--no-session-persistence" {
				opts.persist = false
			}
			continue
		}
		if !valueFlags[name] {
			return nil, fmt.Errorf("unknown flag %q", arg)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag %s needs a value", name)
			}
			i++
			value = args[i]
		}
		switch name {
		case "--model":
			opts.model = value
		case "--effort":
			opts.effort = value
		case "--mcp-config":
			opts.mcpConfig = value
		case "--resume":
			opts.resume = value
		case "--session-id":
			opts.sessionID = value
		case "--system-prompt":
			opts.systemPrompt = value
		case "--append-system-prompt":
			opts.appendPrompt = value
		}
	}
	return opts, nil
}

// envelope is one NDJSON line from the provider.
type envelope struct {
	Type      string               `json:"type"`
	RequestID string               `json:"request_id"`
	Request   json.RawMessage      `json:"request"`
	Response  *controlResponseBody `json:"response"`
	Message   json.RawMessage      `json:"message"`
	UUID      string               `json:"uuid"`
}

// controlResponseBody is the provider's answer to one of the fake's control
// requests.
type controlResponseBody struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response"`
	Error     string          `json:"error"`
}

// cli is the fake's process state.
type cli struct {
	opts *options
	out  *bufio.Writer

	writeMu sync.Mutex

	logMu   sync.Mutex
	logFile *os.File

	pendingMu sync.Mutex
	pending   map[string]chan *controlResponseBody
	nextID    int

	toolsMu sync.Mutex
	tools   []string

	modelMu sync.Mutex
	model   string
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeclaude: %v\n", err)
		os.Exit(2)
	}
	c, err := newCLI(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeclaude: %v\n", err)
		os.Exit(2)
	}
	if err := c.run(); err != nil {
		fmt.Fprintf(os.Stderr, "fakeclaude: %v\n", err)
		os.Exit(1)
	}
}

func newCLI(opts *options) (*cli, error) {
	c := &cli{
		opts:    opts,
		out:     bufio.NewWriter(os.Stdout),
		pending: make(map[string]chan *controlResponseBody),
		model:   opts.model,
	}
	if path := os.Getenv("FAKECLAUDE_LOG"); path != "" {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("opening log: %w", err)
		}
		c.logFile = file
	}
	return c, nil
}

// run reads the provider's messages until stdin closes.
func (c *cli) run() error {
	c.log(map[string]any{"kind": "args", "args": c.opts.args})
	reader := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			c.handleLine(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (c *cli) handleLine(line []byte) {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		c.log(map[string]any{"kind": "bad_line", "line": string(line)})
		return
	}
	switch env.Type {
	case "control_response":
		c.resolveControl(&env)
	case "control_request":
		go func() {
			if err := c.handleControlRequest(&env); err != nil {
				fmt.Fprintf(os.Stderr, "fakeclaude: %v\n", err)
				os.Exit(1)
			}
		}()
	case "user":
		go func() {
			if err := c.handleUser(&env); err != nil {
				fmt.Fprintf(os.Stderr, "fakeclaude: %v\n", err)
				os.Exit(1)
			}
		}()
	default:
		c.log(map[string]any{"kind": "unknown", "type": env.Type})
	}
}

// handleControlRequest answers the provider's control requests. It runs on its
// own goroutine because the MCP handshake it performs waits for answers that
// only the reader loop can deliver.
func (c *cli) handleControlRequest(env *envelope) error {
	var req struct {
		Subtype string `json:"subtype"`
		Model   string `json:"model"`
	}
	if err := json.Unmarshal(env.Request, &req); err != nil {
		return fmt.Errorf("decoding control request: %w", err)
	}
	switch req.Subtype {
	case "initialize":
		return c.handleInitialize(env.RequestID)
	case "interrupt":
		if err := c.respondSuccess(env.RequestID, map[string]any{}); err != nil {
			return err
		}
		// An interrupted turn still ends with a result message.
		return c.result(1, "interrupt", true, usageBlock(10, 1000, 20, 0))
	case "set_model":
		c.modelMu.Lock()
		c.model = req.Model
		c.modelMu.Unlock()
		c.log(map[string]any{"kind": "set_model", "model": req.Model})
		return c.respondSuccess(env.RequestID, map[string]any{})
	default:
		return c.respondError(env.RequestID, fmt.Errorf("unsupported control request %q", req.Subtype))
	}
}

// handleInitialize performs the MCP handshake, answers the provider, and then
// announces the session the way the real CLI does.
func (c *cli) handleInitialize(requestID string) error {
	if c.opts.mcpConfig != "" {
		c.mcpHandshake()
	}
	if err := c.respondSuccess(requestID, map[string]any{
		"claude_code_version": c.version(),
		"account": map[string]any{
			"email":            "fake@example.com",
			"organization":     "Fake Org",
			"subscriptionType": "max",
			"tokenSource":      "oauth",
			"apiProvider":      "firstParty",
		},
		"models": []map[string]any{
			{
				"value":                 "fake-opus",
				"resolvedModel":         "claude-opus-5-5",
				"displayName":           "Fake Opus",
				"supportedEffortLevels": []string{"low", "high"},
			},
			{
				"value":                 "fake-haiku",
				"resolvedModel":         "claude-haiku-5-5",
				"displayName":           "Fake Haiku",
				"supportedEffortLevels": []string{"low"},
			},
		},
		"current_permission_mode": "dontAsk",
	}); err != nil {
		return err
	}
	return c.emitInit()
}

// mcpHandshake asks the provider's MCP server for its tool list. It tolerates
// an error answer: the fake is still able to script a tool call.
func (c *cli) mcpHandshake() {
	resp, err := c.sendControl(map[string]any{
		"subtype":     "mcp_message",
		"server_name": "crush",
		"message": map[string]any{
			"jsonrpc": "2.0",
			"id":      0,
			"method":  "initialize",
			"params":  map[string]any{"protocolVersion": "2025-11-25"},
		},
	})
	if err != nil {
		c.log(map[string]any{"kind": "mcp", "method": "initialize", "error": err.Error()})
		return
	}
	c.log(map[string]any{"kind": "mcp", "method": "initialize", "subtype": resp.Subtype, "error": resp.Error})

	resp, err = c.sendControl(map[string]any{
		"subtype":     "mcp_message",
		"server_name": "crush",
		"message": map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/list",
		},
	})
	if err != nil {
		c.log(map[string]any{"kind": "mcp", "method": "tools/list", "error": err.Error()})
		return
	}
	tools := mcpToolNames(resp)
	c.log(map[string]any{"kind": "mcp", "method": "tools/list", "subtype": resp.Subtype, "error": resp.Error, "tools": tools})
	c.toolsMu.Lock()
	c.tools = tools
	c.toolsMu.Unlock()
}

// mcpToolNames unwraps the tool names from a tools/list answer.
func mcpToolNames(resp *controlResponseBody) []string {
	if resp == nil || resp.Subtype != "success" || len(resp.Response) == 0 {
		return nil
	}
	var wrapper struct {
		MCPResponse struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		} `json:"mcp_response"`
	}
	if err := json.Unmarshal(resp.Response, &wrapper); err != nil {
		return nil
	}
	names := make([]string, 0, len(wrapper.MCPResponse.Result.Tools))
	for _, tool := range wrapper.MCPResponse.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// emitInit announces the session, including the MCP servers that connected.
func (c *cli) emitInit() error {
	c.toolsMu.Lock()
	tools := append([]string(nil), c.tools...)
	c.toolsMu.Unlock()

	servers := []map[string]any{}
	if c.opts.mcpConfig != "" {
		servers = append(servers, map[string]any{
			"name":   "crush",
			"status": "connected",
			"source": "sdk",
		})
	}
	return c.write(map[string]any{
		"type":        "system",
		"subtype":     "init",
		"session_id":  c.sessionID(),
		"model":       c.currentModel(),
		"mcp_servers": servers,
		"tools":       tools,
	})
}

// handleUser records the message and plays the scripted turn for it.
func (c *cli) handleUser(env *envelope) error {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(env.Message, &payload); err != nil {
		return fmt.Errorf("decoding user message: %w", err)
	}
	var text strings.Builder
	for _, block := range payload.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	c.log(map[string]any{"kind": "user", "uuid": env.UUID, "text": text.String()})
	if strings.Contains(text.String(), "CALL_TOOL") {
		return c.runToolTurn()
	}
	return c.runTextTurn()
}

// runTextTurn replies with a short text answer.
func (c *cli) runTextTurn() error {
	const messageID = "msg_fake_text"
	usage := usageBlock(10, 1000, 20, 5)
	if err := c.messageStart(messageID, usage); err != nil {
		return err
	}
	if err := c.contentBlockStart(0, map[string]any{"type": "text", "text": ""}); err != nil {
		return err
	}
	if err := c.contentBlockDelta(0, map[string]any{"type": "text_delta", "text": "fake "}); err != nil {
		return err
	}
	if err := c.contentBlockDelta(0, map[string]any{"type": "text_delta", "text": "reply"}); err != nil {
		return err
	}
	if err := c.contentBlockStop(0); err != nil {
		return err
	}
	if err := c.messageDelta("end_turn", usage); err != nil {
		return err
	}
	if err := c.assistant("uuid_fake_text", messageID, []map[string]any{
		{"type": "text", "text": "fake reply"},
	}, usage); err != nil {
		return err
	}
	if err := c.messageStop(); err != nil {
		return err
	}
	return c.result(1, "end_turn", false, usage)
}

// runToolTurn asks the provider to run a Crush tool, waits for the result, and
// then answers with it. The turn's only result message comes after the tool has
// run, which is what lets the provider pause the step for the tool call.
func (c *cli) runToolTurn() error {
	const (
		messageID      = "msg_fake_tool"
		continuationID = "msg_fake_continuation"
		callID         = "toolu_fake_1"
	)
	name := c.toolToCall()
	input := map[string]any{"text": "hello"}
	usage := usageBlock(10, 1000, 20, 5)

	if err := c.messageStart(messageID, usage); err != nil {
		return err
	}
	if err := c.contentBlockStart(0, map[string]any{
		"type":  "tool_use",
		"id":    callID,
		"name":  name,
		"input": map[string]any{},
	}); err != nil {
		return err
	}
	if err := c.contentBlockStop(0); err != nil {
		return err
	}
	if err := c.messageDelta("tool_use", usage); err != nil {
		return err
	}
	if err := c.assistant("uuid_fake_tool", messageID, []map[string]any{{
		"type":  "tool_use",
		"id":    callID,
		"name":  name,
		"input": input,
	}}, usage); err != nil {
		return err
	}
	if err := c.messageStop(); err != nil {
		return err
	}

	resp, err := c.sendControl(map[string]any{
		"subtype":     "mcp_message",
		"server_name": "crush",
		"message": map[string]any{
			"jsonrpc": "2.0",
			"id":      2,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      name,
				"arguments": input,
				"_meta":     map[string]any{"claudecode/toolUseId": callID},
			},
		},
	})
	if err != nil {
		return err
	}
	text := toolResultText(resp)
	c.log(map[string]any{
		"kind":        "tool_result",
		"tool_use_id": callID,
		"text":        text,
		"subtype":     resp.Subtype,
		"error":       resp.Error,
	})

	continuationUsage := usageBlock(10, 1030, 3, 2)
	answer := "tool said: " + text
	if err := c.messageStart(continuationID, continuationUsage); err != nil {
		return err
	}
	if err := c.contentBlockStart(0, map[string]any{"type": "text", "text": ""}); err != nil {
		return err
	}
	if err := c.contentBlockDelta(0, map[string]any{"type": "text_delta", "text": answer}); err != nil {
		return err
	}
	if err := c.contentBlockStop(0); err != nil {
		return err
	}
	if err := c.messageDelta("end_turn", continuationUsage); err != nil {
		return err
	}
	if err := c.assistant("uuid_fake_continuation", continuationID, []map[string]any{
		{"type": "text", "text": answer},
	}, continuationUsage); err != nil {
		return err
	}
	if err := c.messageStop(); err != nil {
		return err
	}
	return c.result(2, "end_turn", false, continuationUsage)
}

// toolResultText unwraps the text of an MCP tools/call answer.
func toolResultText(resp *controlResponseBody) string {
	if resp == nil || resp.Subtype != "success" || len(resp.Response) == 0 {
		return ""
	}
	var wrapper struct {
		MCPResponse struct {
			Result struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		} `json:"mcp_response"`
	}
	if err := json.Unmarshal(resp.Response, &wrapper); err != nil {
		return ""
	}
	for _, block := range wrapper.MCPResponse.Result.Content {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

// toolToCall picks the tool the CALL_TOOL script invokes.
func (c *cli) toolToCall() string {
	if name := os.Getenv("FAKECLAUDE_TOOL"); name != "" {
		if strings.HasPrefix(name, "mcp__") {
			return name
		}
		return toolPrefix + name
	}
	c.toolsMu.Lock()
	defer c.toolsMu.Unlock()
	if len(c.tools) > 0 {
		return c.tools[0]
	}
	return toolPrefix + "echo"
}

// sendControl writes a control request and waits for the provider's answer.
func (c *cli) sendControl(request map[string]any) (*controlResponseBody, error) {
	c.pendingMu.Lock()
	c.nextID++
	id := fmt.Sprintf("fake_%d", c.nextID)
	ch := make(chan *controlResponseBody, 1)
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.write(map[string]any{
		"type":       "control_request",
		"request_id": id,
		"request":    request,
	}); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(controlTimeout):
		return nil, fmt.Errorf("no answer to control request %s", id)
	}
}

// resolveControl delivers an answer to the goroutine waiting for it.
func (c *cli) resolveControl(env *envelope) {
	if env.Response == nil {
		return
	}
	c.pendingMu.Lock()
	ch, ok := c.pending[env.Response.RequestID]
	delete(c.pending, env.Response.RequestID)
	c.pendingMu.Unlock()
	if ok {
		ch <- env.Response
	}
}

func (c *cli) respondSuccess(requestID string, body any) error {
	return c.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   body,
		},
	})
}

func (c *cli) respondError(requestID string, err error) error {
	return c.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "error",
			"request_id": requestID,
			"error":      err.Error(),
		},
	})
}

// streamEvent wraps one Anthropic streaming event.
func (c *cli) streamEvent(event map[string]any) error {
	return c.write(map[string]any{
		"type":       "stream_event",
		"session_id": c.sessionID(),
		"event":      event,
	})
}

func (c *cli) messageStart(messageID string, usage map[string]any) error {
	return c.streamEvent(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":      messageID,
			"type":    "message",
			"role":    "assistant",
			"model":   c.currentModel(),
			"content": []any{},
			"usage":   usage,
		},
	})
}

func (c *cli) contentBlockStart(index int, block map[string]any) error {
	return c.streamEvent(map[string]any{
		"type":          "content_block_start",
		"index":         index,
		"content_block": block,
	})
}

func (c *cli) contentBlockDelta(index int, delta map[string]any) error {
	return c.streamEvent(map[string]any{
		"type":  "content_block_delta",
		"index": index,
		"delta": delta,
	})
}

func (c *cli) contentBlockStop(index int) error {
	return c.streamEvent(map[string]any{
		"type":  "content_block_stop",
		"index": index,
	})
}

func (c *cli) messageDelta(stopReason string, usage map[string]any) error {
	return c.streamEvent(map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason},
		"usage": usage,
	})
}

func (c *cli) messageStop() error {
	return c.streamEvent(map[string]any{"type": "message_stop"})
}

// assistant reports the completed message, the way the CLI does once the API
// message is assembled.
func (c *cli) assistant(uuid, messageID string, content []map[string]any, usage map[string]any) error {
	return c.write(map[string]any{
		"type":       "assistant",
		"uuid":       uuid,
		"session_id": c.sessionID(),
		"message": map[string]any{
			"id":      messageID,
			"type":    "message",
			"role":    "assistant",
			"model":   c.currentModel(),
			"content": content,
			"usage":   usage,
		},
	})
}

// result ends the turn.
func (c *cli) result(numTurns int, stopReason string, isError bool, usage map[string]any) error {
	return c.write(map[string]any{
		"type":        "result",
		"subtype":     "success",
		"is_error":    isError,
		"num_turns":   numTurns,
		"stop_reason": stopReason,
		"session_id":  c.sessionID(),
		"result":      "fake reply",
		"usage":       usage,
	})
}

// usageBlock is an Anthropic usage report. The provider must keep the highest
// count per response instead of summing the repeats.
func usageBlock(input, cacheRead, cacheCreation, output int64) map[string]any {
	return map[string]any{
		"input_tokens":                input,
		"output_tokens":               output,
		"cache_read_input_tokens":     cacheRead,
		"cache_creation_input_tokens": cacheCreation,
	}
}

// write sends one JSON object to the provider.
func (c *cli) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.out.Write(body); err != nil {
		return err
	}
	return c.out.Flush()
}

// log appends one JSON line to the traffic log, if one was requested.
func (c *cli) log(entry map[string]any) {
	if c.logFile == nil {
		return
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return
	}
	body = append(body, '\n')
	c.logMu.Lock()
	defer c.logMu.Unlock()
	_, _ = c.logFile.Write(body)
}

// version is the CLI version the provider probes for.
func (c *cli) version() string {
	if version := os.Getenv("FAKECLAUDE_VERSION"); version != "" {
		return version
	}
	return defaultVersion
}

// sessionID is the Claude session this process serves.
func (c *cli) sessionID() string {
	if c.opts.resume != "" {
		return c.opts.resume
	}
	return c.opts.sessionID
}

// currentModel is the model the provider last selected.
func (c *cli) currentModel() string {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	return c.model
}
