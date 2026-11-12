package model

import (
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
)

const agentViewerReconcileDelay = 100 * time.Millisecond

type agentViewerReconcileMsg struct{ generation, sequence uint64 }

type agentViewer struct {
	com             *common.Common
	chat            *Chat
	descriptor      dialog.AgentDescriptor
	generation      uint64
	themeGeneration uint64
	session         session.Session

	messagesByID        map[string]message.Message
	itemIDsByMessage    map[string][]string
	lastUserMessageTime int64

	loading  bool
	loadErr  error
	buffered []pubsub.Event[message.Message]

	reconcileNeeded     bool
	reconcileInFlight   bool
	reconcileTimerArmed bool
	reconcileDeadline   time.Time
	reconcileSeq        uint64
	now                 func() time.Time

	width  int
	height int
}

func newAgentViewer(com *common.Common, descriptor dialog.AgentDescriptor, scrollbarMode string, generation uint64) *agentViewer {
	return &agentViewer{
		com:              com,
		chat:             NewChat(com, scrollbarMode),
		descriptor:       descriptor,
		generation:       generation,
		messagesByID:     make(map[string]message.Message),
		itemIDsByMessage: make(map[string][]string),
		now:              time.Now,
	}
}

// Reload refreshes the transcript after connection recovery. The UI owns the
// generation counter so a result cannot match a replacement viewer for the
// same child session.
func (v *agentViewer) Reload(generation uint64) tea.Cmd {
	v.generation = generation
	v.reconcileSeq++
	v.loading = false
	v.reconcileNeeded = false
	v.reconcileTimerArmed = false
	v.reconcileInFlight = true
	return v.loadSnapshot(true)
}

func (v *agentViewer) HandleLoad(msg agentViewerLoadMsg) tea.Cmd {
	if msg.generation != v.generation || msg.sessionID != v.descriptor.SessionID {
		return nil
	}
	if msg.err == nil && msg.themeGeneration != v.themeGeneration {
		// Keep generation checking even though projection now happens at
		// installation: a load that overlapped theme invalidation is retried
		// before any items are installed.
		return v.loadSnapshot(msg.reconcile)
	}
	if msg.reconcile {
		v.reconcileInFlight = false
	} else {
		v.loading = false
		v.chat.SetExternalAnimation(v.descriptor.Status == agentrun.StatusRunning)
	}
	if msg.err != nil {
		v.loadErr = msg.err
		if msg.reconcile {
			// Retain the current transcript, but do not strand events that
			// arrived while the failed read was outstanding. The next selected
			// event arms the only retry; any timer from this read is stale.
			v.drainBuffered()
			v.reconcileNeeded = true
			v.reconcileTimerArmed = false
			v.reconcileSeq++
			return util.ReportWarn(fmt.Sprintf("Could not refresh agent transcript: %v", msg.err))
		}
		return nil
	}

	if msg.reconcile {
		follow := v.chat.Follow()
		selectedID := v.chat.SelectedID()
		offsetIdx, offsetLine := v.chat.ScrollPosition()
		v.installSnapshot(msg)
		if follow {
			v.chat.ScrollToBottom()
		} else {
			v.chat.RestoreScrollPosition(offsetIdx, offsetLine)
		}
		v.chat.SelectID(selectedID)
	} else {
		v.installSnapshot(msg)
		v.chat.ScrollToBottomAndSelectLast()
	}
	animationsAllowed := msg.busy
	if v.descriptor.Status != "" && v.descriptor.Status != agentrun.StatusRunning {
		animationsAllowed = false
	}
	v.chat.SetAnimationsAllowed(animationsAllowed)
	v.loadErr = nil
	reconcileNeeded := v.reconcileNeeded || len(v.buffered) > 0
	v.drainBuffered()
	if reconcileNeeded {
		return v.armReconcile()
	}
	return v.chat.EnsureAnimating()
}

func (v *agentViewer) installSnapshot(msg agentViewerLoadMsg) {
	// Projection is deliberately restricted to the UI goroutine. Using the
	// shared styles pointer keeps every installed item live across later
	// in-place theme apply and preview operations.
	projection := projectTranscriptMessages(v.com, msg.messages)
	attachNestedTranscripts(v.com, projection.items, msg.nestedMessages)

	v.session = msg.session
	v.messagesByID = make(map[string]message.Message, len(msg.messages))
	for _, persisted := range msg.messages {
		v.messagesByID[persisted.ID] = persisted
	}
	v.itemIDsByMessage = projection.itemIDsByMessage
	v.lastUserMessageTime = projection.lastUserMessageTime
	v.chat.SetMessages(projection.items...)
}

func (v *agentViewer) HandleEvent(event pubsub.Event[message.Message]) tea.Cmd {
	if event.Payload.SessionID != v.descriptor.SessionID {
		return nil
	}
	if v.loading || v.reconcileInFlight {
		v.buffered = append(v.buffered, event)
		if v.reconcileInFlight {
			return v.armReconcile()
		}
		return nil
	}
	cmd := v.applyEvent(event)
	if v.reconcileNeeded {
		return tea.Batch(cmd, v.armReconcile())
	}
	return cmd
}

func (v *agentViewer) drainBuffered() {
	events := v.buffered
	v.buffered = nil
	for _, event := range events {
		v.applyEvent(event)
	}
}

func (v *agentViewer) applyEvent(event pubsub.Event[message.Message]) tea.Cmd {
	msg := event.Payload
	switch event.Type {
	case pubsub.CreatedEvent:
		if _, exists := v.messagesByID[msg.ID]; exists {
			return nil
		}
		return v.appendMessage(msg)
	case pubsub.UpdatedEvent:
		return v.updateMessage(msg)
	case pubsub.DeletedEvent:
		if persisted, exists := v.messagesByID[msg.ID]; exists {
			msg = persisted
		}
		if msg.Role == message.Tool {
			v.detachToolResults(msg)
		}
		v.chat.RemoveMessages(v.itemIDsByMessage[msg.ID]...)
		delete(v.itemIDsByMessage, msg.ID)
		delete(v.messagesByID, msg.ID)
	}
	return nil
}

func (v *agentViewer) appendMessage(msg message.Message) tea.Cmd {
	v.chat.SetAnimationsAllowed(true)
	if msg.Role == message.Tool {
		v.messagesByID[msg.ID] = msg
		v.itemIDsByMessage[msg.ID] = nil
		v.attachToolResults(msg)
		v.scrollToNewContent()
		return v.chat.EnsureAnimating()
	}

	projection := projectTranscriptMessage(v.com, &msg, v.toolResultMap(&msg), v.lastUserMessageTime)
	v.chat.AppendMessages(projection.items...)
	v.itemIDsByMessage[msg.ID] = projection.itemIDsByMessage[msg.ID]
	v.lastUserMessageTime = projection.lastUserMessageTime
	v.messagesByID[msg.ID] = msg
	v.scrollToNewContent()
	return v.chat.EnsureAnimating()
}

func (v *agentViewer) updateMessage(msg message.Message) tea.Cmd {
	previous, exists := v.messagesByID[msg.ID]
	if !exists {
		return v.appendMessage(msg)
	}
	v.chat.SetAnimationsAllowed(true)

	switch msg.Role {
	case message.Tool:
		v.detachToolResults(previous)
		v.messagesByID[msg.ID] = msg
		v.itemIDsByMessage[msg.ID] = nil
		v.attachToolResults(msg)
	case message.Assistant:
		v.updateAssistantMessage(msg)
	default:
		v.removeOwnedItems(msg.ID)
		return v.appendMessage(msg)
	}
	v.scrollToNewContent()
	return v.chat.EnsureAnimating()
}

func (v *agentViewer) updateAssistantMessage(msg message.Message) {
	existingItem := v.chat.MessageItem(msg.ID)
	shouldRender := chat.ShouldRenderAssistantMessage(&msg)
	if existingItem != nil {
		if assistantItem, ok := existingItem.(*chat.AssistantMessageItem); ok {
			assistantItem.SetMessage(&msg)
		}
		if compactionItem, ok := existingItem.(*chat.CompactionItem); ok {
			compactionItem.SetMessage(&msg)
		}
		if !shouldRender {
			v.chat.RemoveMessage(msg.ID)
			v.removeOwnedID(msg.ID, msg.ID)
		}
	} else if shouldRender {
		v.chat.AppendMessages(chat.NewAssistantMessageItem(v.com.Styles, &msg))
		v.addOwnedID(msg.ID, msg.ID)
	}

	results := v.toolResultMap(&msg)
	for _, tc := range msg.ToolCalls() {
		var result *message.ToolResult
		if persisted, ok := results[tc.ID]; ok {
			result = &persisted
		}
		existingToolItem := v.chat.MessageItem(tc.ID)
		if toolItem, ok := existingToolItem.(chat.ToolMessageItem); ok {
			existingToolCall := toolItem.ToolCall()
			if (tc.Finished && !existingToolCall.Finished) || tc.Input != existingToolCall.Input {
				toolItem.SetToolCall(tc)
			}
			toolItem.SetResult(result)
		}
		if existingToolItem == nil {
			v.chat.AppendMessages(chat.NewToolMessageItem(v.com.Styles, msg.ID, tc, result, msg.FinishReason() == message.FinishReasonCanceled, v.com.Workspace.WorkingDir()))
		}
		v.addOwnedID(msg.ID, tc.ID)
	}

	infoID := chat.AssistantInfoID(msg.ID)
	infoItem := v.chat.MessageItem(infoID)
	if chat.ShouldShowAssistantInfo(&msg) {
		if infoItem == nil {
			v.chat.AppendMessages(chat.NewAssistantInfoItem(v.com.Styles, &msg, v.com.Config(), time.Unix(v.lastUserMessageTime, 0)))
			v.addOwnedID(msg.ID, infoID)
		}
	} else if infoItem != nil {
		v.chat.RemoveMessage(infoID)
		v.removeOwnedID(msg.ID, infoID)
	}
	v.messagesByID[msg.ID] = msg
}

func (v *agentViewer) toolResultMap(replacement *message.Message) map[string]message.ToolResult {
	messages := make([]*message.Message, 0, len(v.messagesByID)+1)
	for id, persisted := range v.messagesByID {
		if replacement != nil && id == replacement.ID {
			continue
		}
		messages = append(messages, &persisted)
	}
	if replacement != nil {
		messages = append(messages, replacement)
	}
	return chat.BuildToolResultMap(messages)
}

func (v *agentViewer) attachToolResults(msg message.Message) {
	for _, result := range msg.ToolResults() {
		if toolItem, ok := v.chat.MessageItem(result.ToolCallID).(chat.ToolMessageItem); ok {
			toolItem.SetResult(&result)
		}
	}
}

func (v *agentViewer) detachToolResults(msg message.Message) {
	for _, result := range msg.ToolResults() {
		if toolItem, ok := v.chat.MessageItem(result.ToolCallID).(chat.ToolMessageItem); ok {
			toolItem.SetResult(nil)
		}
	}
}

func (v *agentViewer) removeOwnedItems(messageID string) {
	v.chat.RemoveMessages(v.itemIDsByMessage[messageID]...)
	delete(v.itemIDsByMessage, messageID)
}

func (v *agentViewer) addOwnedID(messageID, itemID string) {
	if !slices.Contains(v.itemIDsByMessage[messageID], itemID) {
		v.itemIDsByMessage[messageID] = append(v.itemIDsByMessage[messageID], itemID)
	}
}

func (v *agentViewer) removeOwnedID(messageID, itemID string) {
	v.itemIDsByMessage[messageID] = slices.DeleteFunc(v.itemIDsByMessage[messageID], func(id string) bool {
		return id == itemID
	})
}

func (v *agentViewer) scrollToNewContent() {
	if v.chat.Follow() {
		v.chat.ScrollToBottom()
	}
}

func (v *agentViewer) armReconcile() tea.Cmd {
	v.reconcileNeeded = true
	v.reconcileDeadline = v.now().Add(agentViewerReconcileDelay)
	if v.reconcileTimerArmed {
		return nil
	}
	v.reconcileTimerArmed = true
	v.reconcileSeq++
	return v.reconcileTick(agentViewerReconcileDelay)
}

func (v *agentViewer) reconcileTick(delay time.Duration) tea.Cmd {
	generation, sequence := v.generation, v.reconcileSeq
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return agentViewerReconcileMsg{generation: generation, sequence: sequence}
	})
}

func (v *agentViewer) HandleReconcile(msg agentViewerReconcileMsg) tea.Cmd {
	if msg.generation != v.generation || msg.sequence != v.reconcileSeq {
		return nil
	}
	if !v.reconcileTimerArmed {
		return nil
	}
	if !v.reconcileNeeded {
		v.reconcileTimerArmed = false
		return nil
	}
	if deadline := v.reconcileDeadline.Sub(v.now()); deadline > 0 {
		return v.reconcileTick(deadline)
	}
	v.reconcileTimerArmed = false
	if v.loading || v.reconcileInFlight {
		return nil
	}
	v.reconcileNeeded = false
	v.reconcileInFlight = true
	return v.loadSnapshot(true)
}

func (v *agentViewer) Close() {
	v.generation++
	v.reconcileSeq++
	v.buffered = nil
	v.loading = false
	v.reconcileNeeded = false
	v.reconcileInFlight = false
	v.reconcileTimerArmed = false
	v.chat.SetExternalAnimation(false)
	v.chat.SetAnimationsAllowed(false)
}

func (v *agentViewer) UpdateDescriptor(descriptor dialog.AgentDescriptor) {
	v.descriptor = descriptor
	running := descriptor.Status == agentrun.StatusRunning
	v.chat.SetExternalAnimation(running)
	if !running {
		v.chat.SetAnimationsAllowed(false)
	}
}

// InvalidateRenderCaches drops cached rendered output on every viewer item.
func (v *agentViewer) InvalidateRenderCaches() {
	v.chat.InvalidateRenderCaches()
}

// InvalidateVisibleRenderCaches drops cached output only on visible viewer
// items, keeping theme previews cheap for large transcripts.
func (v *agentViewer) InvalidateVisibleRenderCaches() {
	v.chat.InvalidateVisibleRenderCaches()
}

func (v *agentViewer) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.chat.SetSize(width, max(0, height-2))
}

func (v *agentViewer) Draw(scr uv.Screen, area uv.Rectangle) {
	if area.Dx() <= 0 || area.Dy() <= 0 {
		return
	}
	headerArea := area
	headerArea.Max.Y = min(area.Max.Y, area.Min.Y+1)
	uv.NewStyledString(v.header()).Draw(scr, headerArea)
	if area.Dy() <= 2 {
		return
	}
	chatArea := area
	chatArea.Min.Y += 2
	v.chat.Draw(scr, chatArea)
}

func (v *agentViewer) header() string {
	styles := v.com.Styles
	name := styles.Tool.AgentName.Render(v.descriptor.Name)
	if v.loading && v.chat.Len() == 0 {
		icon := styles.Resource.WorkingIcon.Render(subAgentSpinner(time.Now()))
		return fmt.Sprintf("%s Loading %s…  esc back", icon, name)
	}
	status := v.descriptor.Status
	if status == agentrun.StatusRunning {
		icon := styles.Resource.WorkingIcon.Render(subAgentSpinner(time.Now()))
		return fmt.Sprintf("%s %s · %s  esc back", icon, name, status)
	}
	if status == "" {
		status = agentrun.StatusDone
	}
	return fmt.Sprintf("%s %s · %s  esc back", styles.Resource.OfflineIcon.String(), name, status)
}
