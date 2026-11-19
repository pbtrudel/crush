// Command crush-eval is the eval task suite runner. This build has three
// subcommands: validate, prepare and grade.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/task"
)

const usage = `usage:
  crush-eval validate [dir]
  crush-eval prepare <task-id> <dest>
  crush-eval grade <task-id> <run-dir>
  crush-eval grade --reference <task-id>
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
	case "login":
		return loginCmd(args[1:], stdout, stderr)
	case "candidates":
		return candidatesCmd(args[1:], stdout, stderr)
	case "vet":
		return vetCmd(args[1:], stdout, stderr)
	case "run":
		return runCmd(args[1:], stdout, stderr)
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
	if err := flags.Parse(args); err != nil {
		return 2
	}
	want := 2
	if *reference {
		want = 1
	}
	if flags.NArg() != want {
		fmt.Fprintln(stderr, "usage: crush-eval grade <task-id> <run-dir> | crush-eval grade --reference <task-id>")
		return 2
	}
	tk, err := findTask(flags.Arg(0))
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
		if _, err := checkout.Prepare(context.Background(), ".", tk.Source.Merge, dir); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		dir = flags.Arg(1)
	}

	// Grade with an empty HOME: the tests of the repository load its Crush
	// config and go reads its env file, both of which belong to the user.
	home, err := os.MkdirTemp(scratch, "home-")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(home)

	result, err := grade.Grade(context.Background(), tk, ".", filepath.Join(dir, "src"), goEnv(home))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	line := struct {
		Task  string       `json:"task"`
		Pass  bool         `json:"pass"`
		Grade grade.Result `json:"grade"`
	}{Task: tk.ID, Pass: result.Pass(), Grade: result}
	if code := print(stdout, stderr, line); code != 0 {
		return code
	}
	if !result.Pass() {
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

// goEnv is the environment grading runs go with. The route builds the same
// one for every run; this is its minimum, with home as the empty HOME.
func goEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"GOCACHE=" + goEnvValue("GOCACHE"),
		"GOMODCACHE=" + goEnvValue("GOMODCACHE"),
		"GOFLAGS=-buildvcs=false",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
	}
}

// goEnvValue reads one go value the process environment does not set, so a
// crafted environment still finds the caches.
func goEnvValue(name string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	out, err := exec.CommandContext(context.Background(), "go", "env", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
