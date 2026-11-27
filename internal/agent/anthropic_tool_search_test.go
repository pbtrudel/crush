package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
)

// toolSearchTestBody is a request shaped the way fantasy sends it: the tools
// block first, the core palette and the deferred tail in one array, and the
// cache breakpoint on the last tool (which the palette makes a deferred one).
const toolSearchTestBody = `{
  "model": "claude-sonnet-4-5",
  "max_tokens": 2048,
  "tools": [
    {"name": "view", "description": "view a file", "input_schema": {"type": "object"}},
    {"name": "bash", "description": "run a command", "input_schema": {"type": "object"}},
    {"name": "mcp__srv__alpha", "description": "alpha", "input_schema": {"type": "object"}},
    {"name": "mcp__srv__beta", "description": "beta", "input_schema": {"type": "object"}, "cache_control": {"type": "ephemeral"}}
  ],
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "hi"}]}
  ]
}`

// recordedRequest is one request a capturing base transport received.
type recordedRequest struct {
	body   []byte
	header http.Header
}

// recordingTransport records every request and answers with a canned response.
type recordingTransport struct {
	mu        sync.Mutex
	requests  []recordedRequest
	responder func(*http.Request) *http.Response
}

func (c *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	c.mu.Lock()
	c.requests = append(c.requests, recordedRequest{body: body, header: req.Header.Clone()})
	c.mu.Unlock()
	if c.responder != nil {
		return c.responder(req), nil
	}
	return jsonResponse(req, `{}`), nil
}

func (c *recordingTransport) last(t *testing.T) recordedRequest {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	require.NotEmpty(t, c.requests, "no request reached the base transport")
	return c.requests[len(c.requests)-1]
}

func jsonResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func sseResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func messagesRequest(t *testing.T, ctx context.Context, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	return req
}

// decodedTool is the part of a tool definition the tests assert on.
type decodedTool struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	CacheControl json.RawMessage `json:"cache_control"`
	DeferLoading *bool           `json:"defer_loading"`
}

type decodedBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type decodedMessage struct {
	Role    string          `json:"role"`
	Content []decodedBlock  `json:"content"`
	Raw     json.RawMessage `json:"-"`
}

func decodeRequestTools(t *testing.T, body []byte) []decodedTool {
	t.Helper()
	var request struct {
		Tools []decodedTool `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(body, &request), "body: %s", body)
	return request.Tools
}

func decodeRequestMessages(t *testing.T, body []byte) []decodedMessage {
	t.Helper()
	var request struct {
		Messages []decodedMessage `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &request), "body: %s", body)
	return request.Messages
}

func toolNamesOf(tools []decodedTool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

// TestAnthropicToolSearchOffKeepsRequestBytes pins the opt-in contract: a
// request without the active marker reaches the endpoint byte for byte as
// fantasy wrote it, with no header added.
func TestAnthropicToolSearchOffKeepsRequestBytes(t *testing.T) {
	t.Parallel()

	base := &recordingTransport{}
	transport := newAnthropicToolSearchTransport(base)

	resp, err := transport.RoundTrip(messagesRequest(t, t.Context(), toolSearchTestBody))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	got := base.last(t)
	require.Equal(t, toolSearchTestBody, string(got.body))
	require.Empty(t, got.header.Get("anthropic-beta"))
}

// TestAnthropicToolSearchActiveRequest pins the active request shape:
// exactly the non-core tools are deferred, the server tool appears once right
// after the last non-deferred tool, the cache breakpoint moved off the
// deferred tool, and the beta header is appended to whatever was there.
func TestAnthropicToolSearchActiveRequest(t *testing.T) {
	t.Parallel()

	base := &recordingTransport{}
	transport := newAnthropicToolSearchTransport(base)
	ctx := withAnthropicToolSearch(t.Context())

	req := messagesRequest(t, ctx, toolSearchTestBody)
	req.Header.Set("anthropic-beta", "context-management-2025-06-27")
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	got := base.last(t)
	tools := decodeRequestTools(t, got.body)
	require.Equal(t,
		[]string{"view", "bash", "tool_search_tool_bm25", "mcp__srv__alpha", "mcp__srv__beta"},
		toolNamesOf(tools))

	for _, tool := range tools {
		switch tool.Name {
		case "tool_search_tool_bm25":
			require.Equal(t, "tool_search_tool_bm25_20251119", tool.Type)
			require.Nil(t, tool.DeferLoading)
			require.Empty(t, tool.CacheControl)
		case "mcp__srv__alpha", "mcp__srv__beta":
			require.NotNil(t, tool.DeferLoading, "tool %s must be deferred", tool.Name)
			require.True(t, *tool.DeferLoading)
			require.Empty(t, tool.CacheControl, "a deferred tool cannot carry a breakpoint")
		default:
			require.Nil(t, tool.DeferLoading, "core tool %s must not be deferred", tool.Name)
		}
	}
	require.JSONEq(t, `{"type":"ephemeral"}`, string(tools[1].CacheControl),
		"the breakpoint belongs on the last non-deferred tool")

	require.Equal(t,
		"context-management-2025-06-27,advanced-tool-use-2025-11-20",
		got.header.Get("anthropic-beta"))
	require.Equal(t, 1, strings.Count(got.header.Get("anthropic-beta"), "advanced-tool-use-2025-11-20"))
}

// TestAnthropicToolSearchPalette pins the palette the mode sends: our own
// tool_search is gone, the core tools keep their order, and the deferred ones
// follow sorted by name.
func TestAnthropicToolSearchPalette(t *testing.T) {
	t.Parallel()

	palette := anthropicToolSearchPalette([]fantasy.AgentTool{
		&recordingTool{name: "view"},
		&recordingTool{name: "mcp__srv__beta"},
		&recordingTool{name: "tool_search"},
		&recordingTool{name: "bash"},
		&recordingTool{name: "mcp__srv__alpha"},
	})
	require.Equal(t,
		[]string{"view", "bash", "mcp__srv__alpha", "mcp__srv__beta"},
		toolNames(palette))
}

// TestAnthropicToolSearchNewToolChangesOnlyTail pins the point of the mode: a
// tool that connects later only extends the deferred tail, so the core prefix
// every request caches stays byte-identical.
func TestAnthropicToolSearchNewToolChangesOnlyTail(t *testing.T) {
	t.Parallel()

	before := anthropicToolSearchPalette([]fantasy.AgentTool{
		&recordingTool{name: "view"},
		&recordingTool{name: "bash"},
		&recordingTool{name: "mcp__srv__alpha"},
	})
	after := anthropicToolSearchPalette([]fantasy.AgentTool{
		&recordingTool{name: "view"},
		&recordingTool{name: "bash"},
		&recordingTool{name: "mcp__srv__alpha"},
		&recordingTool{name: "mcp__srv__gamma"},
	})

	require.Equal(t,
		[]string{"view", "bash", "mcp__srv__alpha"},
		toolNames(before))
	require.Equal(t,
		[]string{"view", "bash", "mcp__srv__alpha", "mcp__srv__gamma"},
		toolNames(after))
}

// toolSearchStream is an Anthropic SSE response carrying a server tool search:
// the bm25 call, its result block, and the client tool call the model makes
// with the tool the search loaded.
const toolSearchStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":100,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"tool_search_tool_bm25","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"alpha\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_search_tool_result","tool_use_id":"srvtoolu_1","content":{"type":"tool_search_tool_result","tool_references":[{"type":"tool_reference","tool_name":"mcp__srv__alpha"}]}}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__srv__alpha","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"x\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}`

// TestToolsForStepServerToolSearch pins the wiring: the step list is the
// full palette (no tool_search) and a tool that appears mid-turn is appended
// after the deferred tail, so the cached core prefix never moves.
func TestToolsForStepServerToolSearch(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "prompt",
		&recordingTool{name: "view"},
		&recordingTool{name: "tool_search"},
		&recordingTool{name: "mcp__srv__alpha"},
	).(*sessionAgent)

	turnTools := anthropicToolSearchPalette(agent.tools.Copy())
	unchanged, changed := agent.toolsForStep(t.Context(), "sess", "", turnTools, true)
	require.False(t, changed)
	require.Equal(t, []string{"view", "mcp__srv__alpha"}, toolNames(unchanged))

	agent.SetTools([]fantasy.AgentTool{
		&recordingTool{name: "view"},
		&recordingTool{name: "tool_search"},
		&recordingTool{name: "mcp__srv__alpha"},
		&recordingTool{name: "mcp__srv__gamma"},
	})
	appended, changed := agent.toolsForStep(t.Context(), "sess", "", unchanged, true)
	require.True(t, changed)
	require.Equal(t, []string{"view", "mcp__srv__alpha", "mcp__srv__gamma"}, toolNames(appended),
		"a tool that connects mid-turn belongs at the end of the deferred tail")
}

// TestAnthropicToolSearchCapturesAndReplaysResult pins the replay: the result
// block fantasy drops is captured from the stream and inserted after its
// server_tool_use block on the next request.
func TestAnthropicToolSearchCapturesAndReplaysResult(t *testing.T) {
	t.Parallel()

	base := &recordingTransport{responder: func(req *http.Request) *http.Response {
		return sseResponse(req, toolSearchStream)
	}}
	transport := newAnthropicToolSearchTransport(base)
	ctx := withAnthropicToolSearch(t.Context())

	resp, err := transport.RoundTrip(messagesRequest(t, ctx, toolSearchTestBody))
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	replay := `{
	  "model": "claude-sonnet-4-5",
	  "tools": [
	    {"name": "view", "input_schema": {"type": "object"}},
	    {"name": "mcp__srv__alpha", "input_schema": {"type": "object"}}
	  ],
	  "messages": [
	    {"role": "user", "content": [{"type": "text", "text": "hi"}]},
	    {"role": "assistant", "content": [
	      {"type": "server_tool_use", "id": "srvtoolu_1", "name": "tool_search_tool_bm25", "input": {"query": "alpha"}},
	      {"type": "tool_use", "id": "toolu_1", "name": "mcp__srv__alpha", "input": {"q": "x"}}
	    ]},
	    {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "ok"}]}
	  ]
	}`
	resp, err = transport.RoundTrip(messagesRequest(t, ctx, replay))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	messages := decodeRequestMessages(t, base.last(t).body)
	require.Len(t, messages[1].Content, 3, "the result block belongs between the call and the tool_use")
	require.Equal(t, "server_tool_use", messages[1].Content[0].Type)
	require.Equal(t, "tool_search_tool_result", messages[1].Content[1].Type)
	require.Equal(t, "srvtoolu_1", messages[1].Content[1].ToolUseID)
	require.Contains(t, string(messages[1].Content[1].Content), "mcp__srv__alpha")
	require.Equal(t, "tool_use", messages[1].Content[2].Type)

	tools := decodeRequestTools(t, base.last(t).body)
	for _, tool := range tools {
		if tool.Name == "mcp__srv__alpha" {
			require.NotNil(t, tool.DeferLoading, "a replayed search result keeps the tool deferred")
		}
	}
}

// TestAnthropicToolSearchDropsOrphanedServerToolUse pins the restart case: a
// server_tool_use with no captured result is removed, and the deferred tools
// the history has already used are sent loaded so the request stays valid.
func TestAnthropicToolSearchDropsOrphanedServerToolUse(t *testing.T) {
	t.Parallel()

	base := &recordingTransport{}
	transport := newAnthropicToolSearchTransport(base)
	ctx := withAnthropicToolSearch(t.Context())

	body := `{
	  "model": "claude-sonnet-4-5",
	  "tools": [
	    {"name": "view", "input_schema": {"type": "object"}},
	    {"name": "mcp__srv__alpha", "input_schema": {"type": "object"}},
	    {"name": "mcp__srv__beta", "input_schema": {"type": "object"}}
	  ],
	  "messages": [
	    {"role": "user", "content": [{"type": "text", "text": "hi"}]},
	    {"role": "assistant", "content": [
	      {"type": "server_tool_use", "id": "srvtoolu_gone", "name": "tool_search_tool_bm25", "input": {"query": "alpha"}},
	      {"type": "tool_use", "id": "toolu_1", "name": "mcp__srv__alpha", "input": {"q": "x"}}
	    ]},
	    {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "ok"}]}
	  ]
	}`
	resp, err := transport.RoundTrip(messagesRequest(t, ctx, body))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	sent := base.last(t)
	messages := decodeRequestMessages(t, sent.body)
	require.Len(t, messages[1].Content, 1, "the orphaned server_tool_use is removed")
	require.Equal(t, "tool_use", messages[1].Content[0].Type)

	tools := decodeRequestTools(t, sent.body)
	deferrals := make(map[string]bool, len(tools))
	for _, tool := range tools {
		deferrals[tool.Name] = tool.DeferLoading != nil && *tool.DeferLoading
	}
	require.False(t, deferrals["mcp__srv__alpha"], "a deferred tool the history used must be sent loaded")
	require.True(t, deferrals["mcp__srv__beta"], "an unused deferred tool stays deferred")
}

// TestAnthropicToolSearchActive covers the opt-in gate: the option, lazy
// tools, the serving provider, and the direct Anthropic host all have to line
// up.
func TestAnthropicToolSearchActive(t *testing.T) {
	t.Parallel()

	on, off := true, false
	opts := func(toolSearch, lazy bool) *config.Options {
		return &config.Options{AnthropicToolSearch: &toolSearch, LazyTools: &lazy}
	}

	tests := []struct {
		name    string
		options *config.Options
		serving string
		baseURL string
		want    bool
	}{
		{"option on, lazy on, anthropic host", opts(on, on), anthropic.Name, "https://api.anthropic.com", true},
		{"option on, lazy on, default host", opts(on, on), anthropic.Name, "", true},
		{"option off", opts(off, on), anthropic.Name, "https://api.anthropic.com", false},
		{"lazy off", opts(on, off), anthropic.Name, "https://api.anthropic.com", false},
		{"nil options", nil, anthropic.Name, "https://api.anthropic.com", false},
		{"bedrock", opts(on, on), "bedrock", "https://bedrock-runtime.us-east-1.amazonaws.com", false},
		{"vertex", opts(on, on), "google", "https://us-central1-aiplatform.googleapis.com", false},
		{"openrouter", opts(on, on), "openrouter", "https://openrouter.ai/api", false},
		{"copilot host", opts(on, on), anthropic.Name, "https://api.githubcopilot.com", false},
		{"minimax host", opts(on, on), anthropic.Name, "https://api.minimax.io", false},
		{"proxy host", opts(on, on), anthropic.Name, "https://anthropic-proxy.internal", false},
		{"unparseable base url", opts(on, on), anthropic.Name, "://nope", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, anthropicToolSearchActive(tt.options, tt.serving, tt.baseURL))
		})
	}

	require.True(t, opts(on, off).GetAnthropicToolSearch())
	require.False(t, opts(off, on).GetAnthropicToolSearch())
}

// TestToolResultsForCallsSkipsProviderExecuted pins that Crush never invents
// a reply for a call the provider already ran.
func TestToolResultsForCallsSkipsProviderExecuted(t *testing.T) {
	t.Parallel()

	assistant := message.Message{
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.ToolCall{ID: "srvtoolu_1", Name: "tool_search_tool_bm25", ProviderExecuted: true},
			message.ToolCall{ID: "toolu_1", Name: "view"},
		},
	}
	byCall := map[string]fantasy.ToolResultPart{
		"toolu_1": {ToolCallID: "toolu_1", Output: fantasy.ToolResultOutputContentText{Text: "ok"}},
	}
	got := toolResultsForCalls(assistant, byCall)
	require.Len(t, got.Content, 1, "only the client tool gets a tool_result")
	part, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](got.Content[0])
	require.True(t, ok)
	require.Equal(t, "toolu_1", part.ToolCallID)
}

// TestSelectCompactionTailIgnoresProviderExecuted pins that a server-run call
// cannot pin the tail: it never gets a result, so counting it would keep the
// whole history out of the verbatim tail forever.
func TestSelectCompactionTailIgnoresProviderExecuted(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{Role: message.Assistant, Parts: []message.ContentPart{
			message.ToolCall{ID: "srvtoolu_1", Name: "tool_search_tool_bm25", ProviderExecuted: true},
		}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hello"}}},
	}
	tail := selectCompactionTail(msgs, messageTokens(msgs[1]))
	require.Equal(t, 1, tail, "the tail starts at the user message, not past the end")
}

// TestAnthropicToolSearchStreamThroughFantasy proves a stream carrying the
// server tool blocks reaches fantasy as a provider-executed call and the
// client tool call, without an error, and that the result was captured.
func TestAnthropicToolSearchStreamThroughFantasy(t *testing.T) {
	t.Parallel()

	var (
		receivedBody   []byte
		receivedHeader http.Header
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		receivedHeader = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, toolSearchStream+"\n\n")
	}))
	t.Cleanup(srv.Close)

	transport := newAnthropicToolSearchTransport(http.DefaultTransport)
	provider, err := anthropic.New(
		anthropic.WithBaseURL(srv.URL),
		anthropic.WithAPIKey("x"),
		anthropic.WithHTTPClient(&http.Client{Transport: transport}),
	)
	require.NoError(t, err)
	lm, err := provider.LanguageModel(t.Context(), "claude-sonnet-4-5")
	require.NoError(t, err)

	ctx := withAnthropicToolSearch(t.Context())
	stream, err := lm.Stream(ctx, fantasy.Call{
		Prompt: fantasy.Prompt{
			{Role: fantasy.MessageRoleUser, Content: []fantasy.MessagePart{fantasy.TextPart{Text: "find a tool"}}},
		},
		Tools: []fantasy.Tool{
			fantasy.FunctionTool{Name: "view", Description: "view a file", InputSchema: map[string]any{"type": "object"}},
			fantasy.FunctionTool{Name: "mcp__srv__alpha", Description: "alpha", InputSchema: map[string]any{"type": "object"}},
		},
	})
	require.NoError(t, err)

	var calls []fantasy.ToolCallContent
	for part := range stream {
		switch part.Type {
		case fantasy.StreamPartTypeToolCall:
			calls = append(calls, fantasy.ToolCallContent{
				ToolCallID:       part.ID,
				ToolName:         part.ToolCallName,
				Input:            part.ToolCallInput,
				ProviderExecuted: part.ProviderExecuted,
			})
		case fantasy.StreamPartTypeError:
			t.Fatalf("stream error: %s", fmt.Sprint(part.Error))
		}
	}
	require.Len(t, calls, 2, "calls: %#v", calls)
	require.Equal(t, "tool_search_tool_bm25", calls[0].ToolName)
	require.True(t, calls[0].ProviderExecuted)
	require.Equal(t, "mcp__srv__alpha", calls[1].ToolName)
	require.False(t, calls[1].ProviderExecuted)

	// The marker on the context has to survive fantasy's step preparation and
	// reach the provider's transport, or nothing above would have been
	// rewritten.
	tools := decodeRequestTools(t, receivedBody)
	require.Equal(t,
		[]string{"view", "tool_search_tool_bm25", "mcp__srv__alpha"},
		toolNamesOf(tools))
	require.NotNil(t, tools[2].DeferLoading)
	require.True(t, *tools[2].DeferLoading)
	require.Contains(t, receivedHeader.Get("anthropic-beta"), anthropicToolSearchBeta)
}
