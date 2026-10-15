package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/openrouter"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func TestUpdateSessionTokenCountersAccumulatesCacheTotals(t *testing.T) {
	t.Parallel()

	sess := &session.Session{
		PromptTokens:         150,
		CompletionTokens:     20,
		TotalCacheReadTokens: 50,
		TotalContextTokens:   150,
	}

	updateSessionTokenCounters(sess, fantasy.Usage{
		InputTokens:         10,
		CacheReadTokens:     200,
		CacheCreationTokens: 5,
		OutputTokens:        30,
	})

	// Snapshots reflect the latest step.
	require.Equal(t, int64(215), sess.PromptTokens)
	require.Equal(t, int64(30), sess.CompletionTokens)
	// Cache totals accumulate across steps.
	require.Equal(t, int64(250), sess.TotalCacheReadTokens)
	require.Equal(t, int64(365), sess.TotalContextTokens)

	// A zero-usage step changes neither snapshot nor totals.
	updateSessionTokenCounters(sess, fantasy.Usage{})
	require.Equal(t, int64(215), sess.PromptTokens)
	require.Equal(t, int64(250), sess.TotalCacheReadTokens)
	require.Equal(t, int64(365), sess.TotalContextTokens)
}

func TestUpdateSessionUsageClampsNegativeCacheCounters(t *testing.T) {
	t.Parallel()

	var a sessionAgent
	sess := &session.Session{}
	a.updateSessionUsage(Model{}, sess, fantasy.Usage{
		InputTokens:         100,
		CacheReadTokens:     -50,
		CacheCreationTokens: -10,
		OutputTokens:        5,
	}, nil, true)

	require.Equal(t, int64(0), sess.TotalCacheReadTokens)
	require.Equal(t, int64(100), sess.TotalContextTokens)
	require.Equal(t, int64(100), sess.PromptTokens)
	require.Equal(t, int64(5), sess.CompletionTokens)
}

func TestTokenUsageCost(t *testing.T) {
	t.Parallel()

	cfg := catwalk.Model{
		CostPer1MIn:        3,
		CostPer1MOut:       15,
		CostPer1MInCached:  3.75,
		CostPer1MOutCached: 0.3,
	}
	usage := fantasy.Usage{
		InputTokens:         1000,
		OutputTokens:        2000,
		CacheReadTokens:     4000,
		CacheCreationTokens: 500,
	}

	want := (3.75*500 + 0.3*4000 + 3*1000 + 15*2000) / 1e6
	require.InDelta(t, want, tokenUsageCost(cfg, usage), 1e-12)
}

func TestUpdateSessionUsageCostUsesModelRates(t *testing.T) {
	t.Parallel()

	var a sessionAgent
	sess := &session.Session{}
	model := Model{CatwalkCfg: catwalk.Model{
		CostPer1MIn:        3,
		CostPer1MOut:       15,
		CostPer1MInCached:  3.75,
		CostPer1MOutCached: 0.3,
	}}
	usage := fantasy.Usage{
		InputTokens:         1000,
		OutputTokens:        2000,
		CacheReadTokens:     4000,
		CacheCreationTokens: 500,
	}
	a.updateSessionUsage(model, sess, usage, nil, false)

	require.InDelta(t, tokenUsageCost(model.CatwalkCfg, usage), sess.Cost, 1e-12)
}

func TestOpenRouterCostFromSteps(t *testing.T) {
	t.Parallel()

	var a sessionAgent
	steps := []fantasy.StepResult{
		{Response: fantasy.Response{ProviderMetadata: fantasy.ProviderMetadata{
			openrouter.Name: &openrouter.ProviderMetadata{Usage: openrouter.UsageAccounting{Cost: 0.1}},
		}}},
		{},
		{Response: fantasy.Response{ProviderMetadata: fantasy.ProviderMetadata{
			openrouter.Name: &openrouter.ProviderMetadata{Usage: openrouter.UsageAccounting{Cost: 0.2}},
		}}},
	}

	cost := a.openRouterCostFromSteps(steps)
	require.NotNil(t, cost)
	require.InDelta(t, 0.3, *cost, 1e-12)
	require.Nil(t, a.openRouterCostFromSteps(nil))
}
