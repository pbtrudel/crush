package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/stretchr/testify/require"
)

// setWorkspace puts the option in the workspace config file, which is what
// makes the workspace scope outrank the global one.
func (w *agentTeamsWorkspace) setWorkspace(value bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.workspace = &value
	w.writeWorkspaceFile(value)
}

func (w *agentTeamsWorkspace) writtenScopes() []config.Scope {
	w.mu.Lock()
	defer w.mu.Unlock()
	scopes := make([]config.Scope, 0, len(w.writes))
	for _, write := range w.writes {
		scopes = append(scopes, write.scope)
	}
	return scopes
}

// The toggle writes the global file, and the workspace file too when that one
// sets the option and would otherwise win.
func TestAgentTeamsToggleWritesTheWorkspaceScopeWhenItSetsTheOption(t *testing.T) {
	t.Parallel()

	u, ws := newAgentTeamsTest(t)
	ws.setWorkspace(true)

	applyToggle(t, u, false)

	require.Equal(t, []config.Scope{config.ScopeGlobal, config.ScopeWorkspace}, ws.writtenScopes())
	require.False(t, u.com.Config().Options.GetAgentTeams(), "the toggle took effect")
}

// Without a workspace config that sets the option, the global write is all
// the toggle needs, so no workspace file appears.
func TestAgentTeamsToggleWritesOnlyTheGlobalScope(t *testing.T) {
	t.Parallel()

	u, ws := newAgentTeamsTest(t)

	applyToggle(t, u, false)

	require.Equal(t, []config.Scope{config.ScopeGlobal}, ws.writtenScopes())
	require.False(t, u.com.Config().Options.GetAgentTeams())
}

// A busy agent refuses the reload but must not cost the workspace write: the
// scope is decided from the workspace file, written alongside the global one,
// and the single reload is retried once the agent is idle.
func TestAgentTeamsToggleWritesTheWorkspaceScopeWhileBusy(t *testing.T) {
	t.Parallel()

	u, ws := newAgentTeamsTest(t)
	ws.setWorkspace(true)
	u.agentBusyCache.set(true)
	ws.mu.Lock()
	ws.reloadErr = app.ErrConfigReloadBusy
	ws.mu.Unlock()

	msgs := runCmdCollect(u.setAgentTeams(false))
	require.Len(t, msgs, 1)
	_, _ = u.Update(msgs[0])

	require.Equal(t, []config.Scope{config.ScopeGlobal, config.ScopeWorkspace}, ws.writtenScopes())
	require.Equal(t, 1, ws.reloadCount(), "the writes share one reload")
	require.True(t, u.pendingConfigReload, "the refused reload waits for the agent to idle")

	ws.mu.Lock()
	ws.reloadErr = nil
	ws.mu.Unlock()
	_, cmd := u.Update(busyStateMsg{gen: u.busyFetchGen, agentBusy: false, permMode: permission.PermissionModeNormal})
	require.NotNil(t, cmd)
	for _, msg := range runCmdCollect(cmd) {
		if msg != nil {
			_, _ = u.Update(msg)
		}
	}

	require.False(t, u.pendingConfigReload)
	require.Equal(t, 2, ws.reloadCount())
	require.False(t, u.com.Config().Options.GetAgentTeams(), "the idle retry applied the toggle")
}
