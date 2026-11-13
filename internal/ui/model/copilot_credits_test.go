package model

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newCreditsUI(provider string) *UI {
	u := newTestUI()
	u.com = common.DefaultCommon(&testWorkspace{cfg: &config.Config{
		Options: &config.Options{},
		Providers: csync.NewMapFrom(map[string]config.ProviderConfig{
			provider: {ID: provider, Name: provider},
		}),
		Models: map[config.SelectedModelType]config.SelectedModel{
			config.SelectedModelTypeMain: {Provider: provider, Model: "m"},
		},
	}})
	u.agentReady = true
	u.agentModel = workspace.AgentModel{
		CatwalkCfg: catwalk.Model{ID: "m", Name: "Model"},
		ModelCfg:   config.SelectedModel{Provider: provider, Model: "m"},
	}
	used := 2568
	u.copilotCredits = &used
	return u
}

func TestModelInfoShowsCopilotCreditsForCopilot(t *testing.T) {
	t.Parallel()

	u := newCreditsUI(string(catwalk.InferenceProviderCopilot))
	require.Contains(t, ansi.Strip(u.modelInfo(60)), "2,568 AI credits used")
}

func TestModelInfoHidesCopilotCreditsForOtherProviders(t *testing.T) {
	t.Parallel()

	u := newCreditsUI(string(catwalk.InferenceProviderOpenAI))
	require.NotContains(t, ansi.Strip(u.modelInfo(60)), "AI credits used")
}

func TestSidebarCacheKeyTracksCopilotCredits(t *testing.T) {
	t.Parallel()

	u := newCreditsUI(string(catwalk.InferenceProviderCopilot))
	before := u.sidebarCacheKey()
	used := 2600
	u.copilotCredits = &used
	require.NotEqual(t, before, u.sidebarCacheKey())
}
