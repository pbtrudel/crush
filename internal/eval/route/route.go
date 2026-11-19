// Package route loads route files and derives the generated configuration
// and the child environment of one route (design section 3.3).
package route

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// flashModel is the only model the commandcode provider may serve. The
// owner keeps that provider for the DeepSeek Flash route alone.
const flashModel = "deepseek/deepseek-v4.1-flash"

// Route is one way to reach a model.
type Route struct {
	Name            string         `yaml:"name"`
	Family          string         `yaml:"family"`
	Provider        string         `yaml:"provider"`
	Model           string         `yaml:"model"`
	SmallModel      string         `yaml:"small_model"`
	ReasoningEffort string         `yaml:"reasoning_effort"`
	ProviderConfig  map[string]any `yaml:"provider_config"`
	Env             []string       `yaml:"env"`
	Concurrency     int            `yaml:"concurrency"`
	Limits          Limits         `yaml:"limits"`
}

// Limits bounds how fast a route's runs are started.
type Limits struct {
	// RunsPerWindow bounds the starts per Window seconds. 0 means none.
	RunsPerWindow int `yaml:"runs_per_window"`
	// Window is the length of the window in seconds. 0 means none.
	Window int `yaml:"window"`
}

// Load reads routes/<name>.yaml from dir and validates it.
func Load(dir, name string) (*Route, error) {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("invalid route name %q", name)
	}
	path := filepath.Join(dir, name+".yaml")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open route: %w", err)
	}
	defer f.Close() //nolint:errcheck // Read-only.

	var r Route
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("parse route %s: %w", path, err)
	}
	if r.Concurrency == 0 {
		r.Concurrency = 1
	}
	if err := r.validate(name); err != nil {
		return nil, fmt.Errorf("route %s: %w", path, err)
	}
	return &r, nil
}

func (r *Route) validate(name string) error {
	if r.Name != name {
		return fmt.Errorf("name %q does not match file %q", r.Name, name+".yaml")
	}
	if r.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if r.Model == "" {
		return fmt.Errorf("model is required")
	}
	if r.Provider == "commandcode-claude" {
		return fmt.Errorf("provider commandcode-claude is not used by the suite")
	}
	if r.Provider == "commandcode" && r.Model != flashModel {
		return fmt.Errorf("provider commandcode serves only model %s, got %s", flashModel, r.Model)
	}
	if r.Concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1, got %d", r.Concurrency)
	}

	listed := make(map[string]bool, len(r.Env))
	for _, name := range r.Env {
		listed[name] = true
	}
	var missing []string
	for _, ref := range envRefs(r.ProviderConfig) {
		if !listed[ref] {
			missing = append(missing, ref)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		missing = slices.Compact(missing)
		return fmt.Errorf("provider_config references $%s but env does not list it", strings.Join(missing, ", $"))
	}
	return nil
}

// envRef is a $NAME or ${NAME} reference, as Crush resolves it.
var envRef = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`)

// envRefs lists every variable name referenced by the strings in v,
// including nested maps and lists.
func envRefs(v any) []string {
	var refs []string
	switch t := v.(type) {
	case string:
		for _, m := range envRef.FindAllStringSubmatch(t, -1) {
			refs = append(refs, m[1])
		}
	case map[string]any:
		for _, v := range t {
			refs = append(refs, envRefs(v)...)
		}
	case []any:
		for _, v := range t {
			refs = append(refs, envRefs(v)...)
		}
	}
	return refs
}
