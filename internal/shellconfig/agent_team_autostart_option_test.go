package shellconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOption_AgentTeamAutostart pins the crushrc spelling of
// options.agent_team_autostart.
func TestOption_AgentTeamAutostart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "crushrc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte("option agent-team-autostart false\n"))
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))
	opts := result["options"].(map[string]any)
	require.Equal(t, false, opts["agent_team_autostart"])
}
