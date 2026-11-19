package route

import (
	"testing"

	"github.com/charmbracelet/crush/internal/eval/home"
	"github.com/stretchr/testify/require"
)

func TestLoginCommand(t *testing.T) {
	t.Parallel()

	h := home.At("/state/crush-eval")
	r := &Route{Name: "gpt-6-luna", Provider: "copilot", Model: "gpt-6-luna", Env: []string{"DEEPSEEK_API_KEY"}}

	cmd, err := r.LoginCommand(t.Context(), h, "/usr/local/bin/crush", parentEnv)
	require.NoError(t, err)
	require.Nil(t, cmd.Process)
	require.Equal(t, []string{"/usr/local/bin/crush", "login", "copilot"}, cmd.Args)

	env := envMap(cmd.Env)
	require.Equal(t, h.Dir(), env["HOME"])
	require.Equal(t, h.DataDir(), env["CRUSH_GLOBAL_DATA"])
	require.Equal(t, h.ConfigDir(), env["CRUSH_GLOBAL_CONFIG"])
	require.Equal(t, "sk-parent-value", env["DEEPSEEK_API_KEY"])
	require.NotContains(t, env, "GH_TOKEN")
	require.NotContains(t, env, "GITHUB_TOKEN")
	require.NotContains(t, env, "CRUSH_CLIENT_SERVER")
}

func TestLoginCommandRejectsReservedEnv(t *testing.T) {
	t.Parallel()

	r := &Route{Name: "gpt-6-luna", Provider: "copilot", Env: []string{"GH_TOKEN"}}
	_, err := r.LoginCommand(t.Context(), home.At(t.TempDir()), "crush", parentEnv)
	require.ErrorContains(t, err, "must not set GH_TOKEN")
}
