package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/probe"
	"github.com/charmbracelet/crush/internal/eval/route"
)

const probeUsage = `usage:
  crush-eval probe <route> [--extra JSON] [--reps N]`

// probeCmd asks the route's model to recall a value it noted in its own
// reasoning, once as Crush builds the request and once with extra fields
// merged into the body. It runs on the host, outside the jail, and prints
// only the two recall counts and the short answers.
func probeCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	extra := flags.String("extra", "", "JSON merged into the request body of the second pass")
	reps := flags.Int("reps", 1, "requests per pass")
	positional, err := interleaved(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, probeUsage)
		return 2
	}
	if *reps < 1 {
		fmt.Fprintf(stderr, "probe needs --reps of at least 1, got %d\n", *reps)
		return 2
	}
	var merged map[string]any
	if *extra != "" {
		if err := json.Unmarshal([]byte(*extra), &merged); err != nil {
			fmt.Fprintf(stderr, "parse --extra: %v\n", err)
			return 2
		}
	}

	h, err := home.Default()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	rt, err := route.Load(h.RoutesDir(), positional[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := probe.Run(ctx, probe.Options{Route: rt, Home: h, Extra: merged, Reps: *reps})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printProbe(stdout, report)
	return 0
}

// printProbe prints the two recall counts and the short answers, and nothing
// else: a token has no way into this output.
func printProbe(w io.Writer, report probe.Report) {
	for _, pass := range []struct {
		name     string
		answers  []string
		recalled int
	}{
		{"without", report.Without, report.RecalledWithout},
		{"with", report.With, report.RecalledWith},
	} {
		fmt.Fprintf(w, "%s %d/%d recalled\n", pass.name, pass.recalled, report.Reps)
		for _, answer := range pass.answers {
			fmt.Fprintf(w, "  %s\n", answer)
		}
	}
}
