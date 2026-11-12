package claudeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

const (
	// mcpServerName is the MCP server Claude sees Crush's tools under.
	mcpServerName = "crush"
	// mcpToolPrefix is what Claude Code prepends to an MCP tool's name.
	mcpToolPrefix = "mcp__" + mcpServerName + "__"
	// mcpMaxToolName is the longest tool name MCP allows.
	mcpMaxToolName = 64
	// mcpDescriptionLimit is where Claude Code cuts a tool description.
	mcpDescriptionLimit = 2048
)

// toolResult is what a parked tool call resolves to.
type toolResult struct {
	content []mcpContentBlock
	isError bool
}

// mcpContentBlock is one block of an MCP tool result. MCP carries an image as
// {"type":"image","data":...,"mimeType":...}, which is not the Messages API
// shape, so it has its own type.
type mcpContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// pendingTools parks Claude's tool calls until Crush has run them.
//
// The CLI dispatches an MCP tools/call only after the reply that requested it
// has finished streaming, so the provider has already returned to Crush's agent
// loop by then. The call waits here until the next step delivers its result.
//
// Either side can arrive first: the result may be resolved before the CLI gets
// around to calling, so a result with no waiter is held until the call lands.
type pendingTools struct {
	mu      sync.Mutex
	waiters map[string]chan toolResult
	ready   map[string]toolResult
	// order holds the tool_use ids the reply announced, for a CLI that omits
	// the id from tools/call.
	order []string
}

func newPendingTools() *pendingTools {
	return &pendingTools{
		waiters: make(map[string]chan toolResult),
		ready:   make(map[string]toolResult),
	}
}

// expect records a tool call the reply announced, in the order it appeared.
func (p *pendingTools) expect(id string) {
	if id == "" {
		return
	}
	p.mu.Lock()
	p.order = append(p.order, id)
	p.mu.Unlock()
}

// call blocks until Crush has a result for this tool call.
func (p *pendingTools) call(ctx context.Context, id string) (toolResult, error) {
	p.mu.Lock()
	if id == "" && len(p.order) > 0 {
		id = p.order[0]
		p.order = p.order[1:]
	}
	if id == "" {
		p.mu.Unlock()
		return toolResult{}, fmt.Errorf("tool call arrived without a tool_use id")
	}
	if result, ok := p.ready[id]; ok {
		delete(p.ready, id)
		p.mu.Unlock()
		return result, nil
	}
	ch := make(chan toolResult, 1)
	p.waiters[id] = ch
	p.mu.Unlock()

	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.waiters, id)
		p.mu.Unlock()
		return toolResult{}, ctx.Err()
	}
}

// resolve delivers a tool result, holding it when the call has not arrived yet.
func (p *pendingTools) resolve(id string, result toolResult) {
	if id == "" {
		return
	}
	p.mu.Lock()
	ch, waiting := p.waiters[id]
	if waiting {
		delete(p.waiters, id)
	} else {
		p.ready[id] = result
	}
	p.mu.Unlock()
	if waiting {
		ch <- result
	}
}

// failAll resolves every parked call with an error, so a stopped turn does not
// leave the CLI waiting forever.
func (p *pendingTools) failAll(message string) {
	p.mu.Lock()
	waiters := p.waiters
	p.waiters = make(map[string]chan toolResult)
	p.ready = make(map[string]toolResult)
	p.order = nil
	p.mu.Unlock()
	for _, ch := range waiters {
		select {
		case ch <- mcpContent(message, "", "", true):
		default:
		}
	}
}

// discard forgets a tool call that was answered without ever being parked, so
// it does not keep the session looking busy.
func (p *pendingTools) discard(id string) {
	if id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.ready, id)
	for i, existing := range p.order {
		if existing == id {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
}

// reset forgets everything parked. It is called when a new turn starts, since
// any leftover ids belong to a reply that will never be continued.
func (p *pendingTools) reset() {
	p.mu.Lock()
	p.order = nil
	p.mu.Unlock()
}

// ids returns the tool calls the CLI is parked on, in the order they were
// announced.
func (p *pendingTools) ids() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]bool, len(p.order)+len(p.waiters))
	ids := make([]string, 0, len(p.order)+len(p.waiters))
	for _, id := range p.order {
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for id := range p.waiters {
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// waiting reports whether the CLI is blocked on, or about to ask for, a tool
// result.
func (p *pendingTools) waiting() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.waiters) > 0 || len(p.ready) > 0 || len(p.order) > 0
}

// mcpServer answers the CLI's MCP JSON-RPC requests for Crush's tools.
type mcpServer struct {
	tools   []toolDef
	pending *pendingTools
}

// mcpRequest is one JSON-RPC request from the CLI.
type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      struct {
		ToolUseID string `json:"claudecode/toolUseId"`
	} `json:"_meta"`
}

// handle dispatches one MCP message. A notification returns a nil response.
func (s *mcpServer) handle(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req mcpRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decoding MCP message: %w", err)
	}
	if req.ID == nil {
		// Notifications (initialized, cancelled) need no answer.
		return nil, nil
	}
	result, mcpErr := s.dispatch(ctx, &req)
	if mcpErr != nil {
		body, _ := json.Marshal(mcpResponse{JSONRPC: "2.0", ID: req.ID, Error: mcpErr})
		return body, nil
	}
	body, err := json.Marshal(mcpResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (s *mcpServer) dispatch(ctx context.Context, req *mcpRequest) (any, *mcpError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := params.ProtocolVersion
		if version == "" {
			version = "2024-11-05"
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": mcpServerName, "version": "1.0.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		var params mcpToolsCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &mcpError{Code: -32602, Message: "invalid tools/call params"}
		}
		result, err := s.pending.call(ctx, params.Meta.ToolUseID)
		if err != nil {
			return nil, &mcpError{Code: -32603, Message: err.Error()}
		}
		return map[string]any{
			"content": result.content,
			"isError": result.isError,
		}, nil
	default:
		return nil, &mcpError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

var toolNameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// shortToolName fits a Crush tool name into MCP's 64 character limit while
// staying reversible through originalToolName.
func shortToolName(name string) string {
	limit := mcpMaxToolName - len(mcpToolPrefix)
	if len(name) <= limit && !toolNameUnsafe.MatchString(name) {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := hex.EncodeToString(sum[:])[:10]
	cleaned := toolNameUnsafe.ReplaceAllString(name, "_")
	if len(cleaned) > limit-11 {
		cleaned = cleaned[:limit-11]
	}
	return cleaned + "_" + suffix
}

// originalToolName maps an MCP tool name back to the Crush tool that serves it.
func originalToolName(mcpName string, names []string) (string, bool) {
	for _, name := range names {
		if mcpToolPrefix+shortToolName(name) == mcpName {
			return name, true
		}
	}
	return "", false
}

// truncateDescription cuts a description where Claude Code would. A subagent
// tool's description ends with its agent list, so that list is moved to the
// front to survive the cut.
func truncateDescription(name, description string) string {
	if len(description) <= mcpDescriptionLimit {
		return description
	}
	if name == "agent" || name == "task" {
		if at := strings.Index(description, "Available subagents:"); at >= 0 {
			rotated := description[at:] + "\n\n" + description[:at]
			if len(rotated) > mcpDescriptionLimit {
				return rotated[:mcpDescriptionLimit]
			}
			return rotated
		}
	}
	return description[:mcpDescriptionLimit]
}

// mcpContent converts a Crush tool response into MCP content blocks. data is
// already base64 encoded, which is how fantasy carries media tool results.
func mcpContent(text, data, mediaType string, isError bool) toolResult {
	content := make([]mcpContentBlock, 0, 2)
	if data != "" && mediaType != "" {
		content = append(content, mcpContentBlock{Type: "image", Data: data, MimeType: mediaType})
	}
	if text != "" || len(content) == 0 {
		content = append(content, mcpContentBlock{Type: "text", Text: text})
	}
	return toolResult{content: content, isError: isError}
}
