package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/route"
	runner "github.com/charmbracelet/crush/internal/eval/run"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// runCmd builds the variant's binary, runs one task on the route and prints
// the result line.
func runCmd(args []string, stdout, stderr io.Writer) int {
	// The task id comes first in the usage, but the flag package stops at
	// the first positional argument, so move it behind the flags.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(args[1:], args[0])
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	routeName := flags.String("route", "", "route name to run")
	variant := flags.String("variant", "main", "variant name")
	ref := flags.String("ref", "HEAD", "git ref to build")
	batch := flags.String("batch", "adhoc", "batch id")
	rep := flags.Int("rep", 1, "repetition")
	maxSteps := flags.Int("max-steps", 0, "override the task's step limit")
	timeout := flags.Duration("timeout", 0, "override the task's time limit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 || *routeName == "" {
		fmt.Fprintln(stderr, "usage: crush-eval run <task-id> --route <name> [--variant main] [--ref HEAD] [--batch adhoc] [--rep 1]")
		return 2
	}
	tk, err := findTask(flags.Arg(0))
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
	rt, err := route.Load(h.RoutesDir(), *routeName)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	bin, err := route.Build(ctx, h, ".", *ref)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
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

	line, err := runner.Run(ctx, runner.Options{
		Task:     tk,
		Variant:  *variant + "@" + shortSHA(bin),
		BinPath:  bin,
		Route:    rt,
		Home:     h,
		Batch:    *batch,
		Suite:    suite,
		SuiteDir: "evals",
		Rep:      *rep,
		Attempt:  1,
		RepoDir:  ".",
		Footer:   string(footer),
		MaxSteps: *maxSteps,
		Timeout:  *timeout,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return print(stdout, stderr, line)
}

// shortSHA is the abbreviated commit the binary was built from, which names
// its bin directory.
func shortSHA(bin string) string {
	sha := filepath.Base(filepath.Dir(bin))
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
