package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent"
)

// The client/server path does not implement --json, so the flag is refused
// there before anything is read or started, which exits 1 with nothing on
// standard output.
func TestRunRefusesJSONAgainstAServer(t *testing.T) {
	flag := runCmd.Flags().Lookup("json")
	require.NotNil(t, flag, "crush run has a --json flag")
	require.Equal(t, "bool", flag.Value.Type())

	t.Setenv("CRUSH_CLIENT_SERVER", "1")
	require.EqualError(t, refuseOnServer(true, agent.RunLimits{}, false), "the --json flag is not supported with CRUSH_CLIENT_SERVER=1; unset it to use the flag")
	require.NoError(t, refuseOnServer(false, agent.RunLimits{}, false))

	t.Setenv("CRUSH_CLIENT_SERVER", "0")
	require.NoError(t, refuseOnServer(true, agent.RunLimits{}, false))
}
