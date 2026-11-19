// Package vet checks a task against rules 5 to 7 of the task suite design and
// writes the vetted block back into the task file.
package vet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/charmbracelet/crush/internal/eval/checkout"
	"github.com/charmbracelet/crush/internal/eval/grade"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// MaxGradeTime is rule 7's bound on one grade of the task.
const MaxGradeTime = 5 * time.Minute

// MaxPerPackage is rule 9's limit on the tasks that grade one package.
const MaxPerPackage = 6

// Result is the outcome of vetting one task.
type Result struct {
	Task         string            `json:"task"`
	Reps         int               `json:"reps"`
	BaseFails    string            `json:"base_fails"`
	MergePasses  string            `json:"merge_passes"`
	GradeSeconds task.GradeSeconds `json:"grade_seconds"`
	Flaky        []string          `json:"flaky,omitempty"`
}

// Run vets tk against rules 5 to 7. It writes the hidden tests over the base
// tree and requires the touched tests to fail there on every repetition, then
// grades the merge tree and requires the touched and package tests to pass on
// every repetition. scratch is the parent of its temporary checkouts, and env
// is the environment go runs with.
func Run(ctx context.Context, tk *task.Task, repoDir, scratch string, reps int, env []string) (Result, error) {
	res := Result{Task: tk.ID, Reps: reps}
	if reps <= 0 {
		return res, fmt.Errorf("repetitions must be positive")
	}
	dir, err := os.MkdirTemp(scratch, "vet-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(dir)

	fails := 0
	for i := range reps {
		if err := baseFails(ctx, tk, repoDir, filepath.Join(dir, fmt.Sprintf("base-%d", i)), env); err != nil {
			res.BaseFails = fmt.Sprintf("%d/%d", fails, reps)
			return res, err
		}
		fails++
	}
	res.BaseFails = fmt.Sprintf("%d/%d", fails, reps)

	var touched, packages []int64
	passed := 0
	flaky := map[string]bool{}
	var failure error
	for i := range reps {
		mergeDir := filepath.Join(dir, fmt.Sprintf("merge-%d", i))
		if _, err := checkout.Prepare(ctx, repoDir, tk.Source.Merge, mergeDir); err != nil {
			return res, err
		}
		got, err := grade.Grade(ctx, tk, repoDir, filepath.Join(mergeDir, "src"), env)
		if err != nil {
			return res, err
		}
		touched = append(touched, got.TouchedMS)
		packages = append(packages, got.PackagesMS)
		if got.TouchedMS+got.PackagesMS >= MaxGradeTime.Milliseconds() {
			failure = fmt.Errorf("rep %d: grade took %s, over %s", i+1,
				time.Duration(got.TouchedMS+got.PackagesMS)*time.Millisecond, MaxGradeTime)
			continue
		}
		if !got.Pass() {
			for _, name := range got.Failed {
				flaky[name] = true
			}
			if failure == nil {
				reason := strings.Join(got.Failed, ", ")
				if reason == "" {
					reason = "compile error or missing test"
				}
				failure = fmt.Errorf("rep %d: merge did not pass: %s", i+1, reason)
			}
			continue
		}
		passed++
	}
	res.MergePasses = fmt.Sprintf("%d/%d", passed, reps)
	res.GradeSeconds = task.GradeSeconds{
		Touched:  int(median(touched) / 1000),
		Packages: int(median(packages) / 1000),
	}
	for name := range flaky {
		res.Flaky = append(res.Flaky, name)
	}
	sort.Strings(res.Flaky)
	if failure != nil {
		return res, failure
	}
	return res, nil
}

// baseFails prepares the base tree with the hidden tests written over it and
// reports an error when the touched tests pass there.
func baseFails(ctx context.Context, tk *task.Task, repoDir, dir string, env []string) error {
	dest := filepath.Join(dir, "src")
	if _, err := checkout.Prepare(ctx, repoDir, tk.Source.Base, dir); err != nil {
		return err
	}
	if err := grade.ApplyHidden(ctx, repoDir, dest, tk.Grade.Hidden, tk.Grade.Deleted); err != nil {
		return err
	}
	args := []string{
		"test", "-count=1", "-timeout", tk.Grade.Timeout.String(),
		"-run", "^(" + strings.Join(tk.Grade.Tests, "|") + ")$",
	}
	args = append(args, tk.Grade.Packages...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dest
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return fmt.Errorf("touched tests passed at base: %s", tk.ID)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("run go test at base: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// median returns the middle timing, or the upper of the two middle timings
// for an even count.
func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

// PackageUse is one package and the tasks that grade it.
type PackageUse struct {
	Package string   `json:"package"`
	Tasks   []string `json:"tasks"`
}

// Overused returns the packages graded by more than MaxPerPackage tasks.
func Overused(tasks []*task.Task) []PackageUse {
	uses := map[string][]string{}
	for _, tk := range tasks {
		for _, pkg := range tk.Grade.Packages {
			uses[pkg] = append(uses[pkg], tk.ID)
		}
	}
	var over []PackageUse
	for pkg, ids := range uses {
		if len(ids) <= MaxPerPackage {
			continue
		}
		sort.Strings(ids)
		over = append(over, PackageUse{Package: pkg, Tasks: ids})
	}
	sort.Slice(over, func(i, j int) bool { return over[i].Package < over[j].Package })
	return over
}

// WriteVetted replaces the vetted block of the task file at path, leaving the
// rest of the file byte-for-byte unchanged.
func WriteVetted(path string, v task.Vetted) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	block, err := encodeVetted(v)
	if err != nil {
		return err
	}
	lines := splitLines(data)
	start, end := vettedSpan(lines)
	if start < 0 {
		start, end = len(lines), len(lines)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines = append(lines, "\n")
			start, end = len(lines), len(lines)
		}
	}
	var out bytes.Buffer
	for _, line := range lines[:start] {
		out.WriteString(line)
	}
	out.Write(block)
	for _, line := range lines[end:] {
		out.WriteString(line)
	}
	return os.WriteFile(path, out.Bytes(), 0o644)
}

// splitLines splits a file into lines that keep their trailing newline.
func splitLines(data []byte) []string {
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// vettedSpan returns the line range of the file's top-level vetted block, or
// -1 when the file has none.
func vettedSpan(lines []string) (start, end int) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "")), &doc); err != nil {
		return -1, -1
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return -1, -1
	}
	mapping := doc.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != "vetted" {
			continue
		}
		start = mapping.Content[i].Line - 1
		end = start + 1
		for end < len(lines) && (strings.TrimSpace(lines[end]) == "" || lines[end][0] == ' ' || lines[end][0] == '\t' || lines[end][0] == '#') {
			end++
		}
		return start, end
	}
	return -1, -1
}

// encodeVetted renders the vetted block with the field order of the design.
func encodeVetted(v task.Vetted) ([]byte, error) {
	scalar := func(value string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	}
	seconds := &yaml.Node{
		Kind:  yaml.MappingNode,
		Style: yaml.FlowStyle,
		Content: []*yaml.Node{
			scalar("touched"),
			{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(v.GradeSeconds.Touched)},
			scalar("packages"),
			{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(v.GradeSeconds.Packages)},
		},
	}
	value := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			scalar("date"), scalar(v.Date),
			scalar("go"), scalar(v.Go),
			scalar("base_fails"), scalar(v.BaseFails),
			scalar("merge_passes"), scalar(v.MergePasses),
			scalar("grade_seconds"), seconds,
		},
	}
	root := &yaml.Node{
		Kind:    yaml.MappingNode,
		Content: []*yaml.Node{scalar("vetted"), value},
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
