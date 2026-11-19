package route

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/home"
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

	env, err := r.ChildEnv(h, parentEnv)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"PATH":                  "/usr/local/bin:/usr/bin:/bin",
		"HOME":                  "/home/someone",
		"LANG":                  "en_US.UTF-8",
		"GOCACHE":               "/home/someone/.cache/go-build",
		"GOMODCACHE":            "/home/someone/go/pkg/mod",
		"DEEPSEEK_API_KEY":      "sk-parent-value",
		"CRUSH_GLOBAL_CONFIG":   h.ConfigDir(),
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

	env, err := r.ChildEnv(h, parentEnv)
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
			name:    "no HOME",
			route:   &Route{Name: "r"},
			parent:  []string{"PATH=/bin"},
			wantErr: "no HOME",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := tt.route.ChildEnv(home.At("/state/crush-eval"), tt.parent)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
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
