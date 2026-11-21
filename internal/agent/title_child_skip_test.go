package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// countingTitleModel counts the streams a title attempt starts, so a test
// can prove a session never spent a model request on its title.
type countingTitleModel struct {
	*finishStreamModel
	streams atomic.Int32
}

func (m *countingTitleModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.streams.Add(1)
	return m.finishStreamModel.Stream(ctx, call)
}

func newCountingTitleAgent(t *testing.T) (*sessionAgent, fakeEnv, *countingTitleModel, *countingTitleModel) {
	t.Helper()
	env := testEnv(t)
	small := &countingTitleModel{finishStreamModel: &finishStreamModel{text: "Model Title"}}
	large := &countingTitleModel{finishStreamModel: &finishStreamModel{text: "Model Title"}}
	sa := testSessionAgent(env, large, small, "system").(*sessionAgent)
	return sa, env, small, large
}

// A child session (a teammate or subagent run) is never listed, so its title
// would be a model request nobody reads.
func TestGenerateTitle_SkipsSessionWithParent(t *testing.T) {
	t.Parallel()
	sa, env, small, large := newCountingTitleAgent(t)

	parent, err := env.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	sess, err := env.sessions.CreateTaskSession(t.Context(), "child-tool-call", parent.ID, "Teammate beta")
	require.NoError(t, err)

	sa.GenerateTitle(t.Context(), sess.ID, "hello there")

	require.Zero(t, small.streams.Load(), "child session spent a title request on the small model")
	require.Zero(t, large.streams.Load(), "child session spent a title request on the main model")

	got, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Teammate beta", got.Title, "child session title was clobbered")
}

// A subagent agent (agent tool run) can be handed a session with no parent
// row; it must skip the title too.
func TestGenerateTitle_SkipsSubagent(t *testing.T) {
	t.Parallel()
	sa, env, small, large := newCountingTitleAgent(t)
	sa.isSubAgent = true

	sess, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	sa.GenerateTitle(t.Context(), sess.ID, "hello there")

	require.Zero(t, small.streams.Load(), "subagent spent a title request on the small model")
	require.Zero(t, large.streams.Load(), "subagent spent a title request on the main model")

	got, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "New Session", got.Title, "subagent title was clobbered")
}

// A top-level lead session keeps generating its title exactly as before.
func TestGenerateTitle_TopLevelSessionUnchanged(t *testing.T) {
	t.Parallel()
	sa, env, small, large := newCountingTitleAgent(t)

	sess, err := env.sessions.Create(t.Context(), "New Session")
	require.NoError(t, err)

	sa.GenerateTitle(t.Context(), sess.ID, "hello there")

	require.Equal(t, int32(1), small.streams.Load())
	require.Zero(t, large.streams.Load())

	got, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, "Model Title", got.Title)
}
