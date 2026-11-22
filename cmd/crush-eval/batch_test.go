package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBatchValidatesTasksBeforeStarting checks that a broken task file stops
// the batch before it builds or runs anything: the task format and the
// repository are both checked first.
func TestBatchValidatesTasksBeforeStarting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))

	writeTestFile(t, "evals/footer.md", "Make the change.\n")
	writeTestFile(t, "evals/tasks/pr-0001.yaml", `schema: 1
id: pr-0001
source: {repo: pbtrudel/crush, pr: 1, merge: `+strings.Repeat("1", 40)+`, base: `+strings.Repeat("2", 40)+`}
kind: plain
prompt: fix the problem
grade:
  packages: []
  tests: [TestFix]
  hidden: [{path: p/p_test.go, blob: `+strings.Repeat("3", 40)+`}]
  timeout: 10m
limits: {max_steps: 10, timeout: 25m}
`)
	writeTestFile(t, filepath.Join(os.Getenv("XDG_STATE_HOME"), "crush-eval", "routes", "hyper.yaml"), `name: hyper
provider: hyper
model: glm-5.3
`)
	experiment := filepath.Join(dir, "experiment.yaml")
	writeTestFile(t, experiment, `date: 2026-11-19
name: validation
variants: [{name: control, ref: base}, {name: variant, ref: topic}]
routes: [hyper]
repetitions: 1
primary_metric: pass_rate
margin: 0.05
baseline: baseline.json
`)

	var stdout, stderr bytes.Buffer
	code := batchCmd([]string{experiment}, &stdout, &stderr)

	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "pr-0001")
	require.Contains(t, stderr.String(), "grade.packages is empty")
}

// writeTestFile writes one file of the test project, creating its directory.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
