package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/stretchr/testify/require"
)

// errorStreamModel fails the stream with the given error so Run takes
// the provider-error path.
type errorStreamModel struct {
	finishStreamModel
	err error
}

func (m *errorStreamModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	return nil, m.err
}

// recordingHooks installs hooks for the given events on the agent that
// append "event [last_assistant_message|error|trigger]" to a log, and
// returns a reader for it.
func recordingHooks(t *testing.T, sa *sessionAgent, events ...string) func() []string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "hooks.log")
	record := config.HookConfig{Command: `jq -r '[.hook_event_name, (.last_assistant_message // .error // .trigger // empty)] | join(" ")' >> ` + logPath}
	cfg := make(map[string][]config.HookConfig, len(events))
	for _, ev := range events {
		cfg[ev] = []config.HookConfig{record}
	}
	sa.hooks = hooks.NewRunner(hooks.Static(cfg), dir, dir)
	return func() []string {
		data, _ := os.ReadFile(logPath)
		text := strings.TrimSpace(string(data))
		if text == "" {
			return nil
		}
		return strings.Split(text, "\n")
	}
}

var turnEvents = []string{hooks.EventTurnStart, hooks.EventTurnEnd, hooks.EventInterrupt, hooks.EventStopFailure}

func TestTurnHooks_StartAndEnd(t *testing.T) {
	t.Parallel()
	sa, env := newStreamTestAgent(t)
	read := recordingHooks(t, sa, turnEvents...)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.NoError(t, err)

	require.Equal(t, []string{"TurnStart", "TurnEnd done"}, read())
}

func TestTurnHooks_SubAgentFiresNone(t *testing.T) {
	t.Parallel()
	sa, env := newStreamTestAgent(t)
	read := recordingHooks(t, sa, turnEvents...)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi", NonInteractive: true})
	require.NoError(t, err)

	require.Empty(t, read())
}

func TestTurnHooks_ProviderErrorFiresStopFailureOnly(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &errorStreamModel{err: errors.New("the provider fell over")}
	sa := testSessionAgent(env, model, model, "system").(*sessionAgent)
	read := recordingHooks(t, sa, turnEvents...)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.Error(t, err)

	lines := read()
	require.Len(t, lines, 2)
	require.Equal(t, "TurnStart", lines[0])
	require.True(t, strings.HasPrefix(lines[1], "StopFailure "), lines[1])
	require.Contains(t, lines[1], "fell over")
}

func TestTurnHooks_CancelFiresInterruptOnly(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &errorStreamModel{err: context.Canceled}
	sa := testSessionAgent(env, model, model, "system").(*sessionAgent)
	read := recordingHooks(t, sa, turnEvents...)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.ErrorIs(t, err, context.Canceled)

	require.Equal(t, []string{"TurnStart", "Interrupt"}, read())
}

func TestCompactHooks_PreCompactBlocks(t *testing.T) {
	t.Parallel()
	sa, env := newStreamTestAgent(t)
	dir := t.TempDir()
	sa.hooks = hooks.NewRunner(hooks.Static(map[string][]config.HookConfig{
		hooks.EventPreCompact:  {{Command: `jq -e '.trigger == "manual"' >/dev/null || exit 1; echo "not now" >&2; exit 2`}},
		hooks.EventPostCompact: {{Command: `touch ` + filepath.Join(dir, "post")}},
	}), dir, dir)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.NoError(t, err)

	err = sa.Summarize(t.Context(), sess.ID, nil, nil)
	require.ErrorIs(t, err, ErrCompactionBlocked)
	require.Contains(t, err.Error(), "not now")
	require.NoFileExists(t, filepath.Join(dir, "post"))
	require.False(t, sa.IsSessionBusy(sess.ID))
}

func TestCompactHooks_PostCompactFires(t *testing.T) {
	t.Parallel()
	sa, env := newStreamTestAgent(t)
	read := recordingHooks(t, sa, hooks.EventPreCompact, hooks.EventPostCompact)

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)
	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.NoError(t, err)

	require.NoError(t, sa.Summarize(t.Context(), sess.ID, nil, nil))
	require.Equal(t, []string{"PreCompact manual", "PostCompact manual"}, read())
}

// turnEndHook installs the recording Stop-style hook and returns a reader
// for the events it saw.
func turnEndHook(t *testing.T, sa *sessionAgent) func() []string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "stop.log")
	script := filepath.Join(dir, "stop.sh")
	body := `jq -r '[.hook_event_name, (.stop_hook_active | tostring), (.turn_end // "-")] | join(" ")' >> ` + logPath + "\n"
	body += `if [ "$(wc -l < ` + logPath + `)" -eq 1 ]; then echo "run the tests" >&2; exit 2; fi` + "\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	cfg := map[string][]config.HookConfig{
		hooks.EventTurnEnd: {{Command: "sh " + script}},
	}
	sa.hooks = hooks.NewRunner(hooks.Static(cfg), dir, dir)
	return func() []string {
		data, _ := os.ReadFile(logPath)
		text := strings.TrimSpace(string(data))
		if text == "" {
			return nil
		}
		return strings.Split(text, "\n")
	}
}

// A TurnEnd hook that asks to continue gets one more run, and its second
// call sees stop_hook_active, so a Stop script can skip itself.
func TestTurnHooks_TurnEndContinuesOnce(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{
		answer("done", fantasy.FinishReasonStop, 1_000),
		answer("fixed", fantasy.FinishReasonStop, 1_000),
	}}
	sa := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	read := turnEndHook(t, sa)
	sess, err := env.sessions.Create(t.Context(), "hooks")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Equal(t, 2, model.callCount(), "the hook continues the turn once")
	require.Equal(t, []string{"TurnEnd false completed", "TurnEnd true completed"}, read())
	require.Equal(t, "fixed", finalAssistantText(t, env, sess.ID))
}

// A halting hook refuses the continuation another hook asked for.
func TestTurnHooks_TurnEndHaltWins(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	model := &scriptedModel{script: []scriptedCall{answer("done", fantasy.FinishReasonStop, 1_000)}}
	dir := t.TempDir()
	sa := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
	sa.hooks = hooks.NewRunner(hooks.Static(map[string][]config.HookConfig{
		hooks.EventTurnEnd: {
			{Command: `echo "keep going" >&2; exit 2`},
			{Command: `echo "hands off" >&2; exit 49`},
		},
	}), dir, dir)
	sess, err := env.sessions.Create(t.Context(), "hooks")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Equal(t, 1, model.callCount())
	require.Equal(t, "done", finalAssistantText(t, env, sess.ID))
}

// TurnEnd still fires exactly once for a run the gate does not continue,
// whatever ended it.
func TestTurnHooks_TurnEndFiresOncePerRun(t *testing.T) {
	t.Parallel()

	t.Run("a step limit", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		model := &scriptedModel{script: []scriptedCall{callTool("alpha", 1_000)}}
		sa := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
		sa.limits = &runLimits{}
		read := recordingHooks(t, sa, turnEvents...)
		sess, err := env.sessions.Create(t.Context(), "hooks")
		require.NoError(t, err)
		sa.limits.set(sess.ID, RunLimits{MaxSteps: 1})

		_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		require.NoError(t, err)

		// The step that hit the limit called a tool, so the turn has no
		// assistant text to hand the hook.
		require.Equal(t, []string{"TurnStart", "TurnEnd"}, read())
	})

	t.Run("a compaction that was blocked", func(t *testing.T) {
		t.Parallel()
		env := testEnv(t)
		model := &scriptedModel{script: []scriptedCall{callTool("alpha", 190_000)}}
		sa := newCompactionAgent(env, model, nil, false, &recordingTool{name: "alpha"})
		dir := t.TempDir()
		sa.hooks = hooks.NewRunner(hooks.Static(map[string][]config.HookConfig{
			hooks.EventPreCompact: {{Command: `echo "not now" >&2; exit 2`}},
			hooks.EventTurnEnd:    {{Command: `jq -r '.hook_event_name' >> ` + filepath.Join(dir, "turn.log")}},
		}), dir, dir)
		sess, err := env.sessions.Create(t.Context(), "hooks")
		require.NoError(t, err)

		_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
		require.NoError(t, err)

		data, readErr := os.ReadFile(filepath.Join(dir, "turn.log"))
		require.NoError(t, readErr)
		require.Equal(t, "TurnEnd", strings.TrimSpace(string(data)),
			"a run that skips the gate still fires TurnEnd, exactly once")
	})
}
