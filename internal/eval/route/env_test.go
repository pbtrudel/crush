package route

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/charmbracelet/crush/internal/eval/toolchain"
	"github.com/stretchr/testify/require"
)

// parentEnv is a fake parent process environment. It holds secrets and
// variables the child must never see.
var parentEnv = []string{
	"PATH=/usr/local/bin:/usr/bin:/bin",
	"HOME=/home/someone",
	"LANG=en_US.UTF-8",
	"GOCACHE=/home/someone/.cache/go-build",
	"GOMODCACHE=/home/someone/go/pkg/mod",
	"DEEPSEEK_API_KEY=sk-parent-value",
	"CLAUDE_TOKEN=claude-parent-value",
	"GH_TOKEN=gh-parent-token",
	"GITHUB_TOKEN=gh-parent-token",
	"CRUSH_CLIENT_SERVER=1",
	"UNRELATED=1",
}

func TestChildEnv(t *testing.T) {
	t.Parallel()

	h := home.At("/state/crush-eval")
	r := &Route{Name: "deepseek-flash", Provider: "commandcode", Env: []string{"DEEPSEEK_API_KEY"}}

	dirs := Dirs{Home: "/state/crush-eval/runs/r1/home", Config: "/state/crush-eval/runs/r1/config/crush"}
	env, err := r.childEnv(h, dirs, parentEnv, toolchainAt("/toolchains/go1.27.2"))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"PATH":                  "/toolchains/go1.27.2/bin:/usr/local/bin:/usr/bin:/bin",
		"HOME":                  dirs.Home,
		"LANG":                  "en_US.UTF-8",
		"GOCACHE":               "/home/someone/.cache/go-build",
		"GOMODCACHE":            "/home/someone/go/pkg/mod",
		"DEEPSEEK_API_KEY":      "sk-parent-value",
		"CRUSH_GLOBAL_CONFIG":   dirs.Config,
		"CRUSH_GLOBAL_DATA":     h.DataDir(),
		"CRUSH_SKILLS_DIR":      h.SkillsDir(),
		"CRUSH_DISABLE_METRICS": "1",
		"GH_CONFIG_DIR":         h.GHDir(),
		"GOPROXY":               "off",
		"GOFLAGS":               "-buildvcs=false",
		"GOTOOLCHAIN":           "local",
	}, envMap(env))
	require.NotContains(t, envMap(env), "GH_TOKEN")
	require.NotContains(t, envMap(env), "GITHUB_TOKEN")
	require.NotContains(t, envMap(env), "CRUSH_CLIENT_SERVER")
	require.NotContains(t, envMap(env), "UNRELATED")
}

func TestChildEnvClaudeAgent(t *testing.T) {
	t.Parallel()

	h := home.At("/state/crush-eval")
	r := &Route{Name: "claude-sonnet", Provider: "claude-agent", Env: []string{"CLAUDE_TOKEN"}}

	env, err := r.childEnv(h, Dirs{Home: "/run/home", Config: "/run/config"}, parentEnv, toolchainAt("/toolchains/go1.27.2"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(h.Dir(), "claude"), envMap(env)["CLAUDE_CONFIG_DIR"])
	require.Equal(t, h.Dir(), filepath.Dir(envMap(env)["CLAUDE_CONFIG_DIR"]))
}

func TestChildEnvRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		route   *Route
		parent  []string
		wantErr string
	}{
		{
			name:    "GH_TOKEN",
			route:   &Route{Name: "r", Env: []string{"GH_TOKEN"}},
			parent:  parentEnv,
			wantErr: "must not set GH_TOKEN",
		},
		{
			name:    "GITHUB_TOKEN",
			route:   &Route{Name: "r", Env: []string{"GITHUB_TOKEN"}},
			parent:  parentEnv,
			wantErr: "must not set GITHUB_TOKEN",
		},
		{
			name:    "CRUSH_CLIENT_SERVER",
			route:   &Route{Name: "r", Env: []string{"CRUSH_CLIENT_SERVER"}},
			parent:  parentEnv,
			wantErr: "must not set CRUSH_CLIENT_SERVER",
		},
		{
			name:    "runner owned variable",
			route:   &Route{Name: "r", Env: []string{"GOPROXY"}},
			parent:  parentEnv,
			wantErr: "must not set GOPROXY",
		},
		{
			name:    "no HOME directory",
			route:   &Route{Name: "r"},
			parent:  parentEnv,
			wantErr: "no HOME directory",
		},
		{
			name:    "route variable missing from parent",
			route:   &Route{Name: "r", Env: []string{"ABSENT_KEY"}},
			parent:  parentEnv,
			wantErr: "ABSENT_KEY is not set in the parent",
		},
		{
			name:    "no PATH",
			route:   &Route{Name: "r"},
			parent:  []string{"HOME=/home/someone"},
			wantErr: "no PATH",
		},
		{
			name:    "HOME in the route",
			route:   &Route{Name: "r", Env: []string{"HOME"}},
			parent:  parentEnv,
			wantErr: "must not set HOME",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dirs := Dirs{Home: "/state/crush-eval/runs/r1/home", Config: "/state/crush-eval/runs/r1/config/crush"}
			if tt.wantErr == "no HOME directory" {
				dirs.Home = ""
			}
			_, err := tt.route.childEnv(home.At("/state/crush-eval"), dirs, tt.parent, toolchainAt("/toolchains/go1.27.2"))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestChildEnvSelectsToolchain checks the environment a run really builds:
// the go of the toolchain go.mod selects comes first on PATH, so a go the
// model runs does not fail on GOTOOLCHAIN=local.
func TestChildEnvSelectsToolchain(t *testing.T) {
	t.Parallel()

	h := home.At("/state/crush-eval")
	r := &Route{Name: "deepseek-flash", Provider: "commandcode"}
	env, err := r.ChildEnv(h, Dirs{Home: "/run/home", Config: "/run/config"}, parentEnv)
	require.NoError(t, err)

	path := envMap(env)["PATH"]
	require.True(t, strings.HasSuffix(path, string(os.PathListSeparator)+"/usr/local/bin:/usr/bin:/bin"),
		"PATH %q does not keep the parent's", path)
	require.NotEqual(t, "/usr/local/bin:/usr/bin:/bin", path, "the selected toolchain comes first on PATH")
}

// TestChildEnvResolvesCaches checks the environment a run really builds when
// the user set the caches only in go's own env file: the child's HOME is its
// own empty directory, so go in the child needs both caches from the parent
// or an offline module lookup fails.
func TestChildEnvResolvesCaches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fakeGo(t, root)
	h := home.At("/state/crush-eval")
	r := &Route{Name: "deepseek-flash", Provider: "commandcode"}
	parent := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/home/someone"}

	env, err := r.childEnv(h, Dirs{Home: "/run/home", Config: "/run/config"}, parent, toolchainAt(root))
	require.NoError(t, err)
	require.Equal(t, "/fake/GOCACHE", envMap(env)["GOCACHE"])
	require.Equal(t, "/fake/GOMODCACHE", envMap(env)["GOMODCACHE"])
}

// fakeGo writes a go in root's bin that reports /fake/<name> for the go env
// name it is asked for.
func fakeGo(t *testing.T, root string) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	script := "#!/bin/sh\necho \"/fake/$2\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o700))
}

// toolchainAt is a toolchain resolver that names root.
func toolchainAt(root string) toolchain.Resolver {
	return func([]string) (string, error) { return root, nil }
}

// envMap turns a KEY=VALUE list into a map for assertions.
func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}
