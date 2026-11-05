package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/session"
)

func TestCompleteMatchingTodo(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "Test")
	require.NoError(t, err)
	require.NoError(t, env.sessions.UpdateTodos(t.Context(), sess.ID, []session.Todo{
		{Content: "Review the diff", Status: session.TodoStatusInProgress},
		{Content: "Write the docs", Status: session.TodoStatusInProgress},
	}))

	c := &coordinator{sessions: env.sessions}
	c.completeMatchingTodo(sess.ID, "review the   diff")

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, session.TodoStatusCompleted, updated.Todos[0].Status)
	require.Equal(t, session.TodoStatusInProgress, updated.Todos[1].Status)
}

func TestCompleteMatchingTodoIgnoresUnrelated(t *testing.T) {
	t.Parallel()
	env := testEnv(t)
	sess, err := env.sessions.Create(t.Context(), "Test")
	require.NoError(t, err)
	require.NoError(t, env.sessions.UpdateTodos(t.Context(), sess.ID, []session.Todo{
		{Content: "Review the diff", Status: session.TodoStatusInProgress},
	}))

	c := &coordinator{sessions: env.sessions}
	c.completeMatchingTodo(sess.ID, "Write the docs")

	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, session.TodoStatusInProgress, updated.Todos[0].Status)
}
