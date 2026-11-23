package workspace

import (
	"context"
	"fmt"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/stretchr/testify/require"
)

// modelDouble records the mutations a model switch performs. These tests
// are synchronous, so a plain slice is enough.
type modelDouble struct {
	Workspace

	cfg   *config.Config
	busy  bool
	calls []string
	saved config.SelectedModel
}

func (d *modelDouble) Config() *config.Config { return d.cfg }

func (d *modelDouble) AgentIsBusy() bool { return d.busy }

func (d *modelDouble) UpdatePreferredModel(scope config.Scope, typ config.SelectedModelType, m config.SelectedModel) error {
	d.calls = append(d.calls, fmt.Sprintf("UpdatePreferredModel:%s:%s:%s/%s", scope, typ, m.Provider, m.Model))
	d.saved = m
	return nil
}

func (d *modelDouble) UpdateAgentModel(context.Context) error {
	d.calls = append(d.calls, "UpdateAgentModel")
	return nil
}

// testModelConfig builds the switch config: p1 serves m1 (with effort
// levels) and m2, and the disabled p2 would serve the same pair.
func testModelConfig(t *testing.T) *config.Config {
	t.Helper()

	models := func() []catwalk.Model {
		return []catwalk.Model{
			{
				ID:            "m1",
				Name:          "Model One",
				ContextWindow: 200_000,
				Reasoning: catwalk.Reasoning{
					Thinking:           catwalk.ThinkingToggleable,
					EffortLevels:       catwalk.NewEffortLevels("low", "high"),
					DefaultEffortLevel: "low",
				},
			},
			{ID: "m2", Name: "Model Two", ContextWindow: 100_000},
		}
	}

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("p1", config.ProviderConfig{ID: "p1", Name: "Provider One", Models: models()})
	providers.Set("p2", config.ProviderConfig{ID: "p2", Name: "Provider Two", Models: models(), Disable: true})

	return &config.Config{
		Providers: providers,
		Models: map[config.SelectedModelType]config.SelectedModel{
			config.SelectedModelTypeMain: {Provider: "p1", Model: "m1"},
		},
		Agents: map[string]config.Agent{
			config.AgentCoder: {Model: config.SelectedModelTypeMain},
		},
	}
}

func TestModelChoicesListsEnabledConfiguredModels(t *testing.T) {
	t.Parallel()

	got := ModelChoices(testModelConfig(t))
	require.Len(t, got, 2, "the disabled provider's models are left out")
	require.Equal(t, "p1", got[0].Provider)
	require.Equal(t, "Provider One", got[0].ProviderName)
	require.Equal(t, "m1", got[0].ID)
	require.Equal(t, []string{"low", "high"}, got[0].EffortLevels)
	require.Equal(t, "low", got[0].DefaultEffort)
	require.True(t, got[0].CanReason)
	require.False(t, got[1].CanReason)
	require.Empty(t, got[1].EffortLevels)
}

// TestModelChoicesHidesUnconfiguredProviders mirrors the model dialog's
// TestModelsHidesUnconfiguredProviders: a catalog provider the config does
// not hold is never listed, even though its models are in the catalog.
func TestModelChoicesHidesUnconfiguredProviders(t *testing.T) {
	t.Parallel()

	cfg := testModelConfig(t)
	// The dialog walks the provider catalog; ModelChoices walks the
	// configured providers. Both apply the same predicate.
	require.False(t, cfg.ProviderListed("anthropic", false), "an unconfigured provider is hidden")
	require.True(t, cfg.ProviderListed("anthropic", true), "show all lists it")
	require.False(t, cfg.ProviderListed("p2", false), "a disabled provider is hidden")
	require.False(t, cfg.ProviderListed("p2", true), "show all keeps a disabled provider hidden")

	for _, choice := range ModelChoices(cfg) {
		require.Equal(t, "p1", choice.Provider)
	}
}

func TestModelChoicesWithoutConfig(t *testing.T) {
	t.Parallel()

	require.Empty(t, ModelChoices(nil))
}

func TestSelectModelRefusesWhileBusy(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{cfg: testModelConfig(t), busy: true}
	err := SelectModel(t.Context(), ws, config.SelectedModelTypeMain, config.SelectedModel{Provider: "p1", Model: "m2"})
	require.ErrorIs(t, err, ErrAgentBusy)
	require.Empty(t, ws.calls)
}

func TestSelectModelRefusesWithoutConfig(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{}
	err := SelectModel(t.Context(), ws, config.SelectedModelTypeMain, config.SelectedModel{Provider: "p1", Model: "m2"})
	require.ErrorIs(t, err, ErrNoConfig)
	require.Empty(t, ws.calls)
}

func TestSelectModelUpdatesThenReloads(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{cfg: testModelConfig(t)}
	require.NoError(t, SelectModel(t.Context(), ws, config.SelectedModelTypeMain, config.SelectedModel{Provider: "p1", Model: "m2"}))
	require.Equal(t, []string{"UpdatePreferredModel:global:main:p1/m2", "UpdateAgentModel"}, ws.calls)
	require.Equal(t, "m2", ws.saved.Model)
}

func TestSelectModelRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{cfg: testModelConfig(t)}
	err := SelectModel(t.Context(), ws, config.SelectedModelTypeMain, config.SelectedModel{Provider: "p2", Model: "x"})
	require.ErrorIs(t, err, ErrModelUnavailable)
	require.Empty(t, ws.calls)

	err = SelectModel(t.Context(), ws, config.SelectedModelTypeMain, config.SelectedModel{Provider: "p1", Model: "x"})
	require.ErrorIs(t, err, ErrModelUnavailable)
	require.Empty(t, ws.calls)
}

func TestSetReasoningEffortKeepsTheModel(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{cfg: testModelConfig(t)}
	require.NoError(t, SetReasoningEffort(t.Context(), ws, "high"))
	require.Equal(t, "high", ws.saved.ReasoningEffort)
	require.Equal(t, "m1", ws.saved.Model)
	require.Equal(t, []string{"UpdatePreferredModel:global:main:p1/m1", "UpdateAgentModel"}, ws.calls)
}

func TestSetReasoningEffortRejectsUnknownLevel(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{cfg: testModelConfig(t)}
	err := SetReasoningEffort(t.Context(), ws, "extreme")
	require.EqualError(t, err, `unknown reasoning effort "extreme"`)
	require.Empty(t, ws.calls)
}

func TestSetReasoningEffortRefusesWhileBusy(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{cfg: testModelConfig(t), busy: true}
	err := SetReasoningEffort(t.Context(), ws, "high")
	require.ErrorIs(t, err, ErrAgentBusy)
	require.Empty(t, ws.calls)
}

func TestSetThinkingSetsTheFlag(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{cfg: testModelConfig(t)}
	require.NoError(t, SetThinking(t.Context(), ws, true))
	require.True(t, ws.saved.Think)
	require.Equal(t, "m1", ws.saved.Model)
}

func TestSetThinkingRefusesWhileBusy(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{cfg: testModelConfig(t), busy: true}
	err := SetThinking(t.Context(), ws, true)
	require.ErrorIs(t, err, ErrAgentBusy)
	require.Empty(t, ws.calls)
}

// TestApplyThinkingIgnoresBusy pins the TUI path: the thinking toggle has
// never refused while the agent is busy, so the unguarded helper keeps
// working.
func TestApplyThinkingIgnoresBusy(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{cfg: testModelConfig(t), busy: true}
	require.NoError(t, ApplyThinking(t.Context(), ws, true))
	require.True(t, ws.saved.Think)
	require.Equal(t, []string{"UpdatePreferredModel:global:main:p1/m1", "UpdateAgentModel"}, ws.calls)
}

func TestSetThinkingWithoutConfig(t *testing.T) {
	t.Parallel()

	ws := &modelDouble{}
	require.ErrorIs(t, SetThinking(t.Context(), ws, true), ErrNoConfig)
	require.Empty(t, ws.calls)
}
