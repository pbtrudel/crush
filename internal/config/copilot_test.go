package config

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/stretchr/testify/require"
)

func TestWithCopilotLongContextModels(t *testing.T) {
	t.Parallel()

	models := []catwalk.Model{
		{ID: "claude-sonnet-5.5", Name: "Claude Sonnet 5.5", ContextWindow: 328_000, Capabilities: catwalk.Capabilities{Vision: true}},
		{ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128_000},
	}

	got := withCopilotLongContextModels(models)

	require.Len(t, got, 3)
	require.Equal(t, "claude-sonnet-5.5", got[0].ID)
	require.Equal(t, int64(328_000), got[0].ContextWindow)
	require.Equal(t, "gpt-4o", got[1].ID)

	variant := got[2]
	require.Equal(t, "claude-sonnet-5.5[long]", variant.ID)
	require.Equal(t, "Claude Sonnet 5.5 (936K context)", variant.Name)
	require.Equal(t, int64(936_000), variant.ContextWindow)
	require.True(t, variant.Capabilities.Vision, "the variant keeps the base capabilities")
}

func TestWithCopilotLongContextModels_NoModels(t *testing.T) {
	t.Parallel()

	require.Empty(t, withCopilotLongContextModels(nil))
}

func TestWithCopilotLongContextModels_KeepsConfiguredVariant(t *testing.T) {
	t.Parallel()

	models := []catwalk.Model{
		{ID: "claude-sonnet-5.5", Name: "Claude Sonnet 5.5", ContextWindow: 328_000},
		{ID: "claude-sonnet-5.5[long]", Name: "Mine", ContextWindow: 500_000},
	}

	got := withCopilotLongContextModels(models)

	require.Len(t, got, 2)
	require.Equal(t, "Mine", got[1].Name)
	require.Equal(t, int64(500_000), got[1].ContextWindow)
}

func TestCopilotBaseModelID(t *testing.T) {
	t.Parallel()

	require.Equal(t, "claude-sonnet-5.5", CopilotBaseModelID("claude-sonnet-5.5[long]"))
	require.Equal(t, "claude-sonnet-5.5", CopilotBaseModelID("claude-sonnet-5.5"))
}

func TestCopilotWindowLabel(t *testing.T) {
	t.Parallel()

	require.Equal(t, "936K", copilotWindowLabel(936_000))
	require.Equal(t, "1M", copilotWindowLabel(1_000_000))
	require.Equal(t, "1.05M", copilotWindowLabel(1_050_000))
}
