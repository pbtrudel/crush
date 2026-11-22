package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/gitx"
	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/charmbracelet/crush/internal/eval/result"
	evalrun "github.com/charmbracelet/crush/internal/eval/run"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// rescanSchema is the messages table of the first migration, as the run
// package's tests build it.
const rescanSchema = `CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    parts TEXT NOT NULL default '[]',
    model TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    finished_at INTEGER
);`

// TestRescanRewritesContamination re-runs the scan of every run that kept its
// session database and rewrites the batch's contamination flags. A run whose
// database is gone keeps the flags it had, and every other field is left
// alone.
func TestRescanRewritesContamination(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// rescan derives the contamination markers of the checkout from this
	// directory, as its documentation says it is run from the suite's
	// repository root.
	_, err := gitx.Output(context.Background(), dir, "init", "-q")
	require.NoError(t, err)
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	writeTestFile(t, filepath.Join("evals", "tasks", "pr-0001.yaml"),
		"schema: 1\nid: pr-0001\nsource:\n  repo: pbtrudel/crush\n  pr: 1\n"+
			"  merge: 334c5d791d23\n  base: 6dd947b1716d\n")

	batchDir := writeBatch(t, dir, "b1", []string{
		// r1 and r4 were marked contaminated by the substring match; r2 was
		// not marked and now names a command; r3 has no database left.
		`{"schema":1,"batch":"b1","task":"pr-0001","variant":"control@abc","route":"fake","rep":1,"exit":0,"contaminated":true,"contaminated_matches":3,"pass":true}`,
		`{"schema":1,"batch":"b1","task":"pr-0001","variant":"control@abc","route":"fake","rep":2,"exit":0,"pass":true}`,
		`{"schema":1,"batch":"b1","task":"pr-0001","variant":"control@abc","route":"fake","rep":3,"exit":0,"contaminated":true,"contaminated_matches":1,"pass":false}`,
		`{"schema":1,"batch":"b1","task":"pr-0001","variant":"control@abc","route":"fake","rep":4,"exit":0,"contaminated":true,"contaminated_matches":2,"pass":true}`,
	})
	require.NoError(t, os.Chmod(filepath.Join(batchDir, "results.jsonl"), 0o640))
	writeSessionDB(t, runDB(batchDir, 1), `grep -n "through prefix" pkg.go`)
	writeSessionDB(t, runDB(batchDir, 2), `cd x && gh pr list -R pbtrudel/crush`)
	writeSessionDB(t, runDB(batchDir, 4), `ls pkg`)

	var stdout, stderr bytes.Buffer
	code := rescanCmd([]string{"b1"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	var printed struct {
		Batch              string `json:"batch"`
		Runs               int    `json:"runs"`
		Scanned            int    `json:"scanned"`
		ContaminatedBefore int    `json:"contaminated_before"`
		ContaminatedAfter  int    `json:"contaminated_after"`
		MatchesBefore      int    `json:"matches_before"`
		MatchesAfter       int    `json:"matches_after"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &printed))
	require.Equal(t, "b1", printed.Batch)
	require.Equal(t, 4, printed.Runs)
	require.Equal(t, 3, printed.Scanned,
		"the run without a database is not scanned")
	require.Equal(t, 3, printed.ContaminatedBefore)
	require.Equal(t, 2, printed.ContaminatedAfter)
	require.Equal(t, 6, printed.MatchesBefore)
	require.Equal(t, 2, printed.MatchesAfter)

	path := filepath.Join(batchDir, "results.jsonl")
	lines, ignored, err := result.ReadFile(path)
	require.NoError(t, err)
	require.Zero(t, ignored)
	require.Len(t, lines, 4)
	require.False(t, lines[0].Contaminated)
	require.Zero(t, lines[0].ContaminatedMatches)
	require.True(t, lines[0].Pass, "the rest of the line is unchanged")
	require.True(t, lines[1].Contaminated)
	require.Equal(t, 1, lines[1].ContaminatedMatches)
	require.True(t, lines[2].Contaminated,
		"a run without a database keeps its flags")
	require.Equal(t, 1, lines[2].ContaminatedMatches)
	require.False(t, lines[3].Contaminated)
	require.Zero(t, lines[3].ContaminatedMatches)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm(),
		"the rewrite keeps the results file mode")
}

// runDirOf is the run directory of one run of the test batch.
func runDirOf(batchDir string, rep int) string {
	return filepath.Dir(filepath.Dir(runDB(batchDir, rep)))
}

// TestRescanCutsThePathTheRunWasGiven checks the cut the scan makes: a jailed
// child names the neutral path, so naming it is not contamination and naming
// the host path of its own run is; a child on the host names its own host
// directory, which stays cut.
func TestRescanCutsThePathTheRunWasGiven(t *testing.T) {
	// The checkout and the eval home are separate directories: a marker of
	// the checkout would otherwise match every path of the batch.
	repo := t.TempDir()
	t.Chdir(repo)
	_, err := gitx.Output(context.Background(), repo, "init", "-q")
	require.NoError(t, err)
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	writeTestFile(t, filepath.Join("evals", "tasks", "pr-0001.yaml"),
		"schema: 1\nid: pr-0001\nsource:\n  repo: pbtrudel/crush\n  pr: 1\n"+
			"  merge: 334c5d791d23\n  base: 6dd947b1716d\n")

	batchDir := writeBatch(t, home, "b1", []string{
		`{"schema":1,"batch":"b1","task":"pr-0001","variant":"control@abc","route":"fake","rep":1,"network":"jail","exit":0,"pass":true}`,
		`{"schema":1,"batch":"b1","task":"pr-0001","variant":"control@abc","route":"fake","rep":2,"network":"host","exit":0,"pass":true}`,
	})
	jailed := runDirOf(batchDir, 1)
	writeSessionDB(t, runDB(batchDir, 1),
		"ls "+filepath.Join(netjail.JailRunPath, "src"),
		"ls "+filepath.Join(jailed, "src"),
	)
	writeSessionDB(t, runDB(batchDir, 2), "ls "+filepath.Join(runDirOf(batchDir, 2), "src"))

	var stdout, stderr bytes.Buffer
	code := rescanCmd([]string{"b1"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	lines, ignored, err := result.ReadFile(filepath.Join(batchDir, "results.jsonl"))
	require.NoError(t, err)
	require.Zero(t, ignored)
	require.Len(t, lines, 2)
	require.True(t, lines[0].Contaminated,
		"the host path of the run's own directory counts for a jailed run")
	require.Equal(t, 1, lines[0].ContaminatedMatches, "the neutral path does not count")
	require.False(t, lines[1].Contaminated,
		"a run on the host names its own directory all the time")
	require.Zero(t, lines[1].ContaminatedMatches)
}

// runDB is the session database of one run of the test batch.
func runDB(batchDir string, rep int) string {
	name := evalrun.DirName("pr-0001", "control@abc", "fake", rep)
	return filepath.Join(batchDir, "runs", name, "data", "crush.db")
}

// writeSessionDB creates a run's session database with one assistant message
// holding a bash tool call per input.
func writeSessionDB(t *testing.T, path string, inputs ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	db, err := sql.Open("sqlite", "file:"+path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := context.Background()
	_, err = db.ExecContext(ctx, rescanSchema)
	require.NoError(t, err)
	calls := make([]map[string]any, 0, len(inputs))
	for _, input := range inputs {
		calls = append(calls, map[string]any{
			"type": "tool_call",
			"data": map[string]any{"name": "bash", "input": input},
		})
	}
	parts, err := json.Marshal(calls)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO messages (id, session_id, role, parts, created_at, updated_at)`+
			` VALUES ('m1', 's1', 'assistant', ?, 1, 1)`, string(parts))
	require.NoError(t, err)
}
