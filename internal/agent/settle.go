package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
)

// Replies recorded for a tool call that will never be answered. They are
// stored in the transcript, so they are also what the model reads next turn.
const (
	canceledToolCallResult    = "Error: user cancelled assistant tool calling"
	failedToolCallResult      = "There was an error while executing the tool"
	truncatedToolCallResult   = "the model ran out of output tokens before it finished writing this tool call, so the call was never run; retry with smaller arguments, splitting the work across several calls if that helps"
	refusedToolCallResult     = "the provider's safety classifier stopped this response before the tool call was finished, so the call was never run; rephrase the request or try a different model"
	abandonedToolCallResult   = "the turn ended before the model finished writing this tool call, so the call was never run; retry the call if the result is still needed"
	interruptedToolCallResult = "tool call was interrupted and did not produce a result, you may retry this call if the result is still needed"
	interruptedTurnMessage    = "Interrupted"
)

// toolResultsForCalls builds the tool message that must immediately follow
// an assistant message with tool calls. toolResultsByCall holds one result
// per call and is consumed, so a result stored alongside results for
// other assistants is emitted once, next to the call that asked for it. A
// call with no stored result gets a synthetic one; settling should have made
// that impossible, so it is logged as a warning.
func toolResultsForCalls(m message.Message, toolResultsByCall map[string]fantasy.ToolResultPart) fantasy.Message {
	content := make([]fantasy.MessagePart, 0, len(m.ToolCalls()))
	for _, tc := range m.ToolCalls() {
		if tc.ProviderExecuted {
			// The provider ran the call itself and answers it in the
			// response, so a tool_result here would be a second reply to a
			// call it already paired.
			continue
		}
		result, ok := toolResultsByCall[tc.ID]
		delete(toolResultsByCall, tc.ID)
		if ok {
			content = append(content, result)
			continue
		}
		slog.Warn(
			"Injecting synthetic tool result for orphaned tool call",
			"tool_call_id", tc.ID,
			"tool_name", tc.Name,
		)
		content = append(content, fantasy.ToolResultPart{
			ToolCallID: tc.ID,
			Output: fantasy.ToolResultOutputContentError{
				Error: errors.New(interruptedToolCallResult),
			},
		})
	}
	return fantasy.Message{
		Role:    fantasy.MessageRoleTool,
		Content: content,
	}
}

// turnEndReason reports what to tell the model about a call its turn left
// unanswered. Why the turn stopped decides what a retry should change.
func turnEndReason(assistant *message.Message, runErr error) string {
	switch {
	case errors.Is(runErr, context.Canceled):
		return canceledToolCallResult
	case errors.Is(runErr, errRefused):
		return refusedToolCallResult
	case runErr != nil:
		return failedToolCallResult
	}
	finish := assistant.FinishPart()
	if finish == nil {
		return abandonedToolCallResult
	}
	switch finish.Reason {
	case message.FinishReasonMaxTokens:
		return truncatedToolCallResult
	case message.FinishReasonContentFilter:
		return refusedToolCallResult
	default:
		return abandonedToolCallResult
	}
}

// settleTurn answers every call in the turn's last assistant message that
// has no reply yet. Every way a turn ends reaches this, so a call can never
// outlive the turn that made it.
func (a *sessionAgent) settleTurn(ctx context.Context, assistant *message.Message, answered func(toolCallID string) bool, runErr error) error {
	var open []message.ToolCall
	for _, tc := range assistant.ToolCalls() {
		if tc.ProviderExecuted {
			// Nothing to settle: the provider executed the call inside the
			// response and a synthetic reply would break the replay.
			continue
		}
		if !answered(tc.ID) {
			open = append(open, tc)
		}
	}
	_, err := settleToolCalls(ctx, a.messages, assistant, open, turnEndReason(assistant, runErr))
	return err
}

// settleToolCalls marks calls complete and records reason as each one's
// reply. Partial arguments are kept as written; the send path makes them
// valid JSON. The created replies are returned in the order written.
func settleToolCalls(ctx context.Context, messages message.Service, assistant *message.Message, calls []message.ToolCall, reason string) ([]message.Message, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	for _, tc := range calls {
		slog.Warn("Settling a tool call that will never be answered",
			"session_id", assistant.SessionID,
			"tool_call_id", tc.ID,
			"tool_name", tc.Name,
			"reason", reason)
		if !tc.Finished {
			tc.Finished = true
			assistant.AddToolCall(tc)
		}
	}

	// The results are written before the turn is marked finished: the settle
	// queries select on an unfinished turn, so a repair that dies on its
	// result write has to leave the turn selectable, or the call would stay
	// open forever.
	settled := make([]message.Message, 0, len(calls))
	for _, tc := range calls {
		created, err := messages.Create(ctx, assistant.SessionID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{
				ToolCallID:   tc.ID,
				Name:         tc.Name,
				Content:      reason,
				Metadata:     tools.SetErrorKind("", tools.ErrorKindInterrupted),
				IsError:      true,
				FinishedAtMs: time.Now().UnixMilli(),
			}},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to record settled tool result: %w", err)
		}
		settled = append(settled, created)
	}
	if err := messages.Update(ctx, *assistant); err != nil {
		return nil, fmt.Errorf("failed to settle tool calls: %w", err)
	}
	return settled, nil
}

// repairInterruptedToolCalls settles calls a dead process left without a
// reply and returns msgs with the repair applied. The caller must have
// checked that nothing is running the session.
func repairInterruptedToolCalls(ctx context.Context, messages message.Service, msgs []message.Message) ([]message.Message, error) {
	resolved := make(map[string]struct{})
	for _, msg := range msgs {
		for _, tr := range msg.ToolResults() {
			resolved[tr.ToolCallID] = struct{}{}
		}
	}

	var repaired []message.Message
	for i := range msgs {
		msg := &msgs[i]
		if msg.Role != message.Assistant {
			continue
		}
		var orphans []message.ToolCall
		for _, tc := range msg.ToolCalls() {
			if tc.ProviderExecuted {
				continue
			}
			if _, ok := resolved[tc.ID]; !ok {
				orphans = append(orphans, tc)
			}
		}
		if len(orphans) == 0 {
			continue
		}
		// Backdated, since response-time statistics average over it.
		if !msg.IsFinished() {
			msg.AddFinishAt(message.FinishReasonError, interruptedTurnMessage, "", msg.UpdatedAt)
		}
		settled, err := settleToolCalls(ctx, messages, msg, orphans, interruptedToolCallResult)
		if err != nil {
			return nil, err
		}
		repaired = append(repaired, settled...)
	}
	// Replies pair with calls by ID, so they can simply follow the transcript.
	return append(msgs, repaired...), nil
}

// SettleSession repairs what a dead process left behind in a session with
// nothing running in it. It is ListSettledMessages for a reader that pages
// the store instead of loading the whole transcript, and it reads in two
// steps so a healthy session costs almost nothing: first the unfinished
// assistant rows, then, only if one of them made a call the provider did not
// run itself, the tool results created since the oldest of them. Those rows
// go to the same repair ListSettledMessages uses. busy reports whether a run
// owns the session, and is sampled on both sides of the read, since a run
// that starts during it owns the calls the rows still look open on.
func SettleSession(ctx context.Context, messages message.Service, busy func(sessionID string) bool, sessionID string) error {
	if err := messages.FlushAll(ctx); err != nil {
		return err
	}
	if busy(sessionID) {
		return nil
	}
	turns, err := messages.ListUnfinishedAssistants(ctx, sessionID)
	if err != nil {
		return err
	}
	if len(turns) == 0 || !hasOwnToolCall(turns) || busy(sessionID) {
		// Nothing to settle: the common case, and it reads no tool row.
		return nil
	}
	results, err := messages.ListToolMessagesFrom(ctx, sessionID, turns[0].CreatedAt)
	if err != nil {
		return err
	}
	if _, err := repairInterruptedToolCalls(ctx, messages, append(turns, results...)); err != nil {
		// The repair writes results before it finishes the turn, so a
		// failure here leaves the turn open for the next reader: a result
		// that failed to write is retried, and one that did is not written
		// twice.
		slog.Warn("Failed to settle interrupted tool calls",
			"session_id", sessionID, "error", err)
	}
	return nil
}

// hasOwnToolCall reports whether any of the turns made a call the provider did
// not run itself. A provider-run call is answered in the response that made
// it, so it never needs a settle result and is not worth reading tool rows
// for.
func hasOwnToolCall(turns []message.Message) bool {
	for _, turn := range turns {
		for _, tc := range turn.ToolCalls() {
			if !tc.ProviderExecuted {
				return true
			}
		}
	}
	return false
}

// SettleSession repairs the session's dangling tool calls under the settle
// lock, so a paged read cannot settle the same call twice.
func (c *coordinator) SettleSession(ctx context.Context, sessionID string) error {
	c.settleMu.Lock()
	defer c.settleMu.Unlock()
	return SettleSession(ctx, c.messages, c.IsSessionBusy, sessionID)
}

// ListSettledMessages returns a session's transcript with calls a dead
// process left dangling already settled. Readers are serialized so two
// cannot settle the same call twice. Busy is sampled on both sides of the
// read, since a run that ends mid-read left a snapshot missing its results.
func (c *coordinator) ListSettledMessages(ctx context.Context, sessionID string) ([]message.Message, error) {
	c.settleMu.Lock()
	defer c.settleMu.Unlock()

	if err := c.messages.FlushAll(ctx); err != nil {
		return nil, err
	}
	busy := c.IsSessionBusy(sessionID)
	msgs, err := c.messages.List(ctx, sessionID)
	if err != nil || busy || c.IsSessionBusy(sessionID) {
		return msgs, err
	}
	settled, err := repairInterruptedToolCalls(ctx, c.messages, msgs)
	if err != nil {
		// The repair may have stopped halfway, so reread what was stored.
		slog.Warn("Failed to settle interrupted tool calls",
			"session_id", sessionID, "error", err)
		return c.messages.List(ctx, sessionID)
	}
	return settled, nil
}
