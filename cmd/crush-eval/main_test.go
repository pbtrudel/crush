package main

import (
	"bytes"
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
		"batch", "compare", "baseline", "plan", "login",
	} {
		require.Contains(t, stderr.String(), "crush-eval "+command+" ", "usage does not list %s", command)
	}
}
