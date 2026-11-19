package score_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/eval/route"
	"github.com/charmbracelet/crush/internal/eval/score"
	"github.com/stretchr/testify/require"
)

// TestExperimentOverlayKeepsJSONTypes carries a boolean overlay from the
// experiment file into the generated global config: a config field that is
// a bool must stay a JSON bool, not the string "false".
func TestExperimentOverlayKeepsJSONTypes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "experiment.yaml")
	body := `date: 2026-11-19
name: overlay
variants:
  - {name: control, ref: base}
  - {name: variant, ref: topic, overlay: {agent_teams: false}}
routes: [hyper]
repetitions: 1
primary_metric: pass_rate
margin: 0.05
baseline: baseline.json
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	exp, err := score.LoadExperiment(path)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"agent_teams": false}, exp.Variants[1].Overlay)

	r := &route.Route{Name: "hyper", Provider: "hyper", Model: "glm-5.3"}
	config := filepath.Join(t.TempDir(), "crush", "crush.json")
	require.NoError(t, route.WriteGlobalConfig(config, r, exp.Variants[1].Overlay))

	raw, err := os.ReadFile(config)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"agent_teams": false`)
	require.NotContains(t, string(raw), `"agent_teams": "false"`)
}
