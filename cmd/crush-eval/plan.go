package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/score"
)

const planUsage = `usage:
  crush-eval plan <experiment.yaml>`

// planCmd fills the experiment's smallest detectable effect from its baseline
// and refuses a margin below it, as rule 3 of design 4.3 requires.
func planCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, planUsage)
		return 2
	}

	path := flags.Arg(0)
	exp, err := score.LoadExperiment(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	base, err := score.LoadBaseline(exp.Baseline)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	se, _, err := base.DetectableEffect(exp.Repetitions)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := exp.FillDetectableEffect(base); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writeDetectableEffect(path, exp.DetectableEffect); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "standard error %.4f at %d tasks and %d repetitions\n", se, len(base.Tasks), exp.Repetitions)
	fmt.Fprintf(stdout, "detectable effect %.4f\nmargin %.4f\n", exp.DetectableEffect, exp.Margin)
	return 0
}

// writeDetectableEffect replaces the experiment's top-level
// detectable_effect line, leaving the comments and the rest of the file
// untouched, or appends it when the file has none.
func writeDetectableEffect(path string, effect float64) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	line := "detectable_effect: " + strconv.FormatFloat(effect, 'f', 4, 64) + "\n"
	lines := strings.SplitAfter(string(data), "\n")
	found := false
	for i, current := range lines {
		if strings.HasPrefix(current, "detectable_effect:") {
			lines[i] = line
			found = true
		}
	}
	if !found {
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines = append(lines, "\n")
		}
		lines = append(lines, line)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		return fmt.Errorf("write experiment: %w", err)
	}
	return nil
}
