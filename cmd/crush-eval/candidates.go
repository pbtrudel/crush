package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/charmbracelet/crush/internal/eval/candidates"
	"github.com/charmbracelet/crush/internal/eval/grade"
)

const candidatesUsage = `usage:
  crush-eval candidates [--repo pbtrudel/crush] [--pr N ...] [--out file]`

// candidatesCmd classifies merged pull requests for the task suite and prints
// one JSON line per pull request.
func candidatesCmd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("candidates", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", "pbtrudel/crush", "repository the pull requests belong to")
	out := flags.String("out", "", "write the JSON lines to this file")
	var numbers intList
	flags.Var(&numbers, "pr", "classify this pull request; repeatable")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, candidatesUsage)
		return 2
	}

	ctx := context.Background()
	var gh candidates.GitHub = candidates.GH{}
	explicit := len(numbers) > 0
	var prs []candidates.PullRequest
	if explicit {
		for _, number := range numbers {
			pr, err := gh.View(ctx, *repo, number)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			prs = append(prs, pr)
		}
	} else {
		list, err := gh.List(ctx, *repo)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		for _, pr := range list {
			if candidates.SkipTitle(pr.Title) {
				continue
			}
			view, err := gh.View(ctx, *repo, pr.Number)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if candidates.Filter(view) != "" {
				continue
			}
			prs = append(prs, view)
		}
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
	env := grade.Env(home)

	failed := false
	var lines []byte
	for _, pr := range prs {
		cand, err := candidates.Classify(ctx, ".", pr, scratch, env)
		if err != nil {
			fmt.Fprintf(stderr, "pr %d: %v\n", pr.Number, err)
			failed = true
			continue
		}
		data, err := json.Marshal(cand)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		lines = append(lines, data...)
		lines = append(lines, '\n')
	}

	if *out == "" {
		if _, err := stdout.Write(lines); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else if err := os.WriteFile(*out, lines, 0o644); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

// intList is a repeatable integer flag.
type intList []int

// String renders the numbers for the flag package.
func (l intList) String() string {
	var parts []string
	for _, n := range l {
		parts = append(parts, strconv.Itoa(n))
	}
	return fmt.Sprint(parts)
}

// Set appends one number.
func (l *intList) Set(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	*l = append(*l, n)
	return nil
}
