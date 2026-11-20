package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/gitutil"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// callToolWithInput scripts a model step that calls a tool with a JSON
// input.
func callToolWithInput(name, input string, inputTokens int64) scriptedCall {
	return scriptedCall{parts: []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeToolCall, ID: "call-1", ToolCallName: name, ToolCallInput: input},
		{
			Type:         fantasy.StreamPartTypeFinish,
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        fantasy.Usage{InputTokens: inputTokens, OutputTokens: 10, TotalTokens: inputTokens + 10},
		},
	}}
}

// toolCallWithInput builds a tool call the model streamed with a JSON input.
func toolCallWithInput(id, name, input string) fantasy.ToolCallContent {
	return fantasy.ToolCallContent{ToolCallID: id, ToolName: name, Input: input}
}

func TestStepChangedFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tool  string
		input string
		want  []string
	}{
		{"edit", tools.EditToolName, `{"file_path":"internal/a.go"}`, []string{"internal/a.go"}},
		{"multiedit", tools.MultiEditToolName, `{"file_path":"internal/b.go"}`, []string{"internal/b.go"}},
		{"write", tools.WriteToolName, `{"file_path":"internal/c.go"}`, []string{"internal/c.go"}},
		{"replace symbol", tools.ReplaceSymbolToolName, `{"file_path":"internal/d.go","symbol":"Old"}`, []string{"internal/d.go"}},
		// A rename names no file: it records the symbol it moved.
		{"rename", tools.RenameToolName, `{"symbol":"Old","new_name":"New"}`, []string{"(renamed Old)"}},
		{"bash", tools.BashToolName, `{"command":"gofmt -w ."}`, nil},
		{"no file path", tools.EditToolName, `{}`, nil},
		{"unparseable input", tools.WriteToolName, `{`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			step := todoTestStep(
				[]fantasy.ToolCallContent{toolCallWithInput("1", tt.tool, tt.input)},
				[]fantasy.ToolResultContent{todoTestOK("1", tt.tool)},
			)
			require.Equal(t, tt.want, stepChangedFiles(step))
		})
	}

	t.Run("a failed call changed nothing", func(t *testing.T) {
		t.Parallel()
		step := todoTestStep(
			[]fantasy.ToolCallContent{
				toolCallWithInput("1", tools.WriteToolName, `{"file_path":"a.go"}`),
				toolCallWithInput("2", tools.WriteToolName, `{"file_path":"b.go"}`),
			},
			[]fantasy.ToolResultContent{
				todoTestErr("1", tools.WriteToolName),
				todoTestOK("2", tools.WriteToolName),
			},
		)
		require.Equal(t, []string{"b.go"}, stepChangedFiles(step))
	})

	t.Run("a call with no result yet counts for nothing", func(t *testing.T) {
		t.Parallel()
		step := todoTestStep(
			[]fantasy.ToolCallContent{toolCallWithInput("1", tools.WriteToolName, `{"file_path":"a.go"}`)},
			nil,
		)
		require.Empty(t, stepChangedFiles(step))
	})
}

func TestTurnChangedFiles(t *testing.T) {
	t.Parallel()

	t.Run("the union is sorted and deduplicated", func(t *testing.T) {
		t.Parallel()
		before := gitutil.WorktreeState{Head: "aaa", Entries: map[string]string{}}
		after := gitutil.WorktreeState{Head: "aaa", Entries: map[string]string{
			"b.go": "1 .M N...",
			"c.go": "? c.go",
		}}
		call := SessionAgentCall{
			gateBaseline:     &before,
			gateChangedFiles: []string{"b.go", "a.go"},
		}

		paths, headFrom, headTo := turnChangedFiles(call, after)
		require.Equal(t, []string{"a.go", "b.go", "c.go"}, paths)
		require.Equal(t, "aaa", headFrom)
		require.Equal(t, "aaa", headTo)
	})

	t.Run("a moved head is reported by the two shas", func(t *testing.T) {
		t.Parallel()
		before := gitutil.WorktreeState{Head: "aaa", Entries: map[string]string{}}
		after := gitutil.WorktreeState{Head: "bbb", Entries: map[string]string{}}

		paths, headFrom, headTo := turnChangedFiles(SessionAgentCall{gateBaseline: &before}, after)
		require.Empty(t, paths)
		require.Equal(t, "aaa", headFrom)
		require.Equal(t, "bbb", headTo)
	})

	t.Run("the fallback has tool calls only", func(t *testing.T) {
		t.Parallel()
		call := SessionAgentCall{gateChangedFiles: []string{"z.go"}}
		paths, headFrom, headTo := turnChangedFiles(call, gitutil.WorktreeState{})
		require.Equal(t, []string{"z.go"}, paths)
		require.Empty(t, headFrom)
		require.Equal(t, headFrom, headTo)
	})
}

// gitTest runs one git command in dir.
func gitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-C", dir,
		"-c", "user.name=Test",
		"-c", "user.email=test@example.com",
	}, args...)
	out, err := exec.CommandContext(t.Context(), "git", full...).CombinedOutput()
	require.NoError(t, err, string(out))
}

// newGitEnv makes the working directory a repository with one commit.
func newGitEnv(t *testing.T) fakeEnv {
	t.Helper()
	env := testEnv(t)
	gitTest(t, env.workingDir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(env.workingDir, "tracked.txt"), []byte("one\n"), 0o644))
	gitTest(t, env.workingDir, "add", ".")
	gitTest(t, env.workingDir, "commit", "-q", "-m", "init")
	return env
}

// gateEnvAgent builds an agent over a repository with a cfg store, so the
// gate can read the working tree, and a TurnEnd hook so it does.
func gateEnvAgent(t *testing.T, env fakeEnv, model fantasy.LanguageModel, checks ...gateCheck) *sessionAgent {
	t.Helper()
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	sa := newCompactionAgent(env, model, cfg, false, &recordingTool{name: "alpha"})
	sa.gateChecks = checks
	return sa
}

// shellWritingTool stands in for bash: it changes a file the gate can only
// see through git, and reports no file path of its own.
type shellWritingTool struct {
	recordingTool
	path string
}

func (t *shellWritingTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if err := os.WriteFile(t.path, []byte("written by a shell command\n"), 0o644); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return fantasy.NewTextResponse("ok"), nil
}

// A file a shell command changed is found through git, not through the tool
// call, and the hook payload carries it.
func TestGateChangedFilesFromTheWorkingTree(t *testing.T) {
	t.Parallel()
	env := newGitEnv(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "files.log")
	model := &scriptedModel{script: []scriptedCall{
		callTool("bash", 1_000),
		answer("done", fantasy.FinishReasonStop, 1_000),
	}}
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	path := filepath.Join(env.workingDir, "by-shell.txt")
	sa := newCompactionAgent(env, model, cfg, false, &shellWritingTool{recordingTool{name: tools.BashToolName}, path})
	sa.hooks = hooks.NewRunner(hooks.Static(map[string][]config.HookConfig{
		hooks.EventTurnEnd: {{Command: `jq -r '.changed_files[]?' >> ` + logPath}},
	}), dir, dir)
	sess, err := env.sessions.Create(t.Context(), "changed")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, "by-shell.txt", strings.TrimSpace(string(data)), "the hook payload matches the gate's files")
}

// A TurnEnd hook sees the files the turn's tools changed when git cannot
// answer, as outside a repository.
func TestGateChangedFilesFallBackToToolCalls(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "files.log")
	record := &fakeGateCheck{name: "fake"}
	path := filepath.Join(env.workingDir, "new.txt")
	model := &scriptedModel{script: []scriptedCall{
		callToolWithInput(tools.WriteToolName, `{"file_path":"`+path+`"}`, 1_000),
		answer("done", fantasy.FinishReasonStop, 1_000),
	}}
	sa := gateEnvAgent(t, env, model, record)
	sa.tools = csync.NewSliceFrom([]fantasy.AgentTool{&writeTool{recordingTool: recordingTool{name: tools.WriteToolName}}})
	sa.hooks = hooks.NewRunner(hooks.Static(map[string][]config.HookConfig{
		hooks.EventTurnEnd: {{Command: `jq -r '.changed_files[]?' >> ` + logPath}},
	}), dir, dir)
	sess, err := env.sessions.Create(t.Context(), "changed")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Equal(t, path, strings.TrimSpace(string(data)), "the fallback reports the tool's file")
	require.Len(t, record.recorded(), 1)
	require.Equal(t, []string{path}, record.recorded()[0].ChangedFiles)
}

// writeTool stands in for the write tool, which changes the file its input
// names.
type writeTool struct {
	recordingTool
}

func (t *writeTool) Run(_ context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if err := os.WriteFile(gjson.Get(call.Input, "file_path").String(), []byte("new\n"), 0o644); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	return fantasy.NewTextResponse("ok"), nil
}

// The baseline is taken once per prompt, so a continuation does not read the
// working tree at its start again.
func TestGateBaselineTakenOncePerPrompt(t *testing.T) {
	t.Parallel()
	env := newGitEnv(t)
	check := &fakeGateCheck{name: "fake", reason: "keep going"}
	model := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("fixed", fantasy.FinishReasonStop, 1_000),
	}}
	sa := gateEnvAgent(t, env, model, check)
	dir := t.TempDir()
	sa.hooks = hooks.NewRunner(hooks.Static(map[string][]config.HookConfig{
		hooks.EventTurnEnd: {{Command: `true`}},
	}), dir, dir)
	// Each snapshot reports its own sequence number as HEAD, so the test
	// can tell a baseline from an end.
	calls := 0
	sa.snapshotState = func(context.Context, string) (gitutil.WorktreeState, error) {
		calls++
		return gitutil.WorktreeState{Head: strconv.Itoa(calls), Entries: map[string]string{}}, nil
	}
	sess, err := env.sessions.Create(t.Context(), "changed")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Equal(t, 2, model.callCount(), "the prompt continued once")
	require.Equal(t, 3, calls, "one baseline and one snapshot per ending")
	inputs := check.recorded()
	require.Len(t, inputs, 2)
	require.Equal(t, "1", inputs[0].HeadFrom, "the first run framed from the prompt's baseline")
	require.Equal(t, "2", inputs[0].HeadTo)
	require.Equal(t, "1", inputs[1].HeadFrom, "the continuation reused the same baseline")
	require.Equal(t, "3", inputs[1].HeadTo)
}

// With no TurnEnd hook and the completion check off, the gate never reads the
// working tree.
func TestGateTakesNoSnapshotWhenNotNeeded(t *testing.T) {
	t.Parallel()
	env := newGitEnv(t)
	model := &scriptedModel{script: []scriptedCall{answer("done", fantasy.FinishReasonStop, 1_000)}}
	sa := gateEnvAgent(t, env, model)
	calls := 0
	sa.snapshotState = func(ctx context.Context, dir string) (gitutil.WorktreeState, error) {
		calls++
		return gitutil.Snapshot(ctx, dir)
	}
	sess, err := env.sessions.Create(t.Context(), "changed")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Zero(t, calls, "nothing reads the snapshot, so none is taken")
	require.Equal(t, 1, model.callCount())
	require.Len(t, gateRows(t, env, sess.ID), 0)
}
