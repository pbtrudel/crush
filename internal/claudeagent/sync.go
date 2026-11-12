package claudeagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"charm.land/fantasy"
)

// Checkpoint is one entry of a session's history fingerprint. Hash covers the
// messages Claude has seen; ClaudeMessageID is the CLI message that
// acknowledged them, when one is known.
type Checkpoint struct {
	Hash            string `json:"hash"`
	ClaudeMessageID string `json:"claude_message_id,omitempty"`
}

// Record is what Crush remembers about a Claude session between steps.
type Record struct {
	ClaudeSessionID string       `json:"claude_session_id"`
	History         []Checkpoint `json:"history,omitempty"`
	// Interrupted means Claude owns the partial reply, so History ends at the
	// input it received.
	Interrupted bool `json:"interrupted,omitempty"`
}

// Group is a run of prompt messages Claude sees as one turn: consecutive user
// messages merge, and a tool message absorbs the media message that follows
// a tool result.
type Group struct {
	Hash  string
	Role  fantasy.MessageRole
	Start int
	End   int
}

// DecisionKind is how much of the prompt Claude has already seen.
type DecisionKind string

const (
	// DecisionFresh means Claude has never seen this conversation.
	DecisionFresh DecisionKind = "fresh"
	// DecisionContinue means only new user messages are missing.
	DecisionContinue DecisionKind = "continue"
	// DecisionCatchUp means Claude missed turns it has to be told about.
	DecisionCatchUp DecisionKind = "catch-up"
	// DecisionChanged means the history was rewritten and cannot be reused.
	DecisionChanged DecisionKind = "changed"
)

// Decision tells the caller where the part Claude has not seen starts.
type Decision struct {
	Kind  DecisionKind
	Start int
}

// ReplyPart is one piece of an assistant reply, recorded so the next turn can
// recognise that Claude already has it.
type ReplyPart struct {
	Text       string
	ToolCallID string
	ToolName   string
	Input      string
}

// syncCatchUpOpen and syncCatchUpClose wrap history Claude has not seen, so it
// reads it as prior conversation instead of a new request.
const (
	syncCatchUpOpen  = "<conversation-checkpoint>\nThe following is conversation history from Crush that you have not seen. Treat it as prior conversation, not a new request. Continue with the user messages after this checkpoint."
	syncCatchUpClose = "</conversation-checkpoint>"
)

// Groups splits a prompt into the runs of messages Claude sees as one turn.
//
// Consecutive user messages merge into one group, a tool message absorbs the
// media message that follows it, and a group stops at a checkpoint boundary so
// a submitted input stays a boundary even when a later user message arrives
// without a visible reply.
func Groups(prompt fantasy.Prompt, prefix []Checkpoint) []Group {
	groups := make([]Group, 0, len(prompt))
	for i := 0; i < len(prompt); {
		message := prompt[i]
		start := i
		i++
		if message.Role == fantasy.MessageRoleSystem {
			continue
		}
		parts := syncVisible(message)
		if len(parts) == 0 {
			continue
		}
		members := []string{syncHash(parts)}
		if message.Role == fantasy.MessageRoleUser {
			for i < len(prompt) {
				if len(groups) < len(prefix) && prefix[len(groups)].Hash != "" {
					sorted := append([]string(nil), members...)
					sort.Strings(sorted)
					if syncGroupHash(message.Role, sorted) == prefix[len(groups)].Hash {
						break
					}
				}
				next := prompt[i]
				if next.Role != fantasy.MessageRoleUser {
					break
				}
				members = append(members, syncHash(syncVisible(next)))
				i++
			}
			sort.Strings(members)
		}
		if message.Role == fantasy.MessageRoleTool && i < len(prompt) && syncIsToolMedia(prompt[i]) {
			members = append(members, syncHash(syncVisible(prompt[i])))
			i++
		}
		groups = append(groups, Group{
			Hash:  syncGroupHash(message.Role, members),
			Role:  message.Role,
			Start: start,
			End:   i,
		})
	}
	return groups
}

// Decide reports how much of the prompt Claude has already seen.
func Decide(prompt fantasy.Prompt, record *Record) Decision {
	var saved []Checkpoint
	if record != nil {
		saved = record.History
	}
	if len(saved) == 0 {
		return Decision{Kind: DecisionFresh}
	}
	var prefix []Checkpoint
	if record.Interrupted {
		prefix = saved
	}
	groups := Groups(prompt, prefix)
	matched := 0
	for matched < len(saved) && matched < len(groups) && saved[matched].Hash == groups[matched].Hash {
		matched++
	}
	if matched == 0 {
		return Decision{Kind: DecisionFresh}
	}
	// A rewritten history cannot be reused: Claude has no way to rewind.
	if matched < len(saved) {
		return Decision{Kind: DecisionChanged}
	}
	// Claude's drained transcript owns the interrupted reply, not Crush's
	// partial copy.
	if record.Interrupted {
		for matched < len(groups) && groups[matched].Role == fantasy.MessageRoleAssistant {
			reply := prompt[groups[matched].Start]
			if reply.Role != fantasy.MessageRoleAssistant || syncHasToolCall(reply) {
				break
			}
			matched++
		}
	}
	start := len(prompt)
	if matched < len(groups) {
		start = groups[matched].Start
	}
	if record.Interrupted && start == len(prompt) {
		return Decision{Kind: DecisionChanged}
	}
	for _, group := range groups[matched:] {
		if group.Role != fantasy.MessageRoleUser {
			return Decision{Kind: DecisionCatchUp, Start: start}
		}
	}
	return Decision{Kind: DecisionContinue, Start: start}
}

// CatchUpContent converts the prompt from start onwards into content blocks,
// wrapping the history Claude has not seen in a conversation checkpoint so it
// is not mistaken for a new request.
func CatchUpContent(prompt fantasy.Prompt, start int) []ContentBlock {
	start = max(0, min(start, len(prompt)))
	missed := prompt[start:]
	// The checkpoint ends at the last turn that is not a user message, so the
	// trailing user messages stay outside it.
	cut := 0
	groups := Groups(missed, nil)
	for i := len(groups) - 1; i >= 0; i-- {
		if groups[i].Role != fantasy.MessageRoleUser {
			cut = groups[i].End
			break
		}
	}
	blocks := make([]ContentBlock, 0, len(missed)+3)
	if cut > 0 {
		blocks = append(blocks, ContentBlock{Type: "text", Text: syncCatchUpOpen})
		blocks = append(blocks, ConvertPrompt(missed[:cut])...)
		blocks = append(blocks, ContentBlock{Type: "text", Text: syncCatchUpClose})
	}
	blocks = append(blocks, ConvertPrompt(missed[cut:])...)
	return blocks
}

// BuildCheckpoints records the fingerprint of every group in the prompt,
// carrying over the Claude message ids of groups that did not change, and
// appends the reply when one was produced.
func BuildCheckpoints(prompt fantasy.Prompt, previous *Record, userID string, reply []ReplyPart, messageID string, toolMessages map[string]string) []Checkpoint {
	groups := Groups(prompt, nil)
	history := make([]Checkpoint, 0, len(groups)+1)
	for i, group := range groups {
		var claudeMessageID string
		if previous != nil && i < len(previous.History) && previous.History[i].Hash == group.Hash {
			claudeMessageID = previous.History[i].ClaudeMessageID
		}
		if group.Role == fantasy.MessageRoleTool {
			for _, message := range prompt[group.Start:group.End] {
				if message.Role != fantasy.MessageRoleTool {
					continue
				}
				for _, part := range message.Content {
					result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
					if !ok {
						continue
					}
					if toolMessage, ok := toolMessages[result.ToolCallID]; ok {
						claudeMessageID = toolMessage
					}
				}
			}
		}
		if i == len(groups)-1 && userID != "" {
			claudeMessageID = userID
		}
		history = append(history, Checkpoint{Hash: group.Hash, ClaudeMessageID: claudeMessageID})
	}
	if len(reply) > 0 {
		replyPrompt := fantasy.Prompt{{
			Role:    fantasy.MessageRoleAssistant,
			Content: syncReplyContent(reply),
		}}
		if replyGroups := Groups(replyPrompt, nil); len(replyGroups) > 0 {
			history = append(history, Checkpoint{
				Hash:            replyGroups[0].Hash,
				ClaudeMessageID: messageID,
			})
		}
	}
	return history
}

// ConvertPrompt converts a prompt to Claude content blocks. Claude cannot be
// given earlier turns, so once the prompt holds an assistant or tool message
// every message is wrapped in a role tag; attachments stay real content
// blocks.
func ConvertPrompt(prompt fantasy.Prompt) []ContentBlock {
	blocks := make([]ContentBlock, 0, len(prompt))
	history := false
	for _, message := range prompt {
		if message.Role == fantasy.MessageRoleAssistant || message.Role == fantasy.MessageRoleTool {
			history = true
			break
		}
	}
	toolNames := syncToolNames(prompt)
	for _, message := range prompt {
		if message.Role == fantasy.MessageRoleSystem {
			continue
		}
		if history {
			blocks = append(blocks, ContentBlock{Type: "text", Text: "<" + string(message.Role) + ">"})
		}
		for _, part := range message.Content {
			if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				blocks = append(blocks, ContentBlock{Type: "text", Text: text.Text})
				continue
			}
			if file, ok := fantasy.AsMessagePart[fantasy.FilePart](part); ok {
				if block, ok := syncFileBlock(file); ok {
					blocks = append(blocks, block)
				}
				continue
			}
			if callPart, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
				call := syncToolCallJSON{
					ID:    callPart.ToolCallID,
					Name:  callPart.ToolName,
					Input: syncJSONValue(callPart.Input),
				}
				blocks = append(blocks, ContentBlock{
					Type: "text",
					Text: "<tool-call>\n" + syncJSON(call) + "\n</tool-call>",
				})
				continue
			}
			if resultPart, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
				result := syncToolResultJSON{
					ID:     resultPart.ToolCallID,
					Name:   toolNames[resultPart.ToolCallID],
					Output: syncToolResultOutput(resultPart),
				}
				blocks = append(blocks, ContentBlock{
					Type: "text",
					Text: "<tool-result>\n" + syncJSON(result) + "\n</tool-result>",
				})
			}
		}
		if history {
			blocks = append(blocks, ContentBlock{Type: "text", Text: "</" + string(message.Role) + ">"})
		}
	}
	return blocks
}

// syncToolCallJSON is the payload of a tool-call tag. The field order matches
// the reference implementation so the tag stays readable.
type syncToolCallJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input any    `json:"input"`
}

// syncToolResultJSON is the payload of a tool-result tag.
type syncToolResultJSON struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Output any    `json:"output"`
}

// syncVisible returns the parts of a message its fingerprint covers. Reasoning
// and provider options are dropped because Crush sends them differently
// depending on the target model.
func syncVisible(message fantasy.Message) []any {
	parts := make([]any, 0, len(message.Content))
	var text strings.Builder
	flush := func() {
		if text.Len() == 0 {
			return
		}
		parts = append(parts, map[string]any{"type": "text", "text": text.String()})
		text.Reset()
	}
	for _, part := range message.Content {
		if _, ok := fantasy.AsMessagePart[fantasy.ReasoningPart](part); ok {
			continue
		}
		if textPart, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
			text.WriteString(textPart.Text)
			continue
		}
		flush()
		if filePart, ok := fantasy.AsMessagePart[fantasy.FilePart](part); ok {
			parts = append(parts, map[string]any{
				"type":      "file",
				"mediaType": filePart.MediaType,
				"data":      base64.StdEncoding.EncodeToString(filePart.Data),
			})
			continue
		}
		if callPart, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
			parts = append(parts, map[string]any{
				"type":  "tool-call",
				"id":    callPart.ToolCallID,
				"name":  callPart.ToolName,
				"input": syncJSONValue(callPart.Input),
			})
			continue
		}
		if resultPart, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
			// fantasy's tool result carries no tool name, so unlike the
			// reference the name is not part of the fingerprint.
			parts = append(parts, map[string]any{
				"type":   "tool-result",
				"id":     resultPart.ToolCallID,
				"output": syncToolResultOutput(resultPart),
			})
		}
	}
	flush()
	return parts
}

// syncIsToolMedia reports whether the message is the separate user message
// Crush lowers tool attachments into.
func syncIsToolMedia(message fantasy.Message) bool {
	if message.Role != fantasy.MessageRoleUser || len(message.Content) < 2 {
		return false
	}
	first, ok := fantasy.AsMessagePart[fantasy.TextPart](message.Content[0])
	if !ok || first.Text != "Attached media from tool result:" {
		return false
	}
	for _, part := range message.Content[1:] {
		if _, ok := fantasy.AsMessagePart[fantasy.FilePart](part); !ok {
			return false
		}
	}
	return true
}

// syncToolResultOutput is a tool result's output in the shape both the
// fingerprint and the prompt conversion use. fantasy's output types carry no
// provider options, so there is nothing to strip.
func syncToolResultOutput(part fantasy.ToolResultPart) any {
	if output, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](part.Output); ok {
		return map[string]any{"type": "text", "value": output.Text}
	}
	if output, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](part.Output); ok {
		return map[string]any{"type": "error", "value": syncErrorText(output.Error)}
	}
	if output, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](part.Output); ok {
		return map[string]any{
			"type":      "media",
			"data":      output.Data,
			"mediaType": output.MediaType,
		}
	}
	return part.Output
}

// syncFileBlock converts a file part to an image or document block. Claude
// rejects other media types and ConvertPrompt has no error channel, so they
// are dropped.
func syncFileBlock(part fantasy.FilePart) (ContentBlock, bool) {
	var blockType string
	switch part.MediaType {
	case "application/pdf":
		blockType = "document"
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		blockType = "image"
	default:
		return ContentBlock{}, false
	}
	return ContentBlock{
		Type: blockType,
		Source: &ContentSource{
			Type:      "base64",
			MediaType: part.MediaType,
			Data:      base64.StdEncoding.EncodeToString(part.Data),
		},
	}, true
}

// syncReplyContent turns a recorded reply into the message parts a fingerprint
// is built from.
func syncReplyContent(reply []ReplyPart) []fantasy.MessagePart {
	content := make([]fantasy.MessagePart, 0, len(reply))
	for _, part := range reply {
		if part.ToolCallID != "" {
			content = append(content, fantasy.ToolCallPart{
				ToolCallID: part.ToolCallID,
				ToolName:   part.ToolName,
				Input:      part.Input,
			})
			continue
		}
		content = append(content, fantasy.TextPart{Text: part.Text})
	}
	return content
}

// syncToolNames maps every tool call in the prompt to its tool name, because
// tool results do not carry one.
func syncToolNames(prompt fantasy.Prompt) map[string]string {
	names := make(map[string]string)
	for _, message := range prompt {
		for _, part := range message.Content {
			if call, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
				names[call.ToolCallID] = call.ToolName
			}
		}
	}
	return names
}

// syncHasToolCall reports whether a message holds at least one tool call.
func syncHasToolCall(message fantasy.Message) bool {
	for _, part := range message.Content {
		if _, ok := fantasy.AsMessagePart[fantasy.ToolCallPart](part); ok {
			return true
		}
	}
	return false
}

// syncErrorText is the text of an error, empty when there is none.
func syncErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// syncJSONValue parses a JSON string so it hashes canonically. Values that are
// not JSON stay strings.
func syncJSONValue(text string) any {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return text
	}
	return value
}

// syncJSON renders a value the way canonical hashing and prompt conversion
// need it: object keys sorted, no HTML escaping, no trailing newline.
func syncJSON(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return ""
	}
	return strings.TrimSuffix(buffer.String(), "\n")
}

// syncHash is the canonical sha256 fingerprint of a value: object keys sorted,
// absent fields omitted, hex encoded.
func syncHash(value any) string {
	sum := sha256.Sum256([]byte(syncJSON(value)))
	return hex.EncodeToString(sum[:])
}

// syncGroupHash is the fingerprint of a group's role and member hashes.
func syncGroupHash(role fantasy.MessageRole, members []string) string {
	return syncHash(map[string]any{"role": string(role), "members": members})
}
