package agent

import (
	"context"
	"errors"
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
}

// turnEndForError decides how a run ended when its step loop returned an
// error. The order matters: the repetition detector stops a run by
// cancelling it, so it is tested before the cancel.
func turnEndForError(err error) message.TurnEnd {
	var timeoutErr *requestTimeoutError
	var providerErr *fantasy.ProviderError
	var fantasyErr *fantasy.Error
	switch {
	case errors.Is(err, errRepetitionDetected):
		return message.TurnEndRepetitionGuard
	case errors.Is(err, context.Canceled):
		return message.TurnEndCanceled
	case errors.As(err, &timeoutErr):
		return message.TurnEndRequestTimeout
	case isContextOverflowError(err):
		return message.TurnEndContextOverflow
	case errors.As(err, &providerErr), errors.As(err, &fantasyErr), fantasy.IsTransportError(err):
		return message.TurnEndProviderError
	default:
		return message.TurnEndError
	}
}

// turnEndForStep decides how a run ended without an error, from its last
// step. A step that called no tools means the model had stopped, whatever
// limit was reached on the same step. A step that called tools means
// something stopped the loop.
func turnEndForStep(step stepEnding) message.TurnEnd {
	if !step.hasToolCalls {
		switch {
		case step.finish == message.FinishReasonMaxTokens:
			return message.TurnEndMaxTokens
		case step.finish == message.FinishReasonContentFilter:
			return message.TurnEndContentFilter
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
	default:
		// The provider ended a step that called tools, and no guard
		// stopped the loop, so the model is taken at its word.
		return message.TurnEndCompleted
	}
}

// turnEndAfterCompaction applies the rules that follow the precedence:
// only a compaction the run required changes how the run ended.
func turnEndAfterCompaction(end message.TurnEnd, summarizeErr error) message.TurnEnd {
	if end != message.TurnEndContextGuard && end != message.TurnEndContextOverflow {
		return end
	}
	switch {
	case errors.Is(summarizeErr, ErrCompactionBlocked):
		return message.TurnEndCompactionBlocked
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
