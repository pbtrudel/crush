package agent

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/stretchr/testify/require"
)

func TestTurnCompactionTrigger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		overflowRecovery bool
		guardTripped     bool
		window           int64
		threshold        float64
		ceiling          int64
		want             compactionTrigger
	}{
		{name: "provider rejected the request", overflowRecovery: true, guardTripped: true, window: 200_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerOverflow},
		{name: "guard tripped mid-turn", guardTripped: true, window: 200_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerGuard},
		{name: "window fraction is the lower limit", window: 200_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerThreshold},
		{name: "ceiling is the lower limit", window: 1_000_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerCeiling},
		{name: "ceiling disabled", window: 1_000_000, threshold: 0.5, ceiling: 0, want: compactionTriggerThreshold},
		{name: "ceiling equal to the fraction", window: 800_000, threshold: 0.5, ceiling: 400_000, want: compactionTriggerThreshold},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, turnCompactionTrigger(tt.overflowRecovery, tt.guardTripped, tt.window, tt.threshold, tt.ceiling))
		})
	}
}

func TestCompactionTriggerHookForm(t *testing.T) {
	t.Parallel()

	for _, trigger := range []compactionTrigger{
		compactionTriggerThreshold,
		compactionTriggerCeiling,
		compactionTriggerGuard,
		compactionTriggerOverflow,
		compactionTriggerModelSwitch,
		compactionTriggerFusionPreflight,
		compactionTriggerFusionRoute,
	} {
		require.Equal(t, "auto", trigger.hookTrigger(), string(trigger))
	}
	require.Equal(t, "manual", compactionTriggerManual.hookTrigger())
}

// summaryTrigger returns the trigger recorded on the session's current
// compaction summary.
func summaryTrigger(t *testing.T, env fakeEnv, sessionID string) string {
	t.Helper()
	sess, err := env.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, sess.SummaryMessageID, "the session must have been compacted")
	summary, err := env.messages.Get(t.Context(), sess.SummaryMessageID)
	require.NoError(t, err)
	stats, ok := summary.CompactionStatsPart()
	require.True(t, ok, "the summary must carry compaction stats")
	return stats.Trigger
}

func TestCompactionTrigger_Ceiling(t *testing.T) {
	env := testEnv(t)
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	// Half of the 200,000-token test window is 100,000; a lower ceiling
	// makes the ceiling the limit the turn crossed.
	ceiling := 50_000
	cfg.Config().Options.AutoCompactMaxTokens = &ceiling
	stub := &stubSummaryModel{
		summaryText: "# Conversation Summary\n\nAll good.",
		usage:       fantasy.Usage{InputTokens: 60_000, OutputTokens: 100, TotalTokens: 60_100},
	}
	agent := newCompactionAgent(env, stub, cfg, false)
	sess, err := env.sessions.Create(t.Context(), "ceiling")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)

	require.Equal(t, "ceiling", summaryTrigger(t, env, sess.ID))
}

// Hooks receive the documented auto or manual value, never the finer one.
func TestCompactionTrigger_HooksStillReceiveAuto(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{
		summaryText: "# Conversation Summary\n\nAll good.",
		usage:       fantasy.Usage{InputTokens: 150_000, OutputTokens: 100, TotalTokens: 150_100},
	}
	agent := newCompactionAgent(env, stub, nil, false)
	read := recordingHooks(t, agent, hooks.EventPreCompact, hooks.EventPostCompact)
	sess, err := env.sessions.Create(t.Context(), "threshold hooks")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)

	require.Equal(t, "threshold", summaryTrigger(t, env, sess.ID))
	require.Equal(t, []string{"PreCompact auto", "PostCompact auto"}, read())
}

// overflowOnceModel rejects the first tool-bearing Stream call with a
// provider context overflow and answers every other call like
// stubSummaryModel. The title request carries no tools, so it is skipped.
type overflowOnceModel struct {
	stubSummaryModel
	rejected bool
}

func (m *overflowOnceModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	reject := len(call.Tools) > 0 && !m.rejected
	m.rejected = m.rejected || reject
	m.mu.Unlock()
	if reject {
		return nil, &fantasy.ProviderError{StatusCode: 400, ContextTooLargeErr: true}
	}
	return m.stubSummaryModel.Stream(ctx, call)
}

// A provider overflow is recovered by a compaction labelled overflow, which
// pins the wiring of the overflow and guard flags at the call site.
func TestCompactionTrigger_Overflow(t *testing.T) {
	env := testEnv(t)
	model := &overflowOnceModel{stubSummaryModel: stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}}
	agent := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	sess, err := env.sessions.Create(t.Context(), "overflow")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)

	require.Equal(t, "overflow", summaryTrigger(t, env, sess.ID))
}
