package claudeagent

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	require.NotContains(t, req.SystemPrompt, "<tool_notes>")
}

// longDescription returns a tool description longer than the MCP limit, made
// of whole lines so a cut can be checked against a line break.
func longDescription(name string) string {
	var text strings.Builder
	for line := 1; text.Len() < mcpDescriptionLimit+500; line++ {
		fmt.Fprintf(&text, "Rules for %s, line %d.\n", name, line)
	}
	return text.String()
}

// TestTruncateDescriptionKeepsTheTail checks that a cut description reports
// the text it lost, cut on a line break so neither half ends mid-sentence.
func TestTruncateDescriptionKeepsTheTail(t *testing.T) {
	t.Parallel()

	short := "Echo the given text back to the caller."
	fit, rest := truncateDescription("echo", short)
	require.Equal(t, short, fit)
	require.Empty(t, rest)

	long := longDescription("bash")
	fit, rest = truncateDescription("bash", long)
	require.NotEmpty(t, rest)
	require.Equal(t, long, fit+rest, "the two parts must reassemble the description")
	require.LessOrEqual(t, len(fit), mcpDescriptionLimit)
	require.GreaterOrEqual(t, len(fit), mcpDescriptionLimit/2, "the cut only moves back to a line break in the second half")
	require.True(t, strings.HasSuffix(fit, "\n"), "the cut should land on a line break")
}

// TestTruncateDescriptionKeepsRunesWhole checks a description with no line
// break is cut at the limit without splitting a character.
func TestTruncateDescriptionKeepsRunesWhole(t *testing.T) {
	t.Parallel()

	// A leading byte shifts every two-byte character, so a plain cut at the
	// limit lands inside one.
	long := "a" + strings.Repeat("é", mcpDescriptionLimit)
	fit, rest := truncateDescription("bash", long)
	require.NotEmpty(t, rest)
	require.LessOrEqual(t, len(fit), mcpDescriptionLimit)
	require.True(t, utf8.ValidString(fit), "the cut must not split a character")
	require.Equal(t, long, fit+rest)
}

// TestTruncateDescriptionMovesSubagentListToTheFront checks that the agent
// list of a subagent tool survives the cut, as it did before.
func TestTruncateDescriptionMovesSubagentListToTheFront(t *testing.T) {
	t.Parallel()

	list := "Available subagents:\n- coder: writes code\n- task: runs one task\n"
	description := longDescription("agent") + list
	at := strings.Index(description, list)
	rotated := description[at:] + "\n\n" + description[:at]
	fit, rest := truncateDescription("agent", description)
	require.True(t, strings.HasPrefix(fit, "Available subagents:"), "the agent list should come first")
	require.Equal(t, rotated, fit+rest, "the rotated description must be cut in two, not dropped")

	short := list + "Nothing else."
	fit, rest = truncateDescription("agent", short)
	require.Equal(t, short, fit)
	require.Empty(t, rest)
}

// TestChatPromptCarriesCutToolGuidance checks that chat turns deliver the tool
// descriptions the tool list cuts, in the system prompt, before the date line
// and sorted by name.
func TestChatPromptCarriesCutToolGuidance(t *testing.T) {
	t.Parallel()

	bash := longDescription("bash")
	question := longDescription("question")
	call := chatCall()
	call.Tools = []fantasy.Tool{
		fantasy.FunctionTool{Name: "question", Description: question, InputSchema: toolSchema()},
		fantasy.FunctionTool{Name: "bash", Description: bash, InputSchema: toolSchema()},
	}
	req := buildTestRequest(t, call)

	require.Contains(t, req.SystemPrompt, "<tool_notes>")
	require.Contains(t, req.SystemPrompt, "</tool_notes>")

	bashAt := strings.Index(req.SystemPrompt, "## mcp__crush__bash")
	questionAt := strings.Index(req.SystemPrompt, "## mcp__crush__question")
	require.GreaterOrEqual(t, bashAt, 0, "the bash notes should be present")
	require.Greater(t, questionAt, bashAt, "the notes should be sorted by tool name")
	require.Less(t, bashAt, strings.Index(req.SystemPrompt, "Today's date:"), "the notes come before the date line")

	_, bashRest := truncateDescription("bash", bash)
	require.Contains(t, req.SystemPrompt, bashRest, "the notes should carry the cut text")
}

// toolSchema is the smallest input schema a tool can carry.
func toolSchema() map[string]any {
	return map[string]any{"type": "object"}
}
