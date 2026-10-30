package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// stubSummaryModel is a minimal fantasy.LanguageModel whose stream returns a
// fixed summary text and records every Call it receives, so tests can assert
// on the exact request shape a compaction sends.
type stubSummaryModel struct {
	summaryText string
	mu          sync.Mutex
	calls       []fantasy.Call
	// usage overrides the fixed test usage when non-zero, so a test can
	// report a prompt large enough to cross the auto-compaction threshold.
	usage fantasy.Usage
}

func (m *stubSummaryModel) Provider() string { return "stub" }
func (m *stubSummaryModel) Model() string    { return "stub-model" }

func (m *stubSummaryModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("not implemented")
}

func (m *stubSummaryModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	m.calls = append(m.calls, call)
	m.mu.Unlock()
	return func(yield func(fantasy.StreamPart) bool) {
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "t1"})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "t1", Delta: m.summaryText})
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "t1"})
		usage := fantasy.Usage{InputTokens: 1_000, OutputTokens: 100, TotalTokens: 1_100}
		if m.usage != (fantasy.Usage{}) {
			usage = m.usage
		}
		yield(fantasy.StreamPart{
			Type:         fantasy.StreamPartTypeFinish,
			FinishReason: fantasy.FinishReasonStop,
			Usage:        usage,
		})
	}, nil
}

func (m *stubSummaryModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *stubSummaryModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

// newCompactionAgent builds a sessionAgent for compaction tests, optionally
// with a config store and auto-compaction disabled.
func newCompactionAgent(env fakeEnv, model fantasy.LanguageModel, cfg *config.ConfigStore, disableAutoSummarize bool, tools ...fantasy.AgentTool) *sessionAgent {
	gitCfg := catwalk.Model{ContextWindow: 200_000, DefaultMaxTokens: 32_000}
	m := Model{Model: model, CatwalkCfg: gitCfg}
	return NewSessionAgent(SessionAgentOptions{
		Models:               func() ModelSet { return ModelSet{Large: m, Small: m} },
		SystemPrompt:         "SYSTEM PROMPT",
		Sessions:             env.sessions,
		Messages:             env.messages,
		Cfg:                  cfg,
		DisableAutoSummarize: disableAutoSummarize,
		Tools:                tools,
	}).(*sessionAgent)
}

func textMessage(role message.MessageRole, text string) message.Message {
	return message.Message{Role: role, Parts: []message.ContentPart{message.TextContent{Text: text}}}
}

func toolCallMessage(id, name string) message.Message {
	m := message.Message{Role: message.Assistant}
	m.AddToolCall(message.ToolCall{ID: id, Name: name, Input: "{}", Finished: true})
	return m
}

func toolResultMessage(id, name string) message.Message {
	m := message.Message{Role: message.Tool}
	m.AddToolResult(message.ToolResult{ToolCallID: id, Name: name, Content: "result"})
	return m
}

// bigText returns text whose estimated token count is exactly n (four
// characters per token), for budget-sensitive tests.
func bigText(n int64) string { return strings.Repeat("x", int(n)*4) }

func TestSelectCompactionTail(t *testing.T) {
	t.Parallel()

	t.Run("zero budget keeps nothing", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{textMessage(message.User, "hi"), textMessage(message.Assistant, "yo")}
		require.Equal(t, len(msgs), selectCompactionTail(msgs, 0))
	})

	t.Run("budget covering everything keeps everything", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{textMessage(message.User, bigText(100)), textMessage(message.Assistant, bigText(100))}
		require.Equal(t, 0, selectCompactionTail(msgs, 1_000))
	})

	t.Run("keeps the largest suffix within budget", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{
			textMessage(message.User, bigText(100)),
			textMessage(message.Assistant, bigText(100)),
			textMessage(message.User, bigText(50)),
		}
		// 50 fits, adding the 100-token assistant exceeds 120.
		require.Equal(t, 2, selectCompactionTail(msgs, 120))
		// Exactly 150 fits both trailing messages.
		require.Equal(t, 1, selectCompactionTail(msgs, 150))
	})

	t.Run("never cuts between a tool call and its result", func(t *testing.T) {
		t.Parallel()
		call := message.Message{Role: message.Assistant}
		call.AddToolCall(message.ToolCall{ID: "c1", Name: "bash", Input: bigText(10), Finished: true})
		result := message.Message{Role: message.Tool}
		result.AddToolResult(message.ToolResult{ToolCallID: "c1", Name: "bash", Content: bigText(10)})
		msgs := []message.Message{
			textMessage(message.User, bigText(100)),
			call,
			result,
			textMessage(message.Assistant, bigText(1)),
		}
		// The budget fits the trailing text plus the small tool exchange
		// around it: the cut keeps the call and its result together.
		require.Equal(t, 1, selectCompactionTail(msgs, 60))
		// With a budget that fits only the trailing text and the result,
		// the cut must move past the tool result instead of keeping an
		// orphaned result without its call.
		require.Equal(t, 3, selectCompactionTail(msgs, 21))
	})

	t.Run("never keeps a tool result without its call", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{
			toolCallMessage("c1", "bash"),
			toolResultMessage("c1", "bash"),
		}
		// Budget fits only the result; the cut must move past the call.
		require.Equal(t, 0, selectCompactionTail(msgs, 10))
	})

	t.Run("never keeps a previous summary in the tail", func(t *testing.T) {
		t.Parallel()
		prev := message.Message{Role: message.User, IsSummaryMessage: true, Parts: []message.ContentPart{message.TextContent{Text: bigText(50)}}}
		msgs := []message.Message{prev, textMessage(message.User, bigText(50))}
		// Budget covers both messages, but the old summary gets absorbed
		// into the new one, so the tail starts after it.
		require.Equal(t, 1, selectCompactionTail(msgs, 1_000))
	})

	t.Run("nothing fits when the last message exceeds the budget", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{textMessage(message.User, bigText(1000))}
		require.Equal(t, 1, selectCompactionTail(msgs, 100))
	})
}

func TestMessageTokens(t *testing.T) {
	t.Parallel()

	m := textMessage(message.User, bigText(100))
	m.AddToolCall(message.ToolCall{Name: "bash", Input: `{"cmd":"ls"}`, Finished: true})
	m.AddToolResult(message.ToolResult{ToolCallID: "c1", Name: "bash", Content: "output"})
	require.GreaterOrEqual(t, messageTokens(m), int64(100))
}

func TestCompactionGuardTool(t *testing.T) {
	t.Parallel()

	inner := &recordingTool{name: "alpha"}
	guard := compactionGuardTool{inner: inner}

	require.Equal(t, "alpha", guard.Info().Name)

	resp, err := guard.Run(t.Context(), fantasy.ToolCall{Name: "alpha", Input: "{}"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "disabled")

	// Provider options must flow through to the wrapped tool so the cache
	// breakpoint placed on the last tool still serializes identically.
	opts := fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
	}
	guard.SetProviderOptions(opts)
	require.Equal(t, opts, inner.opts)
	require.Equal(t, opts, guard.ProviderOptions())
}

func TestBuildCompactionPrompt(t *testing.T) {
	t.Parallel()

	t.Run("first compaction", func(t *testing.T) {
		t.Parallel()
		prompt := buildCompactionPrompt(session.Session{}, 12, 40)
		require.Contains(t, prompt, "Compact the conversation above")
		require.Contains(t, prompt, "Preserved Tail")
		require.Contains(t, prompt, "The most recent 12 of the conversation's 40 messages")
		require.NotContains(t, prompt, "Previous Summary")
		require.NotContains(t, prompt, "Current Todo List")
	})

	t.Run("incremental compaction carries the previous summary note", func(t *testing.T) {
		t.Parallel()
		prompt := buildCompactionPrompt(session.Session{SummaryMessageID: "s1"}, 4, 10)
		require.Contains(t, prompt, "Previous Summary")
		require.Contains(t, prompt, "preserve everything in it")
	})

	t.Run("todos are carried over", func(t *testing.T) {
		t.Parallel()
		prompt := buildCompactionPrompt(session.Session{Todos: []session.Todo{{Content: "fix the bug", Status: session.TodoStatusInProgress}}}, 0, 5)
		require.Contains(t, prompt, "Current Todo List")
		require.Contains(t, prompt, "fix the bug")
	})
}

func TestShouldAutoCompact(t *testing.T) {
	env := testEnv(t)

	t.Run("default threshold fires at 50 percent", func(t *testing.T) {
		agent := newCompactionAgent(env, nil, nil, false)
		model := agent.largeModel.Get()
		require.False(t, agent.shouldAutoCompact(model, session.Session{PromptTokens: 99_999}))
		require.True(t, agent.shouldAutoCompact(model, session.Session{PromptTokens: 100_000}))
		require.True(t, agent.shouldAutoCompact(model, session.Session{PromptTokens: 50_000, CompletionTokens: 50_000}))
	})

	t.Run("configured threshold is honored", func(t *testing.T) {
		cfg, err := config.Init(t.Context(), env.workingDir, "", false)
		require.NoError(t, err)
		half := 0.5
		cfg.Config().Options.AutoCompactThreshold = &half

		agent := newCompactionAgent(env, nil, cfg, false)
		model := agent.largeModel.Get()
		require.False(t, agent.shouldAutoCompact(model, session.Session{PromptTokens: 99_999}))
		require.True(t, agent.shouldAutoCompact(model, session.Session{PromptTokens: 100_000}))
	})

	t.Run("zero threshold disables auto-compaction", func(t *testing.T) {
		cfg, err := config.Init(t.Context(), env.workingDir, "", false)
		require.NoError(t, err)
		off := 0.0
		cfg.Config().Options.AutoCompactThreshold = &off

		agent := newCompactionAgent(env, nil, cfg, false)
		require.False(t, agent.shouldAutoCompact(agent.largeModel.Get(), session.Session{PromptTokens: 1_000_000}))
	})

	t.Run("disabled auto summarize wins", func(t *testing.T) {
		agent := newCompactionAgent(env, nil, nil, true)
		require.False(t, agent.shouldAutoCompact(agent.largeModel.Get(), session.Session{PromptTokens: 1_000_000}))
	})

	t.Run("unknown context window never fires", func(t *testing.T) {
		agent := newCompactionAgent(env, nil, nil, false)
		model := Model{CatwalkCfg: catwalk.Model{}}
		require.False(t, agent.shouldAutoCompact(model, session.Session{PromptTokens: 1_000_000}))
	})
}

// TestAutoCompact_DefaultThresholdAfterTurn is the end-to-end counterpart of
// TestShouldAutoCompact: with no threshold configured, a finished turn that
// leaves the session above half the window must compact, so the default
// really does run at 50% and not only when the unit-level predicate is
// called.
func TestAutoCompact_DefaultThresholdAfterTurn(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{
		summaryText: "# Conversation Summary\n\nAll good.",
		usage:       fantasy.Usage{InputTokens: 150_000, OutputTokens: 100, TotalTokens: 150_100},
	}
	agent := newCompactionAgent(env, stub, nil, false)

	sess, err := env.sessions.Create(t.Context(), "Auto compact")
	require.NoError(t, err)

	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID, "a turn at 75%% of the window should auto-compact")
}

func TestCompactionTailBudget(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, nil, nil, false)
	model := agent.largeModel.Get()

	t.Run("default budget applies for a large context", func(t *testing.T) {
		msgs := []message.Message{textMessage(message.User, bigText(100_000))}
		require.Equal(t, int64(config.DefaultCompactTailTokens), agent.compactionTailBudget(model, msgs))
	})

	t.Run("never exceeds half of the compacted context", func(t *testing.T) {
		msgs := []message.Message{textMessage(message.User, bigText(2_000))}
		require.Equal(t, int64(1_000), agent.compactionTailBudget(model, msgs))
	})

	t.Run("zero disables the tail", func(t *testing.T) {
		cfg, err := config.Init(t.Context(), env.workingDir, "", false)
		require.NoError(t, err)
		zero := 0
		cfg.Config().Options.CompactTailTokens = &zero
		agent := newCompactionAgent(env, nil, cfg, false)
		msgs := []message.Message{textMessage(message.User, bigText(1_000))}
		require.Zero(t, agent.compactionTailBudget(agent.largeModel.Get(), msgs))
	})
}

func TestCompactionMaxOutputTokens(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(defaultCompactionMaxOutputTokens), compactionMaxOutputTokens(Model{
		CatwalkCfg: catwalk.Model{ContextWindow: 200_000, DefaultMaxTokens: 32_000},
	}))
	// A model with a lower output cap wins.
	require.Equal(t, int64(4_000), compactionMaxOutputTokens(Model{
		CatwalkCfg: catwalk.Model{ContextWindow: 200_000, DefaultMaxTokens: 4_000},
	}))
	// Small windows scale the cap down.
	require.Equal(t, int64(2_000), compactionMaxOutputTokens(Model{
		CatwalkCfg: catwalk.Model{ContextWindow: 8_000, DefaultMaxTokens: 4_000},
	}))
}

// TestSummarize_RequestKeepsCachePrefix pins the core cache guarantee: the
// compaction request must re-enter through the live system prompt, the live
// tool palette (with the cache breakpoint on the last tool), and the live
// conversation, so the provider serves the whole prefix from cache and only
// the instruction and summary are new tokens.
func TestSummarize_RequestKeepsCachePrefix(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}
	agent := newCompactionAgent(env, stub, nil, false, &recordingTool{name: "alpha"}, &recordingTool{name: "beta"})

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: bigText(100)}},
	})
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.Assistant,
		Parts: []message.ContentPart{message.TextContent{Text: bigText(100)}},
	})
	require.NoError(t, err)

	require.NoError(t, agent.Summarize(ctx, sess.ID, nil, nil))

	require.Len(t, stub.calls, 1)
	call := stub.calls[0]

	// The request opens with the live system prompt.
	require.NotEmpty(t, call.Prompt)
	require.Equal(t, fantasy.MessageRoleSystem, call.Prompt[0].Role)
	sys, ok := fantasy.AsMessagePart[fantasy.TextPart](call.Prompt[0].Content[0])
	require.True(t, ok)
	require.Equal(t, "SYSTEM PROMPT", sys.Text)

	// The live tool palette is present, with the cache marker on the
	// last tool.
	require.Len(t, call.Tools, 2)
	require.Equal(t, "alpha", call.Tools[0].GetName())
	require.Equal(t, "beta", call.Tools[1].GetName())
	lastTool, ok := call.Tools[1].(fantasy.FunctionTool)
	require.True(t, ok)
	require.NotEmpty(t, lastTool.ProviderOptions, "the cache marker must sit on the last tool")

	// The instruction is appended after the conversation as the final
	// user message.
	last := call.Prompt[len(call.Prompt)-1]
	require.Equal(t, fantasy.MessageRoleUser, last.Role)
	instruction, ok := fantasy.AsMessagePart[fantasy.TextPart](last.Content[0])
	require.True(t, ok)
	require.Contains(t, instruction.Text, "Compact the conversation above")

	// The summary is bounded.
	require.NotNil(t, call.MaxOutputTokens)
	require.Equal(t, int64(defaultCompactionMaxOutputTokens), *call.MaxOutputTokens)

	// The session now points at the summary and the verbatim tail.
	updated, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID)
	require.NotEmpty(t, updated.SummaryCutMessageID)
	require.Zero(t, updated.PromptTokens)

	summary, err := env.messages.Get(ctx, updated.SummaryMessageID)
	require.NoError(t, err)
	require.True(t, summary.IsSummaryMessage)
	require.Contains(t, summary.Content().Text, "Conversation Summary")
}

// TestSummarize_EmptySummaryLeavesSessionIntact guards against a model that
// refuses to write a summary (e.g. it keeps trying to call tools): the
// session must keep its old context rather than collapsing to nothing.
func TestSummarize_EmptySummaryLeavesSessionIntact(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: ""}
	agent := newCompactionAgent(env, stub, nil, false)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
	})
	require.NoError(t, err)

	err = agent.Summarize(ctx, sess.ID, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty summary")

	unchanged, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	require.Empty(t, unchanged.SummaryMessageID)
	require.Empty(t, unchanged.SummaryCutMessageID)
}

// TestGetSessionMessages_TailReconstruction covers the read path: a compacted
// session sends the summary (role-flipped to user), then the verbatim tail the
// summary was written around, then everything newer.
func TestGetSessionMessages_TailReconstruction(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, nil, nil, false)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	create := func(role message.MessageRole, text string) message.Message {
		m, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role:  role,
			Parts: []message.ContentPart{message.TextContent{Text: text}},
		})
		require.NoError(t, err)
		return m
	}

	first := create(message.User, "first")
	cut := create(message.Assistant, "second")
	summary, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:             message.Assistant,
		IsSummaryMessage: true,
		Parts:            []message.ContentPart{message.TextContent{Text: "# Conversation Summary"}},
	})
	require.NoError(t, err)
	newer := create(message.User, "after compaction")

	sess.SummaryMessageID = summary.ID
	sess.SummaryCutMessageID = cut.ID
	_, err = env.sessions.Save(ctx, sess)
	require.NoError(t, err)

	got, err := agent.getSessionMessages(ctx, sess)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, summary.ID, got[0].ID)
	require.Equal(t, message.User, got[0].Role, "the summary is replayed as a user message")
	require.Equal(t, cut.ID, got[1].ID)
	require.Equal(t, newer.ID, got[2].ID)
	require.NotEqual(t, first.ID, got[1].ID, "messages before the cut are summarized away")

	// Legacy sessions compacted before cut points existed keep the
	// summary-only view.
	sess.SummaryCutMessageID = ""
	_, err = env.sessions.Save(ctx, sess)
	require.NoError(t, err)
	got, err = agent.getSessionMessages(ctx, sess)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, summary.ID, got[0].ID)
	require.Equal(t, newer.ID, got[1].ID)
}

// TestSummarize_AttachesCompactionStats pins the display contract: the
// measured outcome rides on the summary message itself, so every surface
// that renders the message (live chat, transcript reload, remote clients)
// can show the stats line without new event plumbing.
func TestSummarize_AttachesCompactionStats(t *testing.T) {
	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}
	agent := newCompactionAgent(env, stub, nil, false)

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "test")
	require.NoError(t, err)

	for range 4 {
		_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: bigText(100)}},
		})
		require.NoError(t, err)
	}

	// A real session carries its last turn's usage counters; Summarize
	// reads them for tokens_before. The harness never ran a live turn, so
	// set them the way Run would have left them: 400 prompt tokens.
	sess.PromptTokens = 400
	_, err = env.sessions.Save(ctx, sess)
	require.NoError(t, err)

	err = agent.Summarize(ctx, sess.ID, nil, nil)
	require.NoError(t, err)

	updated, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	summary, err := env.messages.Get(ctx, updated.SummaryMessageID)
	require.NoError(t, err)

	stats, ok := summary.CompactionStatsPart()
	require.True(t, ok, "summary message must carry compaction stats")
	// The stub reports 100 output tokens. The tail budget clamps to half
	// of the compacted context (200 tokens), so the last two of the four
	// 100-token messages are kept verbatim.
	require.Equal(t, int64(400), stats.TokensBefore)
	require.Equal(t, int64(100), stats.SummaryTokens)
	require.Equal(t, int64(200), stats.TailTokens)
	require.Equal(t, int64(100), stats.SavedTokens)
	require.Equal(t, 2, stats.KeptMessages)
}
