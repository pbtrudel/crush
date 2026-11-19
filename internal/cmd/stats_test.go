package cmd

import (
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// executeStraySubcommand runs args through the real root command with the
// RunE of the command cobra resolves to replaced, and reports whether that
// RunE ran. A test can then check that a stray word is rejected without
// loading config, opening a database or writing the stats page.
func executeStraySubcommand(t *testing.T, args ...string) (bool, error) {
	t.Helper()
	cmd, _, err := rootCmd.Find(args)
	require.NoError(t, err, "the typo is expected to resolve to a command")

	origRunE := cmd.RunE
	ran := false
	cmd.RunE = func(*cobra.Command, []string) error {
		ran = true
		return nil
	}
	rootCmd.SetArgs(args)
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	t.Cleanup(func() {
		cmd.RunE = origRunE
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err = rootCmd.Execute()
	return ran, err
}

func TestStatsUnknownSubcommandIsRejected(t *testing.T) {
	cmd, args, err := rootCmd.Find([]string{"stats", "reprot"})
	require.NoError(t, err)
	require.Same(t, statsCmd, cmd)
	require.Equal(t, []string{"reprot"}, args)

	ran, err := executeStraySubcommand(t, "stats", "reprot")
	require.ErrorContains(t, err, `unknown command "reprot" for "crush stats"`)
	require.False(t, ran, "the stats page must not run for an unknown subcommand")
}

func TestStatsFlagsOnlyInvocationRunsThePage(t *testing.T) {
	ran, err := executeStraySubcommand(t, "stats", "--all")
	require.NoError(t, err)
	require.True(t, ran)
}
