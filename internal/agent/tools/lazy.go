package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"

	"charm.land/fantasy"
)

// ToolSearchToolName is the tool that reveals deferred tool schemas when
// options.lazy_tools is on.
const ToolSearchToolName = "tool_search"

// lazyRevealLimit is how many tools one search may reveal. A generic word
// ("search", "file") otherwise reveals half the deferred palette in one
// thoughtless query and keeps it for the session, which is the cost the
// option exists to avoid. The reply says how many matches were left out.
const lazyRevealLimit = 3

//go:embed tool_search.md
var toolSearchDescription string

// LazyRegistry holds the deferred half of one agent's tool palette and the
// names each session has revealed with tool_search. It is the single place
// that decides what a lazy session's request carries, so the palette the
// model sees and the schemas tool_search hands back cannot disagree.
type LazyRegistry struct {
	mu     sync.RWMutex
	always map[string]struct{}
	// deferred is every tool that is not part of the core palette, keyed by
	// name, MCP-backed ones included: an MCP server's schemas cost the same
	// tokens on every request as a built-in's. Filter keeps a tool only when
	// the request's own palette carries it, so deferring an MCP tool hides
	// nothing the session could not reveal anyway.
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
// core becomes deferred, MCP-backed ones included. Registration is cumulative
// across builds because one coordinator shares the registry among its agents
// (the lead, the plan agent, a task subagent rebuilt on every delegated
// turn): a subagent's smaller palette must not make a tool the lead can
// reveal unsearchable. Registering a name says the tool exists somewhere, not
// that every agent can reveal it; Tool scopes a reveal to the palette of the
// agent that owns it, which is also what keeps a tool from one MCP channel
// from being promised to a session that cannot carry it.
func (r *LazyRegistry) Register(list []fantasy.AgentTool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, tool := range list {
		name := tool.Info().Name
		if _, core := r.always[name]; core {
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

// Tool returns the tool_search tool for one agent's palette. The palette is
// the list of tool names that agent actually carries: a match outside it is
// reported but never marked revealed, because revealing it would promise the
// model a tool that cannot arrive — the registry knows every tool any agent in
// the process built, and Filter only ever drops entries from this agent's own
// list.
func (r *LazyRegistry) Tool(palette []string) fantasy.AgentTool {
	own := make(map[string]struct{}, len(palette))
	for _, name := range palette {
		own[name] = struct{}{}
	}
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
			sessionID := GetSessionFromContext(ctx)
			matches, total := r.reveal(sessionID, query, own)
			if total == 0 {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"No deferred tool matches %q. Tools you can reveal in this session:\n- %s",
					query, strings.Join(r.availableNames(sessionID, own), "\n- "),
				)), nil
			}
			return fantasy.NewTextResponse(describeRevealedTools(matches, total)), nil
		},
	)
}

// availableNames lists the deferred tools this agent could reveal and the
// session has not revealed yet, sorted.
func (r *LazyRegistry) availableNames(sessionID string, own map[string]struct{}) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.deferred))
	for name := range r.deferred {
		if _, ok := own[name]; !ok {
			continue
		}
		if r.revealedLocked(sessionID, name) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// LazyToolSearchParams is the tool_search argument.
type LazyToolSearchParams struct {
	Query string `json:"query" description:"A tool name, or words that describe what you want to do."`
}

// lazyCandidate is one tool_search match. already marks a tool the session had
// revealed before this call, and available marks one the calling agent's
// palette carries; a match that is not available is reported, never revealed.
type lazyCandidate struct {
	tool      fantasy.AgentTool
	score     int
	nameHits  int
	available bool
	already   bool
}

// reveal matches the query against the deferred set and unlocks the best few
// matches that the calling agent can actually use. It returns the candidates
// to report and how many matched in total, so the reply can say that the list
// was cut short.
func (r *LazyRegistry) reveal(sessionID, query string, own map[string]struct{}) ([]lazyCandidate, int) {
	tokens := lazyQueryTokens(query)
	if len(tokens) == 0 {
		return nil, 0
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	r.mu.Lock()
	defer r.mu.Unlock()

	candidates := make([]lazyCandidate, 0, len(r.deferred))
	for name, tool := range r.deferred {
		score, nameHits, ok := lazyMatchScore(name, tool.Info().Description, needle, tokens)
		if !ok {
			continue
		}
		_, inPalette := own[name]
		candidates = append(candidates, lazyCandidate{
			tool:      tool,
			score:     score,
			nameHits:  nameHits,
			available: inPalette,
			already:   r.revealedLocked(sessionID, name),
		})
	}
	slices.SortFunc(candidates, func(a, b lazyCandidate) int {
		// A tool this agent can use comes first: an unavailable one must not
		// take a reveal slot from one that would actually arrive.
		if a.available != b.available {
			if a.available {
				return -1
			}
			return 1
		}
		if a.score != b.score {
			return a.score - b.score
		}
		if a.nameHits != b.nameHits {
			return b.nameHits - a.nameHits
		}
		return strings.Compare(a.tool.Info().Name, b.tool.Info().Name)
	})

	total := len(candidates)
	if len(candidates) > lazyRevealLimit {
		candidates = candidates[:lazyRevealLimit]
	}
	unlock := r.revealed[sessionID]
	if unlock == nil {
		unlock = make(map[string]struct{}, len(candidates))
		r.revealed[sessionID] = unlock
	}
	for _, candidate := range candidates {
		if !candidate.available {
			continue
		}
		unlock[candidate.tool.Info().Name] = struct{}{}
	}
	return candidates, total
}

// lazyQueryTokens splits a search into the words a model would say. Tool
// identifiers keep their underscores ("lsp_references"), punctuation splits,
// and one-letter noise ("a", "I") is dropped so it cannot match everything.
func lazyQueryTokens(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		lowered := strings.ToLower(field)
		if len(lowered) < 2 {
			continue
		}
		tokens = append(tokens, lowered)
	}
	return tokens
}

// lazyMatchScore ranks a tool against a query. An exact identifier wins, then
// every word in the name, then a name word plus the rest in the description,
// then the whole query in the description. A word the query does not contain
// means no match at all, which is what keeps a multi-word query precise.
func lazyMatchScore(name, description, needle string, tokens []string) (score, nameHits int, ok bool) {
	loweredName := strings.ToLower(name)
	if loweredName == needle {
		return 0, len(tokens), true
	}
	loweredDescription := strings.ToLower(description)
	allInName, allSomewhere := true, true
	for _, token := range tokens {
		inName := strings.Contains(loweredName, token)
		if inName {
			nameHits++
			continue
		}
		allInName = false
		if !strings.Contains(loweredDescription, token) {
			allSomewhere = false
		}
	}
	switch {
	case allInName:
		return 1, nameHits, true
	case nameHits > 0 && allSomewhere:
		return 2, nameHits, true
	case allSomewhere:
		return 3, nameHits, true
	}
	return 0, 0, false
}

// describeRevealedTools renders the matches with their full schemas, which is
// what the model needs to call them without another round trip.
func describeRevealedTools(matches []lazyCandidate, total int) string {
	var revealed, already, unavailable int
	for _, match := range matches {
		switch {
		case !match.available:
			unavailable++
		case match.already:
			already++
		default:
			revealed++
		}
	}

	var sb strings.Builder
	switch {
	case revealed > 0:
		fmt.Fprintf(&sb, "Revealed %d tool(s)", revealed)
		if already > 0 {
			fmt.Fprintf(&sb, "; %d were already revealed", already)
		}
		sb.WriteString(". Callable from your next step:\n")
	case already > 0:
		fmt.Fprintf(&sb, "These %d tool(s) were already revealed and are callable now:\n", already)
	default:
		sb.WriteString("No tool in this agent's palette matches:\n")
	}

	for _, match := range matches {
		if !match.available {
			fmt.Fprintf(&sb, "\n- %s: not part of this agent's palette, so it cannot be revealed here.", match.tool.Info().Name)
			continue
		}
		info := match.tool.Info()
		fmt.Fprintf(&sb, "\n- %s: %s", info.Name, firstLine(info.Description))
		schema, err := json.Marshal(info)
		if err != nil {
			continue
		}
		fmt.Fprintf(&sb, "\n  schema: %s", schema)
	}

	if remaining := total - len(matches); remaining > 0 {
		fmt.Fprintf(&sb, "\n\n%d more deferred tool(s) match. Narrow the query to see them, or ask for one by name.", remaining)
	}
	return sb.String()
}
