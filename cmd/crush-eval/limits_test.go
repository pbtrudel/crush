package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/stretchr/testify/require"
)

// taskFileWithLimits is a task file whose limits block is the suite default.
func taskFileWithLimits(id string) string {
	return "schema: 1\nid: " + id + "\nsource:\n  repo: pbtrudel/crush\n" +
		"  pr: 1\n  merge: 334c5d791d23\n  base: 6dd947b1716d\n" +
		"limits:\n  max_steps: 120\n  timeout: 25m\nrisks: []\n"
}

// TestLimitsCommandWrites freezes the limits of a task with passing runs and
// leaves a task without one at the defaults. Without --write no file changes.
func TestLimitsCommandWrites(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	for _, id := range []string{"pr-0001", "pr-0002"} {
		writeTestFile(t, filepath.Join("evals", "tasks", id+".yaml"), taskFileWithLimits(id))
	}
	writeBatch(t, dir, "b1", []string{
		// pr-0001 passes twice with 10 and 30 steps and 2m and 6m; its 90th
		// percentile doubles to 60 steps and 12m.
		`{"schema":1,"batch":"b1","task":"pr-0001","variant":"control@abc","route":"fake","rep":1,"exit":0,"steps":10,"duration_ms":120000,"pass":true}`,
		`{"schema":1,"batch":"b1","task":"pr-0001","variant":"same@abc","route":"fake","rep":1,"exit":0,"steps":30,"duration_ms":360000,"pass":true}`,
		`{"schema":1,"batch":"b1","task":"pr-0002","variant":"control@abc","route":"fake","rep":1,"exit":0,"steps":90,"duration_ms":900000,"pass":false}`,
	})

	var stdout, stderr bytes.Buffer
	code := limitsCmd([]string{"b1"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	require.Contains(t, stdout.String(), "pr-0001")
	require.Contains(t, stdout.String(), "pr-0002")
	require.Contains(t, stdout.String(), "60")
	require.Contains(t, stdout.String(), "12m")

	path := filepath.Join("evals", "tasks", "pr-0001.yaml")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, taskFileWithLimits("pr-0001"), string(got),
		"without --write the task file does not change")

	stdout.Reset()
	stderr.Reset()
	code = limitsCmd([]string{"b1", "--write"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	got, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(taskFileWithLimits("pr-0001"),
		"  max_steps: 120\n  timeout: 25m\n",
		"  max_steps: 60\n  timeout: 12m\n", 1), string(got),
		"only the limits block changes")

	tk, err := task.Load(path)
	require.NoError(t, err)
	require.Equal(t, 60, tk.Limits.MaxSteps)

	got, err = os.ReadFile(filepath.Join("evals", "tasks", "pr-0002.yaml"))
	require.NoError(t, err)
	require.Equal(t, taskFileWithLimits("pr-0002"), string(got),
		"a task without a passing run keeps the defaults")
}
