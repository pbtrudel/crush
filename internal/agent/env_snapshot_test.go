package agent

import (
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
