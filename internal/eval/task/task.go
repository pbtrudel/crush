// Package task defines the on-disk format of an eval task, loads task files,
// validates them against the task and against the repository they were cut
// from, assembles the prompt a run receives, and computes the suite version.
package task

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/charmbracelet/crush/internal/eval/gitx"
)

// Schema is the only task file schema this package reads.
const Schema = 1

// Kind is how a task states its interface: plain tasks compile against the
// parent commit, interface tasks name what the pull request introduced.
const (
	KindPlain     = "plain"
	KindInterface = "interface"
)

// Suite defaults for a task's limits. A zero limit in a task file means the
// default.
const (
	DefaultMaxSteps = 120
	DefaultTimeout  = 25 * time.Minute
)

// Task is one problem of the suite: a base commit, a prompt and a grade.
type Task struct {
	Schema    int      `yaml:"schema"`
	ID        string   `yaml:"id"`
	Source    Source   `yaml:"source"`
	Kind      string   `yaml:"kind"`
	Why       string   `yaml:"why"`
	Prompt    string   `yaml:"prompt"`
	Interface string   `yaml:"interface"`
	Grade     Grade    `yaml:"grade"`
	Limits    Limits   `yaml:"limits"`
	Vetted    Vetted   `yaml:"vetted"`
	Risks     []string `yaml:"risks"`

	// path is the file the task was loaded from, empty for a task built in
	// memory. Validate uses it for the file name check.
	path string
}

// Source names the pull request a task was cut from and the commits it runs
// between. Base is the first parent of Merge.
type Source struct {
	Repo  string `yaml:"repo"`
	PR    int    `yaml:"pr"`
	Merge string `yaml:"merge"`
	Base  string `yaml:"base"`
}

// Grade is what proves a run solved the task: the tests the pull request
// touched, the packages that hold them, and the test files written into the
// checkout after the run.
type Grade struct {
	Packages []string `yaml:"packages"`
	Tests    []string `yaml:"tests"`
	Exclude  []string `yaml:"exclude"`
	Hidden   []Hidden `yaml:"hidden"`
	Deleted  []string `yaml:"deleted"`
	Timeout  Duration `yaml:"timeout"`
}

// Hidden is a test file of the merge commit, referenced by the blob sha of
// its contents. The suite directory holds no test code.
type Hidden struct {
	Path string `yaml:"path"`
	Blob string `yaml:"blob"`
}

// Limits bound one run of the task.
type Limits struct {
	MaxSteps int      `yaml:"max_steps"`
	Timeout  Duration `yaml:"timeout"`
}

// Vetted records the vetting of the task. It is filled by the vet command.
type Vetted struct {
	Date         string       `yaml:"date"`
	Go           string       `yaml:"go"`
	BaseFails    string       `yaml:"base_fails"`
	MergePasses  string       `yaml:"merge_passes"`
	GradeSeconds GradeSeconds `yaml:"grade_seconds"`
}

// GradeSeconds are the timings of a vetting grade run.
type GradeSeconds struct {
	Touched  int `yaml:"touched" json:"touched"`
	Packages int `yaml:"packages" json:"packages"`
}

// Duration is a time.Duration written as a Go duration string in YAML.
type Duration time.Duration

// UnmarshalYAML parses a duration string such as "25m".
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// String returns the duration as a Go duration string.
func (d Duration) String() string { return time.Duration(d).String() }

// Load reads one task file. It decodes strictly, so an unknown field is an
// error, and fills the default limits. It does not validate: call Validate.
func Load(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	tk := &Task{}
	if err := dec.Decode(tk); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	tk.path = path
	tk.applyDefaults()
	return tk, nil
}

// LoadDir loads every task file of a directory and returns them sorted by id.
func LoadDir(dir string) ([]*Task, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s: no task files", dir)
	}
	tasks := make([]*Task, 0, len(paths))
	for _, path := range paths {
		tk, err := Load(path)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, tk)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

// Validate reports every problem of the task at once. It fills the default
// limits first, so a task built in memory is checked as it would run.
func (t *Task) Validate() error {
	t.applyDefaults()
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if t.Schema != Schema {
		add("schema is %d, want %d", t.Schema, Schema)
	}
	idOK := idPattern.MatchString(t.ID)
	if !idOK {
		add("id %q does not match pr-NNNN", t.ID)
	}
	if t.path != "" {
		if name := filepath.Base(t.path); name != t.ID+".yaml" {
			add("id %q does not match file name %q", t.ID, name)
		}
	}
	if t.Source.PR <= 0 {
		add("source.pr is %d, want the pull request number", t.Source.PR)
	} else if idOK && fmt.Sprintf("pr-%04d", t.Source.PR) != t.ID {
		add("id %q does not match source.pr %d", t.ID, t.Source.PR)
	}
	switch t.Kind {
	case KindPlain:
		if strings.TrimSpace(t.Interface) != "" {
			add("plain task has an interface paragraph")
		}
	case KindInterface:
		if strings.TrimSpace(t.Interface) == "" {
			add("interface task needs an interface paragraph")
		}
	default:
		add("kind %q is not %s or %s", t.Kind, KindPlain, KindInterface)
	}
	if strings.TrimSpace(t.Prompt) == "" {
		add("prompt is empty")
	}
	if len(t.Grade.Packages) == 0 {
		add("grade.packages is empty")
	}
	if len(t.Grade.Tests) == 0 {
		add("grade.tests is empty")
	}
	if len(t.Grade.Hidden) == 0 {
		add("grade.hidden is empty")
	}
	for i, hidden := range t.Grade.Hidden {
		if hidden.Path == "" {
			add("grade.hidden[%d].path is empty", i)
		} else if !filepath.IsLocal(hidden.Path) {
			add("grade.hidden[%d].path %q is not inside the repository", i, hidden.Path)
		}
		if !blobPattern.MatchString(hidden.Blob) {
			add("grade.hidden[%d].blob %q is not a full blob sha", i, hidden.Blob)
		}
	}
	if t.Grade.Timeout <= 0 {
		add("grade.timeout must be positive")
	}
	if t.Limits.MaxSteps <= 0 {
		add("limits.max_steps must be positive")
	}
	if t.Limits.Timeout <= 0 {
		add("limits.timeout must be positive")
	}
	if t.Vetted != (Vetted{}) {
		if _, err := time.Parse("2006-01-02", t.Vetted.Date); err != nil {
			add("vetted.date %q is not a date", t.Vetted.Date)
		}
		if t.Vetted.Go == "" {
			add("vetted.go is empty")
		}
		if !countPattern.MatchString(t.Vetted.BaseFails) {
			add("vetted.base_fails %q is not n/n", t.Vetted.BaseFails)
		}
		if !countPattern.MatchString(t.Vetted.MergePasses) {
			add("vetted.merge_passes %q is not n/n", t.Vetted.MergePasses)
		}
		if t.Vetted.GradeSeconds.Touched < 0 {
			add("vetted.grade_seconds.touched is negative")
		}
		if t.Vetted.GradeSeconds.Packages < 0 {
			add("vetted.grade_seconds.packages is negative")
		}
	}

	return problemsError(problems)
}

// ValidateRepo checks the task against the object database at gitDir: the
// merge and base commits resolve, base is the first parent of merge, and every
// hidden file exists at the merge with the declared blob. Call Validate first.
func (t *Task) ValidateRepo(gitDir string) error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	merge := ""
	base := ""
	if t.Source.Merge == "" {
		add("source.merge is empty")
	} else if sha, err := gitLine(gitDir, "rev-parse", "--verify", t.Source.Merge+"^{commit}"); err != nil {
		add("merge %s does not resolve: %v", t.Source.Merge, err)
	} else {
		merge = sha
	}
	if t.Source.Base == "" {
		add("source.base is empty")
	} else if sha, err := gitLine(gitDir, "rev-parse", "--verify", t.Source.Base+"^{commit}"); err != nil {
		add("base %s does not resolve: %v", t.Source.Base, err)
	} else {
		base = sha
	}
	if merge != "" && base != "" {
		if parent, err := gitLine(gitDir, "rev-parse", "--verify", t.Source.Merge+"^1"); err != nil {
			add("merge %s has no first parent: %v", t.Source.Merge, err)
		} else if parent != base {
			add("base %s is not merge^1 (%s)", t.Source.Base, parent)
		}
	}
	for _, hidden := range t.Grade.Hidden {
		blob, err := gitLine(gitDir, "rev-parse", "--verify", t.Source.Merge+":"+hidden.Path)
		if err != nil {
			add("hidden %s does not exist at merge %s: %v", hidden.Path, t.Source.Merge, err)
			continue
		}
		if blob != hidden.Blob {
			add("hidden %s blob is %s, merge has %s", hidden.Path, hidden.Blob, blob)
		}
	}

	return problemsError(problems)
}

// Prompt is what a run receives: the task's prompt, then the interface
// paragraph when the task has one, then the footer, separated by blank lines.
// The task's why text is never sent.
func Prompt(t *Task, footer string) string {
	parts := []string{strings.TrimSpace(t.Prompt)}
	if paragraph := strings.TrimSpace(t.Interface); paragraph != "" {
		parts = append(parts, paragraph)
	}
	parts = append(parts, strings.TrimSpace(footer))
	return strings.Join(parts, "\n\n")
}

var (
	idPattern    = regexp.MustCompile(`^pr-\d{4}$`)
	blobPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	countPattern = regexp.MustCompile(`^\d+/\d+$`)
)

func (t *Task) applyDefaults() {
	if t.Limits.MaxSteps == 0 {
		t.Limits.MaxSteps = DefaultMaxSteps
	}
	if t.Limits.Timeout == 0 {
		t.Limits.Timeout = Duration(DefaultTimeout)
	}
}

func problemsError(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "\n"))
}

// gitLine runs one git command in the repository and returns its trimmed
// standard output.
func gitLine(gitDir string, args ...string) (string, error) {
	return gitx.Output(context.Background(), gitDir, args...)
}
