package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/crush/internal/eval/batch"
	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/multiturn"
	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// multiturnCmd runs one task over several prompts in the same session, so the
// turn that adopts a background compaction happens inside the run. See
// internal/eval/multiturn for why a single prompt cannot show that.
func multiturnCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("multiturn", flag.ContinueOnError)
	flags.SetOutput(stderr)
	id := flags.String("id", "multiturn", "batch id the lines and run directories go under")
	ref := flags.String("ref", "HEAD", "git ref to build and run")
	routeName := flags.String("route", "", "route name to run")
	tasks := flags.String("tasks", "", "comma-separated task ids")
	arms := flags.String("arms", "control=0,background=0.1", "comma-separated name=margin pairs")
	reps := flags.Int("reps", 2, "repetitions per task and arm")
	concurrency := flags.Int("concurrency", 2, "runs to execute at once")
	turnTimeout := flags.Duration("turn-timeout", multiturn.DefaultTurnTimeout, "time limit of one prompt")
	maxSteps := flags.Int("max-steps", multiturn.DefaultMaxSteps, "step budget of one session, across its turns")
	threshold := flags.Float64("threshold", multiturn.DefaultThreshold, "auto-compaction threshold both arms share")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *routeName == "" || *tasks == "" {
		fmt.Fprintln(stderr, "usage: crush-eval multiturn --id <batch> --ref <sha> --route <name> --tasks pr-0010,pr-0022 [--arms control=0,background=0.1] [--reps 2]")
		return 2
	}
	var tks []*task.Task
	for _, id := range strings.Split(*tasks, ",") {
		tk, err := findTask(strings.TrimSpace(id))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		tks = append(tks, tk)
	}
	var parsed []multiturn.Arm
	for _, pair := range strings.Split(*arms, ",") {
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			fmt.Fprintf(stderr, "arm %q is not name=margin\n", pair)
			return 2
		}
		margin, err := strconv.ParseFloat(value, 64)
		if err != nil {
			fmt.Fprintf(stderr, "arm %q: %v\n", pair, err)
			return 2
		}
		parsed = append(parsed, multiturn.Arm{Name: name, Margin: margin})
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
	opts := multiturn.Options{
		Home:        h,
		Route:       rt,
		BinPath:     bin,
		Ref:         *ref,
		Batch:       *id,
		RepoDir:     ".",
		Footer:      string(footer),
		Reps:        *reps,
		Arms:        parsed,
		Tasks:       tks,
		Concurrency: *concurrency,
		TurnTimeout: *turnTimeout,
		MaxSteps:    *maxSteps,
		Threshold:   *threshold,
	}
	fmt.Fprintf(stderr, "multiturn %s: %d tasks, %d arms, %d reps, build %s\n",
		*id, len(tks), len(parsed), *reps, batch.ShortSHA(bin))
	runs, err := multiturn.RunAll(ctx, opts)
	for _, r := range runs {
		fmt.Fprintf(stdout, "%s %s rep%d: pass=%v turns=%d wall=%dms tokens=%d compactions=%v grade_touched=%v\n",
			r.Task, r.Arm, r.Rep, r.Pass, len(r.Turns), r.Wall, r.Tokens.Total(), r.Compacted.ByTrigger, r.Grade.Touched)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stderr, "wrote %s\n", multiturn.ResultPath(h, *id))
	return 0
}
