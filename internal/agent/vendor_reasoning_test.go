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

	zai := config.ProviderConfig{ID: string(catwalk.InferenceProviderZAI), Type: openaicompat.Name}
	hyperCfg := config.ProviderConfig{ID: hyper.Name, Type: openaicompat.Name}
	neuralwatt := config.ProviderConfig{ID: string(catwalk.InferenceProviderNeuralwatt), Type: openaicompat.Name}
	alibaba := config.ProviderConfig{
		ID:   string(catwalk.InferenceProviderAlibabaSingapore),
		Type: openaicompat.Name,
	}

	t.Run("glm joins zai's thinking object", func(t *testing.T) {
		t.Parallel()
		// Z.AI documents clear_thinking inside the thinking object, which is
		// what the zai provider already sets.
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("glm-5.3"), zai))
		thinking, ok := extra["thinking"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "enabled", thinking["type"])
		require.Equal(t, false, thinking["clear_thinking"])
	})

	t.Run("glm falls back to a top-level flag beside boolean thinking", func(t *testing.T) {
		t.Parallel()
		// Hyper sends "thinking" as a boolean, which Crush sets itself. The
		// boolean is not rewritten; GLM's flag goes at the top level, which
		// the host may forward.
		model := reasoningGatewayModel("glm-5.3")
		model.ModelCfg.Think = true
		extra := gatewayExtraBody(t, getProviderOptions(model, hyperCfg))
		require.Equal(t, true, extra["thinking"])
		require.Equal(t, false, extra["clear_thinking"])
	})

	t.Run("hyper with thinking off but an effort sent still reasons", func(t *testing.T) {
		t.Parallel()
		// Hyper's capture sent thinking:false with effort max and the model
		// still returned reasoning, so an effort alone asks for it.
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("glm-5.3"), hyperCfg))
		require.Equal(t, false, extra["thinking"])
		require.Equal(t, false, extra["clear_thinking"])
	})

	t.Run("an effort of none gets no flag", func(t *testing.T) {
		t.Parallel()
		model := reasoningGatewayModel("glm-5.3")
		model.CatwalkCfg.Reasoning.EffortLevels = catwalk.NewEffortLevels("none", "max")
		model.ModelCfg.ReasoningEffort = "none"
		extra := gatewayExtraBody(t, getProviderOptions(model, hyperCfg))
		require.NotContains(t, extra, "clear_thinking")
	})

	t.Run("a user's effort of none gets no flag", func(t *testing.T) {
		t.Parallel()
		model := reasoningGatewayModel("glm-5.3")
		model.ModelCfg.ProviderOptions = map[string]any{
			"extra_body": map[string]any{"reasoning_effort": "none"},
		}
		extra := gatewayExtraBody(t, getProviderOptions(model, hyperCfg))
		require.NotContains(t, extra, "clear_thinking")
	})

	t.Run("a user's disabled thinking gets no flag", func(t *testing.T) {
		t.Parallel()
		model := reasoningGatewayModel("glm-5.3")
		model.ModelCfg.ProviderOptions = map[string]any{
			"extra_body": map[string]any{"thinking": map[string]any{"type": "disabled"}},
		}
		extra := gatewayExtraBody(t, getProviderOptions(model, hyperCfg))
		require.NotContains(t, extra, "clear_thinking")
		require.Equal(t, map[string]any{"type": "disabled"}, extra["thinking"])
	})

	t.Run("kimi creates an enabled thinking object at neuralwatt", func(t *testing.T) {
		t.Parallel()
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("moonshotai/Kimi-K3"), neuralwatt))
		thinking, ok := extra["thinking"].(map[string]any)
		require.True(t, ok, "kimi needs a thinking object")
		require.Equal(t, "enabled", thinking["type"])
		require.Equal(t, "all", thinking["keep"])
	})

	t.Run("kimi on boolean thinking gets nothing", func(t *testing.T) {
		t.Parallel()
		// Kimi has no top-level form for thinking.keep, so a boolean
		// thinking leaves the flag out rather than guessing a shape.
		model := reasoningGatewayModel("kimi-k2.6")
		model.ModelCfg.Think = true
		extra := gatewayExtraBody(t, getProviderOptions(model, hyperCfg))
		require.Equal(t, true, extra["thinking"])
		require.NotContains(t, extra, "keep")
	})

	t.Run("qwen reads earlier thinking", func(t *testing.T) {
		t.Parallel()
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("qwen3.8-flash"), alibaba))
		require.Equal(t, true, extra["preserve_thinking"])
	})

	t.Run("mimo needs no flag", func(t *testing.T) {
		t.Parallel()
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("mimo-v2.6-pro"), neuralwatt))
		require.NotContains(t, extra, "thinking")
		require.NotContains(t, extra, "preserve_thinking")
	})

	t.Run("another family gets none", func(t *testing.T) {
		t.Parallel()
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("deepseek-v4.1-flash"), neuralwatt))
		require.NotContains(t, extra, "thinking")
		require.NotContains(t, extra, "preserve_thinking")
	})

	t.Run("a distill model takes its own family's rule", func(t *testing.T) {
		t.Parallel()
		// The name carries qwen, but the model is DeepSeek's own: it must not
		// get the qwen flag and must keep its history.
		extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("deepseek-r1-distill-qwen-32b"), neuralwatt))
		require.NotContains(t, extra, "preserve_thinking")
		require.NotContains(t, extra, "thinking")
		require.True(t, vendorReasoningNeedsHistory("deepseek-r1-distill-qwen-32b"))
	})

	t.Run("a similar name is not glm", func(t *testing.T) {
		t.Parallel()
		for _, id := range []string{"chatglm-6b", "vendor/some-glm-4.6"} {
			extra := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel(id), neuralwatt))
			require.NotContains(t, extra, "thinking", id)
			require.False(t, vendorReasoningNeedsHistory(id), id)
		}
	})

	t.Run("no flag when the request does not reason", func(t *testing.T) {
		t.Parallel()
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "kimi-k2.6"},
			ModelCfg:   config.SelectedModel{Provider: "neuralwatt", Model: "kimi-k2.6"},
		}
		extra := gatewayExtraBody(t, getProviderOptions(model, neuralwatt))
		require.NotContains(t, extra, "thinking")
	})

	t.Run("hosts outside the allow-list get nothing", func(t *testing.T) {
		t.Parallel()
		hosts := map[string]config.ProviderConfig{
			"fireworks": {ID: string(catwalk.InferenceProviderFireworks), Type: openaicompat.Name},
			"ionet":     {ID: string(catwalk.InferenceProviderIoNet), Type: openaicompat.Name},
			"commandcode": {
				ID:      "commandcode",
				Type:    openaicompat.Name,
				BaseURL: "https://api.commandcode.ai/provider/v1",
			},
		}
		for name, providerCfg := range hosts {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				glm := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("glm-5.3"), providerCfg))
				require.NotContains(t, glm, "thinking")
				require.NotContains(t, glm, "clear_thinking")
				qwen := gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel("qwen3.8-flash"), providerCfg))
				require.NotContains(t, qwen, "preserve_thinking")
			})
		}
	})
}

// TestVendorFamily pins the family matching: the base model name decides,
// so a vendor prefix and another family's name in the id do not.
func TestVendorFamily(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id   string
		want string
	}{
		{"GLM-5.3", "glm"},
		{"z-ai/glm-5.3", "glm"},
		{"kimi-k2.6", "kimi"},
		{"moonshotai/Kimi-K3", "kimi"},
		{"qwen3.8-flash", "qwen"},
		{"mimo-v2.6-pro", "mimo"},
		{"deepseek/deepseek-v4.1-flash", "deepseek"},
		{"deepseek-r1-distill-qwen-32b", "deepseek"},
		{"chatglm-6b", ""},
		{"vendor/some-glm-4.6", ""},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, vendorFamily(tt.id))
		})
	}
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
