package agent

import (
	"context"
	"errors"
	"net"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
)

// stepEnding is what the turn-end rules need to know about the last step
// of a run that ended without an error. The fields are named, not
// positional, because two booleans passed in the wrong order would go
// unnoticed.
type stepEnding struct {
	finish       message.FinishReason
	hasText      bool
	hasToolCalls bool
	guardTripped bool
	toolStopped  bool
	loopRepeated bool
	stepsSpent   bool
	// deadlinePassed reports that the tree's deadline passed during the
	// step, whether or not a cancel reached the run.
	deadlinePassed bool
}

// turnEndForError decides how a run ended when its step loop returned an
// error. The order matters: once the deadline of the run's session tree
// has passed, the run was stopped by it, whatever error the cancel left;
// and the repetition detector stops a run by cancelling it, so it is
// tested before the cancel.
func turnEndForError(err error, deadlinePassed bool) message.TurnEnd {
	var timeoutErr *requestTimeoutError
	var providerErr *fantasy.ProviderError
	var fantasyErr *fantasy.Error
	var retryErr *fantasy.RetryError
	var netErr net.Error
	switch {
	case deadlinePassed:
		return message.TurnEndTimeLimit
	case errors.Is(err, errRepetitionDetected):
		return message.TurnEndRepetitionGuard
	case errors.Is(err, context.Canceled):
		return message.TurnEndCanceled
	case errors.As(err, &timeoutErr):
		return message.TurnEndRequestTimeout
	case errors.Is(err, context.DeadlineExceeded):
		// A deadline set by a caller, not by Crush's own request timeout.
		return message.TurnEndCanceled
	case isContextOverflowError(err):
		return message.TurnEndContextOverflow
	case errors.As(err, &providerErr), errors.As(err, &fantasyErr), fantasy.IsTransportError(err),
		errors.As(err, &retryErr), errors.As(err, &netErr):
		return message.TurnEndProviderError
	default:
		return message.TurnEndError
	}
}

// turnEndForStep decides how a run ended without an error, from its last
// step. A step that called no tools means the model had stopped, whatever
// limit was reached on the same step; a response the provider cut short
// counts as a provider error. A step that called tools means something
// stopped the loop.
func turnEndForStep(step stepEnding) message.TurnEnd {
	if !step.hasToolCalls {
		switch {
		case step.finish == message.FinishReasonMaxTokens:
			return message.TurnEndMaxTokens
		case step.finish == message.FinishReasonContentFilter:
			return message.TurnEndContentFilter
		case step.finish == message.FinishReasonError:
			return message.TurnEndProviderError
		case !step.hasText:
			return message.TurnEndEmpty
		default:
			return message.TurnEndCompleted
		}
	}
	switch {
	case step.guardTripped:
		return message.TurnEndContextGuard
	case step.toolStopped:
		return message.TurnEndToolStop
	case step.loopRepeated:
		return message.TurnEndLoopGuard
	case step.finish == message.FinishReasonMaxTokens:
		return message.TurnEndMaxTokens
	case step.finish == message.FinishReasonContentFilter:
		return message.TurnEndContentFilter
	case step.finish == message.FinishReasonError:
		return message.TurnEndProviderError
	case step.deadlinePassed && step.finish == message.FinishReasonToolUse:
		// The loop would have gone on past the tree's deadline.
		return message.TurnEndTimeLimit
	case step.stepsSpent && step.finish == message.FinishReasonToolUse:
		// The loop would have gone on to run the next step.
		return message.TurnEndStepLimit
	default:
		// The provider ended a step that called tools, and no guard
		// stopped the loop, so the model is taken at its word.
		return message.TurnEndCompleted
	}
}

// turnEndAfterCompaction applies the rules that follow the precedence:
// only a compaction the run required changes how the run ended. That
// compaction stopped after the tree's deadline reads time_limit, and a
// cancel during it reads canceled, since the run was stopped, not failed.
func turnEndAfterCompaction(end message.TurnEnd, summarizeErr error, deadlinePassed bool) message.TurnEnd {
	if end != message.TurnEndContextGuard && end != message.TurnEndContextOverflow {
		return end
	}
	switch {
	case errors.Is(summarizeErr, ErrCompactionBlocked):
		return message.TurnEndCompactionBlocked
	case summarizeErr != nil && deadlinePassed:
		return message.TurnEndTimeLimit
	case errors.Is(summarizeErr, context.Canceled):
		return message.TurnEndCanceled
	case summarizeErr != nil:
		return message.TurnEndCompactionFailed
	default:
		return end
	}
}

// lastStepEnding gathers the facts turnEndForStep needs from the run's
// last assistant message and the steps the loop completed. guardTripped
// is the mid-turn context guard's flag.
func lastStepEnding(msg *message.Message, steps []fantasy.StepResult, guardTripped bool) stepEnding {
	ending := stepEnding{
		finish:       msg.FinishReason(),
		hasText:      strings.TrimSpace(msg.Content().Text) != "",
		hasToolCalls: len(msg.ToolCalls()) > 0,
		guardTripped: guardTripped,
		loopRepeated: hasRepeatedToolCalls(steps, loopDetectionWindowSize, loopDetectionMaxRepeats),
	}
	if len(steps) > 0 {
		for _, result := range steps[len(steps)-1].Content.ToolResults() {
			if result.StopTurn {
				ending.toolStopped = true
				break
			}
		}
	}
	return ending
}
