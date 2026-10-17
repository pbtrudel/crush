package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/discover"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSessionAgent is a minimal mock for the SessionAgent interface.
type mockSessionAgent struct {
	model     Model
	runFunc   func(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error)
	cancelled []string
	// tools records the most recent SetTools call, if any.
	tools []fantasy.AgentTool
}

func (m *mockSessionAgent) Run(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
	return m.runFunc(ctx, call)
}

func (m *mockSessionAgent) BeginAccepted(sessionID string) *AcceptedRun {
	return &AcceptedRun{sessionID: sessionID}
}

func (m *mockSessionAgent) Model() Model                 { return m.model }
func (m *mockSessionAgent) SetModels(large, small Model) {}
func (m *mockSessionAgent) SetTools(tools []fantasy.AgentTool) {
	m.tools = tools
}
func (m *mockSessionAgent) SetSystemPrompt(systemPrompt string) {}
func (m *mockSessionAgent) Cancel(sessionID string) {
	m.cancelled = append(m.cancelled, sessionID)
}
func (m *mockSessionAgent) CancelAll()                                  {}
func (m *mockSessionAgent) IsSessionBusy(sessionID string) bool         { return false }
func (m *mockSessionAgent) IsBusy() bool                                { return false }
func (m *mockSessionAgent) QueuedPrompts(sessionID string) int          { return 0 }
func (m *mockSessionAgent) QueuedPromptsList(sessionID string) []string { return nil }
func (m *mockSessionAgent) ClearQueue(sessionID string)                 {}
func (m *mockSessionAgent) Summarize(context.Context, string, fantasy.ProviderOptions, func(context.Context, *fantasy.ProviderError) error) error {
	return nil
}
func (m *mockSessionAgent) GenerateTitle(context.Context, string, string) {}

// newTestCoordinator creates a minimal coordinator for unit testing runSubAgent.
func newTestCoordinator(t *testing.T, env fakeEnv, providerID string, providerCfg config.ProviderConfig) *coordinator {
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().Providers.Set(providerID, providerCfg)
	return &coordinator{
		cfg:      cfg,
		sessions: env.sessions,
		messages: env.messages,
	}
}

// newMockAgent creates a mockSessionAgent with the given provider and run function.
func newMockAgent(providerID string, maxTokens int64, runFunc func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error)) *mockSessionAgent {
	return &mockSessionAgent{
		model: Model{
			CatwalkCfg: catwalk.Model{
				DefaultMaxTokens: maxTokens,
			},
			ModelCfg: config.SelectedModel{
				Provider: providerID,
			},
		},
		runFunc: runFunc,
	}
}

// agentResultWithText creates a minimal AgentResult with the given text response.
func agentResultWithText(text string) *fantasy.AgentResult {
	return &fantasy.AgentResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: text},
			},
		},
	}
}

func TestCopilotResponsesModels(t *testing.T) {
	t.Parallel()

	for _, modelID := range []string{
		"gpt-6-astra",
		"gpt-6-luna",
		"gpt-6-sol",
		"gpt-6.1-sol",
		"grok-4.5",
		"grok-4.6",
		"grok-4.7",
	} {
		assert.True(t, copilotResponsesModels[modelID], modelID)
	}
	assert.False(t, copilotResponsesModels["gpt-4.1"])
}

func TestRunSubAgent(t *testing.T) {
	const providerID = "test-provider"
	providerCfg := config.ProviderConfig{ID: providerID}

	t.Run("happy path", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := newMockAgent(providerID, 4096, func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
			assert.Equal(t, "do something", call.Prompt)
			assert.Equal(t, int64(4096), call.MaxOutputTokens)
			return agentResultWithText("done"), nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "do something",
			SessionTitle:   "Test Session",
		})
		require.NoError(t, err)
		assert.Equal(t, "done", resp.Content)
		assert.False(t, resp.IsError)
	})

	t.Run("cost update failure preserves output", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return agentResultWithText("output before cost failure"), nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      "missing-parent-session",
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.False(t, resp.IsError)
		assert.Equal(t, "output before cost failure", resp.Content)
	})

	t.Run("response with text returns it", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return agentResultWithText("the answer"), nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.False(t, resp.IsError)
		assert.Equal(t, "the answer", resp.Content)
	})

	t.Run("nil result returns error response", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return nil, nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Equal(t, "Sub-agent completed but produced no text output.", resp.Content)
	})

	t.Run("empty result returns error response", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return &fantasy.AgentResult{}, nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Equal(t, "Sub-agent completed but produced no text output.", resp.Content)
	})

	t.Run("ModelCfg.MaxTokens overrides default", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := &mockSessionAgent{
			model: Model{
				CatwalkCfg: catwalk.Model{
					DefaultMaxTokens: 4096,
				},
				ModelCfg: config.SelectedModel{
					Provider:  providerID,
					MaxTokens: 8192,
				},
			},
			runFunc: func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
				assert.Equal(t, int64(8192), call.MaxOutputTokens)
				return agentResultWithText("ok"), nil
			},
		}

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.Equal(t, "ok", resp.Content)
	})

	t.Run("session creation failure with canceled context", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := newMockAgent(providerID, 4096, nil)

		// Use a canceled context to trigger CreateTaskSession failure.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err = coord.runSubAgent(ctx, subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.Error(t, err)
	})

	t.Run("provider not configured", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		// Agent references a provider that doesn't exist in config.
		agent := newMockAgent("unknown-provider", 4096, nil)

		_, err = coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model provider not configured")
	})

	t.Run("agent run error returns error response", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return nil, errors.New("provider request failed")
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		// runSubAgent returns (errorResponse, nil) when agent.Run fails — not a Go error.
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Equal(t, "Failed to generate response: provider request failed", resp.Content)
	})

	t.Run("session setup callback is invoked", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		var setupCalledWith string
		agent := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return agentResultWithText("ok"), nil
		})

		_, err = coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
			SessionSetup: func(sessionID string) {
				setupCalledWith = sessionID
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, setupCalledWith, "SessionSetup should have been called")
	})

	t.Run("usage propagation to parent session", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent := newMockAgent(providerID, 4096, func(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
			// Simulate the agent incurring usage by updating the child session.
			childSession, err := env.sessions.Get(ctx, call.SessionID)
			if err != nil {
				return nil, err
			}
			childSession.Cost = 0.05
			childSession.TotalCacheReadTokens = 700
			childSession.TotalContextTokens = 900
			_, err = env.sessions.Save(ctx, childSession)
			if err != nil {
				return nil, err
			}
			return agentResultWithText("ok"), nil
		})

		_, err = coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)

		updated, err := env.sessions.Get(t.Context(), parentSession.ID)
		require.NoError(t, err)
		assert.InDelta(t, 0.05, updated.Cost, 1e-9)
		assert.Equal(t, int64(700), updated.TotalCacheReadTokens)
		assert.Equal(t, int64(900), updated.TotalContextTokens)
	})
}

// TestWithPromptCacheKeyWorkspaceScoped pins the routing key scope: it must
// be derived from the workspace, not the session, so that separate sessions,
// sub-agents, and compaction calls in one workspace all route to the same
// provider cache shard and reuse the shared tool and system-prompt prefixes.
func TestWithPromptCacheKeyWorkspaceScoped(t *testing.T) {
	env := testEnv(t)

	t.Run("openai uses the workspace hash", func(t *testing.T) {
		providerCfg := config.ProviderConfig{
			ID:   string(catwalk.InferenceProviderOpenAI),
			Type: openai.Name,
		}
		coord := newTestCoordinator(t, env, providerCfg.ID, providerCfg)

		opts := coord.withPromptCacheKey(fantasy.ProviderOptions{}, providerCfg)
		o, ok := opts[openai.Name].(*openai.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, o.PromptCacheKey)
		assert.Equal(t, session.HashID(coord.cfg.WorkingDir()), *o.PromptCacheKey)

		// Repeated calls - as any other session in this workspace would
		// make - derive the same key.
		again := coord.withPromptCacheKey(fantasy.ProviderOptions{}, providerCfg)
		againOpts, ok := again[openai.Name].(*openai.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, againOpts.PromptCacheKey)
		assert.Equal(t, *o.PromptCacheKey, *againOpts.PromptCacheKey)
	})

	t.Run("mistral carries the key in the extra body", func(t *testing.T) {
		providerCfg := config.ProviderConfig{
			ID:      "mistral",
			BaseURL: "https://api.mistral.ai",
		}
		coord := newTestCoordinator(t, env, providerCfg.ID, providerCfg)

		opts := coord.withPromptCacheKey(fantasy.ProviderOptions{}, providerCfg)
		o, ok := opts[openaicompat.Name].(*openaicompat.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, o.ExtraBody)
		assert.Equal(t, session.HashID(coord.cfg.WorkingDir()), o.ExtraBody["prompt_cache_key"])
	})
}

func TestUpdateParentSessionUsage(t *testing.T) {
	t.Run("accumulates cost and cache usage", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		child, err := env.sessions.CreateTaskSession(t.Context(), "tool-1", parent.ID, "Child")
		require.NoError(t, err)

		child.Cost = 0.10
		child.TotalCacheReadTokens = 700
		child.TotalContextTokens = 900
		_, err = env.sessions.Save(t.Context(), child)
		require.NoError(t, err)

		err = coord.updateParentSessionUsage(t.Context(), child.ID, parent.ID)
		require.NoError(t, err)

		updated, err := env.sessions.Get(t.Context(), parent.ID)
		require.NoError(t, err)
		assert.InDelta(t, 0.10, updated.Cost, 1e-9)
		assert.Equal(t, int64(700), updated.TotalCacheReadTokens)
		assert.Equal(t, int64(900), updated.TotalContextTokens)
	})

	t.Run("accumulates multiple child costs", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		child1, err := env.sessions.CreateTaskSession(t.Context(), "tool-1", parent.ID, "Child1")
		require.NoError(t, err)
		child1.Cost = 0.05
		child1.TotalCacheReadTokens = 700
		child1.TotalContextTokens = 900
		_, err = env.sessions.Save(t.Context(), child1)
		require.NoError(t, err)

		child2, err := env.sessions.CreateTaskSession(t.Context(), "tool-2", parent.ID, "Child2")
		require.NoError(t, err)
		child2.Cost = 0.03
		child2.TotalCacheReadTokens = 300
		child2.TotalContextTokens = 400
		_, err = env.sessions.Save(t.Context(), child2)
		require.NoError(t, err)

		err = coord.updateParentSessionUsage(t.Context(), child1.ID, parent.ID)
		require.NoError(t, err)
		err = coord.updateParentSessionUsage(t.Context(), child2.ID, parent.ID)
		require.NoError(t, err)

		updated, err := env.sessions.Get(t.Context(), parent.ID)
		require.NoError(t, err)
		assert.InDelta(t, 0.08, updated.Cost, 1e-9)
		assert.Equal(t, int64(1000), updated.TotalCacheReadTokens)
		assert.Equal(t, int64(1300), updated.TotalContextTokens)
	})

	t.Run("child session not found", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		err = coord.updateParentSessionUsage(t.Context(), "non-existent", parent.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get child session")
	})

	t.Run("parent session not found", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		child, err := env.sessions.CreateTaskSession(t.Context(), "tool-1", parent.ID, "Child")
		require.NoError(t, err)

		err = coord.updateParentSessionUsage(t.Context(), child.ID, "non-existent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get parent session")
	})

	t.Run("zero cost handled correctly", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		child, err := env.sessions.CreateTaskSession(t.Context(), "tool-1", parent.ID, "Child")
		require.NoError(t, err)

		err = coord.updateParentSessionUsage(t.Context(), child.ID, parent.ID)
		require.NoError(t, err)

		updated, err := env.sessions.Get(t.Context(), parent.ID)
		require.NoError(t, err)
		assert.InDelta(t, 0.0, updated.Cost, 1e-9)
	})
}

// TestUpdateParentSessionUsageConcurrent rolls up many child sessions
// at the same time, as parallel task/fetch sub-agents do. Every child's
// usage must land on the parent exactly once.
func TestUpdateParentSessionUsageConcurrent(t *testing.T) {
	env := testEnv(t)
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	coord := &coordinator{cfg: cfg, sessions: env.sessions}

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	const children = 32
	var wantCache, wantContext int64
	var wantCost float64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range children {
		child, err := env.sessions.CreateTaskSession(t.Context(), fmt.Sprintf("tool-%d", i), parent.ID, "Child")
		require.NoError(t, err)
		child.Cost = 0.01
		child.TotalCacheReadTokens = 100
		child.TotalContextTokens = 200
		_, err = env.sessions.Save(t.Context(), child)
		require.NoError(t, err)
		wantCache += 100
		wantContext += 200
		wantCost += 0.01

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := coord.updateParentSessionUsage(t.Context(), child.ID, parent.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()

	updated, err := env.sessions.Get(t.Context(), parent.ID)
	require.NoError(t, err)
	assert.Equal(t, wantCache, updated.TotalCacheReadTokens)
	assert.Equal(t, wantContext, updated.TotalContextTokens)
	assert.InDelta(t, wantCost, updated.Cost, 1e-9)
}

func TestGetProviderOptionsReasoningEffort(t *testing.T) {
	// Bedrock is Fantasy's Anthropic under a different provider name; options
	// must land under anthropic.Name so the Anthropic language model picks them up.
	tests := []struct {
		name         string
		providerType catwalk.Type
	}{
		{"anthropic honors reasoning_effort", catwalk.Type(anthropic.Name)},
		{"bedrock honors reasoning_effort", catwalk.Type(bedrock.Name)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := Model{
				CatwalkCfg: catwalk.Model{
					ID:              "claude-opus-4-7",
					CanReason:       true,
					ReasoningLevels: []string{"max"},
				},
				ModelCfg: config.SelectedModel{
					Provider:        "test",
					ReasoningEffort: "max",
				},
			}
			providerCfg := config.ProviderConfig{ID: "test", Type: tc.providerType}

			opts := getProviderOptions(model, providerCfg)

			raw, ok := opts[anthropic.Name]
			require.True(t, ok, "options should be keyed under anthropic.Name for type %q", tc.providerType)
			parsed, ok := raw.(*anthropic.ProviderOptions)
			require.True(t, ok)
			require.NotNil(t, parsed.Effort)
			assert.Equal(t, anthropic.Effort("max"), *parsed.Effort)
		})
	}
}

func TestIsUnauthorized(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.False(t, isUnauthorized(nil))
	})

	t.Run("non-provider error", func(t *testing.T) {
		assert.False(t, isUnauthorized(errors.New("something broke")))
	})

	t.Run("provider error with 401", func(t *testing.T) {
		err := &fantasy.ProviderError{StatusCode: http.StatusUnauthorized, Message: "unauthorized"}
		assert.True(t, isUnauthorized(err))
	})

	t.Run("provider error with non-401", func(t *testing.T) {
		err := &fantasy.ProviderError{StatusCode: http.StatusForbidden, Message: "forbidden"}
		assert.False(t, isUnauthorized(err))
	})

	t.Run("wrapped provider error with 401", func(t *testing.T) {
		inner := &fantasy.ProviderError{StatusCode: http.StatusUnauthorized, Message: "expired"}
		err := fmt.Errorf("request failed: %w", inner)
		assert.True(t, isUnauthorized(err))
	})
}

func TestGetProviderOptionsReasoningEffortCustomProvider(t *testing.T) {
	// Custom local providers (lmstudio, ollama, omlx, litellm, llamacpp)
	// go through the OpenAI-compat client and must receive the selected
	// reasoning effort like any other OpenAI-compatible provider.
	for _, providerType := range discover.RegisteredProviderTypes() {
		t.Run(providerType, func(t *testing.T) {
			model := Model{
				CatwalkCfg: catwalk.Model{
					ID:              "qwen/qwen3-8b",
					CanReason:       true,
					ReasoningLevels: []string{"low", "medium", "high"},
				},
				ModelCfg: config.SelectedModel{
					Provider:        "local",
					Model:           "qwen/qwen3-8b",
					ReasoningEffort: "high",
				},
			}
			providerCfg := config.ProviderConfig{ID: "local", Type: catwalk.Type(providerType)}

			opts := getProviderOptions(model, providerCfg)

			raw, ok := opts[openaicompat.Name]
			require.True(t, ok, "options should be keyed under openaicompat.Name for type %q", providerType)
			parsed, ok := raw.(*openaicompat.ProviderOptions)
			require.True(t, ok)
			require.NotNil(t, parsed.ReasoningEffort)
			assert.Equal(t, "high", string(*parsed.ReasoningEffort))
		})
	}
}

func TestGetProviderOptionsReasoningEffortFallback(t *testing.T) {
	model := Model{
		CatwalkCfg: catwalk.Model{
			ID:              "glm-5.2",
			CanReason:       true,
			ReasoningLevels: []string{"high", "max"},
		},
		ModelCfg: config.SelectedModel{
			Provider: "zai",
		},
	}
	providerCfg := config.ProviderConfig{
		ID:   string(catwalk.InferenceProviderZAI),
		Type: openaicompat.Name,
	}

	opts := getProviderOptions(model, providerCfg)

	raw, ok := opts[openaicompat.Name]
	require.True(t, ok)
	parsed, ok := raw.(*openaicompat.ProviderOptions)
	require.True(t, ok)
	require.NotNil(t, parsed.ReasoningEffort)
	assert.Equal(t, "high", string(*parsed.ReasoningEffort))

	thinking, ok := parsed.ExtraBody["thinking"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "enabled", thinking["type"])
}

func TestGetProviderOptionsTopKExtraBody(t *testing.T) {
	// "ollama" has a registered discover.Enricher, so it is treated as a
	// known custom provider speaking openai-compat.
	knownCustomProviderCfg := config.ProviderConfig{ID: "ollama", Type: "ollama"}

	t.Run("model top_k is injected into extra_body for known custom providers", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "llama3"},
			ModelCfg:   config.SelectedModel{Provider: "ollama", TopK: ptr(int64(40))},
		}

		opts := getProviderOptions(model, knownCustomProviderCfg)

		raw, ok := opts[openaicompat.Name]
		require.True(t, ok)
		parsed, ok := raw.(*openaicompat.ProviderOptions)
		require.True(t, ok)
		topK, ok := parsed.ExtraBody["top_k"].(int64)
		require.True(t, ok)
		assert.Equal(t, int64(40), topK)
	})

	t.Run("falls back to catwalk top_k when the model config has none", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{
				ID:      "llama3",
				Options: catwalk.ModelOptions{TopK: ptr(int64(64))},
			},
			ModelCfg: config.SelectedModel{Provider: "ollama"},
		}

		opts := getProviderOptions(model, knownCustomProviderCfg)

		raw, ok := opts[openaicompat.Name]
		require.True(t, ok)
		parsed, ok := raw.(*openaicompat.ProviderOptions)
		require.True(t, ok)
		topK, ok := parsed.ExtraBody["top_k"].(int64)
		require.True(t, ok)
		assert.Equal(t, int64(64), topK)
	})

	t.Run("does not set extra_body when no top_k is configured anywhere", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "llama3"},
			ModelCfg:   config.SelectedModel{Provider: "ollama"},
		}

		opts := getProviderOptions(model, knownCustomProviderCfg)

		raw, ok := opts[openaicompat.Name]
		require.True(t, ok)
		parsed, ok := raw.(*openaicompat.ProviderOptions)
		require.True(t, ok)
		_, hasTopK := parsed.ExtraBody["top_k"]
		assert.False(t, hasTopK)
	})

	t.Run("does not overwrite an explicitly configured extra_body.top_k", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "llama3"},
			ModelCfg: config.SelectedModel{
				Provider: "ollama",
				TopK:     ptr(int64(40)),
				ProviderOptions: map[string]any{
					"extra_body": map[string]any{"top_k": 7},
				},
			},
		}

		opts := getProviderOptions(model, knownCustomProviderCfg)

		raw, ok := opts[openaicompat.Name]
		require.True(t, ok)
		parsed, ok := raw.(*openaicompat.ProviderOptions)
		require.True(t, ok)
		assert.EqualValues(t, 7, parsed.ExtraBody["top_k"])
	})

	t.Run("is not injected for providers outside the known-custom-provider default branch", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "glm-5.2"},
			ModelCfg:   config.SelectedModel{Provider: "zai", TopK: ptr(int64(40))},
		}
		providerCfg := config.ProviderConfig{ID: string(catwalk.InferenceProviderZAI), Type: openaicompat.Name}

		opts := getProviderOptions(model, providerCfg)

		raw, ok := opts[openaicompat.Name]
		require.True(t, ok)
		parsed, ok := raw.(*openaicompat.ProviderOptions)
		require.True(t, ok)
		_, hasTopK := parsed.ExtraBody["top_k"]
		assert.False(t, hasTopK)
	})
}

func TestGetProviderOptionsMalformedFallback(t *testing.T) {
	model := Model{
		CatwalkCfg: catwalk.Model{ID: "llama3"},
		ModelCfg: config.SelectedModel{
			Provider:        "ollama",
			TopK:            ptr(int64(40)),
			ProviderOptions: map[string]any{"user": 5.0},
		},
	}
	providerCfg := config.ProviderConfig{ID: "test", Type: "ollama"}

	opts := getProviderOptions(model, providerCfg)

	raw, ok := opts[openaicompat.Name]
	require.True(t, ok, "malformed provider_options should still fall back to top_k")
	parsed, ok := raw.(*openaicompat.ProviderOptions)
	require.True(t, ok)

	// The malformed fields are dropped; only the injected top_k survives.
	assert.Nil(t, parsed.User)
	assert.Nil(t, parsed.ReasoningEffort)
	require.Len(t, parsed.ExtraBody, 1)
	topK, ok := parsed.ExtraBody["top_k"].(int64)
	require.True(t, ok)
	assert.Equal(t, int64(40), topK)
}

func TestCallTopK(t *testing.T) {
	knownCustomProviderCfg := config.ProviderConfig{ID: "ollama", Type: "ollama"}

	tests := []struct {
		name        string
		providerCfg config.ProviderConfig
		want        *int64
	}{
		{
			name:        "suppressed for known custom providers",
			providerCfg: knownCustomProviderCfg,
			want:        nil,
		},
		{
			name:        "passed through for openai-compat hosted providers",
			providerCfg: config.ProviderConfig{ID: "zai", Type: openaicompat.Name},
			want:        ptr(int64(40)),
		},
		{
			name:        "passed through for anthropic",
			providerCfg: config.ProviderConfig{ID: "anthropic", Type: catwalk.Type(anthropic.Name)},
			want:        ptr(int64(40)),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := callTopK(tc.providerCfg, ptr(int64(40)))
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tc.want, *got)
		})
	}

	t.Run("nil input stays nil regardless of provider", func(t *testing.T) {
		assert.Nil(t, callTopK(knownCustomProviderCfg, nil))
		assert.Nil(t, callTopK(config.ProviderConfig{Type: openaicompat.Name}, nil))
	})
}

func ptr[T any](v T) *T { return &v }

func TestCoordinatorSetMainAgent(t *testing.T) {
	t.Run("switches current agent", func(t *testing.T) {
		coder := &mockSessionAgent{}
		plan := &mockSessionAgent{}
		coord := &coordinator{
			mainAgent:     coder,
			mainAgentName: config.AgentCoder,
			agents: map[string]SessionAgent{
				config.AgentCoder: coder,
				config.AgentPlan:  plan,
			},
		}

		err := coord.SetMainAgent(config.AgentPlan)
		require.NoError(t, err)
		assert.Equal(t, config.AgentPlan, coord.mainAgentName)
		assert.Same(t, plan, coord.mainAgent)
	})

	t.Run("returns error for unknown agent", func(t *testing.T) {
		coord := &coordinator{
			agents: map[string]SessionAgent{
				config.AgentCoder: &mockSessionAgent{},
			},
		}

		err := coord.SetMainAgent("unknown")
		require.Error(t, err)
		assert.ErrorIs(t, err, errMainAgentNotFound)
	})
}
