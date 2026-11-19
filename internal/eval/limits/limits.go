// Package limits freezes the run limits of a task from a baseline batch, as
// design section 2 and 4.4 step 2 describe: about twice the 90th percentile
// of the tree steps and the duration of the passing runs.
package limits

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/charmbracelet/crush/internal/eval/result"
	"github.com/charmbracelet/crush/internal/eval/task"
)

// Floors of a frozen limit. The ceilings are the suite defaults.
const (
	MinSteps   = 40
	MinTimeout = 10 * time.Minute
)

// Limit is the frozen limits of one task and the runs they came from.
type Limit struct {
	Task string
	// Runs and Passes count the task's runs on the route.
	Runs   int
	Passes int
	// Steps and Duration are the 90th percentile of the passing runs, and
	// zero when none passed.
	Steps    int
	Duration time.Duration
	MaxSteps int
	Timeout  time.Duration
}

// Compute freezes the limits of every task that ran on route in the batch,
// from the passing runs of every variant. A task with no passing run keeps
// the suite defaults.
func Compute(tasks []*task.Task, runs []result.Result, route string) ([]Limit, error) {
	byID := make(map[string]*task.Task, len(tasks))
	for _, tk := range tasks {
		byID[tk.ID] = tk
	}

	var (
		runCount   = map[string]int{}
		stepCounts = map[string][]int64{}
		durations  = map[string][]int64{}
	)
	for _, r := range runs {
		if r.Route != route {
			continue
		}
		if _, ok := byID[r.Task]; !ok {
			return nil, fmt.Errorf("task %q is not in the suite", r.Task)
		}
		runCount[r.Task]++
		if !r.Pass {
			continue
		}
		stepCounts[r.Task] = append(stepCounts[r.Task], int64(r.Steps))
		durations[r.Task] = append(durations[r.Task], r.DurationMS)
	}

	ids := make([]string, 0, len(runCount))
	for id := range runCount {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	frozen := make([]Limit, 0, len(ids))
	for _, id := range ids {
		limit := Limit{
			Task:     id,
			Runs:     runCount[id],
			Passes:   len(stepCounts[id]),
			MaxSteps: task.DefaultMaxSteps,
			Timeout:  task.DefaultTimeout,
		}
		if limit.Passes > 0 {
			limit.Steps = int(nearestRank(stepCounts[id]))
			limit.Duration = time.Duration(nearestRank(durations[id])) * time.Millisecond
			limit.MaxSteps = min(max(2*limit.Steps, MinSteps), task.DefaultMaxSteps)
			limit.Timeout = min(max(roundUp(2*limit.Duration), MinTimeout), task.DefaultTimeout)
		}
		frozen = append(frozen, limit)
	}
	return frozen, nil
}

// nearestRank returns the 90th percentile of values by the nearest-rank
// method: the value at index ceil(0.9 n) of the sorted values.
func nearestRank(values []int64) int64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	rank := (9*len(sorted) + 9) / 10
	if rank < 1 {
		rank = 1
	}
	return sorted[rank-1]
}

// roundUp rounds a duration up to the next whole minute.
func roundUp(d time.Duration) time.Duration {
	return (d + time.Minute - 1) / time.Minute * time.Minute
}

// WriteLimits replaces the limits block of the task file at path, leaving
// the rest of the file byte-for-byte unchanged.
func WriteLimits(path string, limits task.Limits) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	block, err := encodeLimits(limits)
	if err != nil {
		return err
	}
	lines := splitLines(data)
	start, end := limitsSpan(lines)
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

// limitsSpan returns the line range of the file's top-level limits block, or
// -1 when the file has none.
func limitsSpan(lines []string) (start, end int) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "")), &doc); err != nil {
		return -1, -1
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return -1, -1
	}
	mapping := doc.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != "limits" {
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

// encodeLimits renders the limits block with the field order of the design.
// A timeout is always a whole number of minutes.
func encodeLimits(limits task.Limits) ([]byte, error) {
	scalar := func(tag, value string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
	}
	value := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			scalar("!!str", "max_steps"),
			scalar("!!int", strconv.Itoa(limits.MaxSteps)),
			scalar("!!str", "timeout"),
			scalar("!!str", fmt.Sprintf("%dm", int(time.Duration(limits.Timeout)/time.Minute))),
		},
	}
	root := &yaml.Node{
		Kind:    yaml.MappingNode,
		Content: []*yaml.Node{scalar("!!str", "limits"), value},
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
