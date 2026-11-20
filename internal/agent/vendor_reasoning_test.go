package agent

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/openai"
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

	t.Run("glm falls back to a top-level flag beside boolean thinking", func(t *testing.T) {
		t.Parallel()
		// Hyper sends "thinking" as a boolean, which Crush sets itself. The
		// boolean is not rewritten; GLM's flag goes at the top level, which
		// the host may forward.
		model := reasoningGatewayModel("glm-5.3")
		model.ModelCfg.Think = true
		hyperCfg := config.ProviderConfig{ID: hyper.Name, Type: openaicompat.Name}
		extra := gatewayExtraBody(t, getProviderOptions(model, hyperCfg))
		require.Equal(t, true, extra["thinking"])
		require.Equal(t, false, extra["clear_thinking"])
	})

	t.Run("kimi on boolean thinking gets nothing", func(t *testing.T) {
		t.Parallel()
		// Kimi has no top-level form for thinking.keep, so a boolean
		// thinking leaves the flag out rather than guessing a shape.
		model := reasoningGatewayModel("kimi-k2.6")
		model.ModelCfg.Think = true
		hyperCfg := config.ProviderConfig{ID: hyper.Name, Type: openaicompat.Name}
		extra := gatewayExtraBody(t, getProviderOptions(model, hyperCfg))
		require.Equal(t, true, extra["thinking"])
		require.NotContains(t, extra, "keep")
		require.NotContains(t, extra, "clear_thinking")
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

// TestGetProviderOptionsDeepSeekUnchanged freezes the options a DeepSeek model
// on an OpenAI-compatible route built before the vendor rules landed. DeepSeek
// has no flag, so its request must stay exactly as it was.
func TestGetProviderOptionsDeepSeekUnchanged(t *testing.T) {
	t.Parallel()

	// The model is shaped like the commandcode route: openai-compat,
	// base_url, reasoning on, and no user extra_body. The expected map was
	// taken from a dump of the code at origin/main, before this branch.
	model := Model{
		CatwalkCfg: catwalk.Model{
			ID: "deepseek-v4.1-flash",
			Reasoning: catwalk.Reasoning{
				Thinking:     catwalk.ThinkingToggleable,
				EffortLevels: catwalk.NewEffortLevels("low", "high", "max"),
			},
		},
		ModelCfg: config.SelectedModel{
			Provider:        "commandcode",
			Model:           "deepseek/deepseek-v4.1-flash",
			ReasoningEffort: "high",
		},
	}
	providerCfg := config.ProviderConfig{
		ID:      "commandcode",
		Type:    openaicompat.Name,
		BaseURL: "https://api.commandcode.ai/provider/v1",
	}

	require.Equal(t, fantasy.ProviderOptions{
		openaicompat.Name: &openaicompat.ProviderOptions{
			ReasoningEffort: ptr(openai.ReasoningEffort("high")),
			ExtraBody:       map[string]any{},
		},
	}, getProviderOptions(model, providerCfg))
}
