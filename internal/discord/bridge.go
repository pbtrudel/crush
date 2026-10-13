package discord

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/workspace"
)

const (
	// defaultTurnTick paces live status edits (animation + elapsed
	// timer). Discord allows roughly five edits per five seconds per
	// channel; 1.5s stays well inside that.
	defaultTurnTick = 1500 * time.Millisecond

	// defaultEditThrottle is the minimum gap between event-driven edits.
	defaultEditThrottle = 1400 * time.Millisecond

	// typingInterval is how often the typing indicator refreshes while
	// the agent is busy.
	typingInterval = 9 * time.Second

	// maxAttachmentBytes caps mirrored Discord attachments.
	maxAttachmentBytes = 8 << 20

	// callCacheLimit bounds remembered tool calls.
	callCacheLimit = 256

	// postedResultLimit bounds the tool-result dedupe set.
	postedResultLimit = 256

	// completedLimit bounds remembered completed message ids.
	completedLimit = 128
)

// farewellText is posted when the bridge stops.
const farewellText = "👋 **Remote control ended** — the session continues in the terminal."

// discordAPI is the Discord surface the bridge drives. restClient
// implements it; tests inject a fake.
type discordAPI interface {
	createThread(ctx context.Context, channelID, name string) (createdThread, error)
	createMessage(ctx context.Context, channelID, content string) (string, error)
	editMessage(ctx context.Context, channelID, messageID, content string) error
	triggerTyping(ctx context.Context, channelID string) error
	addReaction(ctx context.Context, channelID, messageID, emoji string) error
	editChannelName(ctx context.Context, channelID, name string) error
	gatewayURL(ctx context.Context) (string, error)
}

// Bridge mirrors a live Crush session into a Discord thread and injects
// thread messages back into the session. It owns one control thread and
// one gateway connection, and is safe to Stop more than once.
type Bridge struct {
	env       commandEnv
	app       *app.App
	sessionID string
	cfg       Config

	api      discordAPI
	gateway  *gateway
	http     *http.Client
	gwLoop   func(ctx context.Context)
	events   <-chan pubsub.Event[tea.Msg]
	input    chan inputEvent
	now      func() time.Time
	editRate time.Duration
	tick     time.Duration
	typing   time.Duration
	catalog  func(cfg *config.Config) ([]catwalk.Provider, error)

	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once

	threadID  string
	threadURL string

	modeMu sync.Mutex
	mode   string // "code" or "plan"

	pendingMu       sync.Mutex
	permissions     map[string]permission.PermissionRequest
	permissionOrder []string
	permMessage     map[string]string
	questions       map[string]question.Request
	questionOrder   []string
	questionMessage map[string]string

	subMu       sync.Mutex
	submissions []promptSubmission

	callMu sync.Mutex
	calls  map[string]message.ToolCall

	postedMu   sync.Mutex
	postedTool map[string]bool

	completedMu sync.Mutex
	completed   map[string]bool

	renameMu  sync.Mutex
	renamedTo string

	turnMu sync.Mutex
	turn   *liveTurn
}

// promptSubmission records a prompt injected from Discord so the
// resulting user-message echo can be skipped (the author's own message
// is already in the thread).
type promptSubmission struct {
	hash string
	when time.Time
}

// bridgeDeps are the injected collaborators of a bridge; production
// Start fills them with real implementations, tests with fakes.
type bridgeDeps struct {
	api          discordAPI
	events       <-chan pubsub.Event[tea.Msg]
	gatewayStart func(ctx context.Context)
	now          func() time.Time
	tick         time.Duration
	editThrottle time.Duration
	http         *http.Client
	catalog      func(cfg *config.Config) ([]catwalk.Provider, error)
	typing       time.Duration
}

// Start creates the Discord control thread for a live session and
// launches the bridge. mode is the TUI's current input mode ("code" or
// "plan"). Only local-mode workspaces are supported.
func Start(ctx context.Context, ws workspace.Workspace, sessionID, mode string) (*Bridge, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	appWS, ok := ws.(*workspace.AppWorkspace)
	if !ok {
		return nil, errors.New("remote control requires local mode (unset CRUSH_CLIENT_SERVER)")
	}
	return start(ctx, ws, appWS.App(), sessionID, cfg, mode)
}

// start wires and launches a bridge, creating its control thread.
func start(ctx context.Context, env commandEnv, application *app.App, sessionID string, cfg Config, mode string) (*Bridge, error) {
	b := newBridge(env, application, sessionID, cfg, mode, bridgeDeps{
		api:  newRESTClient(cfg.Token),
		http: &http.Client{Timeout: 30 * time.Second},
	})
	if _, err := env.GetSession(ctx, sessionID); err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	thread, err := b.api.createThread(ctx, cfg.ChannelIDs[0], b.threadName(ctx))
	if err != nil {
		return nil, fmt.Errorf("create Discord thread: %w", err)
	}
	b.threadID = thread.ID
	b.threadURL = threadURL(thread)
	if err := b.launch(ctx); err != nil {
		return nil, err
	}
	b.post(b.bannerText(ctx))
	return b, nil
}

// newBridge builds an unlaunched bridge from injected dependencies.
func newBridge(env commandEnv, application *app.App, sessionID string, cfg Config, mode string, deps bridgeDeps) *Bridge {
	b := &Bridge{
		env:             env,
		app:             application,
		sessionID:       sessionID,
		cfg:             cfg,
		api:             deps.api,
		http:            deps.http,
		events:          deps.events,
		gwLoop:          deps.gatewayStart,
		now:             deps.now,
		tick:            deps.tick,
		editRate:        deps.editThrottle,
		typing:          deps.typing,
		catalog:         deps.catalog,
		mode:            mode,
		permissions:     map[string]permission.PermissionRequest{},
		permMessage:     map[string]string{},
		questions:       map[string]question.Request{},
		questionMessage: map[string]string{},
		calls:           map[string]message.ToolCall{},
		postedTool:      map[string]bool{},
		completed:       map[string]bool{},
	}
	if b.api == nil {
		b.api = newRESTClient(cfg.Token)
	}
	if b.catalog == nil {
		b.catalog = func(cfg *config.Config) ([]catwalk.Provider, error) {
			return config.Providers(cfg)
		}
	}
	if b.http == nil {
		b.http = &http.Client{Timeout: 30 * time.Second}
	}
	if b.now == nil {
		b.now = time.Now
	}
	if b.tick <= 0 {
		b.tick = defaultTurnTick
	}
	if b.editRate <= 0 {
		b.editRate = defaultEditThrottle
	}
	if b.typing <= 0 {
		b.typing = typingInterval
	}
	b.gateway = newGateway(cfg.Token, defaultGatewayIntents)
	return b
}

// launch starts the bridge's goroutines. The caller must have set
// threadID.
func (b *Bridge) launch(ctx context.Context) error {
	b.ctx, b.cancel = context.WithCancel(ctx)
	if b.events == nil && b.app != nil {
		b.events = b.app.Events(b.ctx)
	}
	if b.events == nil {
		b.events = make(chan pubsub.Event[tea.Msg])
	}
	b.input = make(chan inputEvent, 32)
	go b.eventLoop()
	go b.inputLoop()
	go b.typingLoop()
	if b.gwLoop != nil {
		go b.gwLoop(b.ctx)
	} else {
		go b.runGateway(b.ctx)
	}
	return nil
}

// Stop shuts the bridge down, posting a farewell first so the thread is
// not left dangling. Safe to call multiple times and from any goroutine;
// the farewell is bounded so quitting never hangs.
func (b *Bridge) Stop() {
	b.stopOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if b.threadID != "" {
			_, _ = b.api.createMessage(ctx, b.threadID, farewellText)
		}
		if b.cancel != nil {
			b.cancel()
		}
		if b.app != nil {
			// Let the TUI clear its reference when the stop was initiated
			// from Discord (e.g. "/rc off"); self-initiated stops have
			// already cleared it and ignore this.
			b.app.SendEvent(StoppedMsg{Bridge: b})
		}
	})
}

// ThreadURL returns the Discord link of the control thread.
func (b *Bridge) ThreadURL() string { return b.threadURL }

// Mode reports the bridge's view of the input mode.
func (b *Bridge) modeName() string {
	b.modeMu.Lock()
	defer b.modeMu.Unlock()
	return b.mode
}

// SetMode records a local mode change so /status stays accurate.
func (b *Bridge) SetMode(plan bool) {
	b.modeMu.Lock()
	defer b.modeMu.Unlock()
	if plan {
		b.mode = "plan"
	} else {
		b.mode = "code"
	}
}

// setMode switches the session's agent from Discord and notifies the
// TUI so its badge and caches track the change.
func (b *Bridge) setMode(ctx context.Context, plan bool) error {
	agentID := config.AgentCoder
	if plan {
		agentID = config.AgentPlan
	}
	if err := b.env.AgentSetMain(agentID); err != nil {
		return err
	}
	if err := b.env.UpdateAgentModel(ctx); err != nil {
		return err
	}
	b.SetMode(plan)
	if b.app != nil {
		b.app.SendEvent(ModeChangedMsg{Plan: plan})
	}
	return nil
}

// setYolo toggles permission auto-approval and notifies the TUI.
func (b *Bridge) setYolo(on bool) {
	b.env.PermissionSetSkipRequests(on)
	if b.app != nil {
		b.app.SendEvent(YoloChangedMsg{On: on})
	}
}

// changeModel switches the preferred large model and rebuilds the agent
// models, then notifies the TUI.
func (b *Bridge) changeModel(ctx context.Context, sel config.SelectedModel) error {
	if err := b.env.UpdatePreferredModel(config.ScopeGlobal, config.SelectedModelTypeLarge, sel); err != nil {
		return err
	}
	if err := b.env.UpdateAgentModel(ctx); err != nil {
		return err
	}
	if b.app != nil {
		b.app.SendEvent(ModelChangedMsg{Provider: sel.Provider, Model: sel.Model})
	}
	return nil
}

// post sends one or more chunked messages to the thread. Failures are
// logged only: the session must never break because Discord is down.
func (b *Bridge) post(content string) {
	for _, chunk := range splitMessage(content) {
		if _, err := b.api.createMessage(b.ctx, b.threadID, chunk); err != nil {
			slog.Debug("Discord bridge: failed to post message", "error", err.Error())
			return
		}
	}
}

// note surfaces a bridge notice in the TUI (when hosted in-process) and
// the log.
func (b *Bridge) note(text string, isErr bool) {
	if isErr {
		slog.Warn("Discord bridge: " + text)
	} else {
		slog.Info("Discord bridge: " + text)
	}
	if b.app != nil {
		b.app.SendEvent(StatusNoteMsg{Text: text, IsErr: isErr})
	}
}

// threadName names the control thread after the session or the working
// directory.
func (b *Bridge) threadName(ctx context.Context) string {
	if sess, err := b.env.GetSession(ctx, b.sessionID); err == nil {
		if title := strings.TrimSpace(sess.Title); title != "" {
			return truncateRunes(title, 100)
		}
	}
	dir := filepath.Base(b.env.WorkingDir())
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		dir = "session"
	}
	return truncateRunes("crush — "+dir, 100)
}

// bannerText announces the bridge in the fresh thread.
func (b *Bridge) bannerText(ctx context.Context) string {
	title := b.threadName(ctx)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s **Crush remote control enabled**\n", iconBot)
	fmt.Fprintf(&sb, "-# %s · mode `%s` · yolo `%s`\n", title, b.modeName(), onOff(b.env.PermissionSkipRequests()))
	sb.WriteString("-# type a prompt, or `/help` for commands · `/rc off` to end")
	return sb.String()
}

// threadURL builds the clickable link for a created thread.
func threadURL(t createdThread) string {
	if t.GuildID == "" {
		return "https://discord.com/channels/@me/" + t.ID
	}
	return "https://discord.com/channels/" + t.GuildID + "/" + t.ID
}

// ── Event mirroring ──────────────────────────────────────────────────────

// eventLoop mirrors session events into the thread until the bridge
// context ends.
func (b *Bridge) eventLoop() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case ev, ok := <-b.events:
			if !ok {
				return
			}
			b.handleEvent(ev)
		}
	}
}

// handleEvent routes one broker event.
func (b *Bridge) handleEvent(ev pubsub.Event[tea.Msg]) {
	switch p := ev.Payload.(type) {
	case pubsub.Event[message.Message]:
		b.handleMessageEvent(p)
	case pubsub.Event[notify.RunComplete]:
		b.handleRunComplete(p.Payload)
	case pubsub.Event[notify.Notification]:
		b.handleAgentNotification(p.Payload)
	case pubsub.Event[permission.PermissionRequest]:
		b.handlePermissionRequest(p.Payload)
	case pubsub.Event[permission.PermissionNotification]:
		b.handlePermissionNotification(p.Payload)
	case pubsub.Event[question.Request]:
		b.handleQuestionRequest(p.Payload)
	case pubsub.Event[question.Notification]:
		b.handleQuestionNotification(p.Payload)
	case pubsub.Event[session.Session]:
		b.handleSessionEvent(p)
	}
}

func (b *Bridge) handleMessageEvent(ev pubsub.Event[message.Message]) {
	msg := ev.Payload
	if msg.SessionID != b.sessionID || msg.IsSummaryMessage {
		return
	}
	switch msg.Role {
	case message.User:
		if ev.Type == pubsub.CreatedEvent {
			b.handleUserMessage(msg)
		}
	case message.Assistant:
		if ev.Type == pubsub.CreatedEvent || ev.Type == pubsub.UpdatedEvent {
			b.handleAssistantMessage(msg)
		}
	case message.Tool:
		if ev.Type == pubsub.CreatedEvent || ev.Type == pubsub.UpdatedEvent {
			b.handleToolResultMessage(msg)
		}
	}
}

// handleUserMessage echoes prompts typed in the terminal so remote
// readers see both sides of the conversation. Prompts injected from
// Discord are skipped: the author's own message is already visible.
func (b *Bridge) handleUserMessage(msg message.Message) {
	text := strings.TrimSpace(messageText(msg))
	if text == "" || strings.HasPrefix(text, "<channel") {
		return
	}
	if b.consumeSubmission(text) {
		return
	}
	b.post(iconLocal + " **(terminal)**\n" + text)
}

// handleAssistantMessage starts or advances the live turn message.
func (b *Bridge) handleAssistantMessage(msg message.Message) {
	b.completedMu.Lock()
	done := b.completed[msg.ID]
	b.completedMu.Unlock()
	if done {
		return
	}
	for _, tc := range toolCalls(msg) {
		b.rememberCall(tc)
	}

	t := b.currentTurn()
	if t == nil {
		b.startTurn(msg)
		return
	}
	t.apply(msg, b.hasPendingPermission())
}

// handleToolResultMessage mirrors one tool result as a compact message.
func (b *Bridge) handleToolResultMessage(msg message.Message) {
	tr, ok := firstToolResult(msg)
	if !ok {
		return
	}
	b.postedMu.Lock()
	if b.postedTool[msg.ID] || strings.TrimSpace(tr.Content) == "" {
		b.postedMu.Unlock()
		return
	}
	if len(b.postedTool) >= postedResultLimit {
		clear(b.postedTool)
	}
	b.postedTool[msg.ID] = true
	b.postedMu.Unlock()

	b.post(renderToolResultMessage(b.lookupCall(tr.ToolCallID), tr))
}

// handleRunComplete finalizes the live turn with the authoritative run
// outcome.
func (b *Bridge) handleRunComplete(rc notify.RunComplete) {
	if rc.SessionID != b.sessionID {
		return
	}
	if rc.MessageID != "" {
		b.completedMu.Lock()
		if len(b.completed) >= completedLimit {
			clear(b.completed)
		}
		b.completed[rc.MessageID] = true
		b.completedMu.Unlock()
	}

	t := b.currentTurn()
	if t == nil {
		return
	}
	state := turnDone
	switch {
	case rc.Cancelled:
		state = turnCancelled
	case rc.Error != "":
		state = turnError
	}
	t.finish(state, rc.Error, rc.Text)

	b.turnMu.Lock()
	if b.turn == t {
		b.turn = nil
	}
	b.turnMu.Unlock()
}

// handleAgentNotification mirrors re-authentication requests; run
// terminals are covered by RunComplete.
func (b *Bridge) handleAgentNotification(n notify.Notification) {
	if n.SessionID != b.sessionID {
		return
	}
	if n.Type == notify.TypeReAuthenticate {
		msg := n.Message
		if msg == "" {
			msg = "provider authentication expired"
		}
		b.post("🔑 **Re-authentication needed** — " + compactLine(msg, 300))
	}
}

// handleSessionEvent tracks title generation by renaming the thread.
func (b *Bridge) handleSessionEvent(ev pubsub.Event[session.Session]) {
	if ev.Type != pubsub.UpdatedEvent || ev.Payload.ID != b.sessionID {
		return
	}
	title := strings.TrimSpace(ev.Payload.Title)
	if title == "" {
		return
	}
	b.renameMu.Lock()
	changed := b.renamedTo != title
	b.renamedTo = title
	b.renameMu.Unlock()
	if changed {
		if err := b.api.editChannelName(b.ctx, b.threadID, truncateRunes(title, 100)); err != nil {
			slog.Debug("Discord bridge: failed to rename thread", "error", err.Error())
		}
	}
}

// ── Permissions and questions ────────────────────────────────────────────

// handlePermissionRequest mirrors a tool permission request as a
// reaction-approvable card.
func (b *Bridge) handlePermissionRequest(req permission.PermissionRequest) {
	if req.SessionID != b.sessionID {
		return
	}
	b.pendingMu.Lock()
	b.permissions[req.ID] = req
	b.permissionOrder = append(b.permissionOrder, req.ID)
	b.pendingMu.Unlock()

	id, err := b.api.createMessage(b.ctx, b.threadID, renderPermissionCard(req))
	if err != nil {
		slog.Debug("Discord bridge: failed to post permission card", "error", err.Error())
		return
	}
	b.pendingMu.Lock()
	b.permMessage[req.ID] = id
	b.pendingMu.Unlock()
	for _, emoji := range []string{reactionAllow, reactionAllowSession, reactionDeny} {
		if err := b.api.addReaction(b.ctx, b.threadID, id, emoji); err != nil {
			slog.Debug("Discord bridge: failed to seed reaction", "error", err.Error())
		}
	}
	if t := b.currentTurn(); t != nil {
		t.markWaiting(true)
	}
}

// handlePermissionNotification updates the card once a permission is
// resolved, from either side of the bridge.
func (b *Bridge) handlePermissionNotification(n permission.PermissionNotification) {
	b.pendingMu.Lock()
	var reqID string
	for id, req := range b.permissions {
		if req.ToolCallID == n.ToolCallID {
			reqID = id
			break
		}
	}
	if reqID == "" {
		b.pendingMu.Unlock()
		return
	}
	req := b.permissions[reqID]
	delete(b.permissions, reqID)
	msgID := b.permMessage[reqID]
	delete(b.permMessage, reqID)
	b.pendingMu.Unlock()

	status := "❌ denied"
	if n.Granted {
		status = "✅ approved"
	}
	if msgID != "" {
		card := renderPermissionCard(req) + "\n-# " + status
		if err := b.api.editMessage(b.ctx, b.threadID, msgID, card); err != nil {
			slog.Debug("Discord bridge: failed to update permission card", "error", err.Error())
		}
	}
	if t := b.currentTurn(); t != nil {
		t.markWaiting(false)
	}
}

// handleQuestionRequest mirrors an agent question as a thread card.
func (b *Bridge) handleQuestionRequest(req question.Request) {
	if req.SessionID != b.sessionID {
		return
	}
	b.pendingMu.Lock()
	b.questions[req.ID] = req
	b.questionOrder = append(b.questionOrder, req.ID)
	b.pendingMu.Unlock()

	id, err := b.api.createMessage(b.ctx, b.threadID, renderQuestionCard(req))
	if err != nil {
		slog.Debug("Discord bridge: failed to post question card", "error", err.Error())
		return
	}
	b.pendingMu.Lock()
	b.questionMessage[req.ID] = id
	b.pendingMu.Unlock()
}

// handleQuestionNotification marks the question card resolved.
func (b *Bridge) handleQuestionNotification(n question.Notification) {
	b.pendingMu.Lock()
	req, ok := b.questions[n.BatchID]
	if !ok {
		b.pendingMu.Unlock()
		return
	}
	delete(b.questions, n.BatchID)
	msgID := b.questionMessage[n.BatchID]
	delete(b.questionMessage, n.BatchID)
	b.pendingMu.Unlock()

	if msgID != "" {
		card := renderQuestionCard(req) + "\n-# ✅ answered"
		if err := b.api.editMessage(b.ctx, b.threadID, msgID, card); err != nil {
			slog.Debug("Discord bridge: failed to update question card", "error", err.Error())
		}
	}
}

// hasPendingPermission reports whether any permission card is open.
func (b *Bridge) hasPendingPermission() bool {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	return len(b.permissions) > 0
}

// pendingPermissions returns unresolved requests in arrival order.
func (b *Bridge) pendingPermissions() []permission.PermissionRequest {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	out := make([]permission.PermissionRequest, 0, len(b.permissions))
	for _, id := range b.permissionOrder {
		if req, ok := b.permissions[id]; ok {
			out = append(out, req)
		}
	}
	return out
}

// pendingQuestion returns the newest unresolved question request.
func (b *Bridge) pendingQuestion() (question.Request, bool) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	for i := len(b.questionOrder) - 1; i >= 0; i-- {
		if req, ok := b.questions[b.questionOrder[i]]; ok {
			return req, true
		}
	}
	return question.Request{}, false
}

// renderPermissionCard renders a permission request for the thread.
func renderPermissionCard(req permission.PermissionRequest) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s **Permission needed** · `%s`\n", iconPermission, req.ToolName)
	desc := strings.TrimSpace(req.Description)
	if desc == "" {
		desc = strings.TrimSpace(req.Action + " " + req.Path)
	}
	if desc != "" {
		sb.WriteString(compactLine(desc, 400) + "\n")
	}
	sb.WriteString("-# react ✅ allow · 🔁 allow for session · ❌ deny — or `/allow` · `/deny`")
	return sb.String()
}

// renderQuestionCard renders an agent question for the thread.
func renderQuestionCard(req question.Request) string {
	var sb strings.Builder
	for i, q := range req.Questions {
		if len(req.Questions) > 1 {
			fmt.Fprintf(&sb, "%s **Question %d/%d", iconQuestion, i+1, len(req.Questions))
		} else {
			sb.WriteString(iconQuestion + " **Question")
		}
		if q.Label != "" {
			sb.WriteString(" · " + compactLine(q.Label, 80))
		}
		sb.WriteString("**\n")
		sb.WriteString(q.Text + "\n")
		if q.Description != "" {
			sb.WriteString("-# " + compactLine(q.Description, 300) + "\n")
		}
		for j, choice := range q.Choices {
			fmt.Fprintf(&sb, "**%d.** %s\n", j+1, compactLine(choice.Label, 120))
		}
	}
	sb.WriteString("-# answer with `/answer <text>` or `/answer #1` for a choice")
	return sb.String()
}

// ── Discord input ────────────────────────────────────────────────────────

// inputEvent is one unit of Discord input, consumed in arrival order.
type inputEvent struct {
	message  *gatewayMessage
	reaction *gatewayReaction
}

func (b *Bridge) enqueueInput(ev inputEvent) {
	select {
	case b.input <- ev:
	case <-b.ctx.Done():
	}
}

// inputLoop consumes gateway input serially so prompts keep their
// author's order.
func (b *Bridge) inputLoop() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case ev := <-b.input:
			switch {
			case ev.message != nil:
				b.handleThreadMessage(*ev.message)
			case ev.reaction != nil:
				b.handleThreadReaction(*ev.reaction)
			}
		}
	}
}

// handleThreadMessage routes one thread message: slash commands run
// here, anything else becomes a prompt.
func (b *Bridge) handleThreadMessage(msg gatewayMessage) {
	if msg.ChannelID != b.threadID {
		return
	}
	if !b.cfg.allowedUser(msg.Author.ID) {
		b.post("⛔ `" + msg.Author.Username + "` is not allowed to control this session.")
		return
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" && len(msg.Attachments) == 0 {
		return
	}
	// "\/literal" escapes a prompt that starts with a slash.
	if strings.HasPrefix(text, `\/`) {
		b.submitPrompt(text[1:], msg.Attachments)
		return
	}
	if name, args, ok := parseCommand(text); ok {
		if reply := b.handleCommand(b.ctx, name, args); reply != "" {
			b.post(reply)
		}
		return
	}
	b.submitPrompt(text, msg.Attachments)
}

// handleThreadReaction resolves a permission card from its reactions.
func (b *Bridge) handleThreadReaction(r gatewayReaction) {
	if r.ChannelID != b.threadID || !b.cfg.allowedUser(r.UserID) {
		return
	}
	b.pendingMu.Lock()
	var reqID string
	for id, msgID := range b.permMessage {
		if msgID == r.MessageID {
			reqID = id
			break
		}
	}
	req, ok := b.permissions[reqID]
	b.pendingMu.Unlock()
	if !ok {
		return
	}
	switch r.Emoji.Name {
	case reactionAllow:
		b.env.PermissionGrant(req)
	case reactionAllowSession:
		b.env.PermissionGrantPersistent(req)
	case reactionDeny:
		b.env.PermissionDeny(req)
	}
}

// submitPrompt injects a prompt from the thread into the session,
// mirroring any attachments.
func (b *Bridge) submitPrompt(text string, attachments []gatewayAttachment) {
	b.launchRun(text, b.fetchAttachments(attachments)...)
}

// launchRun submits a prompt to the agent in the background. Run itself
// blocks for the whole turn, so it never runs on an input path. The run
// is started with a background context: stopping remote control must not
// cancel work the local session is still running, matching how the TUI
// submits prompts.
func (b *Bridge) launchRun(prompt string, attachments ...message.Attachment) {
	if strings.TrimSpace(prompt) == "" && len(attachments) == 0 {
		return
	}
	b.noteSubmission(prompt)
	if b.env.AgentIsSessionBusy(b.sessionID) {
		position := b.env.AgentQueuedPrompts(b.sessionID) + 1
		b.post(fmt.Sprintf("📥 Queued (position %d) — the current turn is still running.", position))
	}
	go func() {
		if err := b.env.AgentRun(context.Background(), b.sessionID, prompt, attachments...); err != nil && b.ctx.Err() == nil {
			b.post(iconError + " Failed to send prompt: " + err.Error())
		}
	}()
}

// fetchAttachments downloads mirrored attachments; oversized or failed
// downloads are skipped rather than failing the prompt.
func (b *Bridge) fetchAttachments(atts []gatewayAttachment) []message.Attachment {
	var out []message.Attachment
	for _, a := range atts {
		if a.Size > maxAttachmentBytes {
			b.post(fmt.Sprintf("-# ⚠️ skipped attachment `%s` (larger than %d MiB)", a.Filename, maxAttachmentBytes>>20))
			continue
		}
		ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
		if err != nil {
			cancel()
			continue
		}
		resp, err := b.http.Do(req)
		if err != nil {
			cancel()
			slog.Debug("Discord bridge: attachment download failed", "error", err.Error())
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
		_ = resp.Body.Close()
		cancel()
		if readErr != nil || len(data) > maxAttachmentBytes {
			continue
		}
		out = append(out, message.Attachment{
			FilePath: a.Filename,
			FileName: a.Filename,
			MimeType: a.ContentType,
			Content:  data,
		})
	}
	return out
}

// noteSubmission records a Discord-injected prompt for echo dedupe.
func (b *Bridge) noteSubmission(prompt string) {
	b.subMu.Lock()
	defer b.subMu.Unlock()
	b.submissions = append(b.submissions, promptSubmission{hash: hashPrompt(prompt), when: b.now()})
	if len(b.submissions) > 32 {
		b.submissions = b.submissions[len(b.submissions)-32:]
	}
}

// consumeSubmission reports whether a user-message echo belongs to a
// recently injected Discord prompt.
func (b *Bridge) consumeSubmission(prompt string) bool {
	hash := hashPrompt(prompt)
	b.subMu.Lock()
	defer b.subMu.Unlock()
	for i, s := range b.submissions {
		if s.hash == hash && b.now().Sub(s.when) < 2*time.Minute {
			b.submissions = append(b.submissions[:i], b.submissions[i+1:]...)
			return true
		}
	}
	return false
}

func hashPrompt(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

// ── Gateway and typing ───────────────────────────────────────────────────

// runGateway fetches the gateway URL once and keeps the connection
// alive. Errors are surfaced through notes; the bridge stays usable in
// the meantime (permission cards, etc.) until the gateway returns.
func (b *Bridge) runGateway(ctx context.Context) {
	url, err := b.api.gatewayURL(ctx)
	if err != nil {
		b.note("Discord gateway unavailable: "+err.Error(), true)
		return
	}
	b.gateway.url = url
	b.gateway.onMessage = func(m gatewayMessage) { b.enqueueInput(inputEvent{message: &m}) }
	b.gateway.onReaction = func(r gatewayReaction) { b.enqueueInput(inputEvent{reaction: &r}) }
	_ = b.gateway.run(ctx, func(err error) {
		b.note("Discord gateway error: "+err.Error()+" (check the bot token, MESSAGE CONTENT intent, and channel permissions)", true)
	})
}

// typingLoop shows the typing indicator while the agent is busy.
func (b *Bridge) typingLoop() {
	ticker := time.NewTicker(b.typing)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			if !b.env.AgentIsSessionBusy(b.sessionID) {
				continue
			}
			ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
			_ = b.api.triggerTyping(ctx, b.threadID)
			cancel()
		}
	}
}

// ── Live turn state ──────────────────────────────────────────────────────

// turnState is the live phase of an assistant turn.
type turnState int

const (
	turnThinking turnState = iota
	turnWriting
	turnTool
	turnWaitingPermission
	turnDone
	turnCancelled
	turnError
)

func (s turnState) terminal() bool {
	return s == turnDone || s == turnCancelled || s == turnError
}

// liveTurn is the Discord-side state of one assistant turn: a single
// live-edited message carrying the status header and a tail window of
// the timeline.
type liveTurn struct {
	mu sync.Mutex

	messageID string
	started   time.Time
	state     turnState
	toolName  string
	timeline  string
	model     string
	errText   string
	waiting   bool
	frame     int
	dirty     bool
	lastEdit  time.Time
	lastBody  string

	updates chan struct{}
	done    chan struct{}
	doneOne sync.Once
}

// startTurn begins a live turn for a freshly created assistant message.
func (b *Bridge) startTurn(msg message.Message) {
	t := &liveTurn{
		started: b.now(),
		state:   turnThinking,
		updates: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	t.mu.Lock()
	t.applyLocked(msg, false)
	t.mu.Unlock()
	b.turnMu.Lock()
	b.turn = t
	b.turnMu.Unlock()
	go b.turnLoop(t)
}

// currentTurn returns the in-flight turn, if any.
func (b *Bridge) currentTurn() *liveTurn {
	b.turnMu.Lock()
	defer b.turnMu.Unlock()
	return b.turn
}

// apply advances the turn from a message snapshot.
func (t *liveTurn) apply(msg message.Message, waiting bool) {
	t.mu.Lock()
	t.applyLocked(msg, waiting)
	t.mu.Unlock()
	t.notify()
}

// applyLocked must be called with t.mu held.
func (t *liveTurn) applyLocked(msg message.Message, waiting bool) {
	if msg.Model != "" {
		t.model = msg.Model
		if msg.Provider != "" {
			t.model = msg.Provider + "/" + msg.Model
		}
	}
	t.timeline = renderAssistantTimeline(msg)
	t.state, t.toolName = deriveTurnState(msg)
	t.waiting = waiting
	if waiting && !t.state.terminal() {
		t.state = turnWaitingPermission
	}
	t.dirty = true
}

// markWaiting flips the waiting-for-permission phase.
func (t *liveTurn) markWaiting(waiting bool) {
	t.mu.Lock()
	t.waiting = waiting
	if !t.state.terminal() {
		if waiting {
			t.state = turnWaitingPermission
		} else if t.state == turnWaitingPermission {
			t.state = turnThinking
			if strings.TrimSpace(t.timeline) != "" {
				t.state = turnWriting
			}
		}
	}
	t.dirty = true
	t.mu.Unlock()
	t.notify()
}

// finish marks the turn terminal and wakes its loop for the final
// flush. fallbackText fills a timeline that never streamed (the bridge
// missed the deltas).
func (t *liveTurn) finish(state turnState, errText, fallbackText string) {
	t.mu.Lock()
	t.state = state
	t.errText = errText
	if strings.TrimSpace(t.timeline) == "" && strings.TrimSpace(fallbackText) != "" {
		t.timeline = strings.TrimSpace(fallbackText)
	}
	t.dirty = true
	t.mu.Unlock()
	t.doneOne.Do(func() { close(t.done) })
}

// notify pokes the turn loop without blocking the event goroutine.
func (t *liveTurn) notify() {
	select {
	case t.updates <- struct{}{}:
	default:
	}
}

// turnLoop owns the live message's edit cadence: event pokes are
// throttled, the ticker animates and refreshes the elapsed timer, and
// finish triggers the terminal flush.
func (b *Bridge) turnLoop(t *liveTurn) {
	ticker := time.NewTicker(b.tick)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-t.done:
			b.finishTurn(t)
			return
		case <-t.updates:
			t.mu.Lock()
			due := b.now().Sub(t.lastEdit) >= b.editRate
			t.mu.Unlock()
			if due {
				b.flushTurn(t)
			}
		case <-ticker.C:
			b.flushTurn(t)
		}
	}
}

// flushTurn composes and edits the live message if it changed.
func (b *Bridge) flushTurn(t *liveTurn) {
	t.mu.Lock()
	t.frame++
	body := b.composeTurnLocked(t)
	if body == t.lastBody {
		t.mu.Unlock()
		return
	}
	messageID := t.messageID
	t.mu.Unlock()

	if messageID == "" {
		id, err := b.api.createMessage(b.ctx, b.threadID, body)
		if err != nil {
			slog.Debug("Discord bridge: failed to post turn message", "error", err.Error())
			return
		}
		t.mu.Lock()
		t.messageID = id
		t.lastBody = body
		t.dirty = false
		t.lastEdit = b.now()
		t.mu.Unlock()
		return
	}
	if err := b.api.editMessage(b.ctx, b.threadID, messageID, body); err != nil {
		slog.Debug("Discord bridge: failed to edit turn message", "error", err.Error())
		return
	}
	t.mu.Lock()
	t.lastBody = body
	t.dirty = false
	t.lastEdit = b.now()
	t.mu.Unlock()
}

// finishTurn freezes the full timeline: the live message becomes the
// first chunk and the rest are appended, so nothing is lost to the tail
// window.
func (b *Bridge) finishTurn(t *liveTurn) {
	t.mu.Lock()
	head := b.renderTurnHeaderLocked(t)
	full := head
	if strings.TrimSpace(t.timeline) != "" {
		full = head + "\n\n" + t.timeline
	}
	messageID, lastBody := t.messageID, t.lastBody
	t.mu.Unlock()

	chunks := splitMessage(full)
	if len(chunks) == 0 {
		return
	}
	if messageID != "" {
		if chunks[0] != lastBody {
			if err := b.api.editMessage(b.ctx, b.threadID, messageID, chunks[0]); err != nil {
				slog.Debug("Discord bridge: failed to finalize turn message", "error", err.Error())
			}
		}
		chunks = chunks[1:]
	}
	for _, chunk := range chunks {
		if _, err := b.api.createMessage(b.ctx, b.threadID, chunk); err != nil {
			slog.Debug("Discord bridge: failed to append turn chunk", "error", err.Error())
		}
	}
}

// composeTurnLocked renders the live message: status header plus the
// newest slice of the timeline. Must be called with t.mu held.
func (b *Bridge) composeTurnLocked(t *liveTurn) string {
	head := b.renderTurnHeaderLocked(t)
	if strings.TrimSpace(t.timeline) == "" {
		return head
	}
	room := safeMessageLen - len(head) - 3
	if room < 200 {
		room = 200
	}
	return head + "\n\n" + tailText(t.timeline, room)
}

// renderTurnHeaderLocked renders the animated status line. Must be
// called with t.mu held.
func (b *Bridge) renderTurnHeaderLocked(t *liveTurn) string {
	elapsed := b.now().Sub(t.started)
	switch t.state {
	case turnDone:
		line := fmt.Sprintf("%s **Done in %s**", iconDone, humanDuration(elapsed))
		if t.model != "" {
			line += " · `" + t.model + "`"
		}
		return line
	case turnCancelled:
		return fmt.Sprintf("%s **Cancelled after %s**", iconCancelled, humanDuration(elapsed))
	case turnError:
		line := fmt.Sprintf("%s **Failed after %s**", iconError, humanDuration(elapsed))
		if t.errText != "" {
			line += " — " + compactLine(t.errText, 200)
		}
		return line
	}

	icon, label := iconThinking, "Thinking…"
	switch t.state {
	case turnWriting:
		icon, label = iconWriting, "Writing…"
	case turnTool:
		icon, label = iconTool, "Running "+cmpOr(t.toolName, "tool")+"…"
	case turnWaitingPermission:
		icon, label = iconPermission, "Waiting for permission…"
	}
	return fmt.Sprintf("%s %s **%s** %s", spinnerFrame(t.frame), icon, label, humanDuration(elapsed))
}

// deriveTurnState infers the live phase from a message snapshot.
func deriveTurnState(msg message.Message) (turnState, string) {
	lastTool, lastText, lastReasoning := -1, -1, -1
	toolName := ""
	for i, part := range msg.Parts {
		switch p := part.(type) {
		case message.ToolCall:
			lastTool = i
			toolName = p.Name
		case message.TextContent:
			if !p.Hidden && strings.TrimSpace(p.Text) != "" {
				lastText = i
			}
		case message.ReasoningContent:
			lastReasoning = i
		}
	}
	switch {
	case lastText >= 0 && lastText > lastTool && lastText > lastReasoning:
		return turnWriting, ""
	case lastTool >= 0 && lastTool > lastReasoning:
		return turnTool, toolName
	default:
		return turnThinking, ""
	}
}

// rememberCall caches a tool call so its result renders the call line.
func (b *Bridge) rememberCall(tc message.ToolCall) {
	if tc.ID == "" {
		return
	}
	b.callMu.Lock()
	defer b.callMu.Unlock()
	if len(b.calls) >= callCacheLimit {
		clear(b.calls)
	}
	b.calls[tc.ID] = tc
}

// lookupCall returns a remembered tool call, or the zero value.
func (b *Bridge) lookupCall(id string) message.ToolCall {
	b.callMu.Lock()
	defer b.callMu.Unlock()
	return b.calls[id]
}

// ── Message helpers ──────────────────────────────────────────────────────

// messageText returns a message's first visible text part.
func messageText(msg message.Message) string {
	for _, part := range msg.Parts {
		if content, ok := part.(message.TextContent); ok && !content.Hidden && content.Text != "" {
			return content.Text
		}
	}
	return ""
}

// toolCalls extracts the tool-call parts of a message.
func toolCalls(msg message.Message) []message.ToolCall {
	var out []message.ToolCall
	for _, part := range msg.Parts {
		if tc, ok := part.(message.ToolCall); ok {
			out = append(out, tc)
		}
	}
	return out
}

// firstToolResult extracts a message's first tool result.
func firstToolResult(msg message.Message) (message.ToolResult, bool) {
	for _, part := range msg.Parts {
		if tr, ok := part.(message.ToolResult); ok {
			return tr, true
		}
	}
	return message.ToolResult{}, false
}

// cmpOr returns the first non-empty string.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
