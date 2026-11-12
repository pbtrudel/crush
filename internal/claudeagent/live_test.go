package claudeagent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// TestLiveSession exercises the whole provider against the real `claude` CLI:
// process start, the MCP tool bridge, a tool-result continuation, and usage
// accounting. It needs a signed-in Claude Code install, so it only runs when
// CRUSH_CLAUDE_AGENT_LIVE is set.
//
//	CRUSH_CLAUDE_AGENT_LIVE=1 go test ./internal/claudeagent/ -run TestLiveSession -v
func TestLiveSession(t *testing.T) {
	if os.Getenv("CRUSH_CLAUDE_AGENT_LIVE") == "" {
		t.Skip("set CRUSH_CLAUDE_AGENT_LIVE=1 to run against the real claude CLI")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	manager := NewManager(t.TempDir())
	defer manager.Close()

	echoTool := fantasy.FunctionTool{
		Name:        "echo",
		Description: "Echo the given text back to the caller.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "Text to echo."},
			},
			"required": []string{"text"},
		},
	}

	prompt := fantasy.Prompt{
		{
			Role:    fantasy.MessageRoleSystem,
			Content: []fantasy.MessagePart{fantasy.TextPart{Text: "You are a test agent. Follow instructions exactly."}},
		},
		{
			Role: fantasy.MessageRoleUser,
			Content: []fantasy.MessagePart{fantasy.TextPart{
				Text: "Call the echo tool exactly once with the text \"hello\". Then reply with exactly the echoed text and nothing else.",
			}},
		},
	}

	call := fantasy.Call{
		Prompt:  prompt,
		Tools:   []fantasy.Tool{echoTool},
		Headers: map[string]string{sessionHeader: "live-test-session"},
	}

	parts, err := collectStream(ctx, manager, call, "claude-haiku-5-5")
	require.NoError(t, err)

	var toolCall *fantasy.StreamPart
	for i := range parts {
		if parts[i].Type == fantasy.StreamPartTypeToolCall {
			toolCall = &parts[i]
		}
	}
	require.NotNil(t, toolCall, "the model should have called the echo tool")
	require.Equal(t, "echo", toolCall.ToolCallName)
	require.True(t, strings.Contains(toolCall.ToolCallInput, "hello"), "tool input: %s", toolCall.ToolCallInput)

	firstFinish := lastFinish(t, parts)
	require.Equal(t, fantasy.FinishReasonToolCalls, firstFinish.FinishReason)
	require.Greater(t, firstFinish.Usage.InputTokens, int64(0), "the first step should report input tokens")
	t.Logf("step 1 usage: %s", firstFinish.Usage)

	// Feed the tool result back the way fantasy's agent loop does. This must
	// continue the same Claude turn instead of replaying the conversation.
	continuation := append(prompt,
		fantasy.Message{
			Role: fantasy.MessageRoleAssistant,
			Content: []fantasy.MessagePart{fantasy.ToolCallPart{
				ToolCallID: toolCall.ID,
				ToolName:   toolCall.ToolCallName,
				Input:      toolCall.ToolCallInput,
			}},
		},
		fantasy.Message{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{fantasy.ToolResultPart{
				ToolCallID: toolCall.ID,
				Output:     fantasy.ToolResultOutputContentText{Text: "hello"},
			}},
		},
	)

	parts, err = collectStream(ctx, manager, fantasy.Call{
		Prompt:  continuation,
		Tools:   []fantasy.Tool{echoTool},
		Headers: map[string]string{sessionHeader: "live-test-session"},
	}, "claude-haiku-5-5")
	require.NoError(t, err)

	final := lastFinish(t, parts)
	require.Equal(t, fantasy.FinishReasonStop, final.FinishReason)

	var text strings.Builder
	for _, part := range parts {
		if part.Type == fantasy.StreamPartTypeTextDelta {
			text.WriteString(part.Delta)
		}
	}
	require.Contains(t, strings.ToLower(text.String()), "hello", "the model should have answered with the echoed text")
	t.Logf("step 2 usage: %s", final.Usage)
	t.Logf("answer: %q", text.String())

	// The second step must read the prompt from the cache rather than paying
	// for the whole conversation again.
	require.Greater(t, final.Usage.CacheReadTokens, int64(0), "the continuation should reuse the cached prompt")
	require.Less(t, final.Usage.CacheCreationTokens, final.Usage.CacheReadTokens,
		"a continuation should read far more cache than it writes")
}

// collectStream runs one step and returns every stream part.
func collectStream(ctx context.Context, manager *Manager, call fantasy.Call, model string) ([]fantasy.StreamPart, error) {
	provider := NewProvider(manager, os.TempDir(), "", Connection{})
	languageModel, err := provider.LanguageModel(ctx, model)
	if err != nil {
		return nil, err
	}
	stream, err := languageModel.Stream(ctx, call)
	if err != nil {
		return nil, err
	}
	var parts []fantasy.StreamPart
	for part := range stream {
		if part.Type == fantasy.StreamPartTypeError {
			return parts, part.Error
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func lastFinish(t *testing.T, parts []fantasy.StreamPart) fantasy.StreamPart {
	t.Helper()
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i].Type == fantasy.StreamPartTypeFinish {
			return parts[i]
		}
	}
	t.Fatalf("no finish part in %d parts", len(parts))
	return fantasy.StreamPart{}
}
