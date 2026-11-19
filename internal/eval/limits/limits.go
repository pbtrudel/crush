// Package limits freezes the run limits of a task from a baseline batch, as
// design section 2 and 4.4 step 2 describe: about twice the 90th percentile
// of the tree steps and the duration of the passing runs.
package limits

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
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
// from the clean passing runs of every variant. A task with no such run keeps
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
		// A contaminated pass may have copied the answer, so its steps and
		// time say nothing about how long the task takes.
		if !r.Pass || r.Contaminated {
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
	block, err := encodeLimits(limits)
	if err != nil {
		return err
	}
	return task.ReplaceBlock(path, "limits", block)
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
