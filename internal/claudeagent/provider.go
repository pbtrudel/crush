package claudeagent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
)

// Name is the provider ID. It matches the catalog entry Crush registers.
const Name = "claude-agent"

// DisplayName is what the model picker shows.
const DisplayName = "Claude Agent"

// sessionHeader is the header Crush attaches to every request for a session.
// Its value is a hash, which is what makes it safe to use as a file name.
const sessionHeader = "x-session-id"

// errConsumerStopped is returned when the consumer of a stream stops early.
var errConsumerStopped = errors.New("stream consumer stopped")

var (
	managerMu sync.Mutex
	managers  = map[string]*Manager{}
)

// DefaultManager returns the process manager for a data directory. One manager
// per process is enough: it holds every session's Claude process.
func DefaultManager(dataDir string) *Manager {
	managerMu.Lock()
	defer managerMu.Unlock()
	dir := filepath.Join(dataDir, "claude-agent")
	if manager, ok := managers[dir]; ok {
		return manager
	}
	manager := NewManager(dir)
	managers[dir] = manager
	return manager
}

// Provider is a fantasy provider backed by the user's Claude subscription.
type Provider struct {
	manager    *Manager
	dir        string
	effort     string
	connection Connection
	// now supplies the date line of the chat system prompt. It is a field so
	// tests can pin it.
	now func() time.Time
}

// NewProvider creates a provider. dir is the working directory Claude runs in,
// effort is the reasoning effort to pass to the CLI, and connection carries the
// user's provider settings.
func NewProvider(manager *Manager, dir, effort string, connection Connection) *Provider {
	return &Provider{manager: manager, dir: dir, effort: effort, connection: connection, now: time.Now}
}

// Name implements fantasy.Provider.
func (p *Provider) Name() string { return Name }

// LanguageModel implements fantasy.Provider.
func (p *Provider) LanguageModel(_ context.Context, modelID string) (fantasy.LanguageModel, error) {
	return &Model{provider: p, modelID: modelID}, nil
}

// Model is one Claude model served through the CLI.
type Model struct {
	provider *Provider
	modelID  string
}

// Provider implements fantasy.LanguageModel.
func (m *Model) Provider() string { return Name }

// Model implements fantasy.LanguageModel.
func (m *Model) Model() string { return m.modelID }

// Stream implements fantasy.LanguageModel.
func (m *Model) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	req, err := m.buildRequest(call)
	if err != nil {
		return nil, err
	}
	return func(yield func(fantasy.StreamPart) bool) {
		err := m.provider.manager.Stream(ctx, req, func(part fantasy.StreamPart) error {
			if !yield(part) {
				return errConsumerStopped
			}
			return nil
		})
		if err != nil && !errors.Is(err, errConsumerStopped) {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: err})
		}
	}, nil
}

// Generate implements fantasy.LanguageModel by draining a stream.
func (m *Model) Generate(ctx context.Context, call fantasy.Call) (*fantasy.Response, error) {
	stream, err := m.Stream(ctx, call)
	if err != nil {
		return nil, err
	}
	var (
		response fantasy.Response
		text     strings.Builder
	)
	for part := range stream {
		switch part.Type {
		case fantasy.StreamPartTypeTextDelta:
			text.WriteString(part.Delta)
		case fantasy.StreamPartTypeToolCall:
			response.Content = append(response.Content, fantasy.ToolCallContent{
				ToolCallID: part.ID,
				ToolName:   part.ToolCallName,
				Input:      part.ToolCallInput,
			})
		case fantasy.StreamPartTypeFinish:
			response.FinishReason = part.FinishReason
			response.Usage = part.Usage
		case fantasy.StreamPartTypeError:
			return nil, part.Error
		}
	}
	if text.Len() > 0 {
		response.Content = append(fantasy.ResponseContent{
			fantasy.TextContent{Text: text.String()},
		}, response.Content...)
	}
	return &response, nil
}

// GenerateObject is not supported: Claude Agent models only stream text.
func (m *Model) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("claude-agent models do not support object generation")
}

// StreamObject is not supported: Claude Agent models only stream text.
func (m *Model) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("claude-agent models do not support object generation")
}

// buildRequest turns a fantasy call into what the CLI needs.
func (m *Model) buildRequest(call fantasy.Call) (turnRequest, error) {
	system := systemPrompt(call.Prompt)
	req := turnRequest{
		Dir:        m.provider.dir,
		Model:      m.modelID,
		Effort:     m.provider.effort,
		Connection: m.provider.connection,
		Prompt:     call.Prompt,
	}

	tools := call.Tools
	if call.ToolChoice != nil && *call.ToolChoice == fantasy.ToolChoiceNone {
		tools = nil
	}

	if len(tools) == 0 {
		// A request without tools is a standalone task such as a title or a
		// compaction summary. It gets its own system prompt and its own
		// throwaway process, and never touches the chat's transcript.
		req.SystemPrompt = system
		if call.MaxOutputTokens != nil {
			req.MaxOutputTokens = *call.MaxOutputTokens
		}
		return req, nil
	}

	req.SessionKey = call.Headers[sessionHeader]
	req.SystemPrompt = chatSystemPrompt(system, m.provider.now())
	req.Tools, req.ToolNames = toolDefs(tools)
	return req, nil
}

// chatSystemPrompt builds the system prompt of a chat turn. It replaces
// Claude Code's own prompt, so today's date has to come from here: the date
// changes the prompt once a day, exactly as Claude Code's own prompt did. The
// model name is deliberately absent, because the process switches models in
// place (restartKey excludes the model) and a model name in the prompt would
// force a restart on every switch.
func chatSystemPrompt(system string, now time.Time) string {
	return system + "\n\nToday's date: " + now.Format("2006-01-02")
}

// systemPrompt joins every system message in a prompt.
func systemPrompt(prompt fantasy.Prompt) string {
	var parts []string
	for _, message := range prompt {
		if message.Role != fantasy.MessageRoleSystem {
			continue
		}
		for _, part := range message.Content {
			if text, ok := fantasy.AsContentType[fantasy.TextPart](part); ok && text.Text != "" {
				parts = append(parts, text.Text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// toolDefs converts fantasy tools into MCP tool definitions. The result is
// sorted by name so the tool list is byte-identical between runs, which is what
// keeps Claude's prompt cache warm.
func toolDefs(tools []fantasy.Tool) ([]toolDef, []string) {
	defs := make([]toolDef, 0, len(tools))
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		function, ok := tool.(fantasy.FunctionTool)
		if !ok {
			continue
		}
		names = append(names, function.Name)
		defs = append(defs, toolDef{
			Name:        shortToolName(function.Name),
			Description: truncateDescription(function.Name, function.Description),
			InputSchema: function.InputSchema,
		})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	sort.Strings(names)
	return defs, names
}
