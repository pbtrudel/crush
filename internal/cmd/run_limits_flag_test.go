package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent"
)

// The client/server path does not implement the limits, so their flags are
// refused there before anything is read or started.
func TestRunRefusesLimitsAgainstAServer(t *testing.T) {
	for name, want := range map[string][2]string{"max-steps": {"int", "0"}, "timeout": {"duration", "0s"}} {
		flag := runCmd.Flags().Lookup(name)
		require.NotNil(t, flag, "crush run has a --%s flag", name)
		require.Equal(t, want[0], flag.Value.Type())
		require.Equal(t, want[1], flag.DefValue, "no limit by default")
	}

	t.Setenv("CRUSH_CLIENT_SERVER", "1")
	require.EqualError(t, refuseOnServer(false, agent.RunLimits{MaxSteps: 3}), "the --max-steps flag is not supported with CRUSH_CLIENT_SERVER=1; unset it to use the flag")
	require.EqualError(t, refuseOnServer(false, agent.RunLimits{Timeout: time.Minute}), "the --timeout flag is not supported with CRUSH_CLIENT_SERVER=1; unset it to use the flag")
	require.NoError(t, refuseOnServer(false, agent.RunLimits{}))

	t.Setenv("CRUSH_CLIENT_SERVER", "0")
	require.NoError(t, refuseOnServer(true, agent.RunLimits{MaxSteps: 3, Timeout: time.Minute}))
}

func TestCheckRunLimits(t *testing.T) {
	t.Parallel()
	require.NoError(t, checkRunLimits(agent.RunLimits{}))
	require.NoError(t, checkRunLimits(agent.RunLimits{MaxSteps: 1, Timeout: time.Second}))
	require.EqualError(t, checkRunLimits(agent.RunLimits{MaxSteps: -1}), "the --max-steps value must be zero or more")
	require.EqualError(t, checkRunLimits(agent.RunLimits{Timeout: -time.Second}), "the --timeout value must be zero or more")
}
