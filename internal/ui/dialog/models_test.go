package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

type modelsTestWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (w *modelsTestWorkspace) Config() *config.Config {
	return w.cfg
}

func testModelsCatalog() []catwalk.Provider {
	return []catwalk.Provider{
		{ID: "openai", Name: "OpenAI", Models: []catwalk.Model{{ID: "gpt-4o"}}},
		{ID: "anthropic", Name: "Anthropic", Models: []catwalk.Model{{ID: "claude-sonnet"}}},
	}
}

func testModelsConfig(t *testing.T, opts *config.Options, configured ...string) *config.Config {
	t.Helper()
	if opts == nil {
		opts = &config.Options{}
	}
	cfg := &config.Config{
		Options:   opts,
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}
	for _, id := range configured {
		cfg.Providers.Set(id, config.ProviderConfig{ID: id})
	}
	return cfg
}

func newModelsForTest(t *testing.T, cfg *config.Config, providers []catwalk.Provider, isOnboarding bool) *Models {
	t.Helper()
	sty := styles.CharmtonePantera()
	com := &common.Common{Workspace: &modelsTestWorkspace{cfg: cfg}, Styles: &sty}
	m := &Models{
		com:              com,
		isOnboarding:     isOnboarding,
		modelType:        ModelTypeLarge,
		providers:        providers,
		list:             NewModelsList(&sty),
		showAllProviders: isOnboarding || cfg.Options.GetShowAllProviders(),
	}
	m.initKeyMap()
	require.NoError(t, m.setProviderItems())
	return m
}

func modelGroupTitles(m *Models) []string {
	titles := make([]string, 0, len(m.list.groups))
	for _, g := range m.list.groups {
		titles = append(titles, g.Title)
	}
	return titles
}

func TestModelsHidesUnconfiguredProviders(t *testing.T) {
	t.Parallel()

	m := newModelsForTest(t, testModelsConfig(t, nil, "openai"), testModelsCatalog(), false)
	require.Equal(t, []string{"OpenAI"}, modelGroupTitles(m))
}

func TestModelsShowsAllProvidersWhenPersisted(t *testing.T) {
	t.Parallel()

	cfg := testModelsConfig(t, &config.Options{Models: &config.ModelsOptions{ShowAllProviders: true}}, "openai")
	m := newModelsForTest(t, cfg, testModelsCatalog(), false)
	require.Equal(t, []string{"OpenAI", "Anthropic"}, modelGroupTitles(m))
}

func TestModelsOnboardingShowsAllProviders(t *testing.T) {
	t.Parallel()

	cfg := testModelsConfig(t, &config.Options{Models: &config.ModelsOptions{ShowAllProviders: false}})
	m := newModelsForTest(t, cfg, testModelsCatalog(), true)
	require.Equal(t, []string{"OpenAI", "Anthropic"}, modelGroupTitles(m))
}

func TestModelsToggleReturnsPersistenceAction(t *testing.T) {
	t.Parallel()

	m := newModelsForTest(t, testModelsConfig(t, nil, "openai"), testModelsCatalog(), false)

	action := m.HandleMsg(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	toggled, ok := action.(ActionToggleShowAllProviders)
	require.True(t, ok)
	require.True(t, toggled.ShowAll)
	require.Equal(t, []string{"OpenAI", "Anthropic"}, modelGroupTitles(m))

	action = m.HandleMsg(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	toggled, ok = action.(ActionToggleShowAllProviders)
	require.True(t, ok)
	require.False(t, toggled.ShowAll)
	require.Equal(t, []string{"OpenAI"}, modelGroupTitles(m))
}

func TestModelsToggleIgnoredDuringOnboarding(t *testing.T) {
	t.Parallel()

	m := newModelsForTest(t, testModelsConfig(t, nil), testModelsCatalog(), true)
	require.Nil(t, m.HandleMsg(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}))
	require.Equal(t, []string{"OpenAI", "Anthropic"}, modelGroupTitles(m))
}
