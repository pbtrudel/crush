package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/crush/internal/eval/batch"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/limits"
	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/task"
)

const limitsUsage = `usage:
  crush-eval limits <batch-id> [--write]`

// limitsCmd freezes the tasks' limits from the passing runs of one batch, as
// design 4.4 step 2 asks, and with --write replaces their limits blocks.
func limitsCmd(args []string, stdout, stderr io.Writer) int {
	// The batch id comes first in the usage, but the flag package stops at
	// the first positional argument, so move it behind the flags.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(args[1:], args[0])
	}
	flags := flag.NewFlagSet("limits", flag.ContinueOnError)
	flags.SetOutput(stderr)
	write := flags.Bool("write", false, "write the limits into evals/tasks")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, limitsUsage)
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
	lines, _, err := result.ReadFile(filepath.Join(h.BatchDir(record.ID), "results.jsonl"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	runs := finalRuns(lines)
	route, err := onlyRoute(runs)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	tasks, err := task.LoadDir(filepath.Join("evals", "tasks"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	frozen, err := limits.Compute(tasks, runs, route)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "task\truns\tpassing\tp90_steps\tp90_duration\tmax_steps\ttimeout\twritten")
	for _, limit := range frozen {
		written := ""
		if *write {
			path := filepath.Join("evals", "tasks", limit.Task+".yaml")
			if err := limits.WriteLimits(path, task.Limits{
				MaxSteps: limit.MaxSteps,
				Timeout:  task.Duration(limit.Timeout),
			}); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			written = "yes"
		}
		fmt.Fprintf(table, "%s\t%d\t%d\t%d\t%.1fm\t%d\t%dm\t%s\n",
			limit.Task, limit.Runs, limit.Passes, limit.Steps,
			float64(limit.Duration)/float64(time.Minute), limit.MaxSteps,
			int(limit.Timeout/time.Minute), written)
	}
	if err := table.Flush(); err != nil {
		return 1
	}
	return 0
}
