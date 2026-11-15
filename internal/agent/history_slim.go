package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
)

// staleReadStub replaces a file read that a later change made stale.
const staleReadStub = "[Earlier contents of %s omitted: the file was changed later in this session. View it again for its current contents.]"

var (
	fileReadTools = []string{tools.ViewToolName, tools.ReadToolName}
	// fileEditTools change the file named by their file_path input.
	fileEditTools = []string{tools.EditToolName, tools.MultiEditToolName, tools.WriteToolName, tools.ReplaceSymbolToolName}
)

// slimColdHistory lightens, in the in-memory copy a request is built from,
// the history that precedes a session's latest long idle gap, and returns
// how many leading messages that covers.
//
// After such a gap the provider's prompt cache has expired, so the request
// re-reads the whole context at full price whatever it contains. That is
// the one moment old history can be rewritten for free; doing it at any
// other time would throw away a warm cache. The result depends only on the
// stored messages and next, so every request after the gap, compaction
// included, rebuilds the same bytes. The session log is never changed.
//
// next is the creation time of a user message that follows msgs but is not
// in it yet (the prompt of the turn being started), or zero.
func slimColdHistory(msgs []message.Message, next int64, idle time.Duration, dropReasoning bool) int {
	cut := coldCut(msgs, next, idle)
	if cut == 0 {
		return 0
	}
	stubStaleReads(msgs[:cut])
	if dropReasoning {
		dropPlainReasoning(msgs[:cut])
	}
	return cut
}

// coldCut returns how many leading messages precede the latest user message
// that arrived at least idle after the message stored before it.
func coldCut(msgs []message.Message, next int64, idle time.Duration) int {
	gap := int64(idle / time.Second)
	if gap <= 0 {
		return 0
	}
	// A compaction summary is listed first but was written after the tail
	// that follows it, so it never counts as "the message before".
	previous := func(i int) (int64, bool) {
		for j := i - 1; j >= 0; j-- {
			if !msgs[j].IsSummaryMessage {
				return msgs[j].CreatedAt, true
			}
		}
		return 0, false
	}
	if before, ok := previous(len(msgs)); ok && next > 0 && next-before >= gap {
		return len(msgs)
	}
	for i := len(msgs) - 1; i > 0; i-- {
		if msgs[i].Role != message.User || msgs[i].IsSummaryMessage {
			continue
		}
		if before, ok := previous(i); ok && msgs[i].CreatedAt-before >= gap {
			return i
		}
	}
	return 0
}

// stubStaleReads replaces the result of a file read with a short note when
// the same file was successfully changed later in msgs. The old contents
// are stale by then, and the note tells the model to read the file again.
func stubStaleReads(msgs []message.Message) {
	type fileCall struct {
		name string
		path string
		at   int
	}
	calls := make(map[string]fileCall)
	for i, msg := range msgs {
		if msg.Role != message.Assistant {
			continue
		}
		for _, tc := range msg.ToolCalls() {
			if !slices.Contains(fileReadTools, tc.Name) && !slices.Contains(fileEditTools, tc.Name) {
				continue
			}
			var input struct {
				FilePath string `json:"file_path"`
			}
			if err := json.Unmarshal([]byte(tc.Input), &input); err != nil || input.FilePath == "" {
				continue
			}
			calls[tc.ID] = fileCall{name: tc.Name, path: filepath.Clean(input.FilePath), at: i}
		}
	}

	lastEdit := make(map[string]int)
	for _, msg := range msgs {
		for _, tr := range msg.ToolResults() {
			call, ok := calls[tr.ToolCallID]
			if !ok || tr.IsError || !slices.Contains(fileEditTools, call.name) {
				continue
			}
			if at, seen := lastEdit[call.path]; !seen || call.at > at {
				lastEdit[call.path] = call.at
			}
		}
	}

	for i := range msgs {
		for j, part := range msgs[i].Parts {
			tr, ok := part.(message.ToolResult)
			if !ok || tr.IsError || tr.Data != "" {
				continue
			}
			call, ok := calls[tr.ToolCallID]
			if !ok || !slices.Contains(fileReadTools, call.name) {
				continue
			}
			// A read and an edit issued in the same step have no known
			// order, so only a strictly later edit makes the read stale.
			edited, ok := lastEdit[call.path]
			if !ok || edited <= call.at {
				continue
			}
			stub := fmt.Sprintf(staleReadStub, call.path)
			if len(tr.Content) <= len(stub) {
				continue
			}
			tr.Content = stub
			msgs[i].Parts[j] = tr
		}
	}
}

// dropPlainReasoning removes reasoning text the provider does not need
// back. Signed or encrypted reasoning is protocol state and stays, and so
// does the reasoning of a message that has nothing else: without it the
// message would vanish from the request.
func dropPlainReasoning(msgs []message.Message) {
	for i := range msgs {
		msg := &msgs[i]
		if msg.Role != message.Assistant || msg.IsSummaryMessage {
			continue
		}
		if msg.Content().Text == "" && len(msg.ToolCalls()) == 0 {
			continue
		}
		msg.Parts = slices.DeleteFunc(slices.Clone(msg.Parts), func(part message.ContentPart) bool {
			reasoning, ok := part.(message.ReasoningContent)
			return ok && reasoning.Signature == "" && reasoning.ThoughtSignature == "" && reasoning.ResponsesData == nil
		})
	}
}
