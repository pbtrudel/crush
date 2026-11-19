package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

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
