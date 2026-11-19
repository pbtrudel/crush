// Command crush-eval is the eval task suite runner. This build has one
// subcommand: validate.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/crush/internal/eval/task"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches one command line and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: crush-eval validate [dir]")
		return 2
	}
	switch args[0] {
	case "validate":
		return validate(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\nusage: crush-eval validate [dir]\n", args[0])
		return 2
	}
}

// validate loads the suite in dir, checks every task against the task format
// and against the repository in the current directory, and prints the suite
// version. It returns 1 when any problem is found.
func validate(args []string, stdout, stderr io.Writer) int {
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
