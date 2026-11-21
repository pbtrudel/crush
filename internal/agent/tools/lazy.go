package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"charm.land/fantasy"
)

// ToolSearchToolName is the tool that reveals deferred tool schemas when
// options.lazy_tools is on.
const ToolSearchToolName = "tool_search"

//go:embed tool_search.md
var toolSearchDescription string

// LazyRegistry holds the deferred half of one agent's tool palette and the
// names each session has revealed with tool_search. It is the single place
// that decides what a lazy session's request carries, so the palette the
// model sees and the schemas tool_search hands back cannot disagree.
type LazyRegistry struct {
	mu     sync.RWMutex
	always map[string]struct{}
	// deferred is every built-in tool that is not part of the core palette,
	// keyed by name. MCP tools are never deferred: their schemas come and go
	// with the server, and mcp_servers already owns activating them.
	deferred map[string]fantasy.AgentTool
	// revealed is per session, so one session unlocking a tool cannot grow
	// another session's palette.
	revealed map[string]map[string]struct{}
}

// NewLazyRegistry builds a registry whose core palette is the given names.
// The caller owns the policy of what is core; the registry only enforces it.
func NewLazyRegistry(always map[string]struct{}) *LazyRegistry {
	copied := make(map[string]struct{}, len(always))
	for name := range always {
		copied[name] = struct{}{}
	}
	return &LazyRegistry{
		always:   copied,
		deferred: make(map[string]fantasy.AgentTool),
		revealed: make(map[string]map[string]struct{}),
	}
}

// Register records the palette built for an agent: every tool that is not
// core and not MCP-backed becomes deferred. Registration is cumulative across
// builds because one coordinator shares the registry among its agents (the
// lead, the plan agent, a task subagent rebuilt on every delegated turn): a
// subagent's smaller palette must not make a tool the lead can reveal
// unsearchable. A tool an agent does not have can never enter its palette,
// because Filter only ever drops entries from that agent's own list.
func (r *LazyRegistry) Register(list []fantasy.AgentTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, tool := range list {
		name := tool.Info().Name
		if _, core := r.always[name]; core {
			continue
		}
		if _, isMCP := tool.(interface{ MCP() string }); isMCP {
			continue
		}
		r.deferred[name] = tool
	}
}

// Filter returns the list a session's request carries: everything except the
// deferred tools that session has not revealed. A registry with no catalog
// is a no-op, which is what keeps the option off from changing anything.
func (r *LazyRegistry) Filter(sessionID string, list []fantasy.AgentTool) []fantasy.AgentTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.deferred) == 0 {
		return list
	}
	filtered := make([]fantasy.AgentTool, 0, len(list))
	for _, tool := range list {
		name := tool.Info().Name
		if _, held := r.deferred[name]; held && !r.revealedLocked(sessionID, name) {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func (r *LazyRegistry) revealedLocked(sessionID, name string) bool {
	names, ok := r.revealed[sessionID]
	if !ok {
		return false
	}
	_, ok = names[name]
	return ok
}

// DeferredNames lists the tools the session cannot call yet, sorted, so a
// miss can tell the model what exists.
func (r *LazyRegistry) DeferredNames(sessionID string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.deferred))
	for name := range r.deferred {
		if r.revealedLocked(sessionID, name) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Tool returns the tool_search tool bound to this registry.
func (r *LazyRegistry) Tool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ToolSearchToolName,
		toolSearchDescription,
		func(ctx context.Context, params LazyToolSearchParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			query := strings.TrimSpace(params.Query)
			if query == "" {
				return fantasy.NewTextErrorResponse(
					"tool_search needs a query: a tool name, or words that describe what you want to do.",
				), nil
			}
			matches := r.reveal(GetSessionFromContext(ctx), query)
			if len(matches) == 0 {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"No deferred tool matches %q. Tools held back for this session:\n- %s",
					query, strings.Join(r.DeferredNames(GetSessionFromContext(ctx)), "\n- "),
				)), nil
			}
			return fantasy.NewTextResponse(describeRevealedTools(matches)), nil
		},
	)
}

// LazyToolSearchParams is the tool_search argument.
type LazyToolSearchParams struct {
	Query string `json:"query" description:"A tool name, or words that describe what you want to do."`
}

// lazyCandidate is one tool_search match. already marks a tool the session
// had revealed before this call, so the reply can say so instead of claiming
// a fresh reveal.
type lazyCandidate struct {
	tool    fantasy.AgentTool
	already bool
}

// reveal matches the query against the deferred set and unlocks what it
// matched. Matches already unlocked come back marked, so a repeated search
// answers rather than duplicating a tool in the palette.
func (r *LazyRegistry) reveal(sessionID, query string) []lazyCandidate {
	needle := strings.ToLower(query)
	r.mu.Lock()
	defer r.mu.Unlock()
	candidates := make([]lazyCandidate, 0, len(r.deferred))
	ranks := make(map[string]int, len(r.deferred))
	for name, tool := range r.deferred {
		rank, ok := lazyMatchRank(name, tool.Info().Description, needle)
		if !ok {
			continue
		}
		candidates = append(candidates, lazyCandidate{tool: tool, already: r.revealedLocked(sessionID, name)})
		ranks[name] = rank
	}
	slices.SortFunc(candidates, func(a, b lazyCandidate) int {
		if diff := ranks[a.tool.Info().Name] - ranks[b.tool.Info().Name]; diff != 0 {
			return diff
		}
		return strings.Compare(a.tool.Info().Name, b.tool.Info().Name)
	})
	unlock := r.revealed[sessionID]
	if unlock == nil {
		unlock = make(map[string]struct{}, len(candidates))
		r.revealed[sessionID] = unlock
	}
	for _, candidate := range candidates {
		unlock[candidate.tool.Info().Name] = struct{}{}
	}
	return candidates
}

// lazyMatchRank scores a tool against a lowercased query. An exact tool name
// wins over a name substring, which wins over a description match: asking
// for "grep" should not bury grep under eight tools that mention searching.
func lazyMatchRank(name, description, needle string) (int, bool) {
	loweredName := strings.ToLower(name)
	switch {
	case loweredName == needle:
		return 0, true
	case strings.Contains(loweredName, needle):
		return 1, true
	case strings.Contains(strings.ToLower(description), needle):
		return 2, true
	}
	return 0, false
}

// describeRevealedTools renders the matches with their full schemas, which is
// what the model needs to call them without another round trip.
func describeRevealedTools(matches []lazyCandidate) string {
	var revealed, already int
	for _, match := range matches {
		if match.already {
			already++
			continue
		}
		revealed++
	}

	var sb strings.Builder
	switch {
	case revealed > 0 && already > 0:
		fmt.Fprintf(&sb, "Revealed %d tool(s); %d were already revealed. Callable from your next step:\n", revealed, already)
	case revealed > 0:
		fmt.Fprintf(&sb, "Revealed %d tool(s). Callable from your next step:\n", revealed)
	default:
		fmt.Fprintf(&sb, "These %d tool(s) were already revealed and are callable now:\n", already)
	}
	for _, match := range matches {
		info := match.tool.Info()
		fmt.Fprintf(&sb, "\n- %s: %s", info.Name, firstLine(info.Description))
		schema, err := json.Marshal(info)
		if err != nil {
			continue
		}
		fmt.Fprintf(&sb, "\n  schema: %s", schema)
	}
	return sb.String()
}
