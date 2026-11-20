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

// flagsFor builds the extra body of a reasoning request for one model and
// provider.
func flagsFor(t *testing.T, providerCfg config.ProviderConfig, modelID string) map[string]any {
	t.Helper()
	return gatewayExtraBody(t, getProviderOptions(reasoningGatewayModel(modelID), providerCfg))
}

// withExtraBody sets the model's user extra_body.
func withExtraBody(model Model, extraBody map[string]any) Model {
	model.ModelCfg.ProviderOptions = map[string]any{"extra_body": extraBody}
	return model
}

func TestGetProviderOptionsVendorReasoningFlags(t *testing.T) {
	t.Parallel()

	zai := config.ProviderConfig{ID: string(catwalk.InferenceProviderZAI), Type: openaicompat.Name}
	hyperCfg := config.ProviderConfig{ID: hyper.Name, Type: openaicompat.Name}
	moonshot := config.ProviderConfig{ID: string(catwalk.InferenceProviderMoonshot), Type: openaicompat.Name}
	alibaba := config.ProviderConfig{ID: string(catwalk.InferenceProviderAlibabaSingapore), Type: openaicompat.Name}

	t.Run("glm joins zai's thinking object", func(t *testing.T) {
		t.Parallel()
		// Z.AI documents clear_thinking inside the thinking object, which is
		// what the zai provider already sets.
		thinking, ok := flagsFor(t, zai, "glm-5.3")["thinking"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, map[string]any{"type": "enabled", "clear_thinking": false}, thinking)
	})

	t.Run("glm falls back to a top-level flag beside boolean thinking", func(t *testing.T) {
		t.Parallel()
		// Hyper sends "thinking" as a boolean, which Crush sets itself. The
		// boolean is not rewritten; GLM's flag goes at the top level.
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
		extra := flagsFor(t, hyperCfg, "glm-5.3")
		require.Equal(t, false, extra["thinking"])
		require.Equal(t, false, extra["clear_thinking"])
	})

	t.Run("an effort of none gets no flag", func(t *testing.T) {
		t.Parallel()
		model := reasoningGatewayModel("glm-5.3")
		model.CatwalkCfg.Reasoning.EffortLevels = catwalk.NewEffortLevels("none", "max")
		model.ModelCfg.ReasoningEffort = "none"
		require.NotContains(t, gatewayExtraBody(t, getProviderOptions(model, hyperCfg)), "clear_thinking")
	})

	t.Run("a user's effort of none gets no flag", func(t *testing.T) {
		t.Parallel()
		model := withExtraBody(reasoningGatewayModel("glm-5.3"), map[string]any{"reasoning_effort": "none"})
		require.NotContains(t, gatewayExtraBody(t, getProviderOptions(model, hyperCfg)), "clear_thinking")
	})

	t.Run("a user's disabled thinking gets no flag", func(t *testing.T) {
		t.Parallel()
		disabled := map[string]any{"type": "disabled"}
		model := withExtraBody(reasoningGatewayModel("glm-5.3"), map[string]any{"thinking": disabled})
		extra := gatewayExtraBody(t, getProviderOptions(model, hyperCfg))
		require.NotContains(t, extra, "clear_thinking")
		require.Equal(t, disabled, extra["thinking"])
	})

	t.Run("kimi creates an enabled thinking object at moonshot", func(t *testing.T) {
		t.Parallel()
		thinking, ok := flagsFor(t, moonshot, "moonshotai/Kimi-K3")["thinking"].(map[string]any)
		require.True(t, ok, "kimi needs a thinking object")
		require.Equal(t, map[string]any{"type": "enabled", "keep": "all"}, thinking)
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
		require.Equal(t, true, flagsFor(t, alibaba, "qwen3.8-flash")["preserve_thinking"])
	})

	t.Run("families without a flag get none", func(t *testing.T) {
		t.Parallel()
		for _, id := range []string{"mimo-v2.6-pro", "deepseek/deepseek-v4.1-flash", "deepseek-r1-distill-qwen-32b"} {
			extra := flagsFor(t, moonshot, id)
			require.NotContains(t, extra, "thinking", id)
			require.NotContains(t, extra, "preserve_thinking", id)
		}
		require.True(t, vendorReasoningNeedsHistory("deepseek-r1-distill-qwen-32b"))
	})

	t.Run("a similar name is not glm", func(t *testing.T) {
		t.Parallel()
		for _, id := range []string{"chatglm-6b", "vendor/some-glm-4.6"} {
			require.NotContains(t, flagsFor(t, moonshot, id), "thinking", id)
			require.False(t, vendorReasoningNeedsHistory(id), id)
		}
	})

	t.Run("no flag when the request does not reason", func(t *testing.T) {
		t.Parallel()
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "kimi-k2.6"},
			ModelCfg:   config.SelectedModel{Provider: "moonshot", Model: "kimi-k2.6"},
		}
		require.NotContains(t, gatewayExtraBody(t, getProviderOptions(model, moonshot)), "thinking")
	})

	t.Run("hosts outside the allow-list get nothing", func(t *testing.T) {
		t.Parallel()
		hosts := []config.ProviderConfig{
			{ID: string(catwalk.InferenceProviderFireworks), Type: openaicompat.Name},
			{ID: string(catwalk.InferenceProviderIoNet), Type: openaicompat.Name},
			{ID: string(catwalk.InferenceProviderNeuralwatt), Type: openaicompat.Name},
			{ID: "commandcode", Type: openaicompat.Name, BaseURL: "https://api.commandcode.ai/provider/v1"},
		}
		for _, providerCfg := range hosts {
			require.NotContains(t, flagsFor(t, providerCfg, "glm-5.3"), "thinking", providerCfg.ID)
			require.NotContains(t, flagsFor(t, providerCfg, "qwen3.8-flash"), "preserve_thinking", providerCfg.ID)
		}
	})
}

// TestVendorFamily pins the family matching: the base model name decides,
// so a vendor prefix and another family's name in the id do not.
func TestVendorFamily(t *testing.T) {
	t.Parallel()
	families := map[string]string{
		"GLM-5.3":                      "glm",
		"z-ai/glm-5.3":                 "glm",
		"kimi-k2.6":                    "kimi",
		"moonshotai/Kimi-K3":           "kimi",
		"qwen3.8-flash":                "qwen",
		"mimo-v2.6-pro":                "mimo",
		"deepseek/deepseek-v4.1-flash": "deepseek",
		"deepseek-r1-distill-qwen-32b": "deepseek",
		"chatglm-6b":                   "",
		"vendor/some-glm-4.6":          "",
	}
	for id, want := range families {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, want, vendorFamily(id))
		})
	}
}

// TestGetProviderOptionsDeepSeekUnchanged freezes the options a DeepSeek model
// on an OpenAI-compatible route built before the vendor rules landed. DeepSeek
// has no flag, so its request must stay exactly as it was.
func TestGetProviderOptionsDeepSeekUnchanged(t *testing.T) {
	t.Parallel()

	// The model is shaped like the commandcode route: openai-compat,
	// base_url, reasoning on, and no user extra_body. The expected options
	// were produced by the code at origin/main 41dc556e and rechecked there
	// against this test.
	model := reasoningGatewayModel("deepseek/deepseek-v4.1-flash")
	model.CatwalkCfg.Reasoning.EffortLevels = catwalk.NewEffortLevels("low", "high", "max")
	model.ModelCfg.Provider = "commandcode"
	model.ModelCfg.ReasoningEffort = "max"
	providerCfg := config.ProviderConfig{
		ID:      "commandcode",
		Type:    openaicompat.Name,
		BaseURL: "https://api.commandcode.ai/provider/v1",
	}
	want := fantasy.ProviderOptions{
		openaicompat.Name: &openaicompat.ProviderOptions{
			ReasoningEffort: ptr(openai.ReasoningEffort("max")),
			ExtraBody:       map[string]any{},
		},
	}

	require.Equal(t, want, getProviderOptions(model, providerCfg))
	model.ModelCfg.Think = true
	require.Equal(t, want, getProviderOptions(model, providerCfg),
		"a thinking toggle changes nothing on this route")
}
