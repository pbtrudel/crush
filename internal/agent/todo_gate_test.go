package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// The turn-end gate asks about a stale item once, on the last run of a
// prompt, and only when the reminders already went unanswered.
func TestTodoGateCheck(t *testing.T) {
	t.Parallel()

	open := []session.Todo{{Content: "Deploy it", Status: session.TodoStatusInProgress}}
	done := []session.Todo{{Content: "Deploy it", Status: session.TodoStatusCompleted}}

	tests := []struct {
		name string
		in   gateInput
		want bool
	}{
		{
			name: "asks about an item still open",
			in: gateInput{
				StaleTodo:          "Deploy it",
				StaleTodoReminders: todoReminderEscalateAfter - 1,
				Todos:              open,
			},
			want: true,
		},
		{
			name: "silent once the reminders escalate no further",
			in: gateInput{
				StaleTodo:          "Deploy it",
				StaleTodoReminders: todoReminderEscalateAfter - 2,
				Todos:              open,
			},
		},
		{
			name: "silent on the continuation it already asked for",
			in: gateInput{
				StaleTodo:          "Deploy it",
				StaleTodoReminders: todoReminderEscalateAfter - 1,
				Todos:              open,
				Continuations:      1,
			},
		},
		{
			name: "silent once the item is done",
			in: gateInput{
				StaleTodo:          "Deploy it",
				StaleTodoReminders: todoReminderEscalateAfter - 1,
				Todos:              done,
			},
		},
		{
			name: "silent when the item left the list",
			in: gateInput{
				StaleTodo:          "Deploy it",
				StaleTodoReminders: todoReminderEscalateAfter - 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason := todoGateCheck{}.Check(context.Background(), tt.in).Reason
			require.Equal(t, tt.want, reason != "")
			if !tt.want {
				return
			}
			require.Contains(t, reason, `"Deploy it"`)
			require.Contains(t, reason, "Do not mark it completed just to close this check")
		})
	}
}

// cappedTodoAgent builds an agent with a bash tool and a turn-end
// continuation cap, so a test can end a prompt no continuation can follow.
func cappedTodoAgent(t *testing.T, env fakeEnv, model fantasy.LanguageModel, maxContinuations int) *sessionAgent {
	t.Helper()
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().Options.TurnEndMaxContinuations = &maxContinuations
	return newCompactionAgent(env, model, cfg, false, &recordingTool{name: tools.BashToolName})
}

// stalePromptScript is five progress steps, then an answer: enough for the
// mid-turn reminders about the same item to go unanswered.
func stalePromptScript() []scriptedCall {
	script := make([]scriptedCall, 0, 8)
	for range 5 {
		script = append(script, callTool(tools.BashToolName, 1_000))
	}
	return append(script, answer("done", fantasy.FinishReasonStop, 1_000))
}

// staleTodoSession stores one in_progress item, the list the reminders are
// about.
func staleTodoSession(t *testing.T, env fakeEnv, title string) session.Session {
	t.Helper()
	sess, err := env.sessions.Create(t.Context(), title)
	require.NoError(t, err)
	require.NoError(t, env.sessions.UpdateTodos(t.Context(), sess.ID, []session.Todo{
		{Content: "Deploy it", Status: session.TodoStatusInProgress},
	}))
	return sess
}

// A turn that ends with the same item open answers the gate once, and the
// reply carries no note: the gate asked instead.
func TestTurnGate_AsksAboutTheStaleTodoOnce(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	script := append(stalePromptScript(), answer("Deploy it is still unfinished", fantasy.FinishReasonStop, 1_000))
	sa := newCompactionAgent(env, &scriptedModel{script: script}, nil, false, &recordingTool{name: tools.BashToolName})
	sess := staleTodoSession(t, env, "todo gate")

	_, err := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	rows := gateRows(t, env, sess.ID)
	require.Len(t, rows, 1, "the gate asks once per prompt")
	require.Contains(t, rows[0].Content().Text, `"Deploy it"`)
	require.Contains(t, rows[0].Content().Text, "Do not mark it completed just to close this check")

	reply := finalAssistantText(t, env, sess.ID)
	require.Contains(t, reply, "Deploy it is still unfinished")
	require.NotContains(t, reply, "Todo list still shows",
		"the reply names the item, so no note is repeated")
}

// Once the gate asked, the reply is all the user gets: a model that says the
// item is unfinished must not get a note saying the same thing twice.
func TestTurnGate_AskedReplyNamingTheItemSkipsTheNote(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	script := append(stalePromptScript(),
		answer(`"Deploy it" is still unfinished: the release window is open.`, fantasy.FinishReasonStop, 1_000))
	sa := newCompactionAgent(env, &scriptedModel{script: script}, nil, false, &recordingTool{name: tools.BashToolName})
	sess := staleTodoSession(t, env, "todo gate answered")

	_, err := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Len(t, gateRows(t, env, sess.ID), 1)
	reply := finalAssistantText(t, env, sess.ID)
	require.Contains(t, reply, "the release window is open")
	require.NotContains(t, reply, "Todo list still shows",
		"the reply already reports the item, so no note is owed")
}

// A reply that ignores the item leaves the user with nothing to see, so the
// note is written even though the gate asked.
func TestTurnGate_AskedReplyIgnoringTheItemStillGetsTheNote(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	script := append(stalePromptScript(), answer("done and dusted", fantasy.FinishReasonStop, 1_000))
	sa := newCompactionAgent(env, &scriptedModel{script: script}, nil, false, &recordingTool{name: tools.BashToolName})
	sess := staleTodoSession(t, env, "todo gate ignored")

	_, err := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Len(t, gateRows(t, env, sess.ID), 1)
	t.Logf("texts=%q", assistantTexts(t, env, sess.ID))
	for _, m := range gateRows(t, env, sess.ID) {
		t.Logf("gate row=%q", m.Content().Text)
	}
	require.Contains(t, finalAssistantText(t, env, sess.ID),
		`Todo list still shows "Deploy it" as unfinished`,
		"the reply never mentions the item, so the note is owed")
}

// A prompt that cannot continue never reaches the gate, so the note is still
// written: the user learns the list stayed behind.
func TestTurnGate_TodoNoteFallsBackWithoutContinuations(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sa := cappedTodoAgent(t, env, &scriptedModel{script: stalePromptScript()}, 0)
	sess := staleTodoSession(t, env, "todo gate off")

	_, err := sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "go"})
	require.NoError(t, err)

	require.Empty(t, gateRows(t, env, sess.ID))
	require.Contains(t, strings.Join(assistantTexts(t, env, sess.ID), "\n"),
		`Todo list still shows "Deploy it" as unfinished`,
		"a prompt with no continuation still ends with the note")
}
