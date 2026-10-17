package agent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func fixedSnapshotClock() func() time.Time {
	return func() time.Time {
		ts, _ := time.Parse("1/2/2006", "1/1/2025")
		return ts
	}
}

// snapshotAgent builds a sessionAgent the way production does: a config
// store, not a sub-agent, and a pinned clock so assertions are
// deterministic.
func snapshotAgent(t *testing.T, env fakeEnv) *sessionAgent {
	t.Helper()
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	return NewSessionAgent(SessionAgentOptions{
		Sessions: env.sessions,
		Messages: env.messages,
		Cfg:      cfg,
		Now:      fixedSnapshotClock(),
	}).(*sessionAgent)
}

func TestShouldCreateEnvSnapshot(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := snapshotAgent(t, env)
	snapshotText := prompt.SessionEnvSnapshot(t.Context(), env.workingDir, fixedSnapshotClock()())
	snapshotMsg := message.Message{Role: message.User, Parts: []message.ContentPart{
		message.TextContent{Text: snapshotText, Hidden: true},
	}}
	userMsg := message.Message{Role: message.User, Parts: []message.ContentPart{
		message.TextContent{Text: "hello"},
	}}

	require.True(t, agent.shouldCreateEnvSnapshot(nil),
		"a fresh session needs a snapshot")
	require.False(t, agent.shouldCreateEnvSnapshot([]message.Message{userMsg}),
		"a session that already has user messages (including pre-upgrade resumes) must not gain a snapshot mid-history")
	require.False(t, agent.shouldCreateEnvSnapshot([]message.Message{snapshotMsg}),
		"a retried first run must not duplicate the snapshot")

	// Sub-agents and config-less agents (test agents, the fetch agent)
	// never snapshot.
	subEnv := testEnv(t)
	sub := snapshotAgent(t, subEnv)
	sub.isSubAgent = true
	require.False(t, sub.shouldCreateEnvSnapshot(nil))
	cfgless := NewSessionAgent(SessionAgentOptions{
		Sessions: subEnv.sessions,
		Messages: subEnv.messages,
	}).(*sessionAgent)
	require.False(t, cfgless.shouldCreateEnvSnapshot(nil))
}

func TestCreateEnvSnapshotMessage(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	agent := snapshotAgent(t, env)
	sess, err := env.sessions.Create(t.Context(), "s")
	require.NoError(t, err)

	msg, err := agent.createEnvSnapshotMessage(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, message.User, msg.Role)
	require.True(t, msg.Content().Hidden,
		"the snapshot must stay out of the transcript")
	require.Contains(t, msg.Content().Text, prompt.EnvSnapshotPrefix)
	require.Contains(t, msg.Content().Text, "Today's date: 1/1/2025")

	// The snapshot must replay to the model like any stored user message.
	history := msg.ToAIMessage()
	require.Len(t, history, 1)
	textPart, ok := history[0].Content[0].(fantasy.TextPart)
	require.True(t, ok)
	require.Equal(t, msg.Content().Text, textPart.Text)
}

// captureStreamModel completes each streamed turn successfully and records
// the fantasy calls the model received, so tests can compare what one turn
// sent with what the next turn replays.
type captureStreamModel struct {
	mu    sync.Mutex
	calls []fantasy.Call
}

func (m *captureStreamModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return &fantasy.Response{
		Content:      fantasy.ResponseContent{fantasy.TextContent{Text: "ok"}},
		FinishReason: fantasy.FinishReasonStop,
	}, nil
}

func (m *captureStreamModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	m.calls = append(m.calls, call)
	m.mu.Unlock()
	return func(yield func(fantasy.StreamPart) bool) {
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "1"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "1", Delta: "ok"}) {
			return
		}
		if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "1"}) {
			return
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *captureStreamModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *captureStreamModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *captureStreamModel) Provider() string { return "fake" }
func (m *captureStreamModel) Model() string    { return "fake-model" }

// TestRunSnapshotOrderMatchesReplay pins the invariant the environment
// snapshot exists for: the messages one turn sends live must be an exact
// prefix of what the next turn replays from the session store. A session
// whose first interaction was a bang-mode shell command already has a
// message older than the snapshot, and the snapshot must land after it,
// matching the stored order, instead of being spliced to the front of the
// live request.
func TestRunSnapshotOrderMatchesReplay(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	large, small := &captureStreamModel{}, &captureStreamModel{}
	sa := testSessionAgent(env, large, small, "system").(*sessionAgent)
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	sa.cfg = cfg
	sa.now = fixedSnapshotClock()

	sess, err := env.sessions.Create(t.Context(), "s")
	require.NoError(t, err)

	// Bang-mode shell commands persist a user message with no text, so the
	// session's first real prompt still triggers snapshot creation.
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{
		Role: message.User,
		Parts: []message.ContentPart{
			message.ShellCommand{Command: "echo hi", Output: "hi", ExitCode: 0},
		},
	})
	require.NoError(t, err)

	run := func(prompt string) {
		t.Helper()
		_, err := sa.Run(t.Context(), SessionAgentCall{
			SessionID:       sess.ID,
			Prompt:          prompt,
			MaxOutputTokens: 1000,
		})
		require.NoError(t, err)
	}
	run("first prompt")
	run("second prompt")

	large.mu.Lock()
	calls := slices.Clone(large.calls)
	large.mu.Unlock()
	require.Len(t, calls, 2, "both turns must reach the large model")
	first, second := stripMessageOptions(calls[0].Prompt), stripMessageOptions(calls[1].Prompt)
	require.Greater(t, len(second), len(first))
	require.Equal(t, first, second[:len(first)],
		"the second turn must replay the first turn's prompt byte-identically as a prefix")
}

// stripMessageOptions drops the per-request cache breakpoint options, which
// by design move to the tail of every request and are not part of the cached
// message content.
func stripMessageOptions(p fantasy.Prompt) fantasy.Prompt {
	out := slices.Clone(p)
	for i := range out {
		out[i].ProviderOptions = nil
	}
	return out
}
