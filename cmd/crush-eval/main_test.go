package main

import (
	"bytes"
	"flag"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMoveFirstArg moves the batch id behind the flags without aliasing the
// caller's slice.
func TestMoveFirstArg(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"--write", "b1"}, moveFirstArg([]string{"b1", "--write"}))
	require.Equal(t, []string{"--route", "r", "b1"}, moveFirstArg([]string{"--route", "r", "b1"}),
		"a line that already starts with a flag is left alone")
	require.Nil(t, moveFirstArg(nil))

	args := []string{"b1", "--write"}
	got := moveFirstArg(args)
	got[0] = "changed"
	require.Equal(t, []string{"b1", "--write"}, args, "the caller's slice is not aliased")
}

func TestRunDispatch(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"nope"}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), `unknown command "nope"`)

	stdout.Reset()
	stderr.Reset()
	code = run(nil, &stdout, &stderr)
	require.Equal(t, 2, code)
	for _, command := range []string{
		"validate", "prepare", "grade", "candidates", "vet", "run",
		"probe", "batch", "compare", "baseline", "plan", "login",
	} {
		require.Contains(t, stderr.String(), "crush-eval "+command+" ", "usage does not list %s", command)
	}
}

// TestInterleaved parses a flag that follows a positional argument and keeps
// every positional in order.
func TestInterleaved(t *testing.T) {
	t.Parallel()

	flags := flag.NewFlagSet("grade", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	reference := flags.Bool("reference", false, "reference")
	network := flags.String("network", "host", "network")
	positional, err := interleaved(flags, []string{"--reference", "pr-0065", "--network", "jail"})
	require.NoError(t, err)
	require.Equal(t, []string{"pr-0065"}, positional)
	require.True(t, *reference)
	require.Equal(t, "jail", *network)

	flags = flag.NewFlagSet("grade", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Bool("reference", false, "reference")
	positional, err = interleaved(flags, []string{"pr-0010", "run-dir"})
	require.NoError(t, err)
	require.Equal(t, []string{"pr-0010", "run-dir"}, positional)

	flags = flag.NewFlagSet("grade", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	_, err = interleaved(flags, []string{"pr-0010", "--nope"})
	require.Error(t, err)
}

// TestParseNetworkFor checks that the jail is rejected where it cannot
// exist, instead of failing at the clone.
func TestParseNetworkFor(t *testing.T) {
	t.Parallel()

	jailed, err := parseNetworkFor("jail", "linux")
	require.NoError(t, err)
	require.True(t, jailed)

	_, err = parseNetworkFor("jail", "darwin")
	require.ErrorContains(t, err, "needs Linux")
	require.ErrorContains(t, err, "--network host")

	jailed, err = parseNetworkFor("host", "darwin")
	require.NoError(t, err)
	require.False(t, jailed)

	_, err = parseNetworkFor("vpn", "linux")
	require.ErrorContains(t, err, "want")
}
