package agent

import (
	"context"
	"errors"

	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/message"
)

// ErrCompactionBlocked is returned by Summarize when a PreCompact hook
// blocks compaction. The conversation is left as it was.
var ErrCompactionBlocked = errors.New("compaction blocked by hook")

// Compaction triggers reported to PreCompact and PostCompact hooks.
const (
	compactTriggerAuto   = "auto"
	compactTriggerManual = "manual"
)

// compactionTrigger names what started a compaction. It is recorded on the
// summary's statistics; hooks receive only its auto or manual form, which
// is the payload documented for users.
type compactionTrigger string

const (
	// A finished turn left the session over the window fraction.
	compactionTriggerThreshold compactionTrigger = "threshold"
	// A finished turn left the session over the absolute ceiling, which
	// was the lower of the two limits.
	compactionTriggerCeiling compactionTrigger = "ceiling"
	// The mid-turn guard found too little room for the next step.
	compactionTriggerGuard compactionTrigger = "guard"
	// The provider rejected the request as too long.
	compactionTriggerOverflow compactionTrigger = "overflow"
	// The user switched to a model with a smaller window.
	compactionTriggerModelSwitch compactionTrigger = "model_switch"
	// The prompt would not fit the Fusion budget.
	compactionTriggerFusionPreflight compactionTrigger = "fusion_preflight"
	// Fusion routed the turn to a model with a smaller budget.
	compactionTriggerFusionRoute compactionTrigger = "fusion_route"
	// The user asked for the compaction.
	compactionTriggerManual compactionTrigger = "manual"
	// A finished turn crossed the pre-threshold, so the summary was
	// written ahead of the limit and adopted when the limit was reached.
	compactionTriggerBackground compactionTrigger = "background"
	// A background summary was written but never adopted, because its
	// snapshot no longer matched the history or the session went away. It
	// cost tokens, so it is counted apart from the compactions that ran.
	compactionTriggerBackgroundUnused compactionTrigger = "background_unused"
)

// hookTrigger is the trigger as PreCompact and PostCompact hooks receive it.
func (t compactionTrigger) hookTrigger() string {
	if t == compactionTriggerManual {
		return compactTriggerManual
	}
	return compactTriggerAuto
}

// measuresFromNow reports whether the compaction measures the session's
// trailing idle gap from "now" instead of from the last stored message. The
// preflight compactions run before the prompt that triggered them is stored,
// so the gap they have to measure is the one that prompt will close.
func (t compactionTrigger) measuresFromNow() bool {
	switch t {
	case compactionTriggerFusionPreflight, compactionTriggerFusionRoute, compactionTriggerModelSwitch:
		return true
	}
	return false
}

// turnCompactionTrigger names which of the four causes that share the
// end-of-turn compaction applied. Overflow recovery also trips the guard
// flag, so it is tested first. The ceiling case asks autoCompactLimit which
// limit applied, so the two cannot drift.
func turnCompactionTrigger(overflowRecovery, guardTripped bool, window int64, threshold float64, ceiling int64) compactionTrigger {
	switch {
	case overflowRecovery:
		return compactionTriggerOverflow
	case guardTripped:
		return compactionTriggerGuard
	case autoCompactLimit(window, threshold, ceiling) < int64(threshold*float64(window)):
		return compactionTriggerCeiling
	default:
		return compactionTriggerThreshold
	}
}

// fireTurnHook runs one hook for a top-level turn. Subagent and teammate
// turns are skipped: dispatched subagents fire no hooks at all, and a
// teammate fires tool hooks but not turn hooks because its calls are
// non-interactive. Either way a delegated turn does not fire the user's
// hook N times.
func (a *sessionAgent) fireTurnHook(ctx context.Context, call SessionAgentCall, ev hooks.Event) {
	if call.NonInteractive {
		return
	}
	ev.SessionID = call.SessionID
	a.hooks.Run(ctx, ev)
}

// turnEndEvent names how a turn ended so exactly one hook fires for it:
// Interrupt when the user cancelled, StopFailure when the provider
// failed, and TurnEnd otherwise.
func turnEndEvent(err error, assistant *message.Message) hooks.Event {
	switch {
	case errors.Is(err, context.Canceled):
		return hooks.Event{Name: hooks.EventInterrupt}
	case err != nil:
		return hooks.Event{Name: hooks.EventStopFailure, Error: err.Error()}
	}
	ev := hooks.Event{Name: hooks.EventTurnEnd}
	if assistant != nil {
		ev.LastAssistantMessage = new(assistant.Content().String())
	}
	return ev
}
