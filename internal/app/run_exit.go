package app

import (
	"fmt"

	"github.com/charmbracelet/crush/internal/message"
)

// ExitError ends `crush run --json` with Code. The object the command
// printed already says why, so the error is not printed again.
type ExitError struct {
	Code   int
	Reason message.TurnEnd
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("the run ended with %s", e.Reason)
}

// runExitCode maps the reason of the command's run to the exit code of
// `crush run --json` (measurement design, 3.5): 0 when the model said it
// was done, 2 when the work can be graded but the model did not complete
// the turn, 3 when the outcome says nothing about the model. A reason this
// build does not know is 3.
func runExitCode(reason message.TurnEnd) int {
	switch reason {
	case message.TurnEndCompleted:
		return 0
	case message.TurnEndEmpty, message.TurnEndMaxTokens, message.TurnEndContentFilter,
		message.TurnEndToolStop, message.TurnEndLoopGuard, message.TurnEndRepetitionGuard,
		message.TurnEndContextGuard, message.TurnEndContextOverflow,
		message.TurnEndCompactionBlocked, message.TurnEndStepLimit,
		message.TurnEndTimeLimit:
		return 2
	default:
		return 3
	}
}

// runExitError is what the command returns after printing its object: nil
// for a completed run, else the error that carries the exit code.
func runExitError(reason message.TurnEnd) error {
	code := runExitCode(reason)
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code, Reason: reason}
}
