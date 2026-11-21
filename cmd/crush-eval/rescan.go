package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/charmbracelet/crush/internal/eval/batch"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/result"
	evalrun "github.com/charmbracelet/crush/internal/eval/run"
	"github.com/charmbracelet/crush/internal/eval/task"
)

const rescanUsage = `usage:
  crush-eval rescan <batch-id>

The batch must not be running: a live batch appends result lines that the
rewrite would drop.`

// rescanCmd re-runs the contamination scan of every run of a batch that kept
// its session database and rewrites results.jsonl with the new counts. It
// runs from the suite's repository root, because the scan names that
// repository and the suite directory.
func rescanCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("rescan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, rescanUsage)
		return 2
	}

	h, err := home.Default()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	record, err := batch.Load(h, flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	batchDir := h.BatchDir(record.ID)
	path := filepath.Join(batchDir, "results.jsonl")
	lines, _, err := result.ReadFile(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	tasks, err := task.LoadDir(filepath.Join(suiteDir, "tasks"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	byID := make(map[string]*task.Task, len(tasks))
	for _, tk := range tasks {
		byID[tk.ID] = tk
	}
	// The checkout, the worktrees and the batches are the same for every
	// line; only the run's own directory changes.
	paths, err := evalrun.HostPaths(context.Background(), repoDir, suiteDir, h, "")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	// Only the final attempt of a key left its database behind: the run
	// directory is cleared when an attempt starts.
	final := result.Final(lines)
	var (
		scanned       int
		before, after int
		matchesBefore int
		matchesAfter  int
	)
	for i := range lines {
		line := &lines[i]
		if f, ok := final[line.Key()]; !ok || f.Attempt != line.Attempt {
			continue
		}
		if line.Contaminated {
			before++
		}
		matchesBefore += line.ContaminatedMatches

		tk, ok := byID[line.Task]
		if !ok {
			fmt.Fprintf(stderr, "task %q not found in %s/tasks\n",
				line.Task, suiteDir)
			return 1
		}
		name := evalrun.DirName(line.Task, line.Variant, line.Route, line.Rep)
		runDir := filepath.Join(batchDir, "runs", name)
		paths.Own = runDir
		db := filepath.Join(runDir, "data", "crush.db")
		switch _, err := os.Stat(db); {
		case err == nil:
			found, err := evalrun.Matches(db, tk, paths)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			scanned++
			line.ContaminatedEvidence = found
			line.ContaminatedMatches = len(found)
			line.Contaminated = line.ContaminatedMatches > 0
		case !errors.Is(err, fs.ErrNotExist):
			fmt.Fprintln(stderr, err)
			return 1
		}

		if line.Contaminated {
			after++
		}
		matchesAfter += line.ContaminatedMatches
	}

	if err := writeResults(path, lines); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	line := struct {
		Batch              string `json:"batch"`
		Runs               int    `json:"runs"`
		Scanned            int    `json:"scanned"`
		ContaminatedBefore int    `json:"contaminated_before"`
		ContaminatedAfter  int    `json:"contaminated_after"`
		MatchesBefore      int    `json:"matches_before"`
		MatchesAfter       int    `json:"matches_after"`
	}{
		Batch:              record.ID,
		Runs:               len(lines),
		Scanned:            scanned,
		ContaminatedBefore: before,
		ContaminatedAfter:  after,
		MatchesBefore:      matchesBefore,
		MatchesAfter:       matchesAfter,
	}
	return print(stdout, stderr, line)
}

// writeResults replaces the batch's result lines atomically: it writes a
// temporary file in the same directory and renames it over results.jsonl.
func writeResults(path string, lines []result.Result) error {
	var body []byte
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			return fmt.Errorf("encode result line: %w", err)
		}
		body = append(body, data...)
		body = append(body, '\n')
	}
	// The temporary file takes the mode of the file it replaces, so a
	// rewrite does not change who may read the batch.
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat results: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "results-*.jsonl")
	if err != nil {
		return fmt.Errorf("create the temporary results file: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // Gone after the rename.
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("set the results file mode: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		return fmt.Errorf("write results: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close results: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace results: %w", err)
	}
	return nil
}
