package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTeamAutostartFollowsConfig pins that the service reads the option on
// every pass through the coordinator, so a reload applies without restart.
func TestTeamAutostartFollowsConfig(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	opts := coord.cfg.Config().Options
	require.True(t, coord.teamAutostart())

	off, on := false, true
	opts.AgentTeamAutostart = &off
	require.False(t, coord.teamAutostart())

	opts.AgentTeamAutostart = &on
	opts.AgentTeams = &off
	require.False(t, coord.teamAutostart(), "the pass does nothing with Agent Teams off")
}

func TestTeamRoleExists(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	require.True(t, coord.teamRoleExists("implementer"))
	require.False(t, coord.teamRoleExists("no-such-role"))
}
