package agent

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// reasoningGatewayModel is an OpenAI-compatible model whose request uses
// reasoning, as the vendor flags require.
func reasoningGatewayModel(id string) Model {
	return Model{
		CatwalkCfg: catwalk.Model{
			ID: id,
			Reasoning: catwalk.Reasoning{
				Thinking:     catwalk.ThinkingToggleable,
				EffortLevels: catwalk.NewEffortLevels("low", "high"),
			},
		},
		ModelCfg: config.SelectedModel{
			Provider:        "gateway",
			Model:           id,
			ReasoningEffort: "high",
		},
	}
}

func TestGetProviderOptionsVendorReasoningFlags(t *testing.T) {
	t.Parallel()

	// A third-party gateway speaks chat completions and sets no thinking
	// field of its own, so the family flag is the only carrier.
	gateway := config.ProviderConfig{ID: "gateway", Type: openaicompat.Name}

	t.Run("glm keeps earlier thinking", func(t *testing.T) {
		t.Parallel()
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("GLM-5.3"), gateway))
		thinking, ok := extra["thinking"].(map[string]any)
		require.True(t, ok, "glm needs a thinking object")
		require.Equal(t, false, thinking["clear_thinking"])
	})

	t.Run("kimi keeps all earlier thinking", func(t *testing.T) {
		t.Parallel()
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("moonshotai/Kimi-K3"), gateway))
		thinking, ok := extra["thinking"].(map[string]any)
		require.True(t, ok, "kimi needs a thinking object")
		require.Equal(t, "all", thinking["keep"])
	})

	t.Run("qwen reads earlier thinking", func(t *testing.T) {
		t.Parallel()
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("qwen3.8-flash"), gateway))
		require.Equal(t, true, extra["preserve_thinking"])
	})

	t.Run("mimo needs no flag", func(t *testing.T) {
		t.Parallel()
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("mimo-v2.6-pro"), gateway))
		require.NotContains(t, extra, "thinking")
		require.NotContains(t, extra, "preserve_thinking")
	})

	t.Run("another family gets none", func(t *testing.T) {
		t.Parallel()
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("deepseek-v4.1-flash"), gateway))
		require.NotContains(t, extra, "thinking")
		require.NotContains(t, extra, "preserve_thinking")
	})

	t.Run("no flag when the request does not reason", func(t *testing.T) {
		t.Parallel()
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "glm-5.3"},
			ModelCfg:   config.SelectedModel{Provider: "gateway", Model: "glm-5.3"},
		}
		extra := gatewayExtraBody(t, getProviderOptions(model, gateway))
		require.NotContains(t, extra, "thinking")
	})

	t.Run("a host's boolean thinking is left alone", func(t *testing.T) {
		t.Parallel()
		// Hyper sends "thinking" as a boolean, which Crush sets itself. A
		// nested vendor flag must not rewrite it into an object.
		model := reasoningGatewayModel("glm-5.3")
		model.ModelCfg.Think = true
		hyperCfg := config.ProviderConfig{ID: hyper.Name, Type: openaicompat.Name}
		extra := gatewayExtraBody(t, getProviderOptions(model, hyperCfg))
		require.Equal(t, true, extra["thinking"])
	})

	t.Run("a host's thinking object gains the flag", func(t *testing.T) {
		t.Parallel()
		// Z.AI documents clear_thinking inside the thinking object, which is
		// what the zai provider already sets.
		zai := config.ProviderConfig{
			ID:   string(catwalk.InferenceProviderZAI),
			Type: openaicompat.Name,
		}
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("glm-5.3"), zai))
		thinking, ok := extra["thinking"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "enabled", thinking["type"])
		require.Equal(t, false, thinking["clear_thinking"])
	})
}
