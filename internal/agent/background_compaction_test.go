package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// blockingSummaryModel is a summary model whose request never returns until
// its context is cancelled, so a test can observe a background compaction
// while it is still running.
type blockingSummaryModel struct {
	started chan struct{}
}

func (m *blockingSummaryModel) Provider() string { return "blocking" }
func (m *blockingSummaryModel) Model() string    { return "blocking-model" }

func (m *blockingSummaryModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("not implemented")
}

func (m *blockingSummaryModel) Stream(ctx context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
	select {
	case <-m.started:
	default:
		close(m.started)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingSummaryModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *blockingSummaryModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

// backgroundCompactionConfig builds a config store with the background
// compaction margin set, so the feature is on for the test.
func backgroundCompactionConfig(t *testing.T, env fakeEnv, margin float64) *config.ConfigStore {
	t.Helper()
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().Options.CompactBackgroundMargin = &margin
	return cfg
}

// waitBackgroundCompaction waits for the session's background summary to
// finish and returns the job, failing the test if it does not settle.
func waitBackgroundCompaction(t *testing.T, agent *sessionAgent, sessionID string) *backgroundCompaction {
	t.Helper()
	job, ok := agent.bgCompactions.Get(sessionID)
	require.True(t, ok, "the session must have a background compaction")
	select {
	case <-job.done:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the background compaction did not finish")
	}
	return job
}

// seedCompactionHistory stores user and assistant messages the tail budget
// can keep verbatim.
func seedCompactionHistory(t *testing.T, env fakeEnv, sessionID string, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := range n {
		role := message.User
		if i%2 == 1 {
			role = message.Assistant
		}
		msg, err := env.messages.Create(t.Context(), sessionID, message.CreateMessageParams{
			Role:  role,
			Parts: []message.ContentPart{message.TextContent{Text: bigText(100)}},
		})
		require.NoError(t, err)
		ids = append(ids, msg.ID)
	}
	return ids
}

// unusedTrigger reads the trigger stamped on a background summary that was
// written but never adopted.
func unusedTrigger(t *testing.T, env fakeEnv, messageID string) string {
	t.Helper()
	msg, err := env.messages.Get(t.Context(), messageID)
	require.NoError(t, err)
	stats, ok := msg.CompactionStatsPart()
	require.True(t, ok, "the background summary must carry compaction stats")
	return stats.Trigger
}

func TestShouldStartBackgroundCompaction(t *testing.T) {
	env := testEnv(t)

	// The test model's window is 200k tokens, so the default threshold
	// compacts at 100k and a 0.1 margin starts the summary at 80k.
	below := session.Session{PromptTokens: 79_999}
	atMargin := session.Session{PromptTokens: 80_000}
	atLimit := session.Session{PromptTokens: 100_000}

	t.Run("off by default", func(t *testing.T) {
		agent := newCompactionAgent(env, nil, nil, false)
		require.False(t, agent.shouldStartBackgroundCompaction(agent.largeModel.Get(), atMargin))
	})

	t.Run("starts inside the margin only", func(t *testing.T) {
		agent := newCompactionAgent(env, nil, backgroundCompactionConfig(t, env, 0.1), false)
		model := agent.largeModel.Get()
		require.False(t, agent.shouldStartBackgroundCompaction(model, below))
		require.True(t, agent.shouldStartBackgroundCompaction(model, atMargin))
		require.False(t, agent.shouldStartBackgroundCompaction(model, atLimit), "the limit itself compacts blocking")
	})

	t.Run("disabled automatic compaction starts nothing", func(t *testing.T) {
		cfg := backgroundCompactionConfig(t, env, 0.1)
		off := 0.0
		cfg.Config().Options.AutoCompactThreshold = &off
		agent := newCompactionAgent(env, nil, cfg, false)
		require.False(t, agent.shouldStartBackgroundCompaction(agent.largeModel.Get(), atMargin))
	})

	t.Run("disabled auto-summarize starts nothing", func(t *testing.T) {
		agent := newCompactionAgent(env, nil, backgroundCompactionConfig(t, env, 0.1), true)
		require.False(t, agent.shouldStartBackgroundCompaction(agent.largeModel.Get(), atMargin))
	})
}

// TestBackgroundCompaction_ReadySummaryIsSpliced pins the point of the
// feature: a summary written ahead of the limit is adopted when the limit is
// reached, and everything stored after its snapshot stays verbatim after it.
func TestBackgroundCompaction_ReadySummaryIsSpliced(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}
	agent := newCompactionAgent(env, stub, backgroundCompactionConfig(t, env, 0.1), false)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "background")
	require.NoError(t, err)
	seedCompactionHistory(t, env, sess.ID, 3)

	agent.startBackgroundCompaction(ctx, SessionAgentCall{SessionID: sess.ID}, agent.largeModel.Get(), session.Session{ID: sess.ID, PromptTokens: 85_000})
	job := waitBackgroundCompaction(t, agent, sess.ID)
	require.NoError(t, job.err)
	require.NotNil(t, job.outcome)

	// The summary is written but not adopted: the session still reads the
	// whole history until the limit is reached.
	unchanged, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	require.Empty(t, unchanged.SummaryMessageID)
	require.Empty(t, unchanged.SummaryCutMessageID)

	// A turn lands between the snapshot and the limit. It has to survive
	// the splice: the summary is inserted ahead of it, not over it.
	late, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "later work"}},
	})
	require.NoError(t, err)

	require.True(t, agent.adoptBackgroundCompaction(ctx, sess.ID))

	updated, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	require.Equal(t, job.outcome.summaryID, updated.SummaryMessageID)
	require.Equal(t, job.outcome.cutID, updated.SummaryCutMessageID)
	require.Zero(t, updated.PromptTokens)
	require.Equal(t, string(compactionTriggerBackground), summaryTrigger(t, env, sess.ID))

	msgs, err := agent.getSessionMessages(ctx, updated)
	require.NoError(t, err)
	require.True(t, msgs[0].IsSummaryMessage, "the summary opens the compacted history")
	ids := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		ids = append(ids, msg.ID)
	}
	require.Contains(t, ids, job.outcome.snapshotEnd)
	require.Greater(t, slices.Index(ids, late.ID), slices.Index(ids, job.outcome.snapshotEnd),
		"the message stored after the snapshot stays verbatim after it")

	_, ok := agent.bgCompactions.Get(sess.ID)
	require.False(t, ok, "an adopted summary is forgotten")
}

// TestBackgroundCompaction_StaleSnapshotFallsBack pins the fallback: a
// summary whose snapshot no longer matches the history is dropped, counted,
// and the caller compacts blocking instead.
func TestBackgroundCompaction_StaleSnapshotFallsBack(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}
	agent := newCompactionAgent(env, stub, backgroundCompactionConfig(t, env, 0.1), false)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "stale")
	require.NoError(t, err)
	ids := seedCompactionHistory(t, env, sess.ID, 3)

	agent.startBackgroundCompaction(ctx, SessionAgentCall{SessionID: sess.ID}, agent.largeModel.Get(), session.Session{ID: sess.ID, PromptTokens: 85_000})
	job := waitBackgroundCompaction(t, agent, sess.ID)
	require.NoError(t, job.err)
	require.Equal(t, ids[len(ids)-1], job.outcome.snapshotEnd)

	// The message the snapshot ended on is gone, so the summary would drop
	// history it never read.
	require.NoError(t, env.messages.Delete(ctx, job.outcome.snapshotEnd))

	require.False(t, agent.adoptBackgroundCompaction(ctx, sess.ID))

	unchanged, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	require.Empty(t, unchanged.SummaryMessageID, "a stale summary must not be adopted")
	require.Equal(t, string(compactionTriggerBackgroundUnused), unusedTrigger(t, env, job.outcome.summaryID))

	// The blocking compaction still runs, so the session is not left over
	// the limit with no way to compact.
	_, err = agent.summarize(ctx, compactionRequest{
		sessionID:  sess.ID,
		largeModel: agent.largeModel.Get(),
		trigger:    compactionTriggerThreshold,
	})
	require.NoError(t, err)
	compacted, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, compacted.SummaryMessageID)
	require.NotEqual(t, job.outcome.summaryID, compacted.SummaryMessageID)
}

// TestBackgroundCompaction_CancelStopsTheRun pins cancellation: a cancelled
// session leaves no background run behind and never compacts on its own.
func TestBackgroundCompaction_CancelStopsTheRun(t *testing.T) {
	env := testEnv(t)
	// Taken after the environment is up, so only goroutines the
	// background compaction starts are checked.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	model := &blockingSummaryModel{started: make(chan struct{})}
	agent := newCompactionAgent(env, model, backgroundCompactionConfig(t, env, 0.1), false)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "cancel")
	require.NoError(t, err)
	seedCompactionHistory(t, env, sess.ID, 3)

	agent.startBackgroundCompaction(ctx, SessionAgentCall{SessionID: sess.ID}, agent.largeModel.Get(), session.Session{ID: sess.ID, PromptTokens: 85_000})
	job, ok := agent.bgCompactions.Get(sess.ID)
	require.True(t, ok)
	select {
	case <-model.started:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the background compaction never started")
	}

	agent.Cancel(sess.ID)

	select {
	case <-job.done:
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the cancelled background compaction kept its goroutine")
	}
	_, ok = agent.bgCompactions.Get(sess.ID)
	require.False(t, ok, "a cancelled session keeps no background compaction")

	unchanged, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	require.Empty(t, unchanged.SummaryMessageID)
}

// TestBackgroundCompaction_OptionOffChangesNothing pins the default: without
// the margin, a turn under the limit starts no summary at all.
func TestBackgroundCompaction_OptionOffChangesNothing(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{
		summaryText: "# Conversation Summary\n\nAll good.",
		usage:       fantasy.Usage{InputTokens: 85_000, OutputTokens: 100, TotalTokens: 85_100},
	}
	agent := newCompactionAgent(env, stub, nil, false)

	sess, err := env.sessions.Create(t.Context(), "default")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)

	_, ok := agent.bgCompactions.Get(sess.ID)
	require.False(t, ok, "the option is off by default")

	unchanged, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, unchanged.SummaryMessageID)
	require.Equal(t, 1, workCalls(stub), "only the turn ran")
}

// TestBackgroundCompaction_AdoptsAtTheLimit runs two turns: the first stops
// inside the margin and writes the summary in the background, the second
// crosses the limit and adopts it instead of paying for a second summary.
func TestBackgroundCompaction_AdoptsAtTheLimit(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{
		summaryText: "# Conversation Summary\n\nAll good.",
		usage:       fantasy.Usage{InputTokens: 85_000, OutputTokens: 100, TotalTokens: 85_100},
	}
	agent := newCompactionAgent(env, stub, backgroundCompactionConfig(t, env, 0.1), false)

	sess, err := env.sessions.Create(t.Context(), "two turns")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)
	require.NoError(t, waitBackgroundCompaction(t, agent, sess.ID).err)

	unchanged, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, unchanged.SummaryMessageID, "the summary waits for the limit")

	stub.usage = fantasy.Usage{InputTokens: 105_000, OutputTokens: 100, TotalTokens: 105_100}
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "again"})
	require.NoError(t, err)

	require.Equal(t, string(compactionTriggerBackground), summaryTrigger(t, env, sess.ID))
	require.Equal(t, 3, workCalls(stub), "two turns and one background summary, no blocking summary")
}

// workCalls counts the requests a stub served for the session's own work,
// leaving out the title request, which runs beside the first turn.
func workCalls(stub *stubSummaryModel) int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	count := 0
	for _, call := range stub.calls {
		if len(call.Prompt) > 0 && strings.Contains(fmt.Sprint(call.Prompt[0]), "generate a short title") {
			continue
		}
		count++
	}
	return count
}
