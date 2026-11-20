// Command crush-eval is the eval task suite runner. It prepares, vets,
// grades, runs and compares the tasks of the eval suite without importing
// Crush's agent or config packages.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/charmbracelet/crush/internal/eval/gitx"
	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/netjail"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// The suite and the repository a batch runs with, relative to the directory
// crush-eval runs in. rescan rebuilds its contamination markers from the same
// paths.
const (
	suiteDir = "evals"
	repoDir  = "."
)

const usage = `usage:
  crush-eval validate [dir]
  crush-eval prepare <task-id> <dest>
  crush-eval grade <task-id> <run-dir> [--network jail|host]
  crush-eval grade --reference <task-id> [--network jail|host]
  crush-eval candidates [--repo pbtrudel/crush] [--pr N ...] [--out file]
  crush-eval vet <task-id> [--reps 5] [--write]
  crush-eval vet --all
  crush-eval run <task-id> --route <name> [--variant main] [--ref HEAD] [--network jail|host]
  crush-eval probe <route> [--extra JSON] [--reps N]
  crush-eval batch <experiment.yaml> [--id ID] [--seed N] [--network jail|host]
  crush-eval compare <batch-id> [--json]
  crush-eval baseline <batch-id> [--route <name>] [--out <file>]
  crush-eval rescan <batch-id>
  crush-eval limits <batch-id> [--write]
  crush-eval plan <experiment.yaml>
  crush-eval login <route>`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches one command line and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "validate":
		return validateCmd(args[1:], stdout, stderr)
	case "prepare":
		return prepareCmd(args[1:], stdout, stderr)
	case "grade":
		return gradeCmd(args[1:], stdout, stderr)
	case "candidates":
		return candidatesCmd(args[1:], stdout, stderr)
	case "vet":
		return vetCmd(args[1:], stdout, stderr)
	case "run":
		return runCmd(args[1:], stdout, stderr)
	case "probe":
		return probeCmd(args[1:], stdout, stderr)
	case "batch":
		return batchCmd(args[1:], stdout, stderr)
	case "compare":
		return compareCmd(args[1:], stdout, stderr)
	case "baseline":
		return baselineCmd(args[1:], stdout, stderr)
	case "rescan":
		return rescanCmd(args[1:], stdout, stderr)
	case "limits":
		return limitsCmd(args[1:], stdout, stderr)
	case "plan":
		return planCmd(args[1:], stdout, stderr)
	case "login":
		return loginCmd(args[1:], stdout, stderr)
	case "__jail":
		return netjail.Main(args[1:])
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s\n", args[0], usage)
		return 2
	}
}

// validateCmd loads the suite in dir, checks every task against the task
// format and against the repository in the current directory, and prints the
// suite version. It returns 1 when any problem is found.
func validateCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	dir := "evals"
	if flags.NArg() > 0 {
		dir = flags.Arg(0)
	}

	tasks, err := task.LoadDir(filepath.Join(dir, "tasks"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	failed := false
	for _, tk := range tasks {
		if err := tk.Validate(); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", tk.ID, err)
			failed = true
			continue
		}
		if err := tk.ValidateRepo("."); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", tk.ID, err)
			failed = true
			continue
		}
		fmt.Fprintf(stdout, "ok %s (%s)\n", tk.ID, tk.Kind)
	}

	version, err := task.SuiteVersion(dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "suite version %s\n", version)

	if failed {
		return 1
	}
	return 0
}

// prepareCmd writes the task's base commit into dest/src as a fresh
// repository and prints the checkout.
func prepareCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("prepare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: crush-eval prepare <task-id> <dest>")
		return 2
	}
	tk, err := findTask(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	blob, err := checkout.Prepare(context.Background(), ".", tk.Source.Base, flags.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	line := struct {
		Task      string `json:"task"`
		Base      string `json:"base"`
		Dir       string `json:"dir"`
		CrushJSON string `json:"crush_json,omitempty"`
	}{Task: tk.ID, Base: tk.Source.Base, Dir: flags.Arg(1), CrushJSON: blob}
	return print(stdout, stderr, line)
}

// gradeCmd grades a prepared checkout, or with --reference the merge commit's
// tree in a scratch directory. It prints one JSON line and returns 1 when the
// grade does not pass.
func gradeCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("grade", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reference := flags.Bool("reference", false, "grade the merge commit's tree")
	network := flags.String("network", netjail.DefaultMode(), "network mode of the grade: jail or host")
	positional, err := interleaved(flags, args)
	if err != nil {
		return 2
	}
	jailed, err := parseNetwork(*network)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	want := 2
	if *reference {
		want = 1
	}
	if len(positional) != want {
		fmt.Fprintln(stderr, "usage: crush-eval grade <task-id> <run-dir> | crush-eval grade --reference <task-id> [--network jail|host]")
		return 2
	}
	tk, err := findTask(positional[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	scratch, err := evalTmpDir()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	dir := ""
	if *reference {
		dir, err = os.MkdirTemp(scratch, "reference-")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer os.RemoveAll(dir)
	} else {
		dir = positional[1]
	}

	// Grade with an empty HOME: the tests of the repository load its Crush
	// config and go reads its env file, both of which belong to the user.
	home, err := os.MkdirTemp(scratch, "home-")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(home)

	ctx := context.Background()
	var graded grade.Result
	if *reference {
		graded, err = grade.Reference(ctx, tk, ".", dir, grade.Env(home), jailed)
	} else {
		// A prepared checkout's base is its one root commit. A run that
		// rewrote its history makes this command approximate; the batch
		// passes the base it recorded before the model ran.
		src := filepath.Join(dir, "src")
		var base string
		base, err = gitx.Output(ctx, src, "rev-list", "--max-parents=0", "HEAD")
		if err == nil {
			graded, err = grade.Grade(ctx, tk, ".", src, base, grade.Env(home), jailed)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	line := struct {
		Task  string       `json:"task"`
		Pass  bool         `json:"pass"`
		Grade grade.Result `json:"grade"`
	}{Task: tk.ID, Pass: graded.Pass(), Grade: graded}
	if code := print(stdout, stderr, line); code != 0 {
		return code
	}
	if !graded.Pass() {
		return 1
	}
	return 0
}

// findTask loads the task with the given id from the suite's tasks directory.
func findTask(id string) (*task.Task, error) {
	tasks, err := task.LoadDir(filepath.Join("evals", "tasks"))
	if err != nil {
		return nil, err
	}
	for _, tk := range tasks {
		if tk.ID == id {
			return tk, nil
		}
	}
	return nil, fmt.Errorf("task %q not found in evals/tasks", id)
}

// evalTmpDir is where a grade keeps its scratch directories: the eval state
// directory under XDG_STATE_HOME, which defaults to ~/.local/state.
func evalTmpDir() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	dir = filepath.Join(dir, "crush-eval", "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// interleaved parses args with flags that may follow positional arguments,
// which the flag package alone does not allow, and returns the positionals.
func interleaved(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		if flags.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, flags.Arg(0))
		args = flags.Args()[1:]
	}
}

// parseNetwork checks a --network value on this system and reports whether
// it means the jail.
func parseNetwork(value string) (bool, error) {
	return parseNetworkFor(value, runtime.GOOS)
}

// parseNetworkFor checks a --network value for one system. The jail is
// rejected where it cannot exist instead of failing at the clone.
func parseNetworkFor(value, goos string) (bool, error) {
	switch value {
	case netjail.ModeJail:
		if goos != "linux" {
			return false, fmt.Errorf("network %q needs Linux; use --network host", value)
		}
		return true, nil
	case netjail.ModeHost:
		return false, nil
	}
	return false, fmt.Errorf("network %q: want %s or %s", value, netjail.ModeJail, netjail.ModeHost)
}

// moveFirstArg moves a leading positional argument behind the flags: the flag
// package stops at the first positional argument, so a command states the
// batch id or the experiment first in its usage but parses it last. It returns
// a new slice, so the caller's args stay as they were.
func moveFirstArg(args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	moved := make([]string, 0, len(args))
	moved = append(moved, args[1:]...)
	return append(moved, args[0])
}

// print writes one JSON line and returns 0, or 1 when the value does not
// encode.
func print(stdout, stderr io.Writer, value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}
