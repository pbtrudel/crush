package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/charmbracelet/crush/internal/eval/batch"
	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/charmbracelet/crush/internal/eval/toolchain"
)

const batchUsage = `usage:
  crush-eval batch <experiment.yaml> [--id ID] [--seed N]`

// batchCmd runs an experiment: it builds the variants, grades the references
// and runs the suite on every route under the route and machine caps.
func batchCmd(args []string, stdout, stderr io.Writer) int {
	// The experiment comes first in the usage, but the flag package stops at
	// the first positional argument, so move it behind the flags.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(args[1:], args[0])
	}
	flags := flag.NewFlagSet("batch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("id", "", "batch id; defaults to the experiment's date and name")
	seed := flags.Uint64("seed", 0, "seed that shuffles the task order; 0 means the default")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, batchUsage)
		return 2
	}

	// Ctrl-C stops the batch and every live run's process group with it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Record and load the absolute path, so compare finds the experiment
	// from any directory.
	path, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	exp, err := score.LoadExperiment(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	h, err := home.Default()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := h.Ensure(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	routes := make(map[string]*route.Route, len(exp.Routes))
	for _, name := range exp.Routes {
		rt, err := route.Load(h.RoutesDir(), name)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		routes[name] = rt
	}
	tasks, err := task.LoadDir(filepath.Join("evals", "tasks"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// A broken task file must cost a batch nothing: the format and the
	// repository are both checked before anything is built or run.
	for _, tk := range tasks {
		if err := tk.Validate(); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", tk.ID, err)
			return 1
		}
		if err := tk.ValidateRepo("."); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", tk.ID, err)
			return 1
		}
	}
	footer, err := os.ReadFile(filepath.Join("evals", "footer.md"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	suite, err := task.SuiteVersion("evals")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	scratch, err := evalTmpDir()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	gradeHome, err := os.MkdirTemp(scratch, "home-")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(gradeHome)

	batchID := *id
	if batchID == "" {
		batchID = exp.Date + "-" + exp.Name
	}
	// The record holds the toolchain, so a batch whose references fail under
	// a different go is not mistaken for a drop in pass rate.
	version, err := toolchain.Version(os.Environ())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	sum, err := batch.Run(ctx, batch.Options{
		ID:         batchID,
		Experiment: exp,
		Path:       path,
		Seed:       *seed,
		Tasks:      tasks,
		Routes:     routes,
		RepoDir:    ".",
		Home:       h,
		Suite:      suite,
		SuiteDir:   "evals",
		Footer:     string(footer),
		Toolchain:  version,
		Env:        grade.Env(gradeHome),
		Scratch:    scratch,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	line := struct {
		Batch   string `json:"batch"`
		Seed    uint64 `json:"seed"`
		Suite   string `json:"suite"`
		Planned int    `json:"planned"`
		Resumed int    `json:"resumed"`
		Ran     int    `json:"ran"`
		Invalid int    `json:"invalid"`
	}{batchID, sum.Seed, suite, sum.Planned, sum.Resumed, sum.Ran, sum.Invalid}
	return print(stdout, stderr, line)
}
