package claudeagent

import (
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// fixedClock is the time every request in these tests is built at, so the
// date line is asserted instead of rounded to today.
var fixedClock = time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)

// newTestProvider returns a provider whose clock is fixed.
func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	provider := NewProvider(NewManager(t.TempDir()), t.TempDir(), "", Connection{})
	provider.now = func() time.Time { return fixedClock }
	return provider
}

// buildTestRequest builds a request from a call the way the agent loop does.
func buildTestRequest(t *testing.T, call fantasy.Call) turnRequest {
	t.Helper()
	model, err := newTestProvider(t).LanguageModel(t.Context(), "fake-opus")
	require.NoError(t, err)
	req, err := model.(*Model).buildRequest(call)
	require.NoError(t, err)
	return req
}

// chatCall is a chat turn: it carries tools, the session header and Crush's
// system prompt with its environment block.
func chatCall() fantasy.Call {
	return fantasy.Call{
		Prompt: fantasy.Prompt{
			{
				Role:    fantasy.MessageRoleSystem,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "You are Crush.\n\n<env>\nWorking directory: /tmp\nPlatform: linux\n</env>"}},
			},
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Say hello."}},
			},
		},
		Tools:   []fantasy.Tool{fakeEchoTool()},
		Headers: map[string]string{sessionHeader: "test-session"},
	}
}

// TestChatTurnSendsOneSystemPrompt checks that a chat turn replaces Claude
// Code's system prompt instead of appending to it: the request carries
// Crush's own prompt, environment block included, and ends with the date
// line the CLI's prompt used to supply.
func TestChatTurnSendsOneSystemPrompt(t *testing.T) {
	t.Parallel()

	req := buildTestRequest(t, chatCall())

	require.Contains(t, req.SystemPrompt, "<env>")
	require.Contains(t, req.SystemPrompt, "</env>")
	require.True(
		t,
		strings.HasSuffix(req.SystemPrompt, "\n\nToday's date: 2026-11-14"),
		"the prompt should end with the date line, got %q",
		req.SystemPrompt,
	)

	args := buildArgs(launchFor(req, "claude"))
	require.Contains(t, args, "--system-prompt")
	require.NotContains(t, args, "--append-system-prompt")
	require.Equal(t, req.SystemPrompt, fakeFlagValue(t, args, "--system-prompt"))
}

// TestUtilityTurnSendsItsOwnSystemPrompt checks that a title or compaction
// request keeps its own system prompt and gains no date line: its prompt is
// written by Crush for one small task, not an agent prompt.
func TestUtilityTurnSendsItsOwnSystemPrompt(t *testing.T) {
	t.Parallel()

	req := buildTestRequest(t, fantasy.Call{
		Prompt: fantasy.Prompt{
			{
				Role:    fantasy.MessageRoleSystem,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "You write short titles."}},
			},
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Summarize this."}},
			},
		},
	})

	require.Equal(t, "You write short titles.", req.SystemPrompt)
	require.Empty(t, req.SessionKey, "a utility call must not join the chat transcript")
}
