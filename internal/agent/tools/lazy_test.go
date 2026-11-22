package tools

import (
	"context"
	"strings"
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

// fakeMCPTool is an MCP-backed tool. It is deferred like any other non-core
// tool: an MCP server's schemas cost the same tokens on every request as a
// built-in's, so they are held back and revealed the same way.
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

// searchTool binds a tool_search to the palette an agent was built with, the
// way buildTools does.
func searchTool(registry *LazyRegistry, palette []fantasy.AgentTool) fantasy.AgentTool {
	return registry.Tool(toolNames(palette))
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

func TestLazyRegistryFilterKeepsOnlyCoreAndHoldsBackTheRest(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()

	filtered := registry.Filter("session-1", full)

	require.Equal(t, []string{ReadToolName, GrepToolName}, toolNames(filtered),
		"only the core palette stays in the request; deferred built-ins and MCP tools are held back")
}

func TestToolSearchRevealsAnMCPTool(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()
	search := searchTool(registry, full)

	response, err := search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"open an issue"}`})
	require.NoError(t, err)
	require.False(t, response.IsError)
	require.Contains(t, response.Content, "github_create_issue",
		"an MCP tool is searchable like any other deferred tool")

	revealed := registry.Filter("session-1", full)
	require.Equal(t, []string{ReadToolName, GrepToolName, "github_create_issue"}, toolNames(revealed),
		"the revealed MCP tool joins the session's palette")
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
	search := searchTool(registry, full)
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
	require.Equal(t, []string{ReadToolName, GrepToolName, DiagnosticsToolName},
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

	require.Equal(t, []string{ReadToolName, GrepToolName},
		toolNames(registry.Filter("session-2", full)),
		"a reveal belongs to the session that asked for it")
}

func TestToolSearchUnknownQueryRevealsNothing(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()

	response, err := searchTool(registry, full).Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"zzz-nothing"}`})
	require.NoError(t, err)
	require.True(t, response.IsError)
	require.Contains(t, response.Content, DiagnosticsToolName,
		"a miss still lists the deferred tool names so the model can retry")

	require.Equal(t, []string{ReadToolName, GrepToolName},
		toolNames(registry.Filter("session-1", full)))
}

func TestToolSearchRequiresAQuery(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()

	response, err := searchTool(registry, full).Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{}`})
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

	response, err := searchTool(registry, full).Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"lsp_diagnostics"}`})
	require.NoError(t, err)
	require.False(t, response.IsError)
	require.Contains(t, response.Content, DiagnosticsToolName)
}

func TestRevealSurvivesCatalogRebuild(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()
	_, err := searchTool(registry, full).Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"lsp_diagnostics"}`})
	require.NoError(t, err)

	registry.Register(full)

	require.Contains(t, toolNames(registry.Filter("session-1", full)), DiagnosticsToolName,
		"a rebuilt palette must not re-defer a tool the session already revealed")
}

func TestToolSearchRevealByNameIsStableOnRepeat(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()
	search := searchTool(registry, full)

	first, err := search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"lsp_diagnostics"}`})
	require.NoError(t, err)
	require.Contains(t, first.Content, "Revealed")

	second, err := search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"lsp_diagnostics"}`})
	require.NoError(t, err)
	require.Contains(t, second.Content, "already")
	require.Equal(t, []string{ReadToolName, GrepToolName, DiagnosticsToolName},
		toolNames(registry.Filter("session-1", full)),
		"a repeated reveal must not duplicate the tool")
}

// TestToolSearchMatchesMultiWordQueries covers how a model actually searches:
// it has never seen the identifier, so it describes the job. Matching the
// whole query as one substring answers "no tool matches" to every phrase.
func TestToolSearchMatchesMultiWordQueries(t *testing.T) {
	t.Parallel()

	references := fakeTool(ReferencesToolName, "Find all references to a symbol through the language server")
	webFetch := fakeTool(WebFetchToolName, "Fetch a URL and return its content")
	servers := fakeTool(MCPServersToolName, "List MCP servers and activate one")

	registry := NewLazyRegistry(map[string]struct{}{ReadToolName: {}})
	full := []fantasy.AgentTool{fakeTool(ReadToolName, "read a file"), references, webFetch, servers}
	registry.Register(full)
	search := searchTool(registry, full)

	response, err := search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"language server references"}`})
	require.NoError(t, err)
	require.False(t, response.IsError)
	require.Contains(t, response.Content, ReferencesToolName)
	require.NotContains(t, response.Content, WebFetchToolName)

	response, err = search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"fetch a URL"}`})
	require.NoError(t, err)
	require.False(t, response.IsError)
	require.Contains(t, response.Content, WebFetchToolName)
}

// TestToolSearchRanksAnExactNameFirst keeps a known identifier ahead of every
// tool that merely mentions the word.
func TestToolSearchRanksAnExactNameFirst(t *testing.T) {
	t.Parallel()

	registry := NewLazyRegistry(map[string]struct{}{ReadToolName: {}})
	full := []fantasy.AgentTool{
		fakeTool(ReadToolName, "read a file"),
		fakeTool("aaa_tool", "grep internals for search operators"),
		fakeTool(GrepToolName, "search file contents"),
	}
	registry.Register(full)
	search := searchTool(registry, full)

	response, err := search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"grep"}`})
	require.NoError(t, err)
	require.False(t, response.IsError)

	exact := strings.Index(response.Content, "- "+GrepToolName+":")
	require.GreaterOrEqual(t, exact, 0, "the exact name must be revealed")
	loose := strings.Index(response.Content, "- aaa_tool:")
	require.Greater(t, loose, exact, "the exact name must be listed before a description-only match")
}

// TestToolSearchCapsHowManyToolsOneSearchReveals is the guard against the
// option defeating itself: a generic word used to reveal the whole deferred
// half of the palette, permanently, on one thoughtless query.
func TestToolSearchCapsHowManyToolsOneSearchReveals(t *testing.T) {
	t.Parallel()

	names := []string{"alpha", "beta", "delta", "gamma", "omega"}
	full := []fantasy.AgentTool{fakeTool(ReadToolName, "read a file")}
	for _, name := range names {
		full = append(full, fakeTool(name, "search the repository"))
	}
	registry := NewLazyRegistry(map[string]struct{}{ReadToolName: {}})
	registry.Register(full)
	search := searchTool(registry, full)

	response, err := search.Run(sessionCtx("session-1"), fantasy.ToolCall{Input: `{"query":"repository search"}`})
	require.NoError(t, err)
	require.False(t, response.IsError)

	revealed := toolNames(registry.Filter("session-1", full))
	require.Equal(t, append([]string{ReadToolName}, names[:lazyRevealLimit]...), revealed,
		"one search reveals at most the best few matches, in name order among equals")
	require.Contains(t, response.Content, "more",
		"the reply must say that matches were left out, so the model can narrow the query")
}

// TestToolSearchDoesNotPromiseToolsOutsideTheAgentPalette covers the shared
// registry: the deferred catalog holds every tool any agent in the process
// built, but a reveal only means something if the calling agent's own palette
// has the tool. Promising it would send the model after a tool that never
// arrives.
func TestToolSearchDoesNotPromiseToolsOutsideTheAgentPalette(t *testing.T) {
	t.Parallel()

	registry, full := lazyFixture()
	// Another agent (a subagent) registered a smaller palette afterwards; the
	// catalog is cumulative, so the diagnostics tool is still known.
	registry.Register([]fantasy.AgentTool{full[0]})

	// This agent's own palette: the core tools plus the MCP one, no diagnostics.
	subPalette := []fantasy.AgentTool{full[0], full[1], full[3]}
	search := searchTool(registry, subPalette)

	response, err := search.Run(sessionCtx("sub-session"), fantasy.ToolCall{Input: `{"query":"lsp_diagnostics"}`})
	require.NoError(t, err)
	require.Contains(t, response.Content, DiagnosticsToolName,
		"an unavailable match is still named, so the model learns it exists")
	require.Contains(t, response.Content, "not part of this agent's palette")
	require.NotContains(t, response.Content, "Callable from your next step",
		"an unavailable tool must not be promised")

	require.NotContains(t, toolNames(registry.Filter("sub-session", full)), DiagnosticsToolName,
		"and it must not be marked revealed for the session")
}

// TestLazyQueryTokens pins the tokenizer: words and identifiers, no one-letter
// noise, punctuation dropped.
func TestLazyQueryTokens(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"fetch", "url"}, lazyQueryTokens("Fetch a URL"))
	require.Equal(t, []string{"lsp_references"}, lazyQueryTokens("  lsp_references  "))
	require.Equal(t, []string{"language", "server", "references"}, lazyQueryTokens("language-server references"))
	require.Empty(t, lazyQueryTokens("  a  "))
}
