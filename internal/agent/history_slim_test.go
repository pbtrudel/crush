package agent

import (
	"strings"
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy/providers/openai"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/claudeagent"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

const testIdle = time.Hour

func userAt(seconds int64, text string) message.Message {
	return message.Message{Role: message.User, CreatedAt: seconds, Parts: []message.ContentPart{
		message.TextContent{Text: text},
	}}
}

func assistantAt(seconds int64, text string) message.Message {
	return message.Message{Role: message.Assistant, CreatedAt: seconds, Parts: []message.ContentPart{
		message.TextContent{Text: text},
	}}
}

// callAt builds an assistant message carrying one tool call.
func callAt(seconds int64, id, name, input string) message.Message {
	msg := message.Message{Role: message.Assistant, CreatedAt: seconds}
	msg.AddToolCall(message.ToolCall{ID: id, Name: name, Input: input, Finished: true})
	return msg
}

// resultAt builds a tool message carrying one tool result.
func resultAt(seconds int64, id, name, content string) message.Message {
	msg := message.Message{Role: message.Tool, CreatedAt: seconds}
	msg.AddToolResult(message.ToolResult{ToolCallID: id, Name: name, Content: content})
	return msg
}

func TestColdCut(t *testing.T) {
	t.Parallel()

	t.Run("no gap", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(0, "a"), assistantAt(10, "b"), userAt(20, "c")}
		require.Zero(t, coldCut(msgs, 0, testIdle))
	})

	t.Run("a gap before a user message is the cut", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(0, "a"), assistantAt(1_000, "b"), userAt(4_600, "c")}
		require.Equal(t, 2, coldCut(msgs, 0, testIdle))
	})

	t.Run("one second short is no gap", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(0, "a"), assistantAt(400, "b"), userAt(3_999, "c")}
		require.Zero(t, coldCut(msgs, 0, testIdle))
	})

	t.Run("two gaps take the later one", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{
			userAt(0, "a"), assistantAt(3_700, "b"),
			userAt(7_400, "c"), assistantAt(11_200, "d"),
		}
		require.Equal(t, 2, coldCut(msgs, 0, testIdle))
	})

	t.Run("the next prompt can be the cut", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(0, "a"), assistantAt(10, "b"), userAt(20, "c")}
		require.Equal(t, len(msgs), coldCut(msgs, 20+3_600, testIdle))
	})

	t.Run("a zero next prompt ignores the trailing gap", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(0, "a"), assistantAt(10, "b")}
		require.Zero(t, coldCut(msgs, 0, testIdle))
	})

	t.Run("a summary is never the message before", func(t *testing.T) {
		t.Parallel()
		summary := message.Message{
			Role:             message.User,
			IsSummaryMessage: true,
			CreatedAt:        100,
			Parts:            []message.ContentPart{message.TextContent{Text: "summary"}},
		}
		msgs := []message.Message{
			summary,
			userAt(50, "a"),
			assistantAt(60, "b"),
			userAt(4_000, "c"),
		}
		require.Equal(t, 3, coldCut(msgs, 0, testIdle))
	})

	t.Run("idle of zero disables the cut", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(0, "a"), assistantAt(3_000, "b"), userAt(9_000, "c")}
		require.Zero(t, coldCut(msgs, 0, 0))
	})

	t.Run("a gap before a non-user message is ignored", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(0, "a"), assistantAt(9_000, "b")}
		require.Zero(t, coldCut(msgs, 0, testIdle))
	})
}

// stubText returns the note a stale read is replaced with.
func stubText(path string) string {
	return "[Earlier contents of " + path + " omitted: the file was changed later in this session. View it again for its current contents.]"
}

// stubReadHistory returns a history whose file was read and then edited.
func stubReadHistory(editTool string, editIsError bool) []message.Message {
	return []message.Message{
		userAt(0, "fix the file"),
		callAt(1, "v1", tools.ViewToolName, `{"file_path":"/w/a.go"}`),
		resultAt(1, "v1", tools.ViewToolName, strings.Repeat("old contents ", 100)),
		callAt(2, "e1", editTool, `{"file_path":"/w/a.go"}`),
		{Role: message.Tool, CreatedAt: 2, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: "e1", Name: editTool, Content: "ok", IsError: editIsError},
		}},
	}
}

func TestStubStaleReads(t *testing.T) {
	t.Parallel()

	t.Run("a read the edit superseded is stubbed", func(t *testing.T) {
		t.Parallel()
		msgs := stubReadHistory(tools.EditToolName, false)
		stubStaleReads(msgs)
		require.Equal(t, stubText("/w/a.go"), msgs[2].Parts[0].(message.ToolResult).Content)
	})

	t.Run("a read after the edit is kept", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{
			callAt(1, "e1", tools.EditToolName, `{"file_path":"/w/a.go"}`),
			{Role: message.Tool, CreatedAt: 1, Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "e1", Name: tools.EditToolName, Content: "ok"},
			}},
			callAt(2, "v1", tools.ViewToolName, `{"file_path":"/w/a.go"}`),
			resultAt(2, "v1", tools.ViewToolName, "new contents"),
		}
		stubStaleReads(msgs)
		require.Equal(t, "new contents", msgs[3].Parts[0].(message.ToolResult).Content)
	})

	t.Run("a failed edit leaves the read alone", func(t *testing.T) {
		t.Parallel()
		msgs := stubReadHistory(tools.EditToolName, true)
		before := msgs[2].Parts[0].(message.ToolResult).Content
		stubStaleReads(msgs)
		require.Equal(t, before, msgs[2].Parts[0].(message.ToolResult).Content)
	})

	t.Run("another file is untouched", func(t *testing.T) {
		t.Parallel()
		msgs := stubReadHistory(tools.EditToolName, false)
		msgs[3] = callAt(2, "e1", tools.EditToolName, `{"file_path":"/w/b.go"}`)
		before := msgs[2].Parts[0].(message.ToolResult).Content
		stubStaleReads(msgs)
		require.Equal(t, before, msgs[2].Parts[0].(message.ToolResult).Content)
	})

	t.Run("an image read is untouched", func(t *testing.T) {
		t.Parallel()
		msgs := stubReadHistory(tools.EditToolName, false)
		image := msgs[2].Parts[0].(message.ToolResult)
		image.Data = "cG5n"
		msgs[2].Parts[0] = image
		stubStaleReads(msgs)
		require.Equal(t, image, msgs[2].Parts[0].(message.ToolResult))
	})

	t.Run("an errored read is untouched", func(t *testing.T) {
		t.Parallel()
		msgs := stubReadHistory(tools.EditToolName, false)
		failed := msgs[2].Parts[0].(message.ToolResult)
		failed.IsError = true
		msgs[2].Parts[0] = failed
		stubStaleReads(msgs)
		require.Equal(t, failed, msgs[2].Parts[0].(message.ToolResult))
	})

	t.Run("a read and edit in one message are kept", func(t *testing.T) {
		t.Parallel()
		msg := callAt(1, "v1", tools.ViewToolName, `{"file_path":"/w/a.go"}`)
		msg.AddToolCall(message.ToolCall{ID: "e1", Name: tools.EditToolName, Input: `{"file_path":"/w/a.go"}`, Finished: true})
		msgs := []message.Message{
			msg,
			resultAt(1, "v1", tools.ViewToolName, strings.Repeat("old ", 100)),
			{Role: message.Tool, CreatedAt: 1, Parts: []message.ContentPart{
				message.ToolResult{ToolCallID: "e1", Name: tools.EditToolName, Content: "ok"},
			}},
		}
		before := msgs[1].Parts[0].(message.ToolResult).Content
		stubStaleReads(msgs)
		require.Equal(t, before, msgs[1].Parts[0].(message.ToolResult).Content)
	})

	for _, readTool := range []string{tools.ViewToolName, tools.ReadToolName} {
		t.Run("the "+readTool+" tool is covered", func(t *testing.T) {
			t.Parallel()
			msgs := []message.Message{
				callAt(1, "v1", readTool, `{"file_path":"/w/a.go"}`),
				resultAt(1, "v1", readTool, strings.Repeat("old ", 100)),
				callAt(2, "e1", tools.EditToolName, `{"file_path":"/w/a.go"}`),
				{Role: message.Tool, CreatedAt: 2, Parts: []message.ContentPart{
					message.ToolResult{ToolCallID: "e1", Name: tools.EditToolName, Content: "ok"},
				}},
			}
			stubStaleReads(msgs)
			require.Equal(t, stubText("/w/a.go"), msgs[1].Parts[0].(message.ToolResult).Content)
		})
	}

	for _, editTool := range []string{
		tools.EditToolName,
		tools.MultiEditToolName,
		tools.WriteToolName,
		tools.ReplaceSymbolToolName,
	} {
		t.Run("the "+editTool+" tool is covered", func(t *testing.T) {
			t.Parallel()
			msgs := stubReadHistory(editTool, false)
			stubStaleReads(msgs)
			require.Equal(t, stubText("/w/a.go"), msgs[2].Parts[0].(message.ToolResult).Content)
		})
	}
}

func reasoningParts(msg message.Message) int {
	count := 0
	for _, part := range msg.Parts {
		if _, ok := part.(message.ReasoningContent); ok {
			count++
		}
	}
	return count
}

func reasoningMessage(signature string, responses *openai.ResponsesReasoningMetadata, thoughtSignature string) message.Message {
	msg := message.Message{Role: message.Assistant}
	msg.Parts = append(msg.Parts, message.ReasoningContent{
		Thinking:         "thinking hard",
		Signature:        signature,
		ThoughtSignature: thoughtSignature,
		ResponsesData:    responses,
	})
	msg.Parts = append(msg.Parts, message.TextContent{Text: "answer"})
	return msg
}

func TestDropPlainReasoning(t *testing.T) {
	t.Parallel()

	t.Run("plain reasoning goes and the answer stays", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{reasoningMessage("", nil, "")}
		dropPlainReasoning(msgs)
		require.Zero(t, reasoningParts(msgs[0]))
		require.Equal(t, "answer", msgs[0].Content().Text)
	})

	t.Run("a tool call keeps the message alive", func(t *testing.T) {
		t.Parallel()
		msg := callAt(1, "c1", "bash", "{}")
		msg.Parts = append([]message.ContentPart{message.ReasoningContent{Thinking: "plan"}}, msg.Parts...)
		msgs := []message.Message{msg}
		dropPlainReasoning(msgs)
		require.Zero(t, reasoningParts(msgs[0]))
		require.Len(t, msgs[0].ToolCalls(), 1)
	})

	t.Run("signed reasoning stays", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{reasoningMessage("sig", nil, "")}
		dropPlainReasoning(msgs)
		require.Equal(t, 1, reasoningParts(msgs[0]))
	})

	t.Run("thought signatures stay", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{reasoningMessage("", nil, "thought")}
		dropPlainReasoning(msgs)
		require.Equal(t, 1, reasoningParts(msgs[0]))
	})

	t.Run("responses reasoning stays", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{reasoningMessage("", &openai.ResponsesReasoningMetadata{}, "")}
		dropPlainReasoning(msgs)
		require.Equal(t, 1, reasoningParts(msgs[0]))
	})

	t.Run("a reasoning-only message is untouched", func(t *testing.T) {
		t.Parallel()
		msg := message.Message{Role: message.Assistant, Parts: []message.ContentPart{
			message.ReasoningContent{Thinking: "only thinking"},
		}}
		msgs := []message.Message{msg}
		dropPlainReasoning(msgs)
		require.Equal(t, 1, reasoningParts(msgs[0]))
	})

	t.Run("a summary message is untouched", func(t *testing.T) {
		t.Parallel()
		msg := reasoningMessage("", nil, "")
		msg.IsSummaryMessage = true
		msgs := []message.Message{msg}
		dropPlainReasoning(msgs)
		require.Equal(t, 1, reasoningParts(msgs[0]))
	})
}

// coldHistory returns a history with one long idle gap before its last user
// message, so the read at index 2 and the reasoning at index 5 are cold.
func coldHistory() []message.Message {
	msgs := stubReadHistory(tools.EditToolName, false)
	reasoning := reasoningMessage("", nil, "")
	reasoning.CreatedAt = 3
	return append(msgs, reasoning, userAt(4_000, "and now the next thing"))
}

func TestSlimColdHistory(t *testing.T) {
	t.Parallel()

	t.Run("the cut is the message after the gap", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 6, slimColdHistory(coldHistory(), 0, testIdle, false))
	})

	t.Run("nothing at or after the cut changes", func(t *testing.T) {
		t.Parallel()
		msgs := coldHistory()
		cut := slimColdHistory(msgs, 0, testIdle, true)
		require.Equal(t, 6, cut)
		require.Equal(t, "and now the next thing", msgs[cut].Content().Text)
		require.Equal(t, int64(4_000), msgs[cut].CreatedAt)
	})

	t.Run("a second call rebuilds the same messages", func(t *testing.T) {
		t.Parallel()
		once, twice := coldHistory(), coldHistory()
		slimColdHistory(once, 0, testIdle, true)
		slimColdHistory(twice, 0, testIdle, true)
		require.Equal(t, once, twice, "slimming must be deterministic")
	})

	t.Run("reasoning is kept when the option is off", func(t *testing.T) {
		t.Parallel()
		msgs := coldHistory()
		slimColdHistory(msgs, 0, testIdle, false)
		require.Equal(t, 1, reasoningParts(msgs[5]))
		require.Equal(t, stubText("/w/a.go"), msgs[2].Parts[0].(message.ToolResult).Content)
	})

	t.Run("reasoning is dropped when the option is on", func(t *testing.T) {
		t.Parallel()
		msgs := coldHistory()
		slimColdHistory(msgs, 0, testIdle, true)
		require.Zero(t, reasoningParts(msgs[5]))
		require.Equal(t, "answer", msgs[5].Content().Text)
	})

	t.Run("the turn prompt can close the gap", func(t *testing.T) {
		t.Parallel()
		// This is what Run passes: the creation time of the user message it
		// just stored, which is not in msgs yet.
		msgs := stubReadHistory(tools.EditToolName, false)
		require.Equal(t, len(msgs), slimColdHistory(msgs, 3+3_600, testIdle, false))
		require.Equal(t, stubText("/w/a.go"), msgs[2].Parts[0].(message.ToolResult).Content)
	})

	t.Run("no gap means no change", func(t *testing.T) {
		t.Parallel()
		msgs := coldHistory()
		before := msgs[2].Parts[0].(message.ToolResult).Content
		require.Zero(t, slimColdHistory(msgs, 0, 0, true))
		require.Equal(t, before, msgs[2].Parts[0].(message.ToolResult).Content)
	})
}

// TestSlimHistory_KeepsVendorReasoning covers the families whose API requires
// the reasoning content it produced to be sent back: dropping it from the
// slimmed history would break or degrade their requests.
func TestSlimHistory_KeepsVendorReasoning(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "system").(*sessionAgent)
	agent.cfg = config.NewTestStore(&config.Config{
		Options: &config.Options{HistorySlimReasoning: true},
	})

	slim := func(t *testing.T, id string) []message.Message {
		t.Helper()
		msgs := coldHistory()
		agent.slimHistory(Model{
			Model:      &stubProviderModel{provider: "other"},
			CatwalkCfg: catwalk.Model{ID: id},
		}, msgs, 0)
		return msgs
	}

	for _, id := range []string{"moonshotai/Kimi-K3", "mimo-v2.6-pro", "deepseek-v4.1-flash"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			msgs := slim(t, id)
			require.Equal(t, 1, reasoningParts(msgs[5]), "earlier reasoning must be kept")
			require.Equal(t, stubText("/w/a.go"), msgs[2].Parts[0].(message.ToolResult).Content,
				"the rest of the slimming still applies")
		})
	}

	t.Run("another family still drops it", func(t *testing.T) {
		t.Parallel()
		msgs := slim(t, "glm-5.3")
		require.Zero(t, reasoningParts(msgs[5]))
	})
}

// TestSlimHistory_DeepSeekDefaultUnchanged covers the other half of the
// DeepSeek exemption: history_slim_reasoning is off by default, so with the
// option off DeepSeek's history is slimmed exactly like any other family's.
func TestSlimHistory_DeepSeekDefaultUnchanged(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "system").(*sessionAgent)
	agent.cfg = config.NewTestStore(&config.Config{Options: &config.Options{}})

	deepseek := coldHistory()
	other := coldHistory()
	agent.slimHistory(Model{
		Model:      &stubProviderModel{provider: "other"},
		CatwalkCfg: catwalk.Model{ID: "deepseek-v4.1-flash"},
	}, deepseek, 0)
	agent.slimHistory(Model{
		Model:      &stubProviderModel{provider: "other"},
		CatwalkCfg: catwalk.Model{ID: "glm-5.3"},
	}, other, 0)

	require.Equal(t, other, deepseek)
	require.Equal(t, 1, reasoningParts(deepseek[5]))
	require.Equal(t, stubText("/w/a.go"), deepseek[2].Parts[0].(message.ToolResult).Content)
}

// TestSlimHistory_ProviderGuard covers the provider whose transcript lives on
// its side: a rewritten history is replayed to it as text, which costs more
// than the tokens slimming saves.
func TestSlimHistory_ProviderGuard(t *testing.T) {
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "system").(*sessionAgent)

	claude := Model{CatwalkCfg: catwalk.Model{}, Model: &stubProviderModel{provider: claudeagent.Name}}
	claudeMsgs := coldHistory()
	agent.slimHistory(claude, claudeMsgs, 0)
	require.Equal(t, strings.Repeat("old contents ", 100),
		claudeMsgs[2].Parts[0].(message.ToolResult).Content,
		"claude agent history must not be rewritten")

	// The same history on any other provider is slimmed with the defaults.
	other := Model{CatwalkCfg: catwalk.Model{ContextWindow: 200_000}, Model: &stubProviderModel{provider: "other"}}
	otherMsgs := coldHistory()
	agent.slimHistory(other, otherMsgs, 0)
	require.Equal(t, stubText("/w/a.go"), otherMsgs[2].Parts[0].(message.ToolResult).Content)
}

// TestHistorySlimConfig_WithoutOptions covers a config store whose Config
// has no options block, which is how the pricing test stores are built: the
// duration getter tolerates a nil receiver, but the reasoning field read
// does not.
func TestHistorySlimConfig_WithoutOptions(t *testing.T) {
	env := testEnv(t)
	agent := testSessionAgent(env, nil, nil, "system").(*sessionAgent)
	agent.cfg = config.NewTestStore(&config.Config{})

	idle, dropReasoning := agent.historySlimConfig()
	require.Equal(t, time.Duration(config.DefaultHistorySlimIdleMinutes)*time.Minute, idle)
	require.False(t, dropReasoning)

	// The same store must not panic through the request path either.
	agent.slimHistory(Model{Model: &stubProviderModel{provider: "other"}}, coldHistory(), 0)
}

// stubProviderModel is a fantasy.LanguageModel that only reports a provider
// name, for the provider check.
type stubProviderModel struct {
	fakeLanguageModel
	provider string
}

func (m *stubProviderModel) Provider() string { return m.provider }
