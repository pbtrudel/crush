package agent

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
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
		Pricing: catwalk.Pricing{
			Input:       3,
			Output:      15,
			CacheCreate: 3.75,
			CacheHit:    0.3,
		},
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
		Pricing: catwalk.Pricing{
			Input:       3,
			Output:      15,
			CacheCreate: 3.75,
			CacheHit:    0.3,
		},
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

// stepUsageFor must produce exactly the same outcome as the in-place
// mutation it was extracted from: the incremental session write is only
// safe if the split into snapshots and increments matches what the old
// fetch-modify-save computed.
func TestStepUsageForMatchesUpdateSessionUsage(t *testing.T) {
	t.Parallel()

	a := &sessionAgent{}
	cachedModel := Model{
		CatwalkCfg: catwalk.Model{ID: "m", Pricing: catwalk.Pricing{Input: 3, Output: 15, CacheCreate: 0.5, CacheHit: 0.3}},
	}
	flatModel := cachedModel
	flatModel.FlatRate = true

	cases := []struct {
		name         string
		model        Model
		start        session.Session
		usage        fantasy.Usage
		overrideCost *float64
		estimated    bool
	}{
		{"normal", cachedModel, session.Session{ID: "s", Cost: 1, TotalCacheReadTokens: 10, TotalContextTokens: 100, PromptTokens: 50, CompletionTokens: 5}, fantasy.Usage{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 4000, CacheCreationTokens: 100}, nil, false},
		{"zero usage keeps snapshots", cachedModel, session.Session{ID: "s", PromptTokens: 700, CompletionTokens: 90, TotalContextTokens: 12, EstimatedUsage: true}, fantasy.Usage{}, nil, false},
		{"estimated costs nothing", cachedModel, session.Session{ID: "s"}, fantasy.Usage{InputTokens: 10, OutputTokens: 5}, nil, true},
		{"override cost wins", cachedModel, session.Session{ID: "s"}, fantasy.Usage{InputTokens: 10}, func() *float64 { v := 0.42; return &v }(), false},
		{"flat rate is free", flatModel, session.Session{ID: "s"}, fantasy.Usage{InputTokens: 10, OutputTokens: 5}, nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			legacy := tc.start
			a.updateSessionUsage(tc.model, &legacy, tc.usage, tc.overrideCost, tc.estimated)

			step := a.stepUsageFor(tc.model, tc.start, tc.usage, tc.overrideCost, tc.estimated)
			incremented := tc.start
			incremented.PromptTokens = step.PromptTokens
			incremented.CompletionTokens = step.CompletionTokens
			incremented.TotalCacheReadTokens += step.CacheReadTokens
			incremented.TotalContextTokens += step.ContextTokens
			incremented.Cost += step.Cost
			incremented.EstimatedUsage = step.Estimated

			require.Equal(t, legacy.PromptTokens, incremented.PromptTokens)
			require.Equal(t, legacy.CompletionTokens, incremented.CompletionTokens)
			require.Equal(t, legacy.TotalCacheReadTokens, incremented.TotalCacheReadTokens)
			require.Equal(t, legacy.TotalContextTokens, incremented.TotalContextTokens)
			require.InDelta(t, legacy.Cost, incremented.Cost, 1e-9)
			require.Equal(t, legacy.EstimatedUsage, incremented.EstimatedUsage)
		})
	}
}
