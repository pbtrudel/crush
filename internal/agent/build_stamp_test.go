package agent

import (
	"errors"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/version"
	"github.com/stretchr/testify/require"
)

func lastAssistantFinish(t *testing.T, env fakeEnv, sessionID string) *message.Finish {
	t.Helper()
	msg, err := env.messages.GetLastAssistantMessage(t.Context(), sessionID)
	require.NoError(t, err)
	finish := msg.FinishPart()
	require.NotNil(t, finish, "the assistant message must be finished")
	return finish
}

func TestBuildStamp_CompletedTurn(t *testing.T) {
	sa, env := newStreamTestAgent(t)
	sess, err := env.sessions.Create(t.Context(), "completed")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.NoError(t, err)

	require.Equal(t, version.BuildStamp(), lastAssistantFinish(t, env, sess.ID).Build)
}

func TestBuildStamp_FailedTurn(t *testing.T) {
	env := testEnv(t)
	model := &errorStreamModel{err: errors.New("boom")}
	sa := testSessionAgent(env, model, model, "system").(*sessionAgent)
	sess, err := env.sessions.Create(t.Context(), "failed")
	require.NoError(t, err)

	_, err = sa.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"})
	require.Error(t, err)

	finish := lastAssistantFinish(t, env, sess.ID)
	require.Equal(t, message.FinishReasonError, finish.Reason)
	require.Equal(t, version.BuildStamp(), finish.Build)
}

func TestBuildStamp_CanceledBeforeTheModelAnswered(t *testing.T) {
	sa, env := newStreamTestAgent(t)
	sess, err := env.sessions.Create(t.Context(), "canceled")
	require.NoError(t, err)

	require.NoError(t, sa.persistCanceledTurn(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hi"}, false))

	finish := lastAssistantFinish(t, env, sess.ID)
	require.Equal(t, message.FinishReasonCanceled, finish.Reason)
	require.Equal(t, version.BuildStamp(), finish.Build)
}

// The stamps are log metadata: a session that carries them must produce
// the provider request it produced before they existed.
func TestBuildStamp_NeverReachesTheProvider(t *testing.T) {
	sa, _ := newStreamTestAgent(t)
	transcript := func(build, trigger string) []message.Message {
		return []message.Message{
			{ID: "u1", Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hi"}}},
			{ID: "a1", Role: message.Assistant, Parts: []message.ContentPart{
				message.TextContent{Text: "hello"},
				message.Finish{Reason: message.FinishReasonEndTurn, Time: 1, Build: build},
			}},
			{ID: "s1", Role: message.Assistant, IsSummaryMessage: true, Parts: []message.ContentPart{
				message.TextContent{Text: "# Summary"},
				message.CompactionStats{TokensBefore: 10, Trigger: trigger},
				message.Finish{Reason: message.FinishReasonEndTurn, Time: 2, Build: build},
			}},
		}
	}

	stamped, _ := sa.preparePrompt(transcript("v0.100.0-fork.100+dirty.mx7k2", "guard"), false, false)
	plain, _ := sa.preparePrompt(transcript("", ""), false, false)
	require.Equal(t, plain, stamped)
}
