package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// lazyTestCoordinator builds a coordinator whose tool palette is hermetic
// against the developer's own config, the same way the prompt-cache tests do.
func lazyTestCoordinator(t *testing.T, lazy bool) *coordinator {
	t.Helper()
	env := testEnv(t)
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().Options.Attribution = &config.Attribution{}
	cfg.Config().Options.SkillsPaths = nil
	cfg.Config().Options.DisabledSkills = nil
	cfg.Config().Options.ContextPaths = nil
	cfg.Config().Options.GlobalContextPaths = nil
	cfg.Config().LSP = nil
	cfg.Config().MCP = nil
	cfg.Config().Hooks = nil
	cfg.Config().Options.LazyTools = ptr(lazy)
	return &coordinator{
		cfg:         cfg,
		permissions: env.permissions,
		history:     env.history,
		filetracker: *env.filetracker,
	}
}

// lazyTestAgent is the tool list a test agent is allowed to have: core
// palette members mixed with tools lazy mode holds back.
func lazyTestAgent() config.Agent {
	return config.Agent{
		Name: "test",
		AllowedTools: []string{
			tools.BashToolName,
			tools.DiagnosticsToolName,
			tools.EditToolName,
			tools.FetchToolName,
			tools.GlobToolName,
			tools.GrepToolName,
			tools.LSToolName,
			tools.MultiEditToolName,
			tools.SourcegraphToolName,
			tools.SymbolsToolName,
			tools.TodosToolName,
			tools.ViewToolName,
			tools.WriteToolName,
		},
	}
}

func buildLazyPalette(t *testing.T, c *coordinator) []fantasy.AgentTool {
	t.Helper()
	built, err := c.buildTools(t.Context(), lazyTestAgent(), roleLead, func() Model { return c.mainModels().Large })
	require.NoError(t, err)
	require.NotEmpty(t, built)
	return built
}

func toolListNames(list []fantasy.AgentTool) []string {
	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Info().Name)
	}
	return names
}

// TestLazyToolsReduceThePalette is the core promise of the option: with
// lazy_tools on, a request carries the core palette plus tool_search and
// nothing else, and the deferred schemas are not serialized at all.
func TestLazyToolsReduceThePalette(t *testing.T) {
	c := lazyTestCoordinator(t, true)
	built := buildLazyPalette(t, c)

	require.Contains(t, toolListNames(built), tools.ToolSearchToolName,
		"lazy mode must send the tool that reveals the rest")

	agent := NewSessionAgent(SessionAgentOptions{
		Cfg:       c.cfg,
		Tools:     built,
		LazyTools: c.lazyRegistryIfEnabled,
	}).(*sessionAgent)
	require.NotNil(t, agent.lazyTools())

	filtered := agent.lazyToolsForSession("session-1", built)

	require.Less(t, len(filtered), len(built), "lazy mode must send fewer tool schemas")
	require.Equal(t, []string{
		tools.BashToolName,
		tools.EditToolName,
		tools.GlobToolName,
		tools.GrepToolName,
		tools.LSToolName,
		tools.TodosToolName,
		tools.ToolSearchToolName,
		tools.ViewToolName,
	}, toolListNames(filtered),
		"the request carries the core palette and tool_search, and nothing deferred")
}

// TestLazyToolsOffChangesNothing pins the byte-identical guarantee: with the
// option off, no registry exists, no tool_search is built, and the palette is
// exactly the one that was built.
func TestLazyToolsOffChangesNothing(t *testing.T) {
	c := lazyTestCoordinator(t, false)
	built := buildLazyPalette(t, c)

	require.NotContains(t, toolListNames(built), tools.ToolSearchToolName,
		"tool_search costs tokens, so it is only registered in lazy mode")
	require.Nil(t, c.lazyRegistryIfEnabled())

	agent := NewSessionAgent(SessionAgentOptions{Cfg: c.cfg, Tools: built}).(*sessionAgent)
	filtered := agent.lazyToolsForSession("session-1", built)

	require.Equal(t, toolListNames(built), toolListNames(filtered))
	require.True(t, slices.Equal(built, filtered), "the same tools must come back, in the same order")
}

// TestToolSearchUnlocksTheRealTool walks the whole way: a deferred tool is
// absent from the request, tool_search reveals it, and the following request
// carries the real tool instance, which runs.
func TestToolSearchUnlocksTheRealTool(t *testing.T) {
	c := lazyTestCoordinator(t, true)
	built := buildLazyPalette(t, c)

	agent := NewSessionAgent(SessionAgentOptions{
		Cfg:       c.cfg,
		Tools:     built,
		LazyTools: c.lazyRegistryIfEnabled,
	}).(*sessionAgent)

	before := toolListNames(agent.lazyToolsForSession("session-1", built))
	require.NotContains(t, before, tools.DiagnosticsToolName)

	search := findTool(t, built, tools.ToolSearchToolName)
	response, err := search.Run(
		context.WithValue(t.Context(), tools.SessionIDContextKey, "session-1"),
		fantasy.ToolCall{Input: `{"query":"diagnostics"}`},
	)
	require.NoError(t, err)
	require.False(t, response.IsError)
	require.Contains(t, response.Content, tools.DiagnosticsToolName)

	after := agent.lazyToolsForSession("session-1", built)
	require.Contains(t, toolListNames(after), tools.DiagnosticsToolName,
		"the revealed tool must join the next request")

	unlocked := findTool(t, after, tools.DiagnosticsToolName)
	require.Equal(t, tools.DiagnosticsToolName, unlocked.Info().Name)
	require.NotEmpty(t, unlocked.Info().Description)

	// The reveal belongs to the session that asked: another session still
	// does not see the tool, and an unrevealed call would be answered with
	// "tool not found" by the agent loop rather than executed.
	require.NotContains(t, toolListNames(agent.lazyToolsForSession("session-2", built)), tools.DiagnosticsToolName)
}

// TestToolSearchSchemaMatchesTheSentTool keeps the revealed schema honest: it
// must be the one the request would serialize, so the model's arguments parse
// on the next step.
func TestToolSearchSchemaMatchesTheSentTool(t *testing.T) {
	c := lazyTestCoordinator(t, true)
	built := buildLazyPalette(t, c)

	search := findTool(t, built, tools.ToolSearchToolName)
	response, err := search.Run(
		context.WithValue(t.Context(), tools.SessionIDContextKey, "session-1"),
		fantasy.ToolCall{Input: `{"query":"lsp_symbols"}`},
	)
	require.NoError(t, err)
	require.False(t, response.IsError)

	_, schema, found := strings.Cut(response.Content, "  schema: ")
	require.True(t, found, "the reply must carry the tool's schema")

	var got fantasy.ToolInfo
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(schema)), &got))
	require.Equal(t, tools.SymbolsToolName, got.Name)
	require.NotEmpty(t, got.Parameters, "a tool schema without parameters is useless to the model")
}

func findTool(t *testing.T, list []fantasy.AgentTool, name string) fantasy.AgentTool {
	t.Helper()
	for _, tool := range list {
		if tool.Info().Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found in %v", name, toolListNames(list))
	return nil
}

// lazyRunModel scripts one turn: the first tool-carrying request calls
// tool_search, every later one answers with text. It records the tool names
// each request carried, which is the only observable that matters for the
// token cost. Requests without a tool block are title generation, which runs
// on the same fake model and is not what this test is about.
type lazyRunModel struct {
	mu    sync.Mutex
	steps [][]string
}

func (m *lazyRunModel) Provider() string { return "fake" }
func (m *lazyRunModel) Model() string    { return "fake-model" }

func (m *lazyRunModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("not implemented")
}

func (m *lazyRunModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if len(call.Tools) == 0 {
		return func(yield func(fantasy.StreamPart) bool) {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
		}, nil
	}

	m.mu.Lock()
	step := len(m.steps)
	names := make([]string, 0, len(call.Tools))
	for _, tool := range call.Tools {
		names = append(names, tool.GetName())
	}
	m.steps = append(m.steps, names)
	m.mu.Unlock()

	return func(yield func(fantasy.StreamPart) bool) {
		if step == 0 {
			if !yield(fantasy.StreamPart{
				Type:          fantasy.StreamPartTypeToolCall,
				ID:            "search-1",
				ToolCallName:  tools.ToolSearchToolName,
				ToolCallInput: `{"query":"lsp_symbols"}`,
			}) {
				return
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls})
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "t1"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "t1", Delta: "done"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "t1"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *lazyRunModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *lazyRunModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *lazyRunModel) recordedSteps() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.steps)
}

// TestLazyToolsThroughTheRequestPath is the end-to-end proof: the request the
// provider receives carries the core palette, the reveal the model made in the
// first step puts the deferred schema into the next one, and the tool it
// revealed is callable there.
func TestLazyToolsThroughTheRequestPath(t *testing.T) {
	env := testEnv(t)
	c := lazyTestCoordinator(t, true)
	built := buildLazyPalette(t, c)

	model := &lazyRunModel{}
	sa := testSessionAgent(env, model, model, "system", built...).(*sessionAgent)
	sa.cfg = c.cfg
	sa.lazyTools = c.lazyRegistryIfEnabled

	sess, err := env.sessions.Create(t.Context(), "lazy")
	require.NoError(t, err)
	_, err = sa.Run(t.Context(), SessionAgentCall{Prompt: "go", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)

	steps := model.recordedSteps()
	require.Len(t, steps, 2, "one request for the search, one to answer")

	require.Contains(t, steps[0], tools.ToolSearchToolName)
	require.Contains(t, steps[0], tools.BashToolName)
	require.NotContains(t, steps[0], tools.SymbolsToolName,
		"a deferred schema must not be sent on a request that has not revealed it")
	require.Less(t, len(steps[0]), len(built), "the first request must be smaller than the built palette")

	require.Contains(t, steps[1], tools.SymbolsToolName,
		"the tool revealed in the previous step must be callable in the next request")
	require.Len(t, steps[1], len(steps[0])+1, "the reveal adds exactly the tool that was asked for")
}

// namedTool is a tool that exists only to be named.
type namedTool struct{ name string }

func (t namedTool) Info() fantasy.ToolInfo                     { return fantasy.ToolInfo{Name: t.name} }
func (t namedTool) ProviderOptions() fantasy.ProviderOptions   { return nil }
func (t namedTool) SetProviderOptions(fantasy.ProviderOptions) {}
func (t namedTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return fantasy.NewTextResponse(t.name), nil
}

// mcpNamedTool is an MCP-backed tool, which lazy mode must never defer.
type mcpNamedTool struct{ namedTool }

func (t mcpNamedTool) MCP() string { return "server" }

// TestLazyPaletteKeepsDelegationAndMCPTools pins what the core palette is for:
// a Lead can always delegate, teammates can always talk, and an MCP server's
// tools are the server's business (mcp_servers owns activating them).
func TestLazyPaletteKeepsDelegationAndMCPTools(t *testing.T) {
	registry := tools.NewLazyRegistry(lazyCoreTools)
	full := []fantasy.AgentTool{
		namedTool{name: AgentToolName},
		namedTool{name: AgentDispatchToolName},
		namedTool{name: SendMessageToolName},
		namedTool{name: TeamTaskCreateToolName},
		mcpNamedTool{namedTool{name: "github_create_issue"}},
		namedTool{name: tools.SymbolsToolName},
		namedTool{name: tools.FetchToolName},
	}
	registry.Register(full)

	names := toolListNames(registry.Filter("session-1", full))

	require.Equal(t, []string{
		AgentToolName,
		AgentDispatchToolName,
		SendMessageToolName,
		TeamTaskCreateToolName,
		"github_create_issue",
	}, names, "delegation and MCP tools stay in the request; built-in extras are held back")
}
