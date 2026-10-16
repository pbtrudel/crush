package discord

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// testBridge builds and launches a bridge wired to fakes. The gateway is
// stubbed out; events are delivered through handleEvent directly or via
// the returned channel.
func testBridge(t *testing.T, cfg Config) (*Bridge, *fakeEnv, *fakeAPI, chan pubsub.Event[tea.Msg]) {
	t.Helper()
	env := newFakeEnv()
	env.cfg.Options = &config.Options{DataDirectory: t.TempDir()}
	api := newFakeAPI()
	events := make(chan pubsub.Event[tea.Msg], 32)
	b := newBridge(env, nil, "sess-1", cfg, "code", bridgeDeps{
		api:          api,
		events:       events,
		catalog:      env.catalogFn,
		now:          time.Now,
		tick:         10 * time.Millisecond,
		editThrottle: time.Millisecond,
		typing:       20 * time.Millisecond,
		gatewayStart: func(ctx context.Context) {},
	})
	b.threadID = "thread-1"
	b.threadURL = "https://discord.example/thread-1"

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, b.launch(ctx))
	t.Cleanup(func() {
		cancel()
		b.Stop()
	})
	return b, env, api, events
}

func testConfig() Config {
	return Config{Token: "t", ChannelIDs: []string{"chan-1"}}
}

func evMessage(typ pubsub.EventType, msg message.Message) pubsub.Event[tea.Msg] {
	return pubsub.Event[tea.Msg]{Payload: pubsub.Event[message.Message]{Type: typ, Payload: msg}}
}

func userMessage(text string) message.Message {
	return message.Message{
		ID: "u-" + text, SessionID: "sess-1", Role: message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}
}

func assistantMessage(id string, parts ...message.ContentPart) message.Message {
	return message.Message{ID: id, SessionID: "sess-1", Role: message.Assistant, Parts: parts}
}

func threadMessage(content string) gatewayMessage {
	msg := gatewayMessage{ID: "d1", ChannelID: "thread-1", Content: content}
	msg.Author.ID = "u1"
	msg.Author.Username = "bob"
	return msg
}

func TestBridgeMirrorsAssistantTurn(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	b.handleEvent(evMessage(pubsub.CreatedEvent, assistantMessage("m1")))
	require.Eventually(t, func() bool { return api.hasContentContaining("Thinking") }, 2*time.Second, 10*time.Millisecond)

	b.handleEvent(evMessage(pubsub.UpdatedEvent, assistantMessage("m1", message.TextContent{Text: "Hello from the agent"})))
	require.Eventually(t, func() bool {
		return api.hasContentContaining("Hello from the agent")
	}, 2*time.Second, 10*time.Millisecond)

	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[notify.RunComplete]{
		Type:    pubsub.UpdatedEvent,
		Payload: notify.RunComplete{SessionID: "sess-1", MessageID: "m1", Text: "Hello from the agent"},
	}})
	require.Eventually(t, func() bool {
		return api.hasContentContaining("Done in")
	}, 2*time.Second, 10*time.Millisecond)

	// Late updates for a completed message must not resurrect the turn.
	b.handleEvent(evMessage(pubsub.UpdatedEvent, assistantMessage("m1", message.TextContent{Text: "late"})))
	time.Sleep(50 * time.Millisecond)
	require.False(t, api.hasContentContaining("late"), "completed message ids are ignored")
}

func TestBridgeCancelledAndErrorStates(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	b.handleEvent(evMessage(pubsub.CreatedEvent, assistantMessage("m1")))
	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[notify.RunComplete]{
		Type:    pubsub.UpdatedEvent,
		Payload: notify.RunComplete{SessionID: "sess-1", MessageID: "m1", Cancelled: true},
	}})
	require.Eventually(t, func() bool {
		return api.hasContentContaining("Cancelled")
	}, 2*time.Second, 10*time.Millisecond)

	b.handleEvent(evMessage(pubsub.CreatedEvent, assistantMessage("m2")))
	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[notify.RunComplete]{
		Type:    pubsub.UpdatedEvent,
		Payload: notify.RunComplete{SessionID: "sess-1", MessageID: "m2", Error: "provider exploded"},
	}})
	require.Eventually(t, func() bool {
		return api.hasContentContaining("provider exploded")
	}, 2*time.Second, 10*time.Millisecond)
}

func TestBridgeEchoesTerminalPrompts(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	b.handleEvent(evMessage(pubsub.CreatedEvent, userMessage("run the tests")))
	require.True(t, api.hasContentContaining("(terminal)"))
	require.True(t, api.hasContentContaining("run the tests"))
}

func TestBridgeSkipsEchoForDiscordPrompts(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	b.noteSubmission("remote task")
	b.handleEvent(evMessage(pubsub.CreatedEvent, userMessage("remote task")))
	require.False(t, api.hasContentContaining("(terminal)"))
}

func TestBridgeSkipsChannelInjectionEcho(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())
	b.handleEvent(evMessage(pubsub.CreatedEvent, userMessage(`<channel source="x">hi</channel>`)))
	require.False(t, api.hasContentContaining("(terminal)"))
}

func TestBridgeFreezesCompletedStepsAboveFooter(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	// Wait for the idle footer so the freeze below recycles it.
	require.Eventually(t, func() bool { return b.footerMessageID() != "" }, 2*time.Second, 10*time.Millisecond)

	// Step 1: streamed text plus a tool call, then the Finish part.
	b.handleEvent(evMessage(pubsub.CreatedEvent, assistantMessage("m1")))
	b.handleEvent(evMessage(pubsub.UpdatedEvent, assistantMessage("m1",
		message.TextContent{Text: "first response"},
		message.ToolCall{ID: "tc1", Name: "bash", Input: `{"command":"go build"}`},
		message.Finish{},
	)))
	require.Eventually(t, func() bool { return api.hasContentContaining("first response") }, 2*time.Second, 10*time.Millisecond)

	// The footer was recycled below the frozen transcript and carries
	// the tool summary, not a copy of the text.
	require.Eventually(t, func() bool {
		last := api.lastPostContent()
		return strings.Contains(last, "bash ×1") && !strings.Contains(last, "first response")
	}, 2*time.Second, 10*time.Millisecond)

	// Step 2 freezes as its own message.
	b.handleEvent(evMessage(pubsub.CreatedEvent, assistantMessage("m2", message.TextContent{Text: "second response"})))
	b.handleEvent(evMessage(pubsub.UpdatedEvent, assistantMessage("m2", message.TextContent{Text: "second response"}, message.Finish{})))
	require.Eventually(t, func() bool { return api.hasContentContaining("second response") }, 2*time.Second, 10*time.Millisecond)

	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[notify.RunComplete]{
		Type:    pubsub.UpdatedEvent,
		Payload: notify.RunComplete{SessionID: "sess-1", MessageID: "m2"},
	}})
	require.Eventually(t, func() bool { return api.hasContentContaining("Done in") }, 2*time.Second, 10*time.Millisecond)
	last := api.lastPostContent()
	require.Contains(t, last, "bash ×1", "footer remains the last message")
	require.NotContains(t, last, "second response")
}

func TestBridgeReanchorsFooterBelowUserMessage(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	require.Eventually(t, func() bool { return b.footerMessageID() != "" }, 2*time.Second, 10*time.Millisecond)
	oldID := b.footerMessageID()

	// A prompt posted by a user lands below the footer; the bridge
	// must recycle the footer underneath it.
	b.handleThreadMessage(threadMessage("make the tests pass"))

	require.True(t, api.hasDelete(oldID), "the footer is deleted")
	newID := b.footerMessageID()
	require.NotEmpty(t, newID)
	require.NotEqual(t, oldID, newID)
	require.Equal(t, newID, api.lastPostID(), "footer is the last message")
}

func TestBridgeFooterMetrics(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[session.Session]{
		Type: pubsub.UpdatedEvent,
		Payload: session.Session{
			ID:                   "sess-1",
			PromptTokens:         12600,
			CompletionTokens:     200,
			TotalContextTokens:   400,
			TotalCacheReadTokens: 100,
		},
	}})

	// (12600+200)/128000 is 10%; 100/400 is 25%.
	require.Eventually(t, func() bool { return api.hasContentContaining("context 10%") }, 2*time.Second, 10*time.Millisecond)
	require.True(t, api.hasContentContaining("cache 25%"))
}

func TestBridgePermissionCardAndReaction(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	req := permission.PermissionRequest{
		ID: "p1", SessionID: "sess-1", ToolCallID: "tc1", ToolName: "bash",
		Description: "run npm test",
	}
	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[permission.PermissionRequest]{
		Type: pubsub.CreatedEvent, Payload: req,
	}})

	require.True(t, api.hasContentContaining("Permission needed"))
	require.True(t, api.hasContentContaining("run npm test"))
	require.Equal(t, 3, api.reactionCount())

	cardID := pendingCardID(b, "p1")
	require.NotEmpty(t, cardID)
	reaction := gatewayReaction{UserID: "u1", ChannelID: "thread-1", MessageID: cardID}
	reaction.Emoji.Name = reactionAllow
	b.handleThreadReaction(reaction)
	require.Equal(t, 1, env.grantCount())

	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[permission.PermissionNotification]{
		Type:    pubsub.CreatedEvent,
		Payload: permission.PermissionNotification{ToolCallID: "tc1", Granted: true},
	}})
	require.True(t, api.hasEditContaining("approved"))
}

func TestBridgePermissionDenyAndAllowSession(t *testing.T) {
	t.Parallel()
	b, env, _, _ := testBridge(t, testConfig())

	for _, id := range []string{"p1", "p2"} {
		b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[permission.PermissionRequest]{
			Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
				ID: id, SessionID: "sess-1", ToolCallID: "tc" + id, ToolName: "bash",
			},
		}})
	}

	msgs := map[string]string{"p1": pendingCardID(b, "p1"), "p2": pendingCardID(b, "p2")}
	require.NotEmpty(t, msgs["p1"])
	require.NotEmpty(t, msgs["p2"])

	deny := gatewayReaction{UserID: "u1", ChannelID: "thread-1", MessageID: msgs["p1"]}
	deny.Emoji.Name = reactionDeny
	b.handleThreadReaction(deny)
	require.Len(t, env.denies, 1)

	allowSession := gatewayReaction{UserID: "u1", ChannelID: "thread-1", MessageID: msgs["p2"]}
	allowSession.Emoji.Name = reactionAllowSession
	b.handleThreadReaction(allowSession)
	require.Len(t, env.grantSessions, 1)
}

func TestBridgeReactionUserFilter(t *testing.T) {
	t.Parallel()
	b, env, _, _ := testBridge(t, testConfig())
	restricted := Config{Token: "t", ChannelIDs: []string{"chan-1"}, AllowedUserIDs: []string{"u9"}}
	_ = restricted // The bridge under test keeps its original config.

	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[permission.PermissionRequest]{
		Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
			ID: "p1", SessionID: "sess-1", ToolCallID: "tc1", ToolName: "bash",
		},
	}})
	reaction := gatewayReaction{UserID: "u1", ChannelID: "thread-1", MessageID: pendingCardID(b, "p1")}
	reaction.Emoji.Name = reactionAllow
	b.handleThreadReaction(reaction)
	require.Equal(t, 1, env.grantCount(), "default config allows any thread member")
}

func TestBridgeQuestionCardAndAnswer(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[question.Request]{
		Type: pubsub.CreatedEvent, Payload: question.Request{
			ID: "q1", SessionID: "sess-1",
			Questions: []question.Question{{ID: "qq", Type: question.TypeYesNo, Text: "Proceed?"}},
		},
	}})
	require.True(t, api.hasContentContaining("Proceed?"))

	b.handleThreadMessage(threadMessage("/answer yes"))
	require.Len(t, env.answers, 1)
	require.NotNil(t, env.answers[0].Yes)
	require.True(t, *env.answers[0].Yes)

	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[question.Notification]{
		Type: pubsub.CreatedEvent, Payload: question.Notification{BatchID: "q1"},
	}})
	require.True(t, api.hasEditContaining("answered"))
}

func TestBridgeDiscordPromptRunsAgent(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	b.handleThreadMessage(threadMessage("build the thing"))
	require.Eventually(t, func() bool { return env.lastRun() == "build the thing" }, 2*time.Second, 10*time.Millisecond)

	// The resulting user message must not be echoed back.
	b.handleEvent(evMessage(pubsub.CreatedEvent, userMessage("build the thing")))
	require.False(t, api.hasContentContaining("(terminal)"))
}

func TestBridgeEscapeSlashPrompt(t *testing.T) {
	t.Parallel()
	b, env, _, _ := testBridge(t, testConfig())
	b.handleThreadMessage(threadMessage(`\/not-a-command`))
	require.Eventually(t, func() bool { return env.lastRun() == "/not-a-command" }, 2*time.Second, 10*time.Millisecond)
}

func TestBridgeSlashCommandRouting(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	b.handleThreadMessage(threadMessage("/stop"))
	require.Equal(t, 1, env.cancelCount())
	require.True(t, api.hasContentContaining("Cancelling"))
	require.Zero(t, env.runCount(), "commands must not reach the agent")
}

func TestBridgeFreshThreadIgnored(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	foreign := threadMessage("hello from elsewhere")
	foreign.ChannelID = "other-channel"
	b.handleThreadMessage(foreign)
	time.Sleep(20 * time.Millisecond)
	require.Zero(t, env.runCount())
	require.False(t, api.hasContentContaining("hello from elsewhere"))
}

func TestBridgeAllowedUsers(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.AllowedUserIDs = []string{"u9"}
	b, env, api, _ := testBridge(t, cfg)

	b.handleThreadMessage(threadMessage("not allowed to run this"))
	require.True(t, api.hasContentContaining("not allowed"))
	require.Zero(t, env.runCount())

	allowed := threadMessage("allowed")
	allowed.Author.ID = "u9"
	b.handleThreadMessage(allowed)
	require.Eventually(t, func() bool { return env.runCount() == 1 }, 2*time.Second, 10*time.Millisecond)
}

func TestBridgeQueuedPromptNote(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())
	env.setBusy(true, "one")

	b.handleThreadMessage(threadMessage("second"))
	require.True(t, api.hasContentContaining("Queued (position 2)"))

	// The note lands above the footer, which stays the last message.
	require.Equal(t, b.footerMessageID(), api.lastPostID())
	require.NotContains(t, api.lastPostContent(), "Queued")
}

func TestBridgeThreadRenameFromTitle(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[session.Session]{
		Type:    pubsub.UpdatedEvent,
		Payload: session.Session{ID: "sess-1", Title: "Generated Title"},
	}})
	require.Equal(t, []string{"Generated Title"}, api.renames)

	// Re-emitting the same title does not rename again.
	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[session.Session]{
		Type:    pubsub.UpdatedEvent,
		Payload: session.Session{ID: "sess-1", Title: "Generated Title"},
	}})
	require.Len(t, api.renames, 1)
}

func TestBridgeTypingWhileBusy(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())
	env.setBusy(true)

	require.Eventually(t, func() bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		return len(api.typing) > 0
	}, 2*time.Second, 10*time.Millisecond)
	_ = b
}

func TestBridgeStopIdempotent(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	// Wait for the footer so Stop has one to clean up.
	require.Eventually(t, func() bool { return b.footerMessageID() != "" }, 2*time.Second, 10*time.Millisecond)
	footerID := b.footerMessageID()

	b.Stop()
	b.Stop()

	require.True(t, api.hasDelete(footerID), "the footer is removed on stop")
	require.Empty(t, b.footerMessageID())

	api.mu.Lock()
	farewells := 0
	for _, p := range api.posts {
		if p.content == farewellText {
			farewells++
		}
	}
	api.mu.Unlock()
	require.Equal(t, 1, farewells)
}

func TestBridgeAgentReauthNote(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())
	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[notify.Notification]{
		Type: pubsub.CreatedEvent,
		Payload: notify.Notification{
			SessionID: "sess-1", Type: notify.TypeReAuthenticate, Message: "token expired",
		},
	}})
	require.True(t, api.hasContentContaining("Re-authentication needed"))
	require.True(t, api.hasContentContaining("token expired"))
}

func TestBridgeIgnoresOtherSessions(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	other := userMessage("from another session")
	other.SessionID = "other-sess"
	b.handleEvent(evMessage(pubsub.CreatedEvent, other))
	require.False(t, api.hasContentContaining("(terminal)"))

	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[notify.RunComplete]{
		Type:    pubsub.UpdatedEvent,
		Payload: notify.RunComplete{SessionID: "other-sess", MessageID: "x"},
	}})
	require.Zero(t, env.cancelCount())
}

func TestDeriveTurnState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		parts []message.ContentPart
		want  turnState
		tool  string
	}{
		{"empty", nil, turnThinking, ""},
		{"reasoning only", []message.ContentPart{message.ReasoningContent{Thinking: "x"}}, turnThinking, ""},
		{"text", []message.ContentPart{message.TextContent{Text: "hi"}}, turnWriting, ""},
		{"hidden text counts as nothing", []message.ContentPart{message.TextContent{Text: "hi", Hidden: true}}, turnThinking, ""},
		{
			"tool after text",
			[]message.ContentPart{message.TextContent{Text: "hi"}, message.ToolCall{Name: "bash"}},
			turnTool, "bash",
		},
		{
			"text after tool",
			[]message.ContentPart{message.ToolCall{Name: "bash"}, message.TextContent{Text: "hi"}},
			turnWriting, "",
		},
	}
	for _, tt := range tests {
		state, call := deriveTurnState(message.Message{Parts: tt.parts})
		require.Equal(t, tt.want, state, tt.name)
		require.Equal(t, tt.tool, call.Name, tt.name)
	}
}

// pendingCardID returns the posted card id for a permission request.
func pendingCardID(b *Bridge, reqID string) string {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	return b.permMessage[reqID]
}

func TestBridgeHandleEventViaChannel(t *testing.T) {
	t.Parallel()
	b, _, api, events := testBridge(t, testConfig())

	events <- evMessage(pubsub.CreatedEvent, userMessage("over the wire"))
	require.Eventually(t, func() bool { return api.hasContentContaining("over the wire") }, 2*time.Second, 10*time.Millisecond)
	_ = b
}
