package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestTurnEndForError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		err            error
		deadlinePassed bool
		want           message.TurnEnd
	}{
		// The command cancels a tree whose deadline passed; that cancel
		// carries no cause, so the deadline itself decides.
		{name: "cancel after the deadline", err: context.Canceled, deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "repetition after the deadline", err: errRepetitionDetected, deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "provider failure after the deadline", err: &fantasy.ProviderError{StatusCode: 500, Message: "boom"}, deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "caller deadline after the tree's deadline", err: fmt.Errorf("teammate turn: %w", context.DeadlineExceeded), deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "repetition", err: errRepetitionDetected, want: message.TurnEndRepetitionGuard},
		// The repetition detector stops the run by cancelling it.
		{name: "repetition that also cancelled", err: errors.Join(errRepetitionDetected, context.Canceled), want: message.TurnEndRepetitionGuard},
		{name: "cancel", err: context.Canceled, want: message.TurnEndCanceled},
		{name: "wrapped cancel", err: fmt.Errorf("stream: %w", context.Canceled), want: message.TurnEndCanceled},
		{name: "own request timeout", err: &requestTimeoutError{timeout: time.Second, cause: context.DeadlineExceeded}, want: message.TurnEndRequestTimeout},
		{name: "context overflow", err: &fantasy.ProviderError{StatusCode: 400, ContextTooLargeErr: true}, want: message.TurnEndContextOverflow},
		{name: "provider failure", err: &fantasy.ProviderError{StatusCode: 500, Message: "boom"}, want: message.TurnEndProviderError},
		{name: "sign-in failure", err: &fantasy.ProviderError{StatusCode: 401, Message: "unauthorized"}, want: message.TurnEndProviderError},
		{name: "fantasy error", err: &fantasy.Error{Title: "bad response", Message: "cut short"}, want: message.TurnEndProviderError},
		{name: "network failure after retries", err: &fantasy.RetryError{Errors: []error{errors.New("connection reset")}}, want: message.TurnEndProviderError},
		{name: "network failure", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, want: message.TurnEndProviderError},
		{name: "caller deadline", err: fmt.Errorf("teammate turn: %w", context.DeadlineExceeded), want: message.TurnEndCanceled},
		{name: "anything else", err: errors.New("boom"), want: message.TurnEndError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, turnEndForError(tt.err, tt.deadlinePassed))
		})
	}
}

func TestTurnEndForStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step stepEnding
		want message.TurnEnd
	}{
		{name: "final answer", step: stepEnding{finish: message.FinishReasonEndTurn, hasText: true}, want: message.TurnEndCompleted},
		// The model had already stopped, so the guard tripping on the same
		// step does not count.
		{name: "final answer as the guard trips", step: stepEnding{finish: message.FinishReasonEndTurn, hasText: true, guardTripped: true}, want: message.TurnEndCompleted},
		{name: "cut at the output limit", step: stepEnding{finish: message.FinishReasonMaxTokens, hasText: true}, want: message.TurnEndMaxTokens},
		{name: "cut at the output limit with no text", step: stepEnding{finish: message.FinishReasonMaxTokens}, want: message.TurnEndMaxTokens},
		{name: "refused", step: stepEnding{finish: message.FinishReasonContentFilter}, want: message.TurnEndContentFilter},
		{name: "nothing said", step: stepEnding{finish: message.FinishReasonEndTurn}, want: message.TurnEndEmpty},
		{name: "guard", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, guardTripped: true}, want: message.TurnEndContextGuard},
		{name: "guard and a halting tool", step: stepEnding{finish: message.FinishReasonEndTurn, hasToolCalls: true, guardTripped: true, toolStopped: true}, want: message.TurnEndContextGuard},
		{name: "halting tool", step: stepEnding{finish: message.FinishReasonEndTurn, hasToolCalls: true, toolStopped: true}, want: message.TurnEndToolStop},
		{name: "halting tool in a loop", step: stepEnding{finish: message.FinishReasonEndTurn, hasToolCalls: true, toolStopped: true, loopRepeated: true}, want: message.TurnEndToolStop},
		{name: "loop", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, loopRepeated: true}, want: message.TurnEndLoopGuard},
		{name: "tool call cut at the output limit", step: stepEnding{finish: message.FinishReasonMaxTokens, hasToolCalls: true}, want: message.TurnEndMaxTokens},
		{name: "tool call refused", step: stepEnding{finish: message.FinishReasonContentFilter, hasToolCalls: true}, want: message.TurnEndContentFilter},
		{name: "cut short by the provider", step: stepEnding{finish: message.FinishReasonError, hasText: true}, want: message.TurnEndProviderError},
		{name: "cut short by the provider with no text", step: stepEnding{finish: message.FinishReasonError}, want: message.TurnEndProviderError},
		{name: "provider ended a step that called tools", step: stepEnding{finish: message.FinishReasonEndTurn, hasToolCalls: true, hasText: true}, want: message.TurnEndCompleted},
		{name: "tool call cut short by the provider", step: stepEnding{finish: message.FinishReasonError, hasToolCalls: true}, want: message.TurnEndProviderError},
		{name: "step budget spent", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, stepsSpent: true}, want: message.TurnEndStepLimit},
		// The model had already stopped on the step that spent the budget.
		{name: "final answer on the step that spends the budget", step: stepEnding{finish: message.FinishReasonEndTurn, hasText: true, stepsSpent: true}, want: message.TurnEndCompleted},
		{name: "provider ended the step that spends the budget", step: stepEnding{finish: message.FinishReasonEndTurn, hasToolCalls: true, hasText: true, stepsSpent: true}, want: message.TurnEndCompleted},
		{name: "halting tool on the step that spends the budget", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, toolStopped: true, stepsSpent: true}, want: message.TurnEndToolStop},
		{name: "loop on the step that spends the budget", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, loopRepeated: true, stepsSpent: true}, want: message.TurnEndLoopGuard},
		{name: "guard on the step that spends the budget", step: stepEnding{finish: message.FinishReasonToolUse, hasToolCalls: true, guardTripped: true, stepsSpent: true}, want: message.TurnEndContextGuard},
		{name: "tool call cut at the output limit as the budget is spent", step: stepEnding{finish: message.FinishReasonMaxTokens, hasToolCalls: true, stepsSpent: true}, want: message.TurnEndMaxTokens},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, turnEndForStep(tt.step))
		})
	}
}

func TestTurnEndAfterCompaction(t *testing.T) {
	t.Parallel()

	blocked := fmt.Errorf("%w: not now", ErrCompactionBlocked)
	failed := errors.New("summary failed")
	tests := []struct {
		name           string
		end            message.TurnEnd
		summarizeErr   error
		deadlinePassed bool
		want           message.TurnEnd
	}{
		{name: "guard compaction stopped by the deadline", end: message.TurnEndContextGuard, summarizeErr: context.Canceled, deadlinePassed: true, want: message.TurnEndTimeLimit},
		{name: "overflow compaction failed after the deadline", end: message.TurnEndContextOverflow, summarizeErr: failed, deadlinePassed: true, want: message.TurnEndTimeLimit},
		// A hook's decision is not the deadline's.
		{name: "guard compaction blocked after the deadline", end: message.TurnEndContextGuard, summarizeErr: blocked, deadlinePassed: true, want: message.TurnEndCompactionBlocked},
		{name: "compaction after a completed turn stopped by the deadline", end: message.TurnEndCompleted, summarizeErr: context.Canceled, deadlinePassed: true, want: message.TurnEndCompleted},
		{name: "compaction after a step limit failed", end: message.TurnEndStepLimit, summarizeErr: failed, want: message.TurnEndStepLimit},
		{name: "guard compacted", end: message.TurnEndContextGuard, want: message.TurnEndContextGuard},
		{name: "guard compaction blocked", end: message.TurnEndContextGuard, summarizeErr: blocked, want: message.TurnEndCompactionBlocked},
		{name: "guard compaction failed", end: message.TurnEndContextGuard, summarizeErr: failed, want: message.TurnEndCompactionFailed},
		{name: "guard compaction cancelled", end: message.TurnEndContextGuard, summarizeErr: fmt.Errorf("get session: %w", context.Canceled), want: message.TurnEndCanceled},
		{name: "overflow compaction blocked", end: message.TurnEndContextOverflow, summarizeErr: blocked, want: message.TurnEndCompactionBlocked},
		{name: "overflow compaction failed", end: message.TurnEndContextOverflow, summarizeErr: failed, want: message.TurnEndCompactionFailed},
		{name: "overflow compaction cancelled", end: message.TurnEndContextOverflow, summarizeErr: context.Canceled, want: message.TurnEndCanceled},
		// The model finished; the compaction afterwards is housekeeping.
		{name: "compaction after a completed turn failed", end: message.TurnEndCompleted, summarizeErr: failed, want: message.TurnEndCompleted},
		{name: "compaction after a completed turn blocked", end: message.TurnEndCompleted, summarizeErr: blocked, want: message.TurnEndCompleted},
		{name: "unrelated ending is left alone", end: message.TurnEndCanceled, summarizeErr: errors.New("summary failed"), want: message.TurnEndCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, turnEndAfterCompaction(tt.end, tt.summarizeErr, tt.deadlinePassed))
		})
	}
}

func TestLastStepEnding(t *testing.T) {
	t.Parallel()

	msg := &message.Message{Role: message.Assistant, Parts: []message.ContentPart{
		message.TextContent{Text: "  \n"},
		message.ToolCall{ID: "c1", Name: "bash", Input: "{}", Finished: true},
	}}
	msg.AddFinish(message.FinishReasonEndTurn, "", "")
	steps := []fantasy.StepResult{{Response: fantasy.Response{Content: fantasy.ResponseContent{
		fantasy.ToolCallContent{ToolCallID: "c1", ToolName: "bash", Input: "{}"},
		fantasy.ToolResultContent{ToolCallID: "c1", ToolName: "bash", StopTurn: true},
	}}}}

	require.Equal(t, stepEnding{
		finish:       message.FinishReasonEndTurn,
		hasToolCalls: true,
		guardTripped: true,
		toolStopped:  true,
	}, lastStepEnding(msg, steps, true))
}
