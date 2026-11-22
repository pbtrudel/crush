package agent

import (
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/stretchr/testify/require"
)

func TestTeamControlWithoutTeam(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, sendTeamMessage(t.Context(), nil, "s1", "alice", "hi"), ErrAgentTeamsDisabled)
	require.ErrorIs(t, interruptTeammate(nil, "s1", "alice"), ErrAgentTeamsDisabled)
}

func TestTeamControlRefusesUnknownMember(t *testing.T) {
	t.Parallel()
	team, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, nil)
	require.NoError(t, err)
	t.Cleanup(team.Shutdown)

	err = interruptTeammate(team, "s1", "alice")
	require.Equal(t, agentteam.CodeMemberNotFound, agentteam.ErrorCode(err))

	err = sendTeamMessage(t.Context(), team, "s1", "alice", "hi")
	require.NotEmpty(t, agentteam.ErrorCode(err), "a missing member is a typed Team error: %v", err)
}

func TestTeamControlRefusesEmptyMessage(t *testing.T) {
	t.Parallel()
	team, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, nil)
	require.NoError(t, err)
	t.Cleanup(team.Shutdown)
	err = sendTeamMessage(t.Context(), team, "s1", "alice", "  ")
	require.Equal(t, agentteam.CodeInvalidArgument, agentteam.ErrorCode(err))
}
