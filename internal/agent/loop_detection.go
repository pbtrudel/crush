package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"charm.land/fantasy"
)

const (
	loopDetectionWindowSize = 20
	loopDetectionMaxRepeats = 10
)

// repeatToolReminderThresholds are the consecutive-repeat counts that earn
// an advisory reminder. The hard stop sits above the last one so the model
// gets a chance to change course first.
var repeatToolReminderThresholds = []int{3, 5, 8}

// consecutiveToolCallRepeats returns the number of identical tool-call
// signatures at the end of steps and the name of the repeated tool.
func consecutiveToolCallRepeats(steps []fantasy.StepResult) (count int, toolName string) {
	if len(steps) == 0 {
		return 0, ""
	}
	last := getToolInteractionSignature(steps[len(steps)-1].Content)
	if last == "" {
		return 0, ""
	}
	toolName = lastToolCallName(steps[len(steps)-1])
	for i := len(steps) - 1; i >= 0; i-- {
		if getToolInteractionSignature(steps[i].Content) != last {
			break
		}
		count++
	}
	return count, toolName
}

func lastToolCallName(step fantasy.StepResult) string {
	calls := step.Content.ToolCalls()
	if len(calls) == 0 {
		return ""
	}
	return calls[len(calls)-1].ToolName
}

// repeatToolReminder renders the advisory message injected after a repeated
// tool call. It is a user-role system reminder so every provider accepts it
// and it replays like ordinary conversation history.
func repeatToolReminder(toolName string, count int) string {
	name := toolName
	if name == "" {
		name = "the same tool"
	}
	return fmt.Sprintf(
		"<system_reminder>You are repeating the exact same tool call with identical arguments (tool: %s, %d times). Carefully analyze the previous result before calling again: if the task is not complete, try a different approach or different arguments instead of repeating the call.</system_reminder>",
		name,
		count,
	)
}

// hasRepeatedToolCalls checks whether the agent is stuck in a loop by looking
// at recent steps. It examines the last windowSize steps and returns true if
// any tool-call signature appears more than maxRepeats times.
func hasRepeatedToolCalls(steps []fantasy.StepResult, windowSize, maxRepeats int) bool {
	if len(steps) < windowSize {
		return false
	}

	window := steps[len(steps)-windowSize:]
	counts := make(map[string]int)

	for _, step := range window {
		sig := getToolInteractionSignature(step.Content)
		if sig == "" {
			continue
		}
		counts[sig]++
		if counts[sig] > maxRepeats {
			return true
		}
	}

	return false
}

// getToolInteractionSignature computes a hash signature for the tool
// interactions in a single step's content. It pairs tool calls with their
// results (matched by ToolCallID) and returns a hex-encoded SHA-256 hash.
// If the step contains no tool calls, it returns "".
func getToolInteractionSignature(content fantasy.ResponseContent) string {
	toolCalls := content.ToolCalls()
	if len(toolCalls) == 0 {
		return ""
	}

	// Index tool results by their ToolCallID for fast lookup.
	resultsByID := make(map[string]fantasy.ToolResultContent)
	for _, tr := range content.ToolResults() {
		resultsByID[tr.ToolCallID] = tr
	}

	h := sha256.New()
	for _, tc := range toolCalls {
		output := ""
		if tr, ok := resultsByID[tc.ToolCallID]; ok {
			output = toolResultOutputString(tr.Result)
		}
		io.WriteString(h, tc.ToolName)
		io.WriteString(h, "\x00")
		io.WriteString(h, tc.Input)
		io.WriteString(h, "\x00")
		io.WriteString(h, output)
		io.WriteString(h, "\x00")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// toolResultOutputString converts a ToolResultOutputContent to a stable string
// representation for signature comparison.
func toolResultOutputString(result fantasy.ToolResultOutputContent) string {
	if result == nil {
		return ""
	}
	if text, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result); ok {
		return text.Text
	}
	if errResult, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](result); ok {
		if errResult.Error != nil {
			return errResult.Error.Error()
		}
		return ""
	}
	if media, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result); ok {
		return media.Data
	}
	return ""
}
