package claudeagent

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/fantasy"
)

// stepOutcome is how a step ended.
type stepOutcome int

const (
	// stepPending means the step is still streaming.
	stepPending stepOutcome = iota
	// stepTools means the reply paused for tool calls Crush has to run.
	stepTools
	// stepDone means the turn is over.
	stepDone
)

// streamConverter turns the CLI's messages into fantasy stream parts for one
// step.
type streamConverter struct {
	req     turnRequest
	pending *pendingTools
	emit    emitter
	usage   *usageTracker
	errs    *errorTracker

	blocks      map[int]string
	messageID   string
	streamed    map[string]bool
	calledTools bool

	replyParts    []ReplyPart
	assistantUUID string
	toolMessageID map[string]string
	session       string
}

func newStreamConverter(req turnRequest, pending *pendingTools, emit emitter) *streamConverter {
	return &streamConverter{
		req:           req,
		pending:       pending,
		emit:          emit,
		usage:         newUsageTracker(),
		errs:          newErrorTracker(),
		blocks:        make(map[int]string),
		streamed:      make(map[string]bool),
		toolMessageID: make(map[string]string),
	}
}

// convert consumes one message and reports whether the step is over.
func (c *streamConverter) convert(env *envelope) (stepOutcome, error) {
	c.errs.observe(env)

	switch env.Type {
	case msgSystem:
		return c.convertSystem(env)
	case msgStreamEvent:
		return c.convertStreamEvent(env)
	case msgAssistant:
		return c.convertAssistant(env)
	case msgUser:
		c.convertUser(env)
		return stepPending, nil
	case msgResult:
		return c.convertResult(env)
	case msgRateLimitEvent:
		return stepPending, nil
	default:
		return stepPending, nil
	}
}

func (c *streamConverter) convertSystem(env *envelope) (stepOutcome, error) {
	if env.Subtype != "init" || len(c.req.Tools) == 0 {
		return stepPending, nil
	}
	for _, server := range env.MCPServers {
		if server.Name == mcpServerName && server.Status == "connected" {
			return stepPending, nil
		}
	}
	// Without the tool server Claude would run with no tools at all, which
	// silently breaks every Crush feature that depends on them.
	return stepDone, fmt.Errorf("the Crush tool server did not connect to Claude Code")
}

func (c *streamConverter) convertStreamEvent(env *envelope) (stepOutcome, error) {
	if env.ParentToolUseID != nil {
		// A subagent's stream. Crush does not offer Claude Code's subagent tool,
		// so there is nothing to show.
		return stepPending, nil
	}
	var event streamEvent
	if err := json.Unmarshal(env.Event, &event); err != nil {
		return stepPending, nil
	}
	switch event.Type {
	case "message_start":
		var start streamMessageStart
		if err := json.Unmarshal(event.Message, &start); err != nil {
			return stepPending, nil
		}
		c.messageID = start.ID
		c.streamed[start.ID] = true
		c.usage.update(start.ID, start.Usage)

	case "content_block_start":
		var block streamContentBlock
		if err := json.Unmarshal(event.ContentBlock, &block); err != nil {
			return stepPending, nil
		}
		id := c.blockID(event.Index)
		switch block.Type {
		case "text":
			c.blocks[event.Index] = "text"
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: id}); err != nil {
				return stepDone, err
			}
			if block.Text != "" {
				if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: id, Delta: block.Text}); err != nil {
					return stepDone, err
				}
			}
		case "thinking":
			c.blocks[event.Index] = "reasoning"
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningStart, ID: id}); err != nil {
				return stepDone, err
			}
			if block.Thinking != "" {
				if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: id, Delta: block.Thinking}); err != nil {
					return stepDone, err
				}
			}
		}

	case "content_block_delta":
		var delta streamDelta
		if err := json.Unmarshal(event.Delta, &delta); err != nil {
			return stepPending, nil
		}
		id := c.blockID(event.Index)
		switch delta.Type {
		case "text_delta":
			if delta.Text == "" {
				return stepPending, nil
			}
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: id, Delta: delta.Text}); err != nil {
				return stepDone, err
			}
		case "thinking_delta":
			if delta.Thinking == "" {
				return stepPending, nil
			}
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: id, Delta: delta.Thinking}); err != nil {
				return stepDone, err
			}
		}

	case "content_block_stop":
		kind := c.blocks[event.Index]
		delete(c.blocks, event.Index)
		id := c.blockID(event.Index)
		switch kind {
		case "text":
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: id}); err != nil {
				return stepDone, err
			}
		case "reasoning":
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningEnd, ID: id}); err != nil {
				return stepDone, err
			}
		}

	case "message_delta":
		c.usage.update(c.messageID, event.Usage)

	case "message_stop":
		if !c.calledTools {
			return stepPending, nil
		}
		// The step ends here so Crush can run the tools; the Claude turn keeps
		// waiting for their results.
		return stepTools, c.emit(fantasy.StreamPart{
			Type:         fantasy.StreamPartTypeFinish,
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        c.usage.snapshot(),
		})
	}
	return stepPending, nil
}

func (c *streamConverter) convertAssistant(env *envelope) (stepOutcome, error) {
	if env.ParentToolUseID != nil {
		return stepPending, nil
	}
	if env.Error != "" {
		// A synthetic API error is a diagnostic, not an answer.
		return stepPending, nil
	}
	var payload assistantPayload
	if err := json.Unmarshal(env.Message, &payload); err != nil {
		return stepPending, nil
	}
	if env.UUID != "" {
		c.assistantUUID = env.UUID
	}
	if env.SessionID != "" {
		c.session = env.SessionID
	}
	c.usage.update(payload.ID, payload.Usage)

	for _, block := range payload.Content {
		switch block.Type {
		case "tool_use":
			name, ok := originalToolName(block.Name, c.req.ToolNames)
			if !ok {
				if strings.HasPrefix(block.Name, mcpToolPrefix) {
					// The tool was removed after the process started. Answer it
					// here so the turn continues instead of hanging.
					c.pending.expect(block.ID)
					c.pending.resolve(block.ID, mcpContent("This tool is no longer available.", "", "", true))
				}
				continue
			}
			c.calledTools = true
			c.pending.expect(block.ID)
			c.replyParts = append(c.replyParts, ReplyPart{
				ToolCallID: block.ID,
				ToolName:   name,
				Input:      string(block.Input),
			})
			if err := c.emit(fantasy.StreamPart{
				Type:          fantasy.StreamPartTypeToolCall,
				ID:            block.ID,
				ToolCallName:  name,
				ToolCallInput: string(block.Input),
			}); err != nil {
				return stepDone, err
			}

		case "text":
			// The reply is recorded whether or not it was streamed: its
			// fingerprint is what lets the next turn recognise that Claude
			// already has this message.
			c.replyParts = append(c.replyParts, ReplyPart{Text: block.Text})
			if c.streamed[payload.ID] {
				continue
			}
			id := env.UUID + ":text"
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: id}); err != nil {
				return stepDone, err
			}
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: id, Delta: block.Text}); err != nil {
				return stepDone, err
			}
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: id}); err != nil {
				return stepDone, err
			}

		case "thinking":
			if c.streamed[payload.ID] {
				continue
			}
			id := env.UUID + ":thinking"
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningStart, ID: id}); err != nil {
				return stepDone, err
			}
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: id, Delta: block.Thinking}); err != nil {
				return stepDone, err
			}
			if err := c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningEnd, ID: id}); err != nil {
				return stepDone, err
			}
		}
	}
	return stepPending, nil
}

// convertUser records which transcript message carried each tool result, which
// is what makes a later checkpoint resumable.
func (c *streamConverter) convertUser(env *envelope) {
	var payload userPayload
	if err := json.Unmarshal(env.Message, &payload); err != nil {
		return
	}
	for _, block := range payload.Content {
		if block.Type == "tool_result" && block.ToolUseID != "" {
			c.toolMessageID[block.ToolUseID] = env.UUID
		}
	}
}

func (c *streamConverter) convertResult(env *envelope) (stepOutcome, error) {
	if env.NumTurns == 1 {
		// The turn total is only usable when it covers a single model call;
		// otherwise it sums calls whose usage was already counted.
		c.usage.applyTurnTotal(env.Usage)
	}
	if err := c.errs.result(env); err != nil {
		return stepDone, c.emit(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: err})
	}
	reason := fantasy.FinishReasonStop
	if env.StopReason == "max_tokens" {
		reason = fantasy.FinishReasonLength
	}
	return stepDone, c.emit(fantasy.StreamPart{
		Type:         fantasy.StreamPartTypeFinish,
		FinishReason: reason,
		Usage:        c.usage.snapshot(),
	})
}

func (c *streamConverter) blockID(index int) string {
	return fmt.Sprintf("%s:%d", c.messageID, index)
}

// reply returns the assistant content this step produced, for checkpointing.
func (c *streamConverter) reply() []ReplyPart { return c.replyParts }

// messageUUID returns the transcript ID of the step's final assistant message.
func (c *streamConverter) messageUUID() string { return c.assistantUUID }

// toolMessages returns the transcript ID that carried each tool result.
func (c *streamConverter) toolMessages() map[string]string { return c.toolMessageID }

// sessionID returns the Claude session ID the CLI reported.
func (c *streamConverter) sessionID() string { return c.session }
