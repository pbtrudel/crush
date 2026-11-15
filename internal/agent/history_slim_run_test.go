package agent

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/db"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// movedReadContent is long enough that replacing it is a clear win and short
// enough that the pre-compaction pruner leaves the result alone.
var movedReadContent = "contents of a.go as it was before the edit " + strings.Repeat("x", 2_000)

// coldHistoryEnv builds its own session and message services on a connection
// the test can drive, because backdating a stored message has no API: the
// store stamps created_at itself and UpdateMessage never writes it. Without a
// backdated message no test can produce the idle gap slimming depends on.
func coldHistoryEnv(t *testing.T) (fakeEnv, *sql.DB) {
	t.Helper()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	q := db.New(conn)
	workingDir := t.TempDir()
	permissions := permission.NewPermissionService(workingDir, []string{}, nil)
	permissions.SetPermissionMode(permission.PermissionModeSysadmin)
	fileTracker := filetracker.NewService(q)

	return fakeEnv{
		workingDir:  workingDir,
		sessions:    session.NewService(q, conn),
		messages:    message.NewService(q),
		permissions: permissions,
		history:     history.NewService(q, conn),
		filetracker: &fileTracker,
		lspClients:  csync.NewMap[string, *lsp.Client](),
	}, conn
}

// createMessage stores one message and returns it.
func createMessage(t *testing.T, env fakeEnv, sessionID string, params message.CreateMessageParams) message.Message {
	t.Helper()
	msg, err := env.messages.Create(t.Context(), sessionID, params)
	require.NoError(t, err)
	return msg
}

// seedColdHistory stores a read of a file followed by an edit of the same
// file, all backdated two hours so the next prompt sits behind a long idle
// gap. It returns the session.
func seedColdHistory(t *testing.T, env fakeEnv, conn *sql.DB) (session.Session, int64) {
	t.Helper()

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "cold")
	require.NoError(t, err)

	seeded := []message.Message{
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "add the missing case"}},
		}),
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role: message.Assistant,
			Parts: []message.ContentPart{message.ToolCall{
				ID: "v1", Name: tools.ViewToolName, Input: `{"file_path":"/w/a.go"}`, Finished: true,
			}},
		}),
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{
				ToolCallID: "v1", Name: tools.ViewToolName, Content: movedReadContent,
			}},
		}),
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role: message.Assistant,
			Parts: []message.ContentPart{message.ToolCall{
				ID: "e1", Name: tools.EditToolName, Input: `{"file_path":"/w/a.go"}`, Finished: true,
			}},
		}),
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{
				ToolCallID: "e1", Name: tools.EditToolName, Content: "ok",
			}},
		}),
	}

	// Two hours before now, one second apart, so the gap to the prompt the
	// test sends is unambiguous.
	base := time.Now().Add(-2 * time.Hour).Unix()
	for i, msg := range seeded {
		setCreatedAt(t, conn, msg.ID, base+int64(i))
	}
	return sess, base
}

func setCreatedAt(t *testing.T, conn *sql.DB, messageID string, at int64) {
	t.Helper()
	_, err := conn.ExecContext(t.Context(), `UPDATE messages SET created_at = ? WHERE id = ?`, at, messageID)
	require.NoError(t, err)
}

// promptText renders every text and tool-result text of a recorded prompt, so
// a test can search what the model was actually sent.
func promptWithResults(prompt fantasy.Prompt) string {
	var sb strings.Builder
	for _, msg := range prompt {
		for _, part := range msg.Content {
			if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
				sb.WriteString(text.Text)
				sb.WriteString("\n")
			}
			if result, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](part); ok {
				if output, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentText](result.Output); ok {
					sb.WriteString(output.Text)
					sb.WriteString("\n")
				}
				if failed, ok := fantasy.AsToolResultOutputType[fantasy.ToolResultOutputContentError](result.Output); ok && failed.Error != nil {
					sb.WriteString(failed.Error.Error())
					sb.WriteString("\n")
				}
			}
		}
	}
	return sb.String()
}

// TestRunStubsAReadAfterALongIdleGap is the behavioural guard for the wiring,
// not the helpers: a session resumed after hours must send the note in place
// of the stale read.
func TestRunStubsAReadAfterALongIdleGap(t *testing.T) {
	env, conn := coldHistoryEnv(t)
	sess, _ := seedColdHistory(t, env, conn)

	model := &stepRecordingModel{toolSteps: 1}
	agent := testSessionAgent(env, model, model, "system", &probeTool{output: "ok"})

	_, err := agent.Run(t.Context(), SessionAgentCall{Prompt: "carry on", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.NotEmpty(t, model.prompts)

	sent := promptWithResults(model.prompts[0])
	require.Contains(t, sent, stubText("/w/a.go"), "the request after the gap must carry the note")
	require.NotContains(t, sent, movedReadContent, "the stale contents must not be sent")
}

// TestSummarizeStubsAReadAfterALongIdleGap covers the compaction path, which
// must send the same bytes the turn sent.
func TestSummarizeStubsAReadAfterALongIdleGap(t *testing.T) {
	env, conn := coldHistoryEnv(t)
	sess, base := seedColdHistory(t, env, conn)

	// Compaction runs after a turn, so the log already ends with the prompt
	// whose arrival closed the gap. That user message is what coldCut finds.
	resumed := createMessage(t, env, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "carry on"}},
	})
	// Two hours after the seeded messages, so the log now reads exactly as it
	// does when a turn ends: old work, then the prompt that was just typed.
	setCreatedAt(t, conn, resumed.ID, base+2*60*60)

	stub := &stubSummaryModel{summaryText: "# Conversation Summary\n\nAll good."}
	agent := newCompactionAgent(env, stub, nil, false)

	require.NoError(t, agent.Summarize(t.Context(), sess.ID, nil, nil))
	require.Len(t, stub.calls, 1)

	sent := promptWithResults(stub.calls[0].Prompt)
	require.Contains(t, sent, stubText("/w/a.go"), "the compaction request must carry the note too")
	require.NotContains(t, sent, movedReadContent)
}

// compactionTalkingModel records every prompt it is sent and answers a
// compaction instruction with a summary, so one run can compact and the next
// run can be inspected.
type compactionTalkingModel struct {
	fakeLanguageModel
	mu      sync.Mutex
	prompts []fantasy.Prompt
}

func (m *compactionTalkingModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	m.prompts = append(m.prompts, slices.Clone(call.Prompt))
	m.mu.Unlock()

	text := ""
	if strings.Contains(promptWithResults(call.Prompt), "Compact the conversation above") {
		text = "# Conversation Summary\n\nAll good."
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if text != "" {
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "t1"}) {
				return
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "t1", Delta: text}) {
				return
			}
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "t1"})
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

// seedTailStaleRead stores a history whose stale read ends up in the tail a
// compaction keeps verbatim: a large message early makes the tail budget big
// enough to hold the read, the edit that supersedes it, and nothing more.
func seedTailStaleRead(t *testing.T, env fakeEnv, conn *sql.DB) session.Session {
	t.Helper()

	ctx := t.Context()
	sess, err := env.sessions.Create(ctx, "tail")
	require.NoError(t, err)

	seeded := []message.Message{
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "add the missing case"}},
		}),
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: strings.Repeat("f", 44_000)}},
		}),
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role: message.Assistant,
			Parts: []message.ContentPart{message.ToolCall{
				ID: "v1", Name: tools.ViewToolName, Input: `{"file_path":"/w/a.go"}`, Finished: true,
			}},
		}),
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{
				ToolCallID: "v1", Name: tools.ViewToolName, Content: movedReadContent,
			}},
		}),
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role: message.Assistant,
			Parts: []message.ContentPart{message.ToolCall{
				ID: "e1", Name: tools.EditToolName, Input: `{"file_path":"/w/a.go"}`, Finished: true,
			}},
		}),
		createMessage(t, env, sess.ID, message.CreateMessageParams{
			Role: message.Tool,
			Parts: []message.ContentPart{message.ToolResult{
				ToolCallID: "e1", Name: tools.EditToolName, Content: "ok",
			}},
		}),
	}

	base := time.Now().Add(-2 * time.Hour).Unix()
	for i, msg := range seeded {
		setCreatedAt(t, conn, msg.ID, base+int64(i))
	}
	return sess
}

// TestSummarizeStubsWithoutAPromptInTheLog covers the compaction that runs
// before the next prompt is stored, which is what preflight does: the only
// idle gap is the one the coming prompt would close, so a compaction that
// measured itself against nothing would send the stale read while the turn
// that follows sends the note. Both requests must agree.
func TestSummarizeStubsWithoutAPromptInTheLog(t *testing.T) {
	env, conn := coldHistoryEnv(t)
	sess := seedTailStaleRead(t, env, conn)

	model := &compactionTalkingModel{}
	agent := newWindowAgent(env, model, 200_000)

	require.NoError(t, agent.Summarize(t.Context(), sess.ID, nil, nil))
	require.Len(t, model.prompts, 1)

	compaction := promptWithResults(model.prompts[0])
	require.Contains(t, compaction, "Compact the conversation above")
	require.Contains(t, compaction, stubText("/w/a.go"), "the compaction must carry the note")
	require.NotContains(t, compaction, movedReadContent, "the stale contents must not be sent")

	// The turn that follows rebuilds from the summary and the retained tail.
	_, err := agent.Run(t.Context(), SessionAgentCall{Prompt: "finish it", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.Len(t, model.prompts, 2)

	next := promptWithResults(model.prompts[1])
	require.Contains(t, next, stubText("/w/a.go"), "the next turn must send the note too")
	require.NotContains(t, next, movedReadContent, "the stale contents must not come back")
}

// TestCompactionRecordsPrefixAttribution covers the usage record a compaction
// writes: it is the session's second request, so it must say how much of the
// first one's prompt it reused, and its numbers must cover every step the
// model needed, including a refused tool call.
func TestCompactionRecordsPrefixAttribution(t *testing.T) {
	env := testEnv(t)
	ctx := t.Context()

	refused := fantasy.Usage{InputTokens: 1_000, CacheCreationTokens: 4_000, OutputTokens: 50, TotalTokens: 5_050}
	summary := fantasy.Usage{InputTokens: 200, CacheReadTokens: 9_000, OutputTokens: 150, TotalTokens: 9_350}
	model := &twoStepSummaryModel{usages: []fantasy.Usage{refused, summary}}
	agent := newCompactionAgent(env, model, nil, false, &probeTool{output: "must not run"})

	sess, err := env.sessions.Create(ctx, "attribution")
	require.NoError(t, err)
	_, err = env.messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
	})
	require.NoError(t, err)

	// The turn's own request first, so the compaction has something to
	// compare its prefix with.
	_, err = agent.Run(ctx, SessionAgentCall{Prompt: "go", SessionID: sess.ID, MaxOutputTokens: 1000})
	require.NoError(t, err)

	// The model's script is per request sequence, and the turn already spent
	// it, so restart it for the compaction.
	model.resetSteps()
	require.NoError(t, agent.Summarize(ctx, sess.ID, nil, nil))
	require.NoError(t, env.messages.FlushAll(ctx))

	compacted, err := env.sessions.Get(ctx, sess.ID)
	require.NoError(t, err)
	stored, err := env.messages.Get(ctx, compacted.SummaryMessageID)
	require.NoError(t, err)

	usage := finishUsage(t, stored)
	require.NotNil(t, usage)
	require.Positive(t, usage.PromptMessages, "the record must count the messages the compaction carried")
	require.Positive(t, usage.PreviousMessages, "the turn's request is the previous one")
	require.Positive(t, usage.SharedMessages, "the compaction reuses the turn's prefix")
	require.False(t, usage.ToolsChanged, "the tool list did not change")

	require.Equal(t, int64(1_200), usage.InputTokens, "input tokens of both steps")
	require.Equal(t, int64(9_000), usage.CacheReadTokens)
	require.Equal(t, int64(4_000), usage.CacheWriteTokens)
	require.Equal(t, int64(200), usage.OutputTokens, "output tokens of both steps")
}
