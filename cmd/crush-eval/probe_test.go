package main

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/probe"
	"github.com/stretchr/testify/require"
)

// TestPrintProbe checks the report's shape: the recall count of each pass and
// its short answers, and nothing else.
func TestPrintProbe(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	printProbe(&out, probe.Report{
		Reps:            2,
		Without:         []string{"OK", "no"},
		RecalledWithout: 0,
		With:            []string{probe.Nonce, probe.Nonce},
		RecalledWith:    2,
	})
	require.Equal(t, "without 0/2 recalled\n  OK\n  no\nwith 2/2 recalled\n  A7F3-B2Q9\n  A7F3-B2Q9\n", out.String())
}

// TestProbeRejectsBadInput checks that a malformed line stops before the
// route is loaded or a request is sent.
func TestProbeRejectsBadInput(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"probe", "gpt", "--extra", "{"}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), "parse --extra")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"probe"}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), "crush-eval probe <route>")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"probe", "gpt", "--reps", "0"}, &stdout, &stderr)
	require.Equal(t, 2, code)
	require.Contains(t, stderr.String(), "--reps")
}
