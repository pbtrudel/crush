package config

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

func TestWithCopilotFusionModels(t *testing.T) {
	t.Parallel()

	models := []catwalk.Model{{ID: "claude-sonnet-5.5", Name: "Claude Sonnet 5.5"}}
	got := withCopilotFusionModels(models)

	require.Len(t, got, 4)
	require.Equal(t, "claude-sonnet-5.5", got[0].ID)
	require.Equal(t, CopilotFusionStandardModel, got[1].ID)
	require.Equal(t, "Copilot Fusion (Standard)", got[1].Name)
	require.Equal(t, CopilotFusionHighModel, got[2].ID)
	require.Equal(t, CopilotFusionMaxModel, got[3].ID)
	require.Equal(t, int64(copilotFusionWindow), got[1].ContextWindow)
}

func TestWithCopilotFusionModels_KeepsConfiguredEntry(t *testing.T) {
	t.Parallel()

	models := []catwalk.Model{{ID: CopilotFusionMaxModel, Name: "Mine"}}
	got := withCopilotFusionModels(models)

	require.Len(t, got, 3)
	require.Equal(t, "Mine", got[0].Name)
}

func TestCopilotFusionTier(t *testing.T) {
	t.Parallel()

	tier, ok := CopilotFusionTier(CopilotFusionStandardModel)
	require.True(t, ok)
	require.Equal(t, "standard", tier)

	tier, ok = CopilotFusionTier(CopilotFusionHighModel)
	require.True(t, ok)
	require.Equal(t, "high", tier)

	tier, ok = CopilotFusionTier(CopilotFusionMaxModel)
	require.True(t, ok)
	require.Equal(t, "max", tier)

	_, ok = CopilotFusionTier("claude-sonnet-5.5")
	require.False(t, ok)
	require.False(t, IsCopilotFusionModel("claude-sonnet-5.5"))
}
