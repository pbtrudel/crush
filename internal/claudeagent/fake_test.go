package claudeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// fakePath is the compiled fake CLI, built once for the package's tests.
var fakePath string

// fakeBuildErr is why the fake CLI could not be built, if it could not.
var fakeBuildErr error

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests builds the fake CLI before running the tests. The build always runs
// because testing.Short is not available until the flags are parsed, which
// happens inside m.Run.
func runTests(m *testing.M) int {
	path, err := buildFakeCLI()
	if err != nil {
		fakeBuildErr = err
		return m.Run()
	}
	fakePath = path
	defer os.RemoveAll(filepath.Dir(path))
	return m.Run()
}

// buildFakeCLI compiles the fake CLI into a temporary directory.
func buildFakeCLI() (string, error) {
	dir, err := os.MkdirTemp("", "fakeclaude")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "fakeclaude")
	out, err := exec.CommandContext(context.Background(), "go", "build", "-o", path, "./testdata/fakeclaude").CombinedOutput()
	if err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("building the fake claude CLI: %w\n%s", err, out)
	}
	return path, nil
}

// fakeExecutable points the provider at the fake CLI. Tests that call it must
// not run in parallel: the executable path is process-wide.
func fakeExecutable(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("the fake claude CLI test is skipped in short mode")
	}
	require.NoError(t, fakeBuildErr, "the fake claude CLI must build")
	require.NotEmpty(t, fakePath, "the fake claude CLI must be built")
	t.Setenv(executableEnv, fakePath)
	return fakePath
}

// fakeLogEntry is one line of the fake CLI's traffic log.
type fakeLogEntry struct {
	Kind      string   `json:"kind"`
	Args      []string `json:"args"`
	Text      string   `json:"text"`
	UUID      string   `json:"uuid"`
	Tools     []string `json:"tools"`
	Method    string   `json:"method"`
	Subtype   string   `json:"subtype"`
	Error     string   `json:"error"`
	ToolUseID string   `json:"tool_use_id"`
	Model     string   `json:"model"`
}

// TestFakeCLIToolRoundTrip drives a whole tool round trip against the fake CLI:
// the first step pauses for a tool call, and the second delivers its result.
func TestFakeCLIToolRoundTrip(t *testing.T) {
	manager, req, parts := fakeToolSession(t, filepath.Join(t.TempDir(), "fake.log"))

	var call *fantasy.StreamPart
	for i := range parts {
		if parts[i].Type == fantasy.StreamPartTypeToolCall {
			call = &parts[i]
		}
	}
	require.NotNil(t, call, "the fake should have asked for a tool call")
	require.Equal(t, "echo", call.ToolCallName)
	require.JSONEq(t, `{"text":"hello"}`, call.ToolCallInput)

	first := fakeFinish(t, parts)
	require.Equal(t, fantasy.FinishReasonToolCalls, first.FinishReason)
	// The fake repeats one response's usage on message_start, message_delta
	// and the assistant message. The provider must keep the highest count
	// instead of summing the repeats, and must report the uncached input
	// separately from the cache counts.
	require.Equal(t, int64(10), first.Usage.InputTokens)
	require.Equal(t, int64(1000), first.Usage.CacheReadTokens)
	require.Equal(t, int64(20), first.Usage.CacheCreationTokens)
	require.Equal(t, int64(5), first.Usage.OutputTokens)
	require.Equal(t, int64(15), first.Usage.TotalTokens)

	req.Prompt = fakeContinuation(req.Prompt, *call)
	parts = fakeStream(t, manager, req)

	final := fakeFinish(t, parts)
	require.Equal(t, fantasy.FinishReasonStop, final.FinishReason)
	require.Equal(t, int64(10), final.Usage.InputTokens)
	require.Equal(t, int64(1030), final.Usage.CacheReadTokens)
	require.Equal(t, int64(3), final.Usage.CacheCreationTokens)
	require.Equal(t, int64(2), final.Usage.OutputTokens)
	require.Equal(t, int64(12), final.Usage.TotalTokens)

	var text strings.Builder
	for _, part := range parts {
		if part.Type == fantasy.StreamPartTypeTextDelta {
			text.WriteString(part.Delta)
		}
	}
	require.Contains(t, text.String(), "hello", "the answer should carry the tool result")
}

// TestFakeCLIContinuationSendsOnlyNewContent checks that a tool-result
// continuation resolves the parked call instead of replaying the prompt.
func TestFakeCLIContinuationSendsOnlyNewContent(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "fake.log")
	manager, req, parts := fakeToolSession(t, logPath)

	var call *fantasy.StreamPart
	for i := range parts {
		if parts[i].Type == fantasy.StreamPartTypeToolCall {
			call = &parts[i]
		}
	}
	require.NotNil(t, call, "the fake should have asked for a tool call")

	req.Prompt = fakeContinuation(req.Prompt, *call)
	parts = fakeStream(t, manager, req)
	require.Equal(t, fantasy.FinishReasonStop, fakeFinish(t, parts).FinishReason)

	// Stop the process so the fake has flushed its log.
	manager.Close()

	var users []fakeLogEntry
	for _, entry := range readFakeLog(t, logPath) {
		if entry.Kind == "user" {
			users = append(users, entry)
		}
	}
	require.Len(t, users, 1, "a continuation must not resend the prompt")
	require.Contains(t, users[0].Text, "CALL_TOOL")
}

// TestFakeCLIUtilityRequest checks the throwaway process a title or compaction
// request gets: its own system prompt, no tools, and no transcript.
func TestFakeCLIUtilityRequest(t *testing.T) {
	fakeExecutable(t)
	logPath := filepath.Join(t.TempDir(), "fake.log")
	t.Setenv("FAKECLAUDE_LOG", logPath)

	managerDir := t.TempDir()
	manager := NewManager(managerDir)
	t.Cleanup(manager.Close)

	parts := fakeStream(t, manager, turnRequest{
		Dir:          t.TempDir(),
		Model:        "fake-opus",
		SystemPrompt: "You write short titles.",
		Prompt: fantasy.Prompt{
			{
				Role:    fantasy.MessageRoleSystem,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "You write short titles."}},
			},
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "Summarize this conversation."}},
			},
		},
	})
	require.Equal(t, fantasy.FinishReasonStop, fakeFinish(t, parts).FinishReason)

	manager.Close()

	args := fakeArgs(t, readFakeLog(t, logPath))
	require.Contains(t, args, "--system-prompt")
	require.NotContains(t, args, "--append-system-prompt")
	require.Equal(t, "You write short titles.", fakeFlagValue(t, args, "--system-prompt"))
	require.Contains(t, args, "--no-session-persistence")
	require.NotContains(t, args, "--mcp-config")

	// A utility call leaves no transcript behind, so no session record is
	// saved. The model catalog is written for every process, so it is ignored.
	records, err := filepath.Glob(filepath.Join(managerDir, "*.json"))
	require.NoError(t, err)
	for _, record := range records {
		require.Equal(t, "models.json", filepath.Base(record), "a utility call must not save a session record")
	}
}

// TestFakeCLIVersionTooOld checks that an outdated CLI is rejected with an
// actionable message.
func TestFakeCLIVersionTooOld(t *testing.T) {
	executable := fakeExecutable(t)
	t.Setenv("FAKECLAUDE_VERSION", "2.1.100")

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	catalog, err := Probe(ctx, executable, t.TempDir(), Connection{})
	require.Error(t, err)
	require.Nil(t, catalog)
	require.Contains(t, err.Error(), "2.1.100")
	require.Contains(t, err.Error(), "claude update")
}

// fakeToolSession starts a tool-calling conversation and runs its first step.
func fakeToolSession(t *testing.T, logPath string) (*Manager, turnRequest, []fantasy.StreamPart) {
	t.Helper()
	fakeExecutable(t)
	t.Setenv("FAKECLAUDE_LOG", logPath)
	t.Setenv("FAKECLAUDE_TOOL", "echo")

	manager := NewManager(t.TempDir())
	t.Cleanup(manager.Close)

	defs, names := toolDefs([]fantasy.Tool{fakeEchoTool()})
	require.Equal(t, []string{"echo"}, names)

	req := turnRequest{
		SessionKey:   "fake-tool-session",
		Dir:          t.TempDir(),
		Model:        "fake-opus",
		AppendPrompt: "You are a test agent.",
		Tools:        defs,
		ToolNames:    names,
		Prompt: fantasy.Prompt{
			{
				Role:    fantasy.MessageRoleSystem,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "You are a test agent."}},
			},
			{
				Role:    fantasy.MessageRoleUser,
				Content: []fantasy.MessagePart{fantasy.TextPart{Text: "CALL_TOOL now, then answer with its result."}},
			},
		},
	}
	return manager, req, fakeStream(t, manager, req)
}

// fakeEchoTool is the Crush tool the fake CLI calls.
func fakeEchoTool() fantasy.FunctionTool {
	return fantasy.FunctionTool{
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
}

// fakeContinuation appends the assistant tool call and its result, the way
// fantasy's agent loop feeds a tool result back.
func fakeContinuation(prompt fantasy.Prompt, call fantasy.StreamPart) fantasy.Prompt {
	continuation := make(fantasy.Prompt, len(prompt), len(prompt)+2)
	copy(continuation, prompt)
	return append(continuation,
		fantasy.Message{
			Role: fantasy.MessageRoleAssistant,
			Content: []fantasy.MessagePart{fantasy.ToolCallPart{
				ToolCallID: call.ID,
				ToolName:   call.ToolCallName,
				Input:      call.ToolCallInput,
			}},
		},
		fantasy.Message{
			Role: fantasy.MessageRoleTool,
			Content: []fantasy.MessagePart{fantasy.ToolResultPart{
				ToolCallID: call.ID,
				Output:     fantasy.ToolResultOutputContentText{Text: "hello"},
			}},
		},
	)
}

// fakeStream runs one step and returns every stream part it produced.
func fakeStream(t *testing.T, manager *Manager, req turnRequest) []fantasy.StreamPart {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	var parts []fantasy.StreamPart
	err := manager.Stream(ctx, req, func(part fantasy.StreamPart) error {
		parts = append(parts, part)
		return nil
	})
	require.NoError(t, err)
	return parts
}

// fakeFinish returns the last finish part of a step.
func fakeFinish(t *testing.T, parts []fantasy.StreamPart) fantasy.StreamPart {
	t.Helper()
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i].Type == fantasy.StreamPartTypeFinish {
			return parts[i]
		}
	}
	t.Fatalf("no finish part in %d parts", len(parts))
	return fantasy.StreamPart{}
}

// readFakeLog parses the traffic log the fake CLI wrote.
func readFakeLog(t *testing.T, path string) []fakeLogEntry {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err, "the fake CLI should have written a log")
	var entries []fakeLogEntry
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry fakeLogEntry
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "log line: %s", line)
		entries = append(entries, entry)
	}
	return entries
}

// fakeArgs returns the CLI arguments the fake recorded at startup.
func fakeArgs(t *testing.T, entries []fakeLogEntry) []string {
	t.Helper()
	for _, entry := range entries {
		if entry.Kind == "args" {
			return entry.Args
		}
	}
	t.Fatalf("the fake CLI did not log its arguments")
	return nil
}

// fakeFlagValue returns the value of a flag, in either the "--flag value" or
// the "--flag=value" form.
func fakeFlagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if name != flag {
			continue
		}
		if hasValue {
			return value
		}
		require.Less(t, i+1, len(args), "%s has no value", flag)
		return args[i+1]
	}
	t.Fatalf("%s is missing from %v", flag, args)
	return ""
}
