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
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/netjail"
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
	batchID := flags.String("batch", "adhoc", "batch id")
	rep := flags.Int("rep", 1, "repetition")
	maxSteps := flags.Int("max-steps", 0, "override the task's step limit")
	timeout := flags.Duration("timeout", 0, "override the task's time limit")
	network := flags.String("network", netjail.DefaultMode(), "network mode of the run: jail or host")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if _, err := parseNetwork(*network); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if flags.NArg() != 1 || *routeName == "" {
		fmt.Fprintln(stderr, "usage: crush-eval run <task-id> --route <name> [--variant main] [--ref HEAD] [--batch adhoc] [--rep 1] [--network jail|host]")
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
	// Ctrl-C has to stop the child's process group, not just the runner.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
		Variant:  *variant + "@" + batch.ShortSHA(bin),
		BinPath:  bin,
		Route:    rt,
		Home:     h,
		Batch:    *batchID,
		Suite:    suite,
		SuiteDir: "evals",
		Rep:      *rep,
		Attempt:  1,
		RepoDir:  ".",
		Footer:   string(footer),
		MaxSteps: *maxSteps,
		Timeout:  *timeout,
		Network:  *network,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return print(stdout, stderr, line)
}
