package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The workspace scope is the machine-owned JSON in the data directory, so the
// UI can tell whether it sets an option without the merged config, which a
// busy agent leaves stale.
func TestWorkspaceConfigSets(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	require.False(t, WorkspaceConfigSets(dataDir, "options.agent_teams"),
		"a missing workspace config sets nothing")

	path := WorkspaceConfigPath(dataDir)
	require.Equal(t, filepath.Join(dataDir, "crush.json"), path)
	require.NoError(t, os.WriteFile(path, []byte(`{"options":{"agent_teams":false}}`), 0o600))

	require.True(t, WorkspaceConfigSets(dataDir, "options.agent_teams"))
	require.False(t, WorkspaceConfigSets(dataDir, "options.disable_notifications"))
	require.False(t, WorkspaceConfigSets("", "options.agent_teams"))
}
