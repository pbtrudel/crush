package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsGetAgentTeamAutostart(t *testing.T) {
	t.Parallel()
	on, off := true, false
	require.True(t, (*Options)(nil).GetAgentTeamAutostart())
	require.True(t, (&Options{}).GetAgentTeamAutostart(), "autostart is on by default")
	require.False(t, (&Options{AgentTeamAutostart: &off}).GetAgentTeamAutostart())
	require.True(t, (&Options{AgentTeamAutostart: &on}).GetAgentTeamAutostart())

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{"options":{"agent_team_autostart":false}}`), &cfg))
	require.False(t, cfg.Options.GetAgentTeamAutostart())
}
