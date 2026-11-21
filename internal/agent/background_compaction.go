package agent

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
)

// errEmptyCompaction reports a background summary that returned no result,
// which is what a run cancelled before it wrote one leaves behind.
var errEmptyCompaction = errors.New("background compaction produced no summary")

// backgroundCompactionTimeout bounds a summary written ahead of the
// auto-compaction limit. It runs on a context detached from the turn that
// started it, so nothing else would stop a stalled provider from pinning
// the goroutine.
const backgroundCompactionTimeout = 10 * time.Minute

// backgroundCompaction is one summary written ahead of the auto-compaction
// limit, so the prompt that reaches the limit does not wait for it.
type backgroundCompaction struct {
	cancel context.CancelFunc
	// done is closed when the run returned. outcome and err are written
	// before it is closed and read after, so the channel orders them.
	done    chan struct{}
	outcome *compactionOutcome
	err     error
}

// finished reports whether the run returned, so its result is final.
func (j *backgroundCompaction) finished() bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

// compactionOutcome is what a summary written ahead of the limit left for the
// splice: the summary message, where its verbatim tail starts, and the
// accounting the session row needs once the summary is adopted.
type compactionOutcome struct {
	summaryID   string
	cutID       string
	snapshotEnd string
	stateText   string

	summaryTokens int64
	tokensBefore  int64
	tailTokens    int64
	savedTokens   int64
	keptMessages  int
	estimated     bool
}

// compactBackgroundMargin returns how far below the auto-compaction limit a
// finished turn starts the summary, as a fraction of the context window.
// Zero keeps compaction blocking.
func (a *sessionAgent) compactBackgroundMargin() float64 {
	if a.cfg == nil {
		return defaultCompactBackgroundMargin
	}
	cfg := a.cfg.Config()
	if cfg == nil {
		return defaultCompactBackgroundMargin
	}
	return cfg.Options.GetCompactBackgroundMargin()
}

// shouldStartBackgroundCompaction reports whether a finished turn crossed the
// pre-threshold at which the summary is written ahead of the limit. The
// feature is off unless the margin is configured: a summary the session never
// uses still costs tokens, and the saving has not been measured.
func (a *sessionAgent) shouldStartBackgroundCompaction(model Model, sess session.Session) bool {
	margin := a.compactBackgroundMargin()
	if margin <= 0 || a.disableAutoSummarize {
		return false
	}
	window := effectiveContextWindow(model)
	if window <= 0 {
		return false
	}
	threshold, _ := a.compactionConfig()
	if threshold <= 0 {
		return false
	}
	limit := autoCompactLimit(window, threshold, a.autoCompactMaxTokens())
	tokens := sess.PromptTokens + sess.CompletionTokens
	return tokens >= limit-int64(margin*float64(window)) && tokens < limit
}

// startBackgroundCompaction writes the session's summary ahead of the limit
// when the finished turn crossed the pre-threshold. The summary is written
// against the history as it stands at this turn and adopted only if that
// history is still a prefix of the session's when the limit is reached.
func (a *sessionAgent) startBackgroundCompaction(ctx context.Context, call SessionAgentCall, largeModel Model, sess session.Session) {
	if ctx.Err() != nil {
		// A cancelled turn does not get a summary.
		return
	}
	if !a.shouldStartBackgroundCompaction(largeModel, sess) {
		return
	}
	if job, ok := a.bgCompactions.Get(call.SessionID); ok && (!job.finished() || job.err == nil) {
		// A ready summary is still worth adopting, and a running one is
		// already paid for; only a failed one is worth replacing.
		return
	}
	msgs, err := a.getSessionMessages(ctx, sess)
	if err != nil || len(msgs) == 0 {
		return
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backgroundCompactionTimeout)
	job := &backgroundCompaction{cancel: cancel, done: make(chan struct{})}
	a.bgCompactions.Set(call.SessionID, job)
	go a.runBackgroundCompaction(runCtx, job, call, largeModel, msgs)
}

// runBackgroundCompaction writes one summary and leaves it for the splice.
func (a *sessionAgent) runBackgroundCompaction(ctx context.Context, job *backgroundCompaction, call SessionAgentCall, largeModel Model, msgs []message.Message) {
	defer close(job.done)
	defer job.cancel()

	outcome, err := a.summarize(ctx, compactionRequest{
		sessionID:     call.SessionID,
		largeModel:    largeModel,
		opts:          call.ProviderOptions,
		onAuthRefresh: call.OnAuthRefresh,
		trigger:       compactionTriggerBackground,
		// The turn that started this run owns the session's active-run
		// registration, and the prompt that reaches the limit may own
		// it by the time this returns, so this run must not touch it.
		active:   true,
		snapshot: msgs,
		deferred: true,
	})
	if err == nil && outcome == nil {
		err = errEmptyCompaction
	}
	if err != nil {
		job.err = err
		slog.Warn("Background compaction failed", "session_id", call.SessionID, "error", err)
		return
	}
	job.outcome = outcome
	slog.Debug("Background compaction summary ready", "session_id", call.SessionID, "summary_id", outcome.summaryID)
}

// adoptBackgroundCompaction splices a summary written ahead of the limit into
// the session, when one is ready and the history it was written against is
// still a prefix of the session's. It reports whether the session compacted,
// so the caller knows whether the blocking compaction still has to run.
func (a *sessionAgent) adoptBackgroundCompaction(ctx context.Context, sessionID string) bool {
	job, ok := a.bgCompactions.Get(sessionID)
	if !ok {
		return false
	}
	if !job.finished() {
		// The limit came first: stop the run so it cannot compete with the
		// blocking compaction, and let that one do the work.
		a.cancelBackgroundCompaction(sessionID)
		return false
	}
	a.bgCompactions.Del(sessionID)
	if job.err != nil || job.outcome == nil {
		return false
	}
	out := job.outcome
	if !a.snapshotStillPrefix(ctx, sessionID, out) {
		a.markBackgroundSummaryUnused(sessionID, out)
		return false
	}
	return a.spliceBackgroundSummary(ctx, sessionID, out)
}

// snapshotStillPrefix reports whether the history the summary was written
// against is still a prefix of the session's. History is append-only, so the
// check is that the message the snapshot ended on is still there with
// everything stored after it: a summary adopted over a deleted tail would
// drop messages the summary never read.
func (a *sessionAgent) snapshotStillPrefix(ctx context.Context, sessionID string, out *compactionOutcome) bool {
	if out.snapshotEnd == "" {
		return false
	}
	tail, err := a.messages.ListFromCut(ctx, sessionID, out.snapshotEnd)
	if err != nil || len(tail) == 0 || tail[0].ID != out.snapshotEnd {
		return false
	}
	for _, msg := range tail {
		if msg.ID == out.summaryID {
			return true
		}
	}
	// The summary is gone, so there is nothing to adopt.
	return false
}

// spliceBackgroundSummary adopts the pending summary: the session reads from
// it, with everything stored after the snapshot kept verbatim behind it.
func (a *sessionAgent) spliceBackgroundSummary(ctx context.Context, sessionID string, out *compactionOutcome) bool {
	// The row is read again here so the absolute totals written back are
	// the freshest ones. The summary's own usage was recorded when it was
	// written, with an incrementing write this cannot lose.
	sess, err := a.sessions.Get(ctx, sessionID)
	if err != nil {
		return false
	}
	sess.SummaryMessageID = out.summaryID
	sess.SummaryCutMessageID = out.cutID
	sess.CompletionTokens = out.summaryTokens
	sess.PromptTokens = 0
	sess.EstimatedUsage = out.estimated
	if _, err := a.sessions.Save(ctx, sess); err != nil {
		slog.Warn("Failed to adopt the background compaction summary", "session_id", sessionID, "error", err)
		return false
	}
	// The state block is created now, not when the summary was written, so
	// it describes the session the summary is adopted into.
	if out.stateText != "" {
		if _, err := a.createCompactionStateMessage(ctx, sessionID, out.stateText); err != nil {
			slog.Warn("Failed to store the compaction state message", "session_id", sessionID, "error", err)
		}
	}
	a.hooks.Run(ctx, hooks.Event{Name: hooks.EventPostCompact, SessionID: sessionID, Trigger: compactionTriggerBackground.hookTrigger()})
	slog.Info(
		"Context compacted",
		"session_id", sessionID,
		"trigger", string(compactionTriggerBackground),
		"tokens_before", out.tokensBefore,
		"summary_tokens", out.summaryTokens,
		"tail_tokens", out.tailTokens,
		"saved_tokens", out.savedTokens,
		"kept_messages", out.keptMessages,
	)
	return true
}

// cancelBackgroundCompaction stops the session's background summary and
// forgets it. A summary that was already written is marked unused, so
// logreport can count what the feature spent without compacting anything.
func (a *sessionAgent) cancelBackgroundCompaction(sessionID string) {
	job, ok := a.bgCompactions.Take(sessionID)
	if !ok {
		return
	}
	job.cancel()
	if !job.finished() {
		// The run tears itself down on the cancelled context; nothing more
		// is written to the session, so there is no reason to wait for it
		// here.
		return
	}
	if job.err == nil && job.outcome != nil {
		a.markBackgroundSummaryUnused(sessionID, job.outcome)
	}
}

// CancelBackgroundCompaction stops the summary a session wrote ahead of the
// auto-compaction limit. It is called when the session is deleted, since a
// summary has nothing left to compact into.
func (a *sessionAgent) CancelBackgroundCompaction(sessionID string) {
	a.cancelBackgroundCompaction(sessionID)
}

// markBackgroundSummaryUnused stamps a summary that will never be adopted, so
// the wasted summary shows up as its own trigger instead of reading as a
// compaction that happened.
func (a *sessionAgent) markBackgroundSummaryUnused(sessionID string, out *compactionOutcome) {
	if out.summaryID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, err := a.messages.Get(ctx, out.summaryID)
	if err != nil {
		return
	}
	stats, ok := msg.CompactionStatsPart()
	if !ok || stats.Trigger != string(compactionTriggerBackground) {
		return
	}
	stats.Trigger = string(compactionTriggerBackgroundUnused)
	for i, part := range msg.Parts {
		if _, isStats := part.(message.CompactionStats); isStats {
			msg.Parts[i] = stats
			break
		}
	}
	if err := a.messages.Update(ctx, msg); err != nil {
		slog.Warn("Failed to record an unused background summary", "session_id", sessionID, "error", err)
		return
	}
	slog.Info("Discarded an unused background compaction summary", "session_id", sessionID, "summary_id", out.summaryID, "tokens", out.summaryTokens)
}
