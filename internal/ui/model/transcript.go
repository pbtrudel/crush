package model

import (
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
)

// transcriptProjection is what a set of persisted messages projects to: the
// items a chat renders, the items each message owns, and the timestamp of the
// last real user message, which the assistant info footer measures response
// time against.
type transcriptProjection struct {
	items               []chat.MessageItem
	itemIDsByMessage    map[string][]string
	lastUserMessageTime int64
}

// projectTranscriptMessages projects every message of a session into the
// transcript items a chat renders. It is pure: it performs no I/O and touches
// no chat, so a caller can run it off the UI goroutine.
func projectTranscriptMessages(com *common.Common, msgs []message.Message) transcriptProjection {
	msgPtrs := make([]*message.Message, len(msgs))
	for i := range msgs {
		msgPtrs[i] = &msgs[i]
	}
	results := chat.BuildToolResultMap(msgPtrs)

	projection := transcriptProjection{
		items:            make([]chat.MessageItem, 0, len(msgs)*2),
		itemIDsByMessage: make(map[string][]string, len(msgs)),
	}
	if len(msgPtrs) > 0 {
		projection.lastUserMessageTime = msgPtrs[0].CreatedAt
	}
	for _, msg := range msgPtrs {
		projected := projectTranscriptMessage(com, msg, results, projection.lastUserMessageTime)
		projection.items = append(projection.items, projected.items...)
		projection.lastUserMessageTime = projected.lastUserMessageTime
		for messageID, ids := range projected.itemIDsByMessage {
			projection.itemIDsByMessage[messageID] = ids
		}
	}
	return projection
}

// projectTranscriptMessage projects a single message. lastUser carries the
// current last-user timestamp so incremental updates apply the same
// assistant-info rules as a full load.
func projectTranscriptMessage(com *common.Common, msg *message.Message, results map[string]message.ToolResult, lastUser int64) transcriptProjection {
	items := chat.ExtractMessageItems(com.Styles, msg, results, com.Workspace.WorkingDir())

	// The info footer belongs to the assistant turn it follows, so the
	// message owns it too and removing the message removes the footer.
	if msg.Role == message.Assistant && chat.ShouldShowAssistantInfo(msg) {
		items = append(items, chat.NewAssistantInfoItem(com.Styles, msg, com.Config(), time.Unix(lastUser, 0)))
	}

	projection := transcriptProjection{
		items:               items,
		itemIDsByMessage:    make(map[string][]string, 1),
		lastUserMessageTime: lastUser,
	}
	recordProjectionItems(projection.itemIDsByMessage, msg.ID, items)

	// History reload advances the last-user mark for every user message,
	// subagent reports included; that is the behavior persisted sessions
	// have always had. Live report handling is the live path's business.
	if msg.Role == message.User {
		projection.lastUserMessageTime = msg.CreatedAt
	}
	return projection
}

// recordProjectionItems records the items a message owns, reusing the slice
// already stored for it.
func recordProjectionItems(dst map[string][]string, messageID string, items []chat.MessageItem) {
	ids := dst[messageID][:0]
	for _, item := range items {
		ids = append(ids, item.ID())
	}
	dst[messageID] = ids
}

// loadNestedToolCalls recursively loads nested tool calls for
// agent/agentic_fetch tools. It performs blocking workspace reads, so callers
// run it from a command rather than from Update or Draw.
func loadNestedToolCalls(com *common.Common, items []chat.MessageItem) {
	for _, item := range items {
		container, containerOK := item.(chat.NestedToolContainer)
		tool, toolOK := item.(chat.ToolMessageItem)
		if !containerOK || !toolOK {
			continue
		}
		sessionID := com.Workspace.CreateAgentToolSessionID(tool.MessageID(), tool.ToolCall().ID)
		ctx, cancel := common.WorkspaceOpContext()
		msgs, err := com.Workspace.ListMessages(ctx, sessionID)
		cancel()
		if err != nil || len(msgs) == 0 {
			continue
		}
		nested := projectTranscriptMessages(com, msgs)
		tools := make([]chat.ToolMessageItem, 0, len(nested.items))
		for _, nestedItem := range nested.items {
			if toolItem, ok := nestedItem.(chat.ToolMessageItem); ok {
				if compact, ok := toolItem.(chat.Compactable); ok {
					compact.SetCompact(true)
				}
				tools = append(tools, toolItem)
			}
		}
		nestedItems := make([]chat.MessageItem, len(tools))
		for i := range tools {
			nestedItems[i] = tools[i]
		}
		loadNestedToolCalls(com, nestedItems)
		container.SetNestedTools(tools)
	}
}
