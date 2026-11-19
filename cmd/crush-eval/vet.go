package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/task"
	"github.com/charmbracelet/crush/internal/eval/vet"
)

const vetUsage = `usage:
  crush-eval vet <task-id> [--reps 5] [--write]
  crush-eval vet --all`

// vetCmd checks one task against rules 5 to 7, or with --all checks rule 9
// over the whole suite.
func vetCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("vet", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reps := flags.Int("reps", 5, "repetitions of each rule")
	write := flags.Bool("write", false, "write the vetted block into the task file")
	all := flags.Bool("all", false, "report packages graded by more than six tasks")
	// Parse repeated times so the task id may come before its flags.
	var positional []string
	for rest := args; len(rest) > 0; {
		if err := flags.Parse(rest); err != nil {
			return 2
		}
		tail := flags.Args()
		if len(tail) == 0 {
			break
		}
		positional = append(positional, tail[0])
		rest = tail[1:]
	}

	if *all {
		if len(positional) != 0 || *write {
			fmt.Fprintln(stderr, vetUsage)
			return 2
		}
		tasks, err := task.LoadDir(filepath.Join("evals", "tasks"))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		over := vet.Overused(tasks)
		line := struct {
			OverLimit []vet.PackageUse `json:"over_limit"`
		}{OverLimit: over}
		if code := print(stdout, stderr, line); code != 0 {
			return code
		}
		if len(over) > 0 {
			return 1
		}
		return 0
	}

	if len(positional) != 1 {
		fmt.Fprintln(stderr, vetUsage)
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
	home, err := os.MkdirTemp(scratch, "home-")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(home)

	result, vetErr := vet.Run(context.Background(), tk, ".", scratch, *reps, grade.Env(home))
	written := ""
	if vetErr == nil && *write {
		version, err := goVersion()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		block := task.Vetted{
			Date:         time.Now().Format("2006-01-02"),
			Go:           version,
			BaseFails:    result.BaseFails,
			MergePasses:  result.MergePasses,
			GradeSeconds: result.GradeSeconds,
		}
		path := filepath.Join("evals", "tasks", tk.ID+".yaml")
		if err := vet.WriteVetted(path, block); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		written = path
	}
	line := struct {
		Task         string            `json:"task"`
		Reps         int               `json:"reps"`
		BaseFails    string            `json:"base_fails"`
		MergePasses  string            `json:"merge_passes"`
		GradeSeconds task.GradeSeconds `json:"grade_seconds"`
		Flaky        []string          `json:"flaky,omitempty"`
		Written      string            `json:"written,omitempty"`
	}{
		Task:         result.Task,
		Reps:         result.Reps,
		BaseFails:    result.BaseFails,
		MergePasses:  result.MergePasses,
		GradeSeconds: result.GradeSeconds,
		Flaky:        result.Flaky,
		Written:      written,
	}
	if code := print(stdout, stderr, line); code != 0 {
		return code
	}
	if vetErr != nil {
		fmt.Fprintln(stderr, vetErr)
		return 1
	}
	return 0
}

// goVersion is the toolchain vetting ran with, as the vetted block records
// it.
func goVersion() (string, error) {
	out, err := exec.CommandContext(context.Background(), "go", "env", "GOVERSION").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOVERSION: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
