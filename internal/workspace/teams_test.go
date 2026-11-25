package workspace

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// teamsRecorder records the scopes and values WriteAgentTeams writes.
type teamsRecorder struct {
	Workspace

	cfg *config.Config

	mu    sync.Mutex
	calls []string
}

func (r *teamsRecorder) Config() *config.Config { return r.cfg }

func (r *teamsRecorder) SetConfigField(scope config.Scope, key string, value any) error {
	enabled, _ := value.(bool)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "SetConfigField:"+scope.String()+":"+key+":"+strconv.FormatBool(enabled))
	return nil
}

// callLog returns a copy of the recorded writes.
func (r *teamsRecorder) callLog() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func TestWriteAgentTeamsWritesGlobal(t *testing.T) {
	t.Parallel()
	ws := &teamsRecorder{cfg: &config.Config{Options: &config.Options{DataDirectory: t.TempDir()}}}
	require.NoError(t, WriteAgentTeams(ws, true))
	require.Equal(t, []string{"SetConfigField:global:options.agent_teams:true"}, ws.callLog())
}

func TestWriteAgentTeamsAlsoWritesTheWorkspace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "crush.json"), []byte(`{"options":{"agent_teams":false}}`), 0o600))
	ws := &teamsRecorder{cfg: &config.Config{Options: &config.Options{DataDirectory: dir}}}
	require.NoError(t, WriteAgentTeams(ws, true))
	require.Equal(t, []string{
		"SetConfigField:global:options.agent_teams:true",
		"SetConfigField:workspace:options.agent_teams:true",
	}, ws.callLog(), "the workspace file that sets the option outranks the global one")
}
