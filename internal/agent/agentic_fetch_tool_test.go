package agent

import (
	"errors"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

func fetchPaletteNames(palette []fantasy.AgentTool) []string {
	names := make([]string, 0, len(palette))
	for _, tool := range palette {
		names = append(names, tool.Info().Name)
	}
	return names
}

// A blocked search backend must not sit in the fetch subagent's palette: the
// call would spend a step to learn what the coordinator already knows.
func TestFetchSubAgentPaletteDropsSearchWhileBlocked(t *testing.T) {
	t.Parallel()

	const providerID = "test-provider"
	env := testEnv(t)
	coord := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	dir := t.TempDir()

	free := fetchPaletteNames(fetchSubAgentTools(coord, dir, nil, false))
	require.Contains(t, free, tools.WebSearchToolName)
	require.Contains(t, free, tools.WebFetchToolName)

	blocked := fetchPaletteNames(fetchSubAgentTools(coord, dir, nil, true))
	require.NotContains(t, blocked, tools.WebSearchToolName)
	require.Contains(t, blocked, tools.WebFetchToolName, "fetching a known URL still works while search is blocked")
}

// A search-mode call while the backend is blocked gets an answer instead of a
// subagent that has nothing to search with.
func TestSearchModePromptWhileBlocked(t *testing.T) {
	t.Parallel()

	prompt, ok := searchModePrompt("find the release notes", true)
	require.False(t, ok)
	require.Empty(t, prompt)

	prompt, ok = searchModePrompt("find the release notes", false)
	require.True(t, ok)
	require.Contains(t, prompt, "web_search")
	require.Contains(t, searchBlockedMessage, "blocked")
	require.Contains(t, searchBlockedMessage, "search tool you have directly")
}

// A guessed URL that will not come back is worth one search; anything else
// keeps today's error.
func TestFetchFallbackPromptOnMissingURL(t *testing.T) {
	t.Parallel()

	prompt, ok := fetchFallbackPrompt("find the release notes", "https://e.dev/gone", &tools.FetchStatusError{Status: 404}, false)
	require.True(t, ok)
	require.Contains(t, prompt, "The URL https://e.dev/gone returned 404, so search for the answer instead.")
	require.Contains(t, prompt, "find the release notes")

	_, ok = fetchFallbackPrompt("p", "u", &tools.FetchStatusError{Status: 404}, true)
	require.False(t, ok, "a blocked search cannot be the fallback")

	_, ok = fetchFallbackPrompt("p", "u", &tools.FetchStatusError{Status: 500}, false)
	require.False(t, ok, "a server fault is not a guessed address")

	_, ok = fetchFallbackPrompt("p", "u", errors.New("dial tcp: refused"), false)
	require.False(t, ok)
}
