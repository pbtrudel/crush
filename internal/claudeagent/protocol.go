// Package claudeagent implements a fantasy provider that runs Claude models
// through the user's own `claude` CLI and Claude subscription.
//
// The CLI is driven the same way the Claude Agent SDK drives it: one
// long-running `claude` process per session, speaking NDJSON over stdio with a
// control protocol layered on top. Crush's tools are exposed to Claude as an
// in-process MCP server, so every tool call runs through Crush's normal
// execution and permission path.
package claudeagent

import "encoding/json"

// Message types on the CLI's stdout stream.
const (
	msgUser            = "user"
	msgAssistant       = "assistant"
	msgResult          = "result"
	msgSystem          = "system"
	msgStreamEvent     = "stream_event"
	msgRateLimitEvent  = "rate_limit_event"
	msgControlRequest  = "control_request"
	msgControlResponse = "control_response"
)

// Control request subtypes the CLI sends to us.
const (
	ctrlCanUseTool   = "can_use_tool"
	ctrlHookCallback = "hook_callback"
	ctrlMCPMessage   = "mcp_message"
)

// Control request subtypes we send to the CLI.
const (
	ctrlInitialize = "initialize"
	ctrlInterrupt  = "interrupt"
	ctrlSetModel   = "set_model"
)

// envelope is one NDJSON line from the CLI. Payloads are decoded lazily
// because the shape depends on Type.
type envelope struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	UUID      string          `json:"uuid"`
	SessionID string          `json:"session_id"`
	Event     json.RawMessage `json:"event"`
	Message   json.RawMessage `json:"message"`
	Result    json.RawMessage `json:"result"`

	// Stream routing.
	ParentToolUseID *string `json:"parent_tool_use_id"`

	// Error is set on an assistant message the CLI could not complete, for
	// example "rate_limit" or "billing_error".
	Error string `json:"error"`

	// control_response
	Response *controlResponseBody `json:"response"`

	// control_request
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`

	// result
	NumTurns       int             `json:"num_turns"`
	IsError        bool            `json:"is_error"`
	StopReason     string          `json:"stop_reason"`
	APIErrorStatus *int            `json:"api_error_status"`
	Errors         []string        `json:"errors"`
	Usage          *wireUsage      `json:"usage"`
	ModelUsage     json.RawMessage `json:"modelUsage"`
	TotalCostUSD   float64         `json:"total_cost_usd"`

	// system/init
	MCPServers []wireMCPServer `json:"mcp_servers"`
	Model      string          `json:"model"`
	Tools      []string        `json:"tools"`

	// rate_limit_event
	RateLimitInfo *wireRateLimit `json:"rate_limit_info"`
}

// controlResponseBody is the CLI's answer to one of our control requests.
type controlResponseBody struct {
	Subtype   string          `json:"subtype"`
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response"`
	Error     string          `json:"error"`
}

// controlRequestBody is the CLI's request to us.
type controlRequestBody struct {
	Subtype    string          `json:"subtype"`
	ToolName   string          `json:"tool_name"`
	ToolUseID  string          `json:"tool_use_id"`
	ServerName string          `json:"server_name"`
	CallbackID string          `json:"callback_id"`
	Message    json.RawMessage `json:"message"`
	Input      json.RawMessage `json:"input"`
}

// wireUsage is Anthropic's usage block as the CLI reports it. The CLI repeats
// a response's usage several times while streaming, so callers take the
// maximum per response rather than summing.
type wireUsage struct {
	InputTokens              *int64             `json:"input_tokens"`
	OutputTokens             *int64             `json:"output_tokens"`
	CacheReadInputTokens     *int64             `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64             `json:"cache_creation_input_tokens"`
	OutputTokensDetails      *outputTokenDetail `json:"output_tokens_details"`
}

type outputTokenDetail struct {
	ThinkingTokens *int64 `json:"thinking_tokens"`
}

// wireMCPServer is one entry of the init message's server list.
type wireMCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Source string `json:"source"`
}

// wireRateLimit is the plan-usage snapshot the CLI emits per response.
type wireRateLimit struct {
	Status        string `json:"status"`
	ResetsAt      int64  `json:"resetsAt"`
	RateLimitType string `json:"rateLimitType"`
	IsUsingOver   bool   `json:"isUsingOverage"`
}

// wireModelUsage is one entry of a result message's per-model usage map.
type wireModelUsage struct {
	InputTokens              int64  `json:"inputTokens"`
	OutputTokens             int64  `json:"outputTokens"`
	CacheReadInputTokens     int64  `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64  `json:"cacheCreationInputTokens"`
	ContextWindow            int64  `json:"contextWindow"`
	MaxOutputTokens          int64  `json:"maxOutputTokens"`
	CanonicalModel           string `json:"canonicalModel"`
}

// assistantPayload is the body of a type=assistant message.
type assistantPayload struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Content []ContentBlock `json:"content"`
	Usage   *wireUsage     `json:"usage"`
}

// userPayload is the body of a type=user message. The CLI echoes tool results
// back on the stream with this type.
type userPayload struct {
	Content []ContentBlock `json:"content"`
}

// streamEvent is one Anthropic streaming event forwarded by the CLI.
type streamEvent struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	Message      json.RawMessage `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        json.RawMessage `json:"delta"`
	Usage        *wireUsage      `json:"usage"`
}

// streamMessageStart is the subset of message_start we read.
type streamMessageStart struct {
	ID    string     `json:"id"`
	Model string     `json:"model"`
	Usage *wireUsage `json:"usage"`
}

// streamContentBlock is the subset of content_block_start we read.
type streamContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
}

// streamDelta is the subset of content_block_delta we read.
type streamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
}

// ContentBlock is one block of a Claude message, in the shape the CLI accepts
// on stdin and reports on stdout.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Source    *ContentSource  `json:"source,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   []ContentBlock  `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// ContentSource is the source of an image or document block.
type ContentSource struct {
	Type      string `json:"type"` // base64 or url
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// userMessage is a message we write to the CLI's stdin.
type userMessage struct {
	Type            string   `json:"type"`
	UUID            string   `json:"uuid"`
	ParentToolUseID *string  `json:"parent_tool_use_id"`
	SessionID       string   `json:"session_id,omitempty"`
	Message         wireBody `json:"message"`
	// ClientComposed tells the CLI to deliver the message exactly as written,
	// without expanding slash commands or injecting per-turn context.
	ClientComposed bool `json:"client_composed,omitempty"`
	// Priority "next" steers an in-flight turn instead of starting a new one.
	Priority string `json:"priority,omitempty"`
}

type wireBody struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// controlRequest is a control request we write to the CLI.
type controlRequest struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Request   any    `json:"request"`
}

// controlResponse is our answer to a CLI control request.
type controlResponse struct {
	Type     string                 `json:"type"`
	Response controlResponsePayload `json:"response"`
}

type controlResponsePayload struct {
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Response  any    `json:"response,omitempty"`
	Error     string `json:"error,omitempty"`
}

// initializeRequest registers our hooks with the CLI. Hooks are how the CLI
// tells us which tool_use_id an MCP tools/call belongs to.
type initializeRequest struct {
	Subtype string                   `json:"subtype"`
	Hooks   map[string][]hookMatcher `json:"hooks"`
}

type hookMatcher struct {
	Matcher         *string  `json:"matcher"`
	HookCallbackIDs []string `json:"hookCallbackIds"`
}

// initializeResult is the subset of the CLI's initialize answer we use.
type initializeResult struct {
	ClaudeCodeVersion string      `json:"claude_code_version"`
	Account           accountInfo `json:"account"`
	Models            []modelInfo `json:"models"`
	CurrentPermission string      `json:"current_permission_mode"`
}

// accountInfo describes the login the CLI is running as.
type accountInfo struct {
	Email            string `json:"email"`
	Organization     string `json:"organization"`
	SubscriptionType string `json:"subscriptionType"`
	APIProvider      string `json:"apiProvider"`
	TokenSource      string `json:"tokenSource"`
	APIKeySource     string `json:"apiKeySource"`
}

// modelInfo is one model the user's login can run.
type modelInfo struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
	SupportsEffort        bool     `json:"supportsEffort"`
}
