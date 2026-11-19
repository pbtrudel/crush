package route

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const flashRoute = `name: deepseek-flash
family: deepseek
provider: commandcode
model: deepseek/deepseek-v4.1-flash
small_model: deepseek/deepseek-v4.1-flash
reasoning_effort: ""
provider_config:
  api_key: "$DEEPSEEK_API_KEY"
env: [DEEPSEEK_API_KEY]
concurrency: 3
limits: {runs_per_window: 100, window: 3600}
`

func TestLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeRoute(t, dir, "deepseek-flash", flashRoute)

	r, err := Load(dir, "deepseek-flash")
	require.NoError(t, err)
	require.Equal(t, "deepseek-flash", r.Name)
	require.Equal(t, "deepseek", r.Family)
	require.Equal(t, "commandcode", r.Provider)
	require.Equal(t, "deepseek/deepseek-v4.1-flash", r.Model)
	require.Equal(t, "deepseek/deepseek-v4.1-flash", r.SmallModel)
	require.Empty(t, r.ReasoningEffort)
	require.Equal(t, "$DEEPSEEK_API_KEY", r.ProviderConfig["api_key"])
	require.Equal(t, []string{"DEEPSEEK_API_KEY"}, r.Env)
	require.Equal(t, 3, r.Concurrency)
	require.Equal(t, Limits{RunsPerWindow: 100, Window: 3600}, r.Limits)
}

func TestLoadDefaultsConcurrency(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeRoute(t, dir, "flash", `name: flash
provider: hyper
model: glm-5.3
`)
	r, err := Load(dir, "flash")
	require.NoError(t, err)
	require.Equal(t, 1, r.Concurrency)
	require.Equal(t, Limits{}, r.Limits)
}

func TestLoadNestedEnvReference(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeRoute(t, dir, "hyper", `name: hyper
provider: hyper
model: glm-5.3
provider_config:
  extra_headers: {X-Api-Key: "${HYPER_KEY}"}
env: [HYPER_KEY]
`)
	_, err := Load(dir, "hyper")
	require.NoError(t, err)
}

func TestLoadRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		file    string
		body    string
		wantErr string
	}{
		{
			name:    "name mismatch",
			file:    "flash",
			body:    "name: other\nprovider: hyper\nmodel: glm-5.3\n",
			wantErr: "does not match file",
		},
		{
			name:    "missing provider",
			file:    "flash",
			body:    "name: flash\nmodel: glm-5.3\n",
			wantErr: "provider is required",
		},
		{
			name:    "missing model",
			file:    "flash",
			body:    "name: flash\nprovider: hyper\n",
			wantErr: "model is required",
		},
		{
			name:    "env not listed",
			file:    "flash",
			body:    "name: flash\nprovider: hyper\nmodel: glm-5.3\nprovider_config:\n  api_key: $HYPER_KEY\n",
			wantErr: "HYPER_KEY",
		},
		{
			name:    "nested env not listed",
			file:    "flash",
			body:    "name: flash\nprovider: hyper\nmodel: glm-5.3\nprovider_config:\n  extra_headers: {X-Api-Key: $HYPER_KEY}\n",
			wantErr: "HYPER_KEY",
		},
		{
			name:    "unknown field",
			file:    "flash",
			body:    "name: flash\nprovider: hyper\nmodel: glm-5.3\ntemperature: 1\n",
			wantErr: "temperature",
		},
		{
			name:    "concurrency below one",
			file:    "flash",
			body:    "name: flash\nprovider: hyper\nmodel: glm-5.3\nconcurrency: -1\n",
			wantErr: "concurrency",
		},
		{
			name:    "commandcode-claude",
			file:    "flash",
			body:    "name: flash\nprovider: commandcode-claude\nmodel: deepseek/deepseek-v4.1-flash\n",
			wantErr: "commandcode-claude is not used",
		},
		{
			name:    "commandcode other model",
			file:    "flash",
			body:    "name: flash\nprovider: commandcode\nmodel: glm-5.3\n",
			wantErr: "deepseek/deepseek-v4.1-flash",
		},
		{
			name:    "missing file",
			file:    "absent",
			wantErr: "no such file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tt.body != "" {
				writeRoute(t, dir, tt.file, tt.body)
			}
			_, err := Load(dir, tt.file)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func writeRoute(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600))
}
