package agent

import (
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestIsUserTurnStart(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  message.Message
		want bool
	}{
		{
			"real prompt",
			message.Message{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "look at this"}}},
			true,
		},
		{
			"hidden continuation",
			message.Message{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "continue", Hidden: true}}},
			false,
		},
		{
			"compaction summary",
			message.Message{Role: message.User, IsSummaryMessage: true, Parts: []message.ContentPart{message.TextContent{Text: "summary of earlier turns"}}},
			false,
		},
		{
			"subagent report",
			message.Message{Role: message.User, Parts: []message.ContentPart{message.SubAgentReport{Label: "search", Output: "done"}}},
			false,
		},
		{
			"blank text",
			message.Message{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: " \n\t"}}},
			false,
		},
		{
			"image without text",
			message.Message{Role: message.User, Parts: []message.ContentPart{message.BinaryContent{Path: "a.png", MIMEType: "image/png", Data: []byte("img")}}},
			false,
		},
		{
			"assistant reply",
			message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "ok"}}},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isUserTurnStart(tc.msg))
		})
	}
}

func TestImageRetentionCut(t *testing.T) {
	t.Parallel()

	turn := func(text string) message.Message {
		return message.Message{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: text}}}
	}
	reply := message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "ok"}}}
	// Real user turns start at indexes 0, 2, and 4.
	msgs := []message.Message{turn("one"), reply, turn("two"), reply, turn("three"), reply}

	require.Equal(t, 0, imageRetentionCut(msgs, -1), "negative disables expiry")
	require.Equal(t, len(msgs), imageRetentionCut(msgs, 0), "zero expires every history image")
	require.Equal(t, 0, imageRetentionCut(msgs, 3), "retaining every turn keeps every image")
	require.Equal(t, 0, imageRetentionCut(msgs, 5), "retaining more turns than exist keeps every image")
	require.Equal(t, 4, imageRetentionCut(msgs, 1), "retain 1 keeps only the last turn")
	require.Equal(t, 2, imageRetentionCut(msgs, 2), "retain 2 keeps the last two turns")
	require.Equal(t, 0, imageRetentionCut(nil, 1), "no history means no cut")
}

func TestImageRetentionCutIgnoresNonTurnMessages(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "one"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "keep going", Hidden: true}}},
		{Role: message.User, IsSummaryMessage: true, Parts: []message.ContentPart{message.TextContent{Text: "summary"}}},
		{Role: message.User, Parts: []message.ContentPart{message.SubAgentReport{Label: "search", Output: "done"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "two"}}},
	}

	require.Equal(t, 4, imageRetentionCut(msgs, 1), "only real prompts start turns")
}

func TestOmitImagesInMessage(t *testing.T) {
	t.Parallel()

	msg := imageBudgetMessage(
		fantasy.TextPart{Text: "look"},
		imageBudgetFile("a.png", "image/png"),
	)
	got := omitImagesInMessage(msg, "expired note")

	text, ok := fantasy.AsMessagePart[fantasy.TextPart](got.Content[0])
	require.True(t, ok)
	require.Equal(t, "look", text.Text, "non-image parts must survive")

	placeholder, ok := fantasy.AsMessagePart[fantasy.TextPart](got.Content[1])
	require.True(t, ok, "the image must become the placeholder text")
	require.Equal(t, "expired note", placeholder.Text)

	_, stillFile := fantasy.AsMessagePart[fantasy.FilePart](msg.Content[1])
	require.True(t, stillFile, "the input message's backing array must not be mutated")
}

func TestOmitImagesInMessageToolResult(t *testing.T) {
	t.Parallel()

	msg := imageBudgetMessage(imageBudgetMedia("call-1", "image/png"))
	got := omitImagesInMessage(msg, "expired note")

	result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](got.Content[0])
	require.True(t, ok)
	require.Equal(t, "call-1", result.ToolCallID)
	output, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Output)
	require.True(t, ok, "media output must become text")
	require.Equal(t, "tool image\nexpired note", output.Text, "the caption must survive above the placeholder")

	_, stillMedia := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](
		msg.Content[0].(fantasy.ToolResultPart).Output)
	require.True(t, stillMedia, "the input message's backing array must not be mutated")
}

func TestOmitImagesInMessageWithoutImages(t *testing.T) {
	t.Parallel()

	msg := imageBudgetMessage(fantasy.TextPart{Text: "plain"}, imageBudgetFile("notes.txt", "text/plain"))
	got := omitImagesInMessage(msg, "expired note")
	require.Equal(t, msg, got, "a message without images must pass through unchanged")
}

// retentionTestImage is valid base64 because ToAIMessage only replays a tool
// result as media when its stored Data decodes.
const retentionTestImage = "ZmFrZS1wbmc="

func retentionUserTurn(text string, images ...string) message.Message {
	parts := []message.ContentPart{message.TextContent{Text: text}}
	for _, name := range images {
		parts = append(parts, message.BinaryContent{Path: name, MIMEType: "image/png", Data: []byte("fake-png")})
	}
	return message.Message{Role: message.User, Parts: parts}
}

func retentionReply(text string) message.Message {
	return message.Message{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: text}}}
}

func retentionHistoryHasImage(history []fantasy.Message, name string) bool {
	for _, msg := range history {
		for _, part := range msg.Content {
			if file, ok := fantasy.AsMessagePart[fantasy.FilePart](part); ok && file.Filename == name {
				return true
			}
		}
	}
	return false
}

func retentionPlaceholders(history []fantasy.Message) int {
	count := 0
	for _, msg := range history {
		for _, part := range msg.Content {
			if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok && text.Text == imageExpiredText {
				count++
			}
		}
	}
	return count
}

// retentionAgent builds an agent whose retention setting is retain, or unset
// (the default) when retain is nil.
func retentionAgent(t *testing.T, retain *int) *sessionAgent {
	t.Helper()
	env := testEnv(t)
	m := Model{CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}}
	opts := SessionAgentOptions{
		Models:       fixedModels(m, m),
		SystemPrompt: "test prompt",
		Sessions:     env.sessions,
		Messages:     env.messages,
		titles:       env.titles,
	}
	if retain != nil {
		opts.Cfg = config.NewTestStore(&config.Config{Options: &config.Options{ImageRetentionTurns: retain}})
	}
	return NewSessionAgent(opts).(*sessionAgent)
}

func TestPreparePrompt_ExpiresOldTurnImages(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	sa := testSessionAgent(env, nil, nil, "test prompt").(*sessionAgent)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	create := func(role message.MessageRole, parts ...message.ContentPart) {
		t.Helper()
		_, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{Role: role, Parts: parts})
		require.NoError(t, err)
	}
	create(message.User, message.TextContent{Text: "first"}, message.BinaryContent{Path: "old.png", MIMEType: "image/png", Data: []byte("fake-png")})
	create(message.Assistant, message.ToolCall{ID: "shot-1", Name: "screenshot", Input: "{}", Finished: true})
	create(message.Tool, message.ToolResult{ToolCallID: "shot-1", Name: "screenshot", Content: "captured", Data: retentionTestImage, MIMEType: "image/png"})
	create(message.Assistant, message.TextContent{Text: "done"})
	create(message.User, message.TextContent{Text: "second"}, message.BinaryContent{Path: "recent.png", MIMEType: "image/png", Data: []byte("fake-png")})

	msgs, err := env.messages.List(ctx, sess.ID)
	require.NoError(t, err)

	live := message.Attachment{FileName: "live.png", MimeType: "image/png", Content: []byte("live-shot")}
	history, files := sa.preparePrompt(msgs, true, false, live)

	require.False(t, retentionHistoryHasImage(history, "old.png"), "the old turn's pasted image must expire")
	require.True(t, retentionHistoryHasImage(history, "recent.png"), "the retained turn's image must stay")
	require.Equal(t, 1, retentionPlaceholders(history), "the expired image must become the staleness placeholder")

	var toolText string
	foundTool := false
	for _, msg := range history {
		for _, part := range msg.Content {
			result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok || result.ToolCallID != "shot-1" {
				continue
			}
			output, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Output)
			require.True(t, ok, "the stale screenshot must become text")
			toolText = output.Text
			foundTool = true
		}
	}
	require.True(t, foundTool, "the screenshot result must be replayed")
	require.Contains(t, toolText, "captured", "the caption must survive")
	require.Contains(t, toolText, imageExpiredText)

	require.Len(t, files, 1, "the current turn's attachment must pass through untouched")
	require.Equal(t, "live.png", files[0].Filename)
	require.Equal(t, []byte("live-shot"), files[0].Data)

	require.Equal(t, []byte("fake-png"), msgs[0].BinaryContent()[0].Data, "stored messages must not be mutated")
	require.Equal(t, retentionTestImage, msgs[2].ToolResults()[0].Data, "stored tool results must not be mutated")
}

func TestPreparePrompt_ImageRetentionSetting(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		retentionUserTurn("first", "old.png"),
		retentionReply("ok"),
		retentionUserTurn("second", "mid.png"),
		retentionReply("ok"),
		retentionUserTurn("third", "new.png"),
	}

	cases := []struct {
		name    string
		retain  *int
		kept    []string
		expired []string
	}{
		{"default keeps one turn", nil, []string{"new.png"}, []string{"old.png", "mid.png"}},
		{"two turns", ptr(2), []string{"mid.png", "new.png"}, []string{"old.png"}},
		{"zero drops all history images", ptr(0), nil, []string{"old.png", "mid.png", "new.png"}},
		{"negative keeps everything", ptr(-1), []string{"old.png", "mid.png", "new.png"}, nil},
		{"more turns than exist", ptr(9), []string{"old.png", "mid.png", "new.png"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sa := retentionAgent(t, tc.retain)
			history, files := sa.preparePrompt(msgs, true, false)
			require.Empty(t, files)
			for _, name := range tc.kept {
				require.Truef(t, retentionHistoryHasImage(history, name), "%s must stay", name)
			}
			for _, name := range tc.expired {
				require.Falsef(t, retentionHistoryHasImage(history, name), "%s must expire", name)
			}
			require.Equal(t, len(tc.expired), retentionPlaceholders(history))
		})
	}
}

func TestPreparePrompt_KeepsRetainedToolResultMedia(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		retentionUserTurn("only turn"),
		{Role: message.Assistant, Parts: []message.ContentPart{message.ToolCall{ID: "shot-1", Name: "screenshot", Input: "{}", Finished: true}}},
		{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{ToolCallID: "shot-1", Name: "screenshot", Content: "captured", Data: retentionTestImage, MIMEType: "image/png"}}},
	}

	sa := retentionAgent(t, nil)
	history, _ := sa.preparePrompt(msgs, true, false)

	foundMedia := false
	for _, msg := range history {
		for _, part := range msg.Content {
			result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part)
			if !ok || result.ToolCallID != "shot-1" {
				continue
			}
			_, ok = fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Output)
			foundMedia = ok
		}
	}
	require.True(t, foundMedia, "the retained turn's fresh screenshot must stay an image")
	require.Zero(t, retentionPlaceholders(history))
}

// TestSummarize_ExtendsLivePrefixUnderDefaultRetention pins the prefix-cache
// invariant under the default retention window: the compaction request must
// expire exactly the images the last live step expired, so it extends the live
// prefix byte-for-byte instead of advancing the retention window.
func TestSummarize_ExtendsLivePrefixUnderDefaultRetention(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	model := &toolRequestModel{text: "done"}
	// Default retention (1) and the default image budget, so retention is
	// the only pass that can expire these images.
	cfg := config.NewTestStore(&config.Config{Options: &config.Options{}})
	agent := newVisionCompactionAgent(env, model, cfg, &recordingTool{name: "alpha"})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	for _, name := range []string{"first.png", "second.png"} {
		_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "look at " + name},
				message.BinaryContent{Path: name, MIMEType: "image/png", Data: []byte("fake-png")},
			},
		})
		require.NoError(t, err)
	}

	_, err = agent.Run(ctx, SessionAgentCall{Prompt: "carry on", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.Len(t, model.calls, 1)
	live := messagesJSON(t, model.calls[0].Prompt)

	require.NoError(t, agent.Summarize(ctx, sess.ID, nil, nil))
	require.Len(t, model.calls, 2)
	compaction := messagesJSON(t, model.calls[1].Prompt)

	require.Greater(t, len(compaction), len(live))
	require.Equal(t, live, compaction[:len(live)],
		"compaction must extend the live prefix byte-for-byte under default retention")
	require.False(t, retentionHistoryHasImage(model.calls[1].Prompt, "first.png"),
		"the image the live step expired must stay expired in compaction")
	require.True(t, retentionHistoryHasImage(model.calls[1].Prompt, "second.png"),
		"the image the live step kept must stay kept in compaction")
}

func TestPreparePrompt_ByteStableAcrossCalls(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		retentionUserTurn("first", "old.png"),
		retentionReply("ok"),
		retentionUserTurn("second", "new.png"),
	}
	sa := retentionAgent(t, nil)

	h1, f1 := sa.preparePrompt(msgs, true, false)
	h2, f2 := sa.preparePrompt(msgs, true, false)
	require.Equal(t, h1, h2, "two replays of the same msgs must be byte-identical")
	require.Equal(t, f1, f2)
}

func retentionToolResultIsMedia(history []fantasy.Message, callID string) bool {
	for _, msg := range history {
		for _, part := range msg.Content {
			if result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok && result.ToolCallID == callID {
				_, isMedia := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentMedia](result.Output)
				return isMedia
			}
		}
	}
	return false
}

func TestPreparePrompt_ExpiresToolResultMediaByRetain(t *testing.T) {
	t.Parallel()

	// Real user turns start at indexes 0 and 3; each is followed by a
	// screenshot tool call and its media result.
	msgs := []message.Message{
		retentionUserTurn("first"),
		{Role: message.Assistant, Parts: []message.ContentPart{message.ToolCall{ID: "shot-1", Name: "screenshot", Input: "{}", Finished: true}}},
		{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{ToolCallID: "shot-1", Name: "screenshot", Content: "captured", Data: retentionTestImage, MIMEType: "image/png"}}},
		retentionUserTurn("second"),
		{Role: message.Assistant, Parts: []message.ContentPart{message.ToolCall{ID: "shot-2", Name: "screenshot", Input: "{}", Finished: true}}},
		{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{ToolCallID: "shot-2", Name: "screenshot", Content: "fresh", Data: retentionTestImage, MIMEType: "image/png"}}},
	}

	cases := []struct {
		name   string
		retain int
		shot1  bool
		shot2  bool
	}{
		{"zero expires all media", 0, false, false},
		{"one keeps the last turn's media", 1, false, true},
		{"two keeps both turns' media", 2, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sa := retentionAgent(t, &tc.retain)
			history, _ := sa.preparePrompt(msgs, true, false)
			require.Equal(t, tc.shot1, retentionToolResultIsMedia(history, "shot-1"), "shot-1 media")
			require.Equal(t, tc.shot2, retentionToolResultIsMedia(history, "shot-2"), "shot-2 media")
		})
	}
}

func TestPreparePrompt_NoImagesSupportWithRetention(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		retentionUserTurn("first", "old.png"),
		retentionReply("ok"),
		retentionUserTurn("second", "new.png"),
	}
	sa := retentionAgent(t, nil)
	live := message.Attachment{FileName: "live.png", MimeType: "image/png", Content: []byte("live")}

	history, files := sa.preparePrompt(msgs, false, false, live)
	require.False(t, retentionHistoryHasImage(history, "old.png"))
	require.False(t, retentionHistoryHasImage(history, "new.png"))
	require.Empty(t, files, "a model without vision gets no image attachments")
	require.Zero(t, retentionPlaceholders(history), "images are filtered before retention, so no placeholders are left")
}
