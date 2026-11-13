package agent

import (
	"path/filepath"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
)

func TestTeammateRunRegistersInAgentRunRegistry(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	path := filepath.Join(t.TempDir(), "agents.json")
	coord.agentRuns = agentrun.New(path)

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	sess, err := coord.sessions.CreateTaskSession(t.Context(), "team-member-1", lead.ID, "Teammate reviewer")
	require.NoError(t, err)
	member := agentteam.Member{ID: "member-1", Name: "reviewer", Role: agentteam.RoleTeammate, ParentID: lead.ID}

	coord.startTeammateRun(sess, member)

	runs := agentrun.Load(path)
	require.Len(t, runs, 1)
	require.Equal(t, agentrun.KindTeammate, runs[0].Kind)
	require.Equal(t, "reviewer", runs[0].Name)
	require.Equal(t, lead.ID, runs[0].ParentSessionID)
	require.Equal(t, agentrun.StatusRunning, runs[0].Status)

	coord.finishAgentRun(sess.ID, nil)

	runs = agentrun.Load(path)
	require.Len(t, runs, 1)
	require.Equal(t, agentrun.StatusDone, runs[0].Status)
}

func TestTeammateSessionIDDoesNotCollideWithAnOlderTeam(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, nil)
	require.NoError(t, err)
	coord.team = service

	lead, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	_, err = coord.sessions.CreateTaskSession(t.Context(), "team-member-1", lead.ID, "Old teammate")
	require.NoError(t, err)

	runner := &teamRunner{c: coord}
	sess, err := runner.teammateSession(t.Context(), agentteam.Member{
		ID: "member-1", Name: "reviewer", ParentID: lead.ID,
	})
	require.NoError(t, err)
	require.NotEqual(t, "team-member-1", sess.ID, "a new team must not reuse an old teammate session id")
}

// TestTeammateAgentRunsOnModelOverride pins that a teammate with a persisted
// override builds its agent on that model on every run, and that an override
// the config can no longer serve is an error rather than a silent fallback to
// the default model.
func TestTeammateAgentRunsOnModelOverride(t *testing.T) {
	coord := newTeammateTestCoordinator(t)

	mockProvider, ok := coord.cfg.Config().Providers.Get("mock")
	require.True(t, ok)
	mockProvider.Models = append(mockProvider.Models, catwalk.Model{
		ID:               "mock-reasoner",
		Name:             "Mock Reasoner",
		ContextWindow:    8192,
		DefaultMaxTokens: 128,
		Reasoning: catwalk.Reasoning{
			Thinking:           catwalk.ThinkingToggleable,
			EffortLevels:       catwalk.NewEffortLevels("low", "high"),
			DefaultEffortLevel: "low",
		},
	})
	coord.cfg.Config().Providers.Set("mock", mockProvider)
	coord.cfg.Config().Providers.Set("other", config.ProviderConfig{
		ID:      "other",
		Name:    "Other",
		Type:    "openai",
		BaseURL: "http://127.0.0.1:9/v1",
		APIKey:  "test-key",
		Models: []catwalk.Model{{
			ID: "other-model", Name: "Other", ContextWindow: 8192, DefaultMaxTokens: 128,
		}},
	})
	runner := &teamRunner{c: coord}

	overridden, err := runner.teammateAgent(t.Context(), agentteam.Member{
		Name: "reviewer", Role: agentteam.RoleTeammate,
		Model: "other-model", ModelProvider: "other",
	})
	require.NoError(t, err)
	require.Equal(t, "other-model", overridden.Model().ModelCfg.Model)
	require.Equal(t, "other", overridden.Model().ModelCfg.Provider)

	// Reasoning recorded at spawn is re-applied on the rebuilt model.
	withReasoning, err := runner.teammateAgent(t.Context(), agentteam.Member{
		Name: "reviewer", Role: agentteam.RoleTeammate,
		Model: "mock-reasoner", ModelProvider: "mock", Reasoning: "high",
	})
	require.NoError(t, err)
	require.Equal(t, "high", withReasoning.Model().ModelCfg.ReasoningEffort)

	// A member without an override keeps the subagent default.
	plain, err := runner.teammateAgent(t.Context(), agentteam.Member{
		Name: "reviewer", Role: agentteam.RoleTeammate,
	})
	require.NoError(t, err)
	require.Equal(t, coord.subAgentModels().Large.ModelCfg, plain.Model().ModelCfg)

	_, err = runner.teammateAgent(t.Context(), agentteam.Member{
		Name: "reviewer", Role: agentteam.RoleTeammate,
		Model: "other-model", ModelProvider: "gone",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `provider "gone" is not available`)

	_, err = runner.teammateAgent(t.Context(), agentteam.Member{
		Name: "reviewer", Role: agentteam.RoleTeammate,
		Model: "gone-model", ModelProvider: "other",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `model "gone-model" is not available from provider "other"`)
}
