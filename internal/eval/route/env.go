package route

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/home"
)

// runnerEnv are the variables the runner sets itself. A route may not
// name one of them. CRUSH_CLIENT_SERVER, GH_TOKEN and GITHUB_TOKEN are
// never passed at all, so a child runs standalone and holds no GitHub
// credentials even when the parent holds them.
var runnerEnv = []string{
	"PATH",
	"HOME",
	"LANG",
	"GOCACHE",
	"GOMODCACHE",
	"CRUSH_GLOBAL_CONFIG",
	"CRUSH_GLOBAL_DATA",
	"CRUSH_SKILLS_DIR",
	"CRUSH_DISABLE_METRICS",
	"CRUSH_CLIENT_SERVER",
	"GH_CONFIG_DIR",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GOPROXY",
	"GOFLAGS",
	"GOTOOLCHAIN",
	"CLAUDE_CONFIG_DIR",
}

// Dirs are the per-run directories a child gets: its own empty HOME and the
// directory that holds its generated global config.
type Dirs struct {
	Home string
	// Config is CRUSH_GLOBAL_CONFIG, which holds the run's own crush.json.
	Config string
}

// ChildEnv builds the environment of one run from scratch. Only the
// variables the run needs are passed: PATH and the toolchain settings, the
// route's own variables, and the paths of dirs and the eval home. The
// child's HOME is its run's own directory, so a tool it starts cannot read
// the user's ~/.config/crush, ~/.config/gh or ~/.claude. Everything else in
// parent, in particular the user's tokens, stays out.
func (r *Route) ChildEnv(h *home.Home, dirs Dirs, parent []string) ([]string, error) {
	parentVars := make(map[string]string, len(parent))
	for _, kv := range parent {
		if k, v, ok := strings.Cut(kv, "="); ok {
			parentVars[k] = v
		}
	}

	for _, name := range r.Env {
		if slices.Contains(runnerEnv, name) {
			return nil, fmt.Errorf("route %s: env must not set %s", r.Name, name)
		}
	}

	env := make([]string, 0, 14+len(r.Env))
	add := func(key, value string) {
		env = append(env, key+"="+value)
	}

	path, ok := parentVars["PATH"]
	if !ok {
		return nil, fmt.Errorf("parent environment has no PATH")
	}
	add("PATH", path)
	if dirs.Home == "" {
		return nil, fmt.Errorf("run has no HOME directory")
	}
	add("HOME", dirs.Home)
	for _, key := range []string{"LANG", "GOCACHE", "GOMODCACHE"} {
		if value, ok := parentVars[key]; ok {
			add(key, value)
		}
	}
	for _, name := range r.Env {
		value, ok := parentVars[name]
		if !ok {
			return nil, fmt.Errorf("route %s: %s is not set in the parent environment", r.Name, name)
		}
		add(name, value)
	}

	add("CRUSH_GLOBAL_CONFIG", dirs.Config)
	add("CRUSH_GLOBAL_DATA", h.DataDir())
	add("CRUSH_SKILLS_DIR", h.SkillsDir())
	add("CRUSH_DISABLE_METRICS", "1")
	add("GH_CONFIG_DIR", h.GHDir())
	add("GOPROXY", "off")
	add("GOFLAGS", "-buildvcs=false")
	add("GOTOOLCHAIN", "local")
	if r.Provider == "claude-agent" {
		add("CLAUDE_CONFIG_DIR", h.ClaudeDir())
	}
	return env, nil
}
