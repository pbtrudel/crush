package tools

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// fakeToolParams is what a fake tool takes. A real tool has a parameters
// object, and tool_search must hand that object back, so the fake has one too.
type fakeToolParams struct {
	Path string `json:"path" description:"a path"`
}

// fakeTool builds a named tool that answers with its own name, so a test can
// tell which tool actually ran.
func fakeTool(name, description string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		name,
		description,
		func(context.Context, fakeToolParams, fantasy.ToolCall) (fantasy.ToolResponse, error) {
			return fantasy.NewTextResponse(name + " ran"), nil
		},
	)
}

// fakeMCPTool is an MCP-backed tool: the registry must never defer those,
// because MCP laziness belongs to the mcp_servers tool.
type fakeMCPTool struct {
	fantasy.AgentTool
	server string
}

func (t fakeMCPTool) MCP() string { return t.server }

func toolNames(list []fantasy.AgentTool) []string {
	names := make([]string, 0, len(list))
	for _, tool := range list {
		names = append(names, tool.Info().Name)
	}
	return names
}

func sessionCtx(sessionID string) context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, sessionID)
}

// lazyFixture builds a registry over four tools: two of them core palette
// members, one built-in that must be held back, and one MCP tool.
func lazyFixture() (*LazyRegistry, []fantasy.AgentTool) {
	read := fakeTool(ReadToolName, "read a file")
	grep := fakeTool(GrepToolName, "search file contents")
	diagnostics := fakeTool(DiagnosticsToolName, "report language server diagnostics for a file")
	mcp := fakeMCPTool{AgentTool: fakeTool("github_create_issue", "open an issue"), server: "github"}

	registry := NewLazyRegistry(map[string]struct{}{
		ReadToolName: {},
		GrepToolName: {},
	})
	full := []fantasy.AgentTool{read, grep, diagnostics, mcp}
	registry.Register(full)
	return registry, full
}

func TestLazyRegistryFilterKeepsCoreAndMCPAndHoldsBackTheRest(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()

	filtered := registry.Filter("session-1", full)

	require.Equal(t, []string{ReadToolName, GrepToolName, "github_create_issue"}, toolNames(filtered),
		"core palette members and MCP tools stay; deferred built-ins are held back")
}

func TestLazyRegistryFilterWithoutCatalogChangesNothing(t *testing.T) {
	t.Parallel()

	registry := NewLazyRegistry(map[string]struct{}{ReadToolName: {}})
	full := []fantasy.AgentTool{
		fakeTool(ReadToolName, "read a file"),
		fakeTool(DiagnosticsToolName, "diagnostics"),
	}

	filtered := registry.Filter("session-1", full)

	require.Equal(t, toolNames(full), toolNames(filtered),
		"a registry that was never given a catalog must not drop anything")
}

func TestToolSearchRevealsToolForTheSessionOnly(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()
	search := registry.Tool()
	require.Equal(t, ToolSearchToolName, search.Info().Name)

	response, err := search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"diagnostics"}`})
	require.NoError(t, err)
	require.False(t, response.IsError)
	require.Contains(t, response.Content, DiagnosticsToolName,
		"the reply must name the tool it revealed")
	require.Contains(t, response.Content, `"parameters"`,
		"the reply must carry the tool's full schema")
	require.Contains(t, response.Content, `"required":["path"]`,
		"the reply must carry the tool's full schema")
	require.NotContains(t, response.Content, ReadToolName,
		"a core palette tool is never deferred, so a search must not offer it")

	revealed := registry.Filter("session-1", full)
	require.Equal(t, []string{ReadToolName, GrepToolName, DiagnosticsToolName, "github_create_issue"},
		toolNames(revealed), "the revealed tool joins the session's palette")

	// The revealed tool is the real one: it is callable, not a placeholder.
	var unlocked fantasy.AgentTool
	for _, tool := range revealed {
		if tool.Info().Name == DiagnosticsToolName {
			unlocked = tool
		}
	}
	require.NotNil(t, unlocked)
	ran, err := unlocked.Run(context.Background(), fantasy.ToolCall{Input: `{"path":"main.go"}`})
	require.NoError(t, err)
	require.Equal(t, DiagnosticsToolName+" ran", ran.Content)

	require.Equal(t, []string{ReadToolName, GrepToolName, "github_create_issue"},
		toolNames(registry.Filter("session-2", full)),
		"a reveal belongs to the session that asked for it")
}

func TestToolSearchUnknownQueryRevealsNothing(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()

	response, err := registry.Tool().Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"zzz-nothing"}`})
	require.NoError(t, err)
	require.True(t, response.IsError)
	require.Contains(t, response.Content, DiagnosticsToolName,
		"a miss still lists the deferred tool names so the model can retry")

	require.Equal(t, []string{ReadToolName, GrepToolName, "github_create_issue"},
		toolNames(registry.Filter("session-1", full)))
}

func TestToolSearchRequiresAQuery(t *testing.T) {
	t.Parallel()

	registry, _ := lazyFixture()

	response, err := registry.Tool().Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{}`})
	require.NoError(t, err)
	require.True(t, response.IsError)
}

// TestRegisterIsCumulativeAcrossAgents covers the coordinator sharing one
// registry: a task subagent's smaller palette is registered after the lead's
// on every delegated turn, and that must not make a lead-only tool
// unsearchable.
func TestRegisterIsCumulativeAcrossAgents(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()
	registry.Register([]fantasy.AgentTool{full[0]})

	response, err := registry.Tool().Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"lsp_diagnostics"}`})
	require.NoError(t, err)
	require.False(t, response.IsError)
	require.Contains(t, response.Content, DiagnosticsToolName)
}

func TestRevealSurvivesCatalogRebuild(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()
	_, err := registry.Tool().Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"lsp_diagnostics"}`})
	require.NoError(t, err)

	registry.Register(full)

	require.Contains(t, toolNames(registry.Filter("session-1", full)), DiagnosticsToolName,
		"a rebuilt palette must not re-defer a tool the session already revealed")
}

func TestToolSearchRevealByNameIsStableOnRepeat(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()
	search := registry.Tool()

	first, err := search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"lsp_diagnostics"}`})
	require.NoError(t, err)
	require.Contains(t, first.Content, "Revealed")

	second, err := search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"lsp_diagnostics"}`})
	require.NoError(t, err)
	require.Contains(t, second.Content, "already")
	require.Equal(t, []string{ReadToolName, GrepToolName, DiagnosticsToolName, "github_create_issue"},
		toolNames(registry.Filter("session-1", full)),
		"a repeated reveal must not duplicate the tool")
}
