package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// compactionStateAgent builds the agent the state tests compact with: a
// config store (so the block carries an environment snapshot), a pinned
// clock, and the fake environment's session and message stores.
func compactionStateAgent(t *testing.T, env fakeEnv, model fantasy.LanguageModel) *sessionAgent {
	t.Helper()
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	m := Model{Model: model, CatwalkCfg: catwalk.Model{ContextWindow: 200_000, DefaultMaxTokens: 32_000}}
	sa := NewSessionAgent(SessionAgentOptions{
		Models:       func() ModelSet { return ModelSet{Large: m, Small: m} },
		SystemPrompt: "SYSTEM PROMPT",
		Sessions:     env.sessions,
		Messages:     env.messages,
		History:      env.history,
		Cfg:          cfg,
		Now:          fixedSnapshotClock(),
		titles:       env.titles,
	}).(*sessionAgent)
	return sa
}

// skillResultMetadata is the metadata the view tool stores for a skill
// file (tools.ViewResponseMetadata).
func skillResultMetadata(name string) string {
	return fmt.Sprintf(`{"file_path":"/skills/%s/SKILL.md","resource_type":"skill","resource_name":%q}`, name, name)
}

// compactionStateSeed writes a session whose early history read a skill,
// followed by enough conversation that a compaction keeps only the tail.
// It returns the session id.
func compactionStateSeed(t *testing.T, env fakeEnv, skillNames ...string) string {
	t.Helper()
	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "state")
	require.NoError(t, err)
	require.NoError(t, env.sessions.UpdateTodos(ctx, sess.ID, []session.Todo{
		{Content: "Write the reasoning helpers", Status: session.TodoStatusCompleted},
		{Content: "Wire the state block", Status: session.TodoStatusInProgress},
	}))

	create := func(role message.MessageRole, parts ...message.ContentPart) {
		t.Helper()
		_, err := env.messages.Create(ctx, sess.ID, message.CreateMessageParams{Role: role, Parts: parts})
		require.NoError(t, err)
	}
	readSkill := func(callID, name string) {
		t.Helper()
		call := message.Message{Role: message.Assistant}
		call.AddToolCall(message.ToolCall{ID: callID, Name: "view", Input: "{}", Finished: true})
		create(message.Assistant, call.Parts...)
		create(message.Tool, message.ToolResult{
			ToolCallID: callID,
			Name:       "view",
			Content:    "skill instructions",
			Metadata:   skillResultMetadata(name),
		})
	}

	create(message.User, message.TextContent{Text: "Read the skill and start."})
	for i, name := range skillNames {
		readSkill(fmt.Sprintf("call-skill-%d", i+1), name)
	}
	// Fill the tail budget, so the skill reads above land before the cut.
	for range 6 {
		create(message.User, message.TextContent{Text: bigText(5_000)})
		create(message.Assistant, message.TextContent{Text: bigText(5_000)})
	}
	return sess.ID
}

// compactionStateBlock compacts the session and returns the text of the
// state message the next request replays, plus every replayed text in
// order.
func compactionStateBlock(t *testing.T, sa *sessionAgent, env fakeEnv, sessionID string) (string, []string) {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, sa.Summarize(ctx, sessionID, nil, nil))

	sess, err := env.sessions.Get(ctx, sessionID)
	require.NoError(t, err)
	msgs, err := sa.getSessionMessages(ctx, sess)
	require.NoError(t, err)
	prompt, _ := sa.preparePrompt(msgs, false, false)

	texts := make([]string, 0, len(prompt))
	for _, msg := range prompt {
		for _, part := range msg.Content {
			if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	last := texts[len(texts)-1]
	return last, texts
}

// A compaction leaves the working model without the state the summary only
// describes. The summary must be labelled as a summary at replay, and the
// state block must follow the preserved tail with the todo list, a fresh
// environment snapshot, the changed files and the skills it summarized
// away.
func TestCompactionStateFollowsTheSummary(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Summary\n\nRead the skill and started."}
	sa := compactionStateAgent(t, env, stub)
	sessionID := compactionStateSeed(t, env, "s2-common")
	_, err := env.history.Create(t.Context(), sessionID, filepath.Join(env.workingDir, "internal/agent/agent.go"), "package agent\n")
	require.NoError(t, err)

	block, texts := compactionStateBlock(t, sa, env, sessionID)

	// The summary is labelled where it is replayed, and the stored message
	// stays as the summary wrote it.
	var summaryText string
	for _, text := range texts {
		if strings.HasPrefix(text, "<compacted_history>") {
			summaryText = text
		}
	}
	require.NotEmpty(t, summaryText, "the replayed summary must open with the label")
	require.Contains(t, summaryText, "Read the skill and started.")
	require.Contains(t, summaryText, "kept verbatim")
	require.True(t, strings.HasSuffix(summaryText, "</compacted_history>"))

	require.True(t, strings.HasPrefix(block, "<compaction_state>"), "the last replayed message must be the state block, got %q", block)
	require.Contains(t, block, "Write the reasoning helpers")
	require.Contains(t, block, "Wire the state block")
	require.Contains(t, block, "Today's date: 1/1/2025")
	require.Contains(t, block, "internal/agent/agent.go")
	require.Contains(t, block, "s2-common")
	require.Contains(t, block, "Read them again before relying on them")
}

// A skill read again after the cut is still in the model's context, so the
// state block must not send the model back to it.
func TestCompactionStateSkipsSkillsReadAfterTheCut(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Summary"}
	sa := compactionStateAgent(t, env, stub)
	sessionID := compactionStateSeed(t, env, "s2-common")
	_, err := env.messages.Create(t.Context(), sessionID, message.CreateMessageParams{
		Role: message.Tool,
		Parts: []message.ContentPart{message.ToolResult{
			ToolCallID: "call-skill-1",
			Name:       "view",
			Content:    "skill instructions again",
			Metadata:   skillResultMetadata("s2-common"),
		}},
	})
	require.NoError(t, err)

	block, _ := compactionStateBlock(t, sa, env, sessionID)
	require.True(t, strings.HasPrefix(block, "<compaction_state>"))
	require.NotContains(t, block, "s2-common", "a skill the tail still holds needs no reload")
}

// A second compaction must carry a skill the previous block listed and
// nothing has read since: its instructions were summarized away and are no
// longer detectable from the messages the compaction sees.
func TestCompactionStateCarriesSkillsAcrossCompactions(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Summary"}
	sa := compactionStateAgent(t, env, stub)
	sessionID := compactionStateSeed(t, env, "s2-common")

	first, _ := compactionStateBlock(t, sa, env, sessionID)
	require.Contains(t, first, "s2-common")

	// More conversation without a reread, then a second compaction.
	ctx := t.Context()
	for range 6 {
		_, err := env.messages.Create(ctx, sessionID, message.CreateMessageParams{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: bigText(5_000)}},
		})
		require.NoError(t, err)
		_, err = env.messages.Create(ctx, sessionID, message.CreateMessageParams{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: bigText(5_000)}},
		})
		require.NoError(t, err)
	}

	second, _ := compactionStateBlock(t, sa, env, sessionID)
	require.True(t, strings.HasPrefix(second, "<compaction_state>"))
	require.Contains(t, second, "s2-common", "the previous block's skills must be carried forward")
}

// The caps keep a compaction forced by an overflow from refilling the
// window it just cleared.
func TestCompactionStateIsCapped(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Summary"}
	sa := compactionStateAgent(t, env, stub)
	ctx := t.Context()

	todos := make([]session.Todo, 0, 500)
	for i := range 500 {
		todos = append(todos, session.Todo{
			Content: fmt.Sprintf("Todo %03d %s", i, strings.Repeat("x", 190)),
			Status:  session.TodoStatusPending,
		})
	}
	sessionID := compactionStateSeed(t, env, "s2-common")
	require.NoError(t, env.sessions.UpdateTodos(ctx, sessionID, todos))
	for i := range 500 {
		_, err := env.history.Create(ctx, sessionID, filepath.Join(env.workingDir, fmt.Sprintf("docs/changed-%03d.md", i)), "content\n")
		require.NoError(t, err)
	}

	block, _ := compactionStateBlock(t, sa, env, sessionID)
	require.LessOrEqual(t, len(block), 4_000, "the state block must stay within its byte cap")
	require.Regexp(t, `and \d+ more`, block)
	require.GreaterOrEqual(t, strings.Count(block, " more"), 2,
		"both the todo list and the file list must say how many entries they left out")
}

// Sub-agents and teammates never get an environment snapshot, so their
// state block carries none either.
func TestCompactionStateSubAgentHasNoSnapshot(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	stub := &stubSummaryModel{summaryText: "# Summary"}
	sa := compactionStateAgent(t, env, stub)
	sa.isSubAgent = true
	sessionID := compactionStateSeed(t, env, "s2-common")

	block, _ := compactionStateBlock(t, sa, env, sessionID)
	require.True(t, strings.HasPrefix(block, "<compaction_state>"))
	require.NotContains(t, block, "Today's date")
	require.NotContains(t, block, "<env_snapshot>")
}

// The whole-block cap holds even when the lists fit their own caps: the
// renderer shrinks them until the block fits, and every list it cut says
// how many entries it left out.
func TestRenderCompactionStateShrinksToTheByteCap(t *testing.T) {
	t.Parallel()

	todos := make([]session.Todo, 0, 50)
	for i := range 50 {
		todos = append(todos, session.Todo{
			Content: fmt.Sprintf("Todo %03d %s", i, strings.Repeat("y", 199)),
			Status:  session.TodoStatusPending,
		})
	}
	files := make([]string, 0, 50)
	for i := range 50 {
		files = append(files, fmt.Sprintf("internal/agent/changed-%03d.go", i))
	}

	block, listed := renderCompactionState(todos, "", files, []string{"s2-common"})
	require.LessOrEqual(t, len(block), 4_000)
	require.Regexp(t, `and \d+ more`, block)
	require.Contains(t, block, "s2-common", "the shortest list survives the shrink")
	require.Equal(t, []string{"s2-common"}, listed)

	// A session with nothing to repeat gets no block at all.
	empty, listed := renderCompactionState(nil, "", nil, nil)
	require.Empty(t, empty)
	require.Empty(t, listed)
}
