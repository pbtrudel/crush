package route

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/toolchain"
)

// blockedEnv are agent and session sockets the jail cannot isolate: a
// filesystem unix socket would survive the network namespace, so a route
// may not pass one and the child environment never carries one.
var blockedEnv = []string{
	"SSH_AUTH_SOCK",
	"DOCKER_HOST",
	"WSL_INTEROP",
	"DBUS_SESSION_BUS_ADDRESS",
	"XDG_RUNTIME_DIR",
	"GPG_AGENT_INFO",
}

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
// parent, in particular the user's tokens, stays out. The go toolchain
// go.mod selects comes first on PATH, so a go the model runs uses the
// selected version and GOTOOLCHAIN=local does not refuse it. GOCACHE and
// GOMODCACHE are set from the parent or from go's own env file when either
// has them, so the child's empty HOME does not leave it without the user's
// caches.
func (r *Route) ChildEnv(h *home.Home, dirs Dirs, parent []string) ([]string, error) {
	return r.childEnv(h, dirs, parent, toolchain.GOROOT)
}

// childEnv builds ChildEnv, with resolve naming the toolchain that goes first
// on the child's PATH.
func (r *Route) childEnv(h *home.Home, dirs Dirs, parent []string, resolve toolchain.Resolver) ([]string, error) {
	root := toolchain.Root(parent, resolve)
	parentVars := make(map[string]string, len(parent))
	for _, kv := range parent {
		if k, v, ok := strings.Cut(kv, "="); ok {
			parentVars[k] = v
		}
	}

	for _, name := range r.Env {
		if slices.Contains(runnerEnv, name) || slices.Contains(blockedEnv, name) {
			return nil, fmt.Errorf("route %s: env must not set %s", r.Name, name)
		}
	}

	env := make([]string, 0, 14+len(r.Env))
	add := func(key, value string) {
		env = append(env, key+"="+value)
	}
	// A cache neither the parent nor the selected go sets is left out: an
	// empty GOCACHE makes go refuse to build.
	addIfSet := func(key, value string) {
		if value != "" {
			add(key, value)
		}
	}

	if _, ok := parentVars["PATH"]; !ok {
		return nil, fmt.Errorf("parent environment has no PATH")
	}
	add("PATH", toolchain.PathWithRoot(parent, root))
	if dirs.Home == "" {
		return nil, fmt.Errorf("run has no HOME directory")
	}
	add("HOME", dirs.Home)
	if lang, ok := parentVars["LANG"]; ok {
		add("LANG", lang)
	}
	// The user's go env file holds the caches, and the child's HOME is its
	// own empty directory, so go in the child would otherwise build without
	// a build cache and find no downloaded module.
	addIfSet("GOCACHE", toolchain.ValueWithRoot(parent, "GOCACHE", root))
	addIfSet("GOMODCACHE", toolchain.ValueWithRoot(parent, "GOMODCACHE", root))
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
