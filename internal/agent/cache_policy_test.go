package agent

import (
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/azure"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"charm.land/fantasy/providers/openrouter"
	"charm.land/fantasy/providers/vercel"
	"github.com/charmbracelet/crush/internal/agent/hyper"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestCachePolicyMarksRequest is the acceptance table: for each provider type
// and model id, resolve the cache policy and apply it to one request, then
// check which messages and which tools carry provider options. Messages are
// [system, one, two, three] in order and tools are [a, b], where b is last.
//
// The provider type is the one that serves the request (see
// servingProvider), so a Claude model behind Copilot or the OpenCode
// gateways arrives here as "anthropic" and keeps the full layout, while a
// gateway forwarding to a model with different caching rules gets that
// rule instead.
func TestCachePolicyMarksRequest(t *testing.T) {
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "test prompt").(*sessionAgent)

	tests := []struct {
		name         string
		providerType string
		modelID      string
		wantMessages []bool
		wantTools    []bool
	}{
		{"direct anthropic", anthropic.Name, "claude-sonnet-4-5", []bool{true, false, true, true}, []bool{false, true}},
		{"claude through copilot uses the anthropic provider", anthropic.Name, "claude-sonnet-4", []bool{true, false, true, true}, []bool{false, true}},
		{"bedrock claude", bedrock.Name, "us.anthropic.claude-sonnet-4-5-20250929-v1:0", []bool{true, false, true, true}, []bool{false, true}},
		{"vertex claude", google.Name, "claude-sonnet-4@20250514", []bool{true, false, true, true}, []bool{false, true}},
		{"direct gemini", google.Name, "gemini-2.5-pro", []bool{true, false, false, false}, []bool{false, false}},
		{"openrouter anthropic", openrouter.Name, "anthropic/claude-sonnet-4.5", []bool{true, false, true, true}, []bool{false, true}},
		{"openrouter qwen", openrouter.Name, "qwen/qwen3-coder-plus", []bool{true, false, false, true}, []bool{false, false}},
		{"openrouter alibaba qwen", openrouter.Name, "alibaba/qwen3-coder-plus", []bool{true, false, false, true}, []bool{false, false}},
		{"openrouter gemini", openrouter.Name, "google/gemini-2.5-pro", []bool{true, false, false, false}, []bool{false, false}},
		{"openrouter openai caches automatically", openrouter.Name, "openai/gpt-5", []bool{false, false, false, false}, []bool{false, false}},
		{"vercel anthropic", vercel.Name, "anthropic/claude-sonnet-4.5", []bool{true, false, true, true}, []bool{false, true}},
		{"vercel qwen", vercel.Name, "qwen/qwen3-coder", []bool{true, false, false, true}, []bool{false, false}},
		{"vercel other upstream", vercel.Name, "meta/llama-4-maverick", []bool{false, false, false, false}, []bool{false, false}},
		{"openai caches automatically", openai.Name, "gpt-5", []bool{false, false, false, false}, []bool{false, false}},
		{"azure caches automatically", azure.Name, "gpt-4o", []bool{false, false, false, false}, []bool{false, false}},
		{"openai compatible caches automatically", openaicompat.Name, "deepseek-chat", []bool{false, false, false, false}, []bool{false, false}},
		{"hyper caches automatically", hyper.Name, "hyper-model", []bool{false, false, false, false}, []bool{false, false}},
		{"custom provider type gets nothing", "litellm", "claude-sonnet-4", []bool{false, false, false, false}, []bool{false, false}},
		{"unset provider gets nothing", "", "some-model", []bool{false, false, false, false}, []bool{false, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := cachePolicyFor(tt.providerType, tt.modelID)
			messages := []fantasy.Message{
				fantasy.NewSystemMessage("system"),
				fantasy.NewUserMessage("one"),
				fantasy.NewUserMessage("two"),
				fantasy.NewUserMessage("three"),
			}
			tools := []*recordingTool{{name: "a"}, {name: "b"}}
			agent.applyCacheBreakpoints(messages, policy)
			agent.applyToolCacheBreakpoint([]fantasy.AgentTool{tools[0], tools[1]}, policy)

			for i, want := range tt.wantMessages {
				require.Equal(t, want, len(messages[i].ProviderOptions) > 0, "message %d", i)
			}
			for i, want := range tt.wantTools {
				require.Equal(t, want, len(tools[i].opts) > 0, "tool %d", i)
			}
		})
	}
}

// TestCachePolicyForModel pins where the resolver reads the provider and the
// model from: the serving provider recorded when the model was built, with
// the configured provider id as the fallback for models built by hand.
func TestCachePolicyForModel(t *testing.T) {
	t.Parallel()

	require.Equal(t, cachePolicyFull, cachePolicyForModel(Model{
		ServingProvider: anthropic.Name,
		ModelCfg:        config.SelectedModel{Provider: vercel.Name, Model: "anthropic/claude-sonnet-4.5"},
	}), "the serving provider wins over the configured provider id")

	require.Equal(t, cachePolicyFull, cachePolicyForModel(Model{
		ModelCfg: config.SelectedModel{Provider: anthropic.Name, Model: "claude-sonnet-4-5"},
	}), "a model built without a serving provider falls back to its provider id")

	require.Equal(t, cachePolicyNone, cachePolicyForModel(Model{}))
}
