package agent

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/gitutil"
	"github.com/stretchr/testify/require"
)

// The text of the completion check, as the design states it (section 4.2).
const completionSpecBlock = `Before you end this turn, check your work and then answer in your reply to the user, in this form:

Asked: <the request in one sentence>
Verified:
- ` + "`" + `<exact command you ran in this turn>` + "`" + `: <what it showed>
Unverified: <what you changed but did not check, and why; "nothing" if all is checked>

List under Verified only commands you ran in this turn, exactly as you ran them. If you ran none, write "Verified: none". If a check is missing that you can still run, run it now before answering. If a check shows the work is unfinished or broken, fix it first.

Files changed in this turn: `

func TestCompletionMessage(t *testing.T) {
	t.Parallel()

	t.Run("one file matches the design", func(t *testing.T) {
		t.Parallel()
		message := completionMessage([]string{"internal/agent/agent.go"}, "aaa", "aaa")
		require.Equal(t, completionSpecBlock+"internal/agent/agent.go", message)
	})

	t.Run("twenty-five files are cut to twenty", func(t *testing.T) {
		t.Parallel()
		files := make([]string, 25)
		for i := range files {
			files[i] = "dir/file" + strconv.Itoa(i) + ".go"
		}
		message := completionMessage(files, "aaa", "aaa")
		require.Contains(t, message, "Files changed in this turn: "+strings.Join(files[:20], ", ")+", and 5 more")
		require.NotContains(t, message, "file24.go")
	})

	t.Run("a moved head adds the commits line", func(t *testing.T) {
		t.Parallel()
		message := completionMessage([]string{"a.go"}, "1234567890abcdef", "abcdef1234567890")
		require.True(t, strings.HasSuffix(message, "\nCommits made in this turn: 1234567..abcdef1"), message)
	})
}

func TestCompletionCheckAsksOnlyWhenSomethingChanged(t *testing.T) {
	t.Parallel()

	check := completionGateCheck{}
	require.Equal(t, "completion", check.Name())

	require.Equal(t, gateVerdict{}, check.Check(t.Context(), gateInput{}))
	require.NotEmpty(t, check.Check(t.Context(), gateInput{ChangedFiles: []string{"a.go"}}).Reason)
	// A commit leaves a clean tree and still counts as a change.
	require.NotEmpty(t, check.Check(t.Context(), gateInput{HeadFrom: "aaa", HeadTo: "bbb"}).Reason)
}

// resetCompletionCheckEnv restores the once-value so a test can change the
// variable it reads.
func resetCompletionCheckEnv(t *testing.T) {
	t.Helper()
	previous := turnEndCompletionEnv
	turnEndCompletionEnv = sync.OnceValue(readTurnEndCompletionEnv)
	t.Cleanup(func() { turnEndCompletionEnv = previous })
}

// The check is off by default, and either switch turns it on.
func TestCompletionCheckEnabled(t *testing.T) {
	t.Run("option off and no override", func(t *testing.T) {
		resetCompletionCheckEnv(t)
		t.Setenv("CRUSH_TURN_END_COMPLETION_CHECK", "")
		require.False(t, completionCheckEnabled(false))
	})
	t.Run("option on and no override", func(t *testing.T) {
		resetCompletionCheckEnv(t)
		t.Setenv("CRUSH_TURN_END_COMPLETION_CHECK", "")
		require.True(t, completionCheckEnabled(true))
	})
	t.Run("override one", func(t *testing.T) {
		resetCompletionCheckEnv(t)
		t.Setenv("CRUSH_TURN_END_COMPLETION_CHECK", "1")
		require.True(t, completionCheckEnabled(false))
	})
	t.Run("override true", func(t *testing.T) {
		resetCompletionCheckEnv(t)
		t.Setenv("CRUSH_TURN_END_COMPLETION_CHECK", "true")
		require.True(t, completionCheckEnabled(false))
	})
	t.Run("override zero", func(t *testing.T) {
		resetCompletionCheckEnv(t)
		t.Setenv("CRUSH_TURN_END_COMPLETION_CHECK", "0")
		require.False(t, completionCheckEnabled(false))
	})
	t.Run("unparseable override follows the option", func(t *testing.T) {
		resetCompletionCheckEnv(t)
		t.Setenv("CRUSH_TURN_END_COMPLETION_CHECK", "sometimes")
		require.False(t, completionCheckEnabled(false))
	})
}

// completionAgent builds an agent with the completion check switched on by
// the config option.
func completionAgent(t *testing.T, env fakeEnv, model fantasy.LanguageModel) *sessionAgent {
	t.Helper()
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().Options.TurnEndCompletionCheck = true
	sa := newCompactionAgent(env, model, cfg, false, &writeTool{recordingTool: recordingTool{name: tools.WriteToolName}})
	return sa
}

// A turn that wrote a file is asked to verify it, and the continuation gets
// the tools it needs to do the checking.
func TestCompletionCheckEndToEnd(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	path := filepath.Join(env.workingDir, "written.txt")
	inner := &scriptedModel{script: []scriptedCall{
		callToolWithInput(tools.WriteToolName, `{"file_path":"`+path+`"}`, 1_000),
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("verified", fantasy.FinishReasonStop, 1_000),
	}}
	model := &promptRecordingModel{scriptedModel: inner}
	sa := completionAgent(t, env, model)
	sess, err := env.sessions.Create(t.Context(), "completion")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "write the file"})
	require.NoError(t, err)

	require.Equal(t, 3, inner.callCount(), "the write, the answer, and the continuation")
	rows := gateRows(t, env, sess.ID)
	require.Len(t, rows, 1)
	require.Contains(t, rows[0].Content().Text, "[check: completion]")
	require.Contains(t, rows[0].Content().Text, "Files changed in this turn: "+path)
	require.Equal(t, "verified", finalAssistantText(t, env, sess.ID))
	require.Equal(t, model.toolNames(t, 1), model.toolNames(t, 2),
		"the continuation offers the tools the first run had")
	require.Contains(t, model.toolNames(t, 2), tools.WriteToolName)
}

// The check is skipped for a subagent turn, which never continues.
func TestCompletionCheckSkipsNonInteractive(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	path := filepath.Join(env.workingDir, "written.txt")
	inner := &scriptedModel{script: []scriptedCall{
		callToolWithInput(tools.WriteToolName, `{"file_path":"`+path+`"}`, 1_000),
		answer("done", fantasy.FinishReasonStop, 1_000),
	}}
	sa := completionAgent(t, env, inner)
	sess, err := env.sessions.Create(t.Context(), "completion")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "write the file", NonInteractive: true})
	require.NoError(t, err)

	require.Empty(t, gateRows(t, env, sess.ID))
	require.Equal(t, 2, inner.callCount())
}

// With the option off and the variable unset, a turn that changed files ends
// where it did before the check existed.
func TestCompletionCheckOffByDefault(t *testing.T) {
	resetCompletionCheckEnv(t)
	t.Setenv("CRUSH_TURN_END_COMPLETION_CHECK", "")
	env := testEnv(t)
	path := filepath.Join(env.workingDir, "written.txt")
	inner := &scriptedModel{script: []scriptedCall{
		callToolWithInput(tools.WriteToolName, `{"file_path":"`+path+`"}`, 1_000),
		answer("done", fantasy.FinishReasonStop, 1_000),
	}}
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	sa := newCompactionAgent(env, inner, cfg, false, &writeTool{recordingTool: recordingTool{name: tools.WriteToolName}})
	sess, err := env.sessions.Create(t.Context(), "completion")
	require.NoError(t, err)
	snapshotCalls := 0
	sa.snapshotState = func(context.Context, string) (gitutil.WorktreeState, error) {
		snapshotCalls++
		return gitutil.WorktreeState{}, nil
	}

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "write the file"})
	require.NoError(t, err)

	require.Empty(t, gateRows(t, env, sess.ID))
	require.Equal(t, 2, inner.callCount())
	require.Zero(t, snapshotCalls, "nothing reads the tree, so none is read")
}
