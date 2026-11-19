package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/eval/batch"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/score"
)

const baselineUsage = `usage:
  crush-eval baseline <batch-id> [--route <name>] [--out <file>]`

// baselineCmd writes the baseline of design 4.4 for one route of a batch:
// the passes and valid runs of every task, every variant pooled, counts only.
func baselineCmd(args []string, stdout, stderr io.Writer) int {
	// The batch id comes first in the usage, but the flag package stops at
	// the first positional argument, so move it behind the flags.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(args[1:], args[0])
	}
	flags := flag.NewFlagSet("baseline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	routeName := flags.String("route", "", "route to count; defaults to the batch's only route")
	out := flags.String("out", "", "baseline file; defaults to evals/baselines/<date>-<route>.json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, baselineUsage)
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

	route := *routeName
	if route == "" {
		if route, err = onlyRoute(runs); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	date := time.Now().Format(time.DateOnly)
	path := *out
	if path == "" {
		path = filepath.Join("evals", "baselines", date+"-"+route+".json")
	}
	base, err := score.NewBaseline(date, route, runs)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := base.WriteFile(path); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	line := struct {
		Batch  string `json:"batch"`
		Route  string `json:"route"`
		Out    string `json:"out"`
		Tasks  int    `json:"tasks"`
		Passes int    `json:"passes"`
		Runs   int    `json:"runs"`
	}{Batch: record.ID, Route: route, Out: path, Tasks: len(base.Tasks)}
	for _, task := range base.Tasks {
		line.Passes += task.Passes
		line.Runs += task.Runs
	}
	return print(stdout, stderr, line)
}

// onlyRoute is the batch's only route, or an error naming the routes when it
// ran more than one, so a baseline never picks a route by chance.
func onlyRoute(runs []result.Result) (string, error) {
	seen := make(map[string]bool, 1)
	for _, r := range runs {
		seen[r.Route] = true
	}
	routes := make([]string, 0, len(seen))
	for name := range seen {
		routes = append(routes, name)
	}
	sort.Strings(routes)
	switch len(routes) {
	case 0:
		return "", errors.New("batch has no results")
	case 1:
		return routes[0], nil
	}
	return "", fmt.Errorf("batch ran routes %s: state --route", strings.Join(routes, ", "))
}
