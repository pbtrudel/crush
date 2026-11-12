package claudeagent

import (
	"fmt"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func usageTokens(v int64) *int64 { return &v }

func TestUsageRepeatedReportsDoNotDoubleCount(t *testing.T) {
	t.Parallel()

	tracker := newUsageTracker()
	for _, output := range []int64{5, 20, 40} {
		tracker.update("msg_1", &wireUsage{
			InputTokens:  usageTokens(1200),
			OutputTokens: usageTokens(output),
		})
	}

	require.Equal(t, fantasy.Usage{
		InputTokens:  1200,
		OutputTokens: 40,
		TotalTokens:  1240,
	}, tracker.snapshot())
}

func TestUsageKeepsHighestCountPerResponse(t *testing.T) {
	t.Parallel()

	tracker := newUsageTracker()
	tracker.update("msg_1", &wireUsage{
		InputTokens:              usageTokens(900),
		CacheReadInputTokens:     usageTokens(300),
		CacheCreationInputTokens: usageTokens(50),
		OutputTokens:             usageTokens(80),
		OutputTokensDetails:      &outputTokenDetail{ThinkingTokens: usageTokens(60)},
	})
	// A later report for the same response must not lower any count.
	tracker.update("msg_1", &wireUsage{
		InputTokens:              usageTokens(100),
		CacheReadInputTokens:     usageTokens(20),
		CacheCreationInputTokens: usageTokens(5),
		OutputTokens:             usageTokens(10),
		OutputTokensDetails:      &outputTokenDetail{ThinkingTokens: usageTokens(1)},
	})

	// InputTokens is the uncached part of the prompt; Crush adds the cache
	// counts back when it sizes the context.
	require.Equal(t, fantasy.Usage{
		InputTokens:         900,
		OutputTokens:        80,
		TotalTokens:         980,
		ReasoningTokens:     60,
		CacheReadTokens:     300,
		CacheCreationTokens: 50,
	}, tracker.snapshot())
}

func TestUsageSnapshotUsesLastResponseInput(t *testing.T) {
	t.Parallel()

	tracker := newUsageTracker()
	tracker.update("msg_1", &wireUsage{
		InputTokens:  usageTokens(1000),
		OutputTokens: usageTokens(10),
	})
	tracker.update("msg_2", &wireUsage{
		InputTokens:              usageTokens(2000),
		CacheReadInputTokens:     usageTokens(500),
		CacheCreationInputTokens: usageTokens(100),
		OutputTokens:             usageTokens(20),
	})

	// The context size is the last API call's prompt, not the sum of
	// every call's prompt.
	require.Equal(t, fantasy.Usage{
		InputTokens:         2000,
		OutputTokens:        30,
		TotalTokens:         2030,
		CacheReadTokens:     500,
		CacheCreationTokens: 100,
	}, tracker.snapshot())
}

func TestUsageSumsOutputAndThinking(t *testing.T) {
	t.Parallel()

	tracker := newUsageTracker()
	tracker.update("msg_1", &wireUsage{
		InputTokens:         usageTokens(100),
		OutputTokens:        usageTokens(50),
		OutputTokensDetails: &outputTokenDetail{ThinkingTokens: usageTokens(30)},
	})
	tracker.update("msg_2", &wireUsage{
		InputTokens:         usageTokens(200),
		OutputTokens:        usageTokens(70),
		OutputTokensDetails: &outputTokenDetail{ThinkingTokens: usageTokens(40)},
	})

	usage := tracker.snapshot()
	require.Equal(t, int64(120), usage.OutputTokens)
	require.Equal(t, int64(70), usage.ReasoningTokens)
	require.Equal(t, int64(320), usage.TotalTokens)
	require.Equal(t, int64(200), usage.InputTokens)
}

func TestUsageTurnTotal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		updates []*wireUsage
		total   *wireUsage
		want    fantasy.Usage
	}{
		{
			name:    "applied when a single response was tracked",
			updates: []*wireUsage{{InputTokens: usageTokens(100), OutputTokens: usageTokens(10)}},
			total:   &wireUsage{InputTokens: usageTokens(500), OutputTokens: usageTokens(60)},
			want:    fantasy.Usage{InputTokens: 500, OutputTokens: 60, TotalTokens: 560},
		},
		{
			name: "ignored when several responses were tracked",
			updates: []*wireUsage{
				{InputTokens: usageTokens(100), OutputTokens: usageTokens(10)},
				{InputTokens: usageTokens(200), OutputTokens: usageTokens(20)},
			},
			total: &wireUsage{InputTokens: usageTokens(900), OutputTokens: usageTokens(90)},
			want:  fantasy.Usage{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
		},
		{
			name:  "applied when no response was tracked",
			total: &wireUsage{InputTokens: usageTokens(300), OutputTokens: usageTokens(20)},
			want:  fantasy.Usage{InputTokens: 300, OutputTokens: 20, TotalTokens: 320},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tracker := newUsageTracker()
			for i, usage := range tt.updates {
				tracker.update(fmt.Sprintf("msg_%d", i), usage)
			}
			tracker.applyTurnTotal(tt.total)

			require.Equal(t, tt.want, tracker.snapshot())
		})
	}
}

func TestUsageEmptyTracker(t *testing.T) {
	t.Parallel()

	require.Equal(t, fantasy.Usage{}, newUsageTracker().snapshot())
	require.Equal(t, fantasy.Usage{}, (*usageTracker)(nil).snapshot())
}

func TestUsageNilUsageIsIgnored(t *testing.T) {
	t.Parallel()

	tracker := newUsageTracker()
	tracker.update("msg_1", nil)
	tracker.applyTurnTotal(nil)

	require.Equal(t, fantasy.Usage{}, tracker.snapshot())

	var nilTracker *usageTracker
	require.NotPanics(t, func() {
		nilTracker.update("msg_1", &wireUsage{OutputTokens: usageTokens(1)})
		nilTracker.applyTurnTotal(&wireUsage{OutputTokens: usageTokens(1)})
	})
}
