package model

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// agentTeamsWorkspace records what the Agent Teams toggle writes and reports
// the merged setting the way the config loader would: a workspace config file
// that sets the option outranks the global file.
type agentTeamsWorkspace struct {
	*agentViewerUIWorkspace

	// dataDir holds the workspace config file, as the real data directory
	// would.
	dataDir string

	mu        sync.Mutex
	global    *bool
	workspace *bool
	writes    []agentTeamsWrite
	reloads   int
	reloadErr error
	busy      bool
}

// agentTeamsWrite is one config write the toggle made.
type agentTeamsWrite struct {
	scope config.Scope
	value bool
}

func (w *agentTeamsWorkspace) Config() *config.Config {
	w.mu.Lock()
	defer w.mu.Unlock()
	enabled := true
	switch {
	case w.workspace != nil:
		enabled = *w.workspace
	case w.global != nil:
		enabled = *w.global
	}
	return &config.Config{Options: &config.Options{AgentTeams: &enabled, DataDirectory: w.dataDir}}
}

func (w *agentTeamsWorkspace) SetConfigField(scope config.Scope, key string, value any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	enabled, ok := value.(bool)
	if !ok || key != "options.agent_teams" {
		return nil
	}
	if scope == config.ScopeWorkspace {
		w.workspace = &enabled
		w.writeWorkspaceFile(enabled)
	} else {
		w.global = &enabled
	}
	w.writes = append(w.writes, agentTeamsWrite{scope: scope, value: enabled})
	return nil
}

// writeWorkspaceFile keeps the workspace config file in step with the writes,
// since the toggle decides the scope from that file's contents. The caller
// holds the lock.
func (w *agentTeamsWorkspace) writeWorkspaceFile(enabled bool) {
	data := fmt.Sprintf(`{"options":{"agent_teams":%t}}`, enabled)
	_ = os.WriteFile(config.WorkspaceConfigPath(w.dataDir), []byte(data), 0o600)
}

func (w *agentTeamsWorkspace) AgentIsReady() bool { return true }

func (w *agentTeamsWorkspace) AgentModel() workspace.AgentModel { return workspace.AgentModel{} }

func (w *agentTeamsWorkspace) PermissionMode() permission.PermissionMode {
	return permission.PermissionModeNormal
}

func (w *agentTeamsWorkspace) AgentIsBusy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.busy
}

func (w *agentTeamsWorkspace) ReloadConfig(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reloads++
	return w.reloadErr
}

func (w *agentTeamsWorkspace) ListSkills(context.Context) ([]skills.CatalogEntry, error) {
	return nil, nil
}

func (w *agentTeamsWorkspace) reloadCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.reloads
}

// newAgentTeamsTest builds a UI whose workspace records the toggle's writes.
func newAgentTeamsTest(t *testing.T) (*UI, *agentTeamsWorkspace) {
	t.Helper()
	u, viewerWorkspace := newAgentViewerUITest(t)
	ws := &agentTeamsWorkspace{agentViewerUIWorkspace: viewerWorkspace, dataDir: t.TempDir()}
	u.com.Workspace = ws
	return u, ws
}

// applyToggle runs the toggle's command and feeds its messages back to the
// UI, the way the program loop would.
func applyToggle(t *testing.T, u *UI, enabled bool) {
	t.Helper()
	for _, msg := range runCmdCollect(u.setAgentTeams(enabled)) {
		if msg == nil {
			continue
		}
		if _, cmd := u.Update(msg); cmd != nil {
			runCmdCollect(cmd)
		}
	}
}

// A reload refused because the agent is busy is not lost: it runs once the
// agent goes idle, which is when the coordinator can swap its tools and
// prompt.
func TestAgentTeamsToggleRetriesTheReloadOnceTheAgentIsIdle(t *testing.T) {
	t.Parallel()

	u, ws := newAgentTeamsTest(t)
	u.agentBusyCache.set(true)
	ws.reloadErr = app.ErrConfigReloadBusy

	applyToggle(t, u, false)
	require.True(t, u.pendingConfigReload, "the refused reload is remembered")
	require.Equal(t, 1, ws.reloadCount(), "the toggle tried to reload once and was refused")
	require.False(t, u.com.Config().Options.GetAgentTeams(),
		"the write landed, so the agents still have to be reloaded")

	ws.mu.Lock()
	ws.reloadErr = nil
	ws.mu.Unlock()

	_, cmd := u.Update(busyStateMsg{gen: u.busyFetchGen, agentBusy: false, permMode: permission.PermissionModeNormal})
	require.NotNil(t, cmd, "the idle edge runs the reload")
	for _, msg := range runCmdCollect(cmd) {
		if msg == nil {
			continue
		}
		_, _ = u.Update(msg)
	}

	require.False(t, u.pendingConfigReload)
	require.Equal(t, 2, ws.reloadCount(), "the idle edge ran the reload again")
}

// A successful reload leaves nothing pending, and the busy refusal of a plain
// reload is still reported rather than retried.
func TestAgentTeamsReloadPendingOnlyOnBusyToggle(t *testing.T) {
	t.Parallel()

	u, ws := newAgentTeamsTest(t)
	_, _ = u.Update(configReloadedMsg{err: app.ErrConfigReloadBusy})
	require.False(t, u.pendingConfigReload, "a plain reload keeps reporting the refusal")

	_, _ = u.Update(configReloadedMsg{retryWhenIdle: true})
	require.False(t, u.pendingConfigReload, "a reload that worked leaves nothing behind")
	require.Zero(t, ws.reloadCount())
}

// A retry that is refused again keeps the reload pending instead of turning
// the second refusal into an error, and the next idle edge applies it.
func TestAgentTeamsReloadRetryStaysArmedWhileBusy(t *testing.T) {
	t.Parallel()

	u, ws := newAgentTeamsTest(t)
	u.agentBusyCache.set(true)
	ws.mu.Lock()
	ws.reloadErr = app.ErrConfigReloadBusy
	ws.mu.Unlock()

	msgs := runCmdCollect(u.setAgentTeams(false))
	require.Len(t, msgs, 1)
	_, _ = u.Update(msgs[0])
	require.True(t, u.pendingConfigReload)

	// The idle edge fires the retry, which the agent refuses again.
	_, cmd := u.Update(busyStateMsg{gen: u.busyFetchGen, agentBusy: false, permMode: permission.PermissionModeNormal})
	require.NotNil(t, cmd)
	require.Equal(t, 1, ws.reloadCount(), "nothing was reloaded before the edge")
	for _, msg := range runCmdCollect(cmd) {
		require.NotNil(t, msg)
		_, next := u.Update(msg)
		for _, out := range runCmdCollect(next) {
			if info, ok := out.(util.InfoMsg); ok {
				require.NotEqual(t, util.InfoTypeError, info.Type, "a second busy result is not an error")
			}
		}
	}
	require.Equal(t, 2, ws.reloadCount())
	require.True(t, u.pendingConfigReload, "a second busy result re-arms the retry")

	ws.mu.Lock()
	ws.reloadErr = nil
	ws.mu.Unlock()
	_, cmd = u.Update(busyStateMsg{gen: u.busyFetchGen, agentBusy: false, permMode: permission.PermissionModeNormal})
	require.NotNil(t, cmd)
	for _, msg := range runCmdCollect(cmd) {
		if msg != nil {
			_, _ = u.Update(msg)
		}
	}
	require.False(t, u.pendingConfigReload)
	require.Equal(t, 3, ws.reloadCount())
	require.False(t, u.com.Config().Options.GetAgentTeams())
}

// When the memoized state already reads idle, a refusal cannot wait for a
// busy-to-idle edge that will never come: the agent is probed instead.
func TestAgentTeamsReloadRetryArmsWhenTheCacheReadsIdle(t *testing.T) {
	t.Parallel()

	u, ws := newAgentTeamsTest(t)
	u.agentBusyCache.set(false)
	ws.mu.Lock()
	ws.reloadErr = app.ErrConfigReloadBusy
	ws.mu.Unlock()

	msgs := runCmdCollect(u.setAgentTeams(false))
	require.Len(t, msgs, 1)
	_, cmd := u.Update(msgs[0])
	require.True(t, u.pendingConfigReload)
	require.NotNil(t, cmd, "the refusal probes the agent instead of waiting for an edge")

	ws.mu.Lock()
	ws.reloadErr = nil
	ws.mu.Unlock()
	var probed busyStateMsg
	found := false
	for _, msg := range runCmdCollect(cmd) {
		if state, ok := msg.(busyStateMsg); ok {
			probed, found = state, true
		}
	}
	require.True(t, found, "the refusal dispatches a busy probe")
	require.False(t, probed.agentBusy)

	_, retry := u.Update(probed)
	require.NotNil(t, retry, "an idle probe runs the pending reload")
	for _, msg := range runCmdCollect(retry) {
		if msg != nil {
			_, _ = u.Update(msg)
		}
	}
	require.False(t, u.pendingConfigReload)
	require.Equal(t, 2, ws.reloadCount())
	require.False(t, u.com.Config().Options.GetAgentTeams())
}
