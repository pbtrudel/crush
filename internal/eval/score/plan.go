package score

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/charmbracelet/crush/internal/eval/result"
)

// powerFactor turns the standard error into the effect detectable at 80%
// power with a two-sided 5% test.
const powerFactor = 2.8

// Baseline is the per-task pass counts of one route, stored in
// evals/baselines/<date>-<route>.json. It holds counts only, no text.
type Baseline struct {
	Schema int             `json:"schema"`
	Date   string          `json:"date"`
	Route  string          `json:"route"`
	Tasks  []TaskPassCount `json:"tasks"`
}

// TaskPassCount is one task's pass count in a baseline.
type TaskPassCount struct {
	Task   string `json:"task"`
	Passes int    `json:"passes"`
	Runs   int    `json:"runs"`
}

// LoadBaseline reads and validates a baseline file.
func LoadBaseline(path string) (*Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline: %w", err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse baseline: %w", err)
	}
	if b.Schema != 1 {
		return nil, fmt.Errorf("baseline schema %d: want 1", b.Schema)
	}
	if _, err := time.Parse(time.DateOnly, b.Date); err != nil {
		return nil, fmt.Errorf("baseline date %q: want YYYY-MM-DD", b.Date)
	}
	if b.Route == "" {
		return nil, errors.New("baseline has no route")
	}
	if len(b.Tasks) == 0 {
		return nil, errors.New("baseline has no tasks")
	}
	for _, t := range b.Tasks {
		if t.Task == "" || t.Runs <= 0 || t.Passes < 0 || t.Passes > t.Runs {
			return nil, fmt.Errorf("baseline task %q: %d passes of %d runs", t.Task, t.Passes, t.Runs)
		}
	}
	return &b, nil
}

// NewBaseline counts the passes and the valid runs of every task of one
// route over the final runs of a batch, every variant pooled. Contaminated
// and invalid runs are left out, and so is a task with no valid run: it has
// no pass probability to plan from.
func NewBaseline(date, route string, runs []result.Result) (*Baseline, error) {
	type counts struct{ passes, runs int }
	byTask := make(map[string]*counts, len(runs))
	for _, r := range runs {
		if r.Route != route || r.Contaminated {
			continue
		}
		outcome := Classify(r)
		if outcome == Invalid {
			continue
		}
		c := byTask[r.Task]
		if c == nil {
			c = &counts{}
			byTask[r.Task] = c
		}
		c.runs++
		if outcome == Pass {
			c.passes++
		}
	}
	if len(byTask) == 0 {
		return nil, fmt.Errorf("route %q: no valid runs", route)
	}
	b := &Baseline{Schema: 1, Date: date, Route: route, Tasks: make([]TaskPassCount, 0, len(byTask))}
	for task, c := range byTask {
		b.Tasks = append(b.Tasks, TaskPassCount{Task: task, Passes: c.passes, Runs: c.runs})
	}
	sort.Slice(b.Tasks, func(i, j int) bool { return b.Tasks[i].Task < b.Tasks[j].Task })
	return b, nil
}

// WriteFile writes the baseline as indented JSON.
func (b *Baseline) WriteFile(path string) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	return nil
}

// DetectableEffect returns the standard error of the mean paired pass-rate
// difference and the effect detectable at 80% power, for reps repetitions
// per variant. A task adds 2*p*(1-p)/reps, so a task that always passes or
// always fails adds nothing.
func (b *Baseline) DetectableEffect(reps int) (se, effect float64, err error) {
	if reps < 1 {
		return 0, 0, fmt.Errorf("repetitions %d: want at least 1", reps)
	}
	if len(b.Tasks) == 0 {
		return 0, 0, errors.New("baseline has no tasks")
	}
	var sum float64
	for _, t := range b.Tasks {
		p := float64(t.Passes) / float64(t.Runs)
		sum += 2 * p * (1 - p) / float64(reps)
	}
	se = math.Sqrt(sum) / float64(len(b.Tasks))
	return se, powerFactor * se, nil
}

// Variant is one build under comparison.
type Variant struct {
	Name string `yaml:"name"`
	Ref  string `yaml:"ref"`
	// Overlay is merged into the generated config's options. Values keep
	// the YAML type they were written with: a field Crush decodes as a bool
	// must reach it as a JSON bool, not as the string "false".
	Overlay map[string]any `yaml:"overlay,omitempty"`
}

// Experiment is the comparison stated before it runs, in
// evals/experiments/<date>-<name>.yaml.
type Experiment struct {
	Date             string    `yaml:"date"`
	Name             string    `yaml:"name"`
	Variants         []Variant `yaml:"variants"`
	Routes           []string  `yaml:"routes"`
	Repetitions      int       `yaml:"repetitions"`
	PrimaryMetric    Metric    `yaml:"primary_metric"`
	Margin           float64   `yaml:"margin"`
	Baseline         string    `yaml:"baseline"`
	DetectableEffect float64   `yaml:"detectable_effect"`
}

// LoadExperiment reads and validates an experiment file. Unknown fields are
// rejected so a typo cannot drop a stated rule.
func LoadExperiment(path string) (*Experiment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open experiment: %w", err)
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var e Experiment
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("parse experiment: %w", err)
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return &e, nil
}

// Validate checks the experiment of 4.3. DetectableEffect may be zero: plan
// fills it.
func (e *Experiment) Validate() error {
	if _, err := time.Parse(time.DateOnly, e.Date); err != nil {
		return fmt.Errorf("experiment date %q: want YYYY-MM-DD", e.Date)
	}
	if e.Name == "" {
		return errors.New("experiment has no name")
	}
	if len(e.Variants) < 2 {
		return errors.New("experiment needs a control and at least one variant")
	}
	names := make(map[string]bool, len(e.Variants))
	for _, v := range e.Variants {
		if v.Name == "" {
			return errors.New("experiment has a variant without a name")
		}
		if v.Ref == "" {
			return fmt.Errorf("variant %q has no ref", v.Name)
		}
		if names[v.Name] {
			return fmt.Errorf("duplicate variant %q", v.Name)
		}
		names[v.Name] = true
	}
	if len(e.Routes) == 0 {
		return errors.New("experiment has no routes")
	}
	if e.Repetitions < 1 {
		return fmt.Errorf("repetitions %d: want at least 1", e.Repetitions)
	}
	if !e.PrimaryMetric.Valid() {
		return fmt.Errorf("primary metric %q: want pass_rate, false_completions, steps, tokens or edit_refusals", e.PrimaryMetric)
	}
	if e.Margin < 0 {
		return fmt.Errorf("margin %v: want zero or more", e.Margin)
	}
	if e.Baseline == "" {
		return errors.New("experiment has no baseline")
	}
	if e.DetectableEffect < 0 {
		return fmt.Errorf("detectable effect %v: want zero or more", e.DetectableEffect)
	}
	return nil
}

// FillDetectableEffect fills DetectableEffect from the baseline and rejects
// a margin below it (rule 3).
func (e *Experiment) FillDetectableEffect(b *Baseline) error {
	_, effect, err := b.DetectableEffect(e.Repetitions)
	if err != nil {
		return err
	}
	e.DetectableEffect = effect
	if e.Margin < effect {
		return fmt.Errorf("margin %.4f below detectable effect %.4f with %d tasks at %d repetitions: want more tasks, more repetitions or pooled routes",
			e.Margin, effect, len(b.Tasks), e.Repetitions)
	}
	return nil
}
