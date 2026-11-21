package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent"
)

// A scripted run archives its session so the session lists stay readable
// when CI runs many of them. The client/server path creates the session on
// the server, so the flag is refused there instead of doing nothing.
func TestRunTakesTheArchivedFlag(t *testing.T) {
	flag := runCmd.Flags().Lookup("archived")
	require.NotNil(t, flag, "crush run has an --archived flag")
	require.Equal(t, "bool", flag.Value.Type())
	require.Equal(t, "false", flag.DefValue, "not archived by default")

	t.Setenv("CRUSH_CLIENT_SERVER", "1")
	require.EqualError(t, refuseOnServer(false, agent.RunLimits{}, true), "the --archived flag is not supported with CRUSH_CLIENT_SERVER=1; unset it to use the flag")
	require.NoError(t, refuseOnServer(false, agent.RunLimits{}, false))

	t.Setenv("CRUSH_CLIENT_SERVER", "0")
	require.NoError(t, refuseOnServer(false, agent.RunLimits{}, true))
}
