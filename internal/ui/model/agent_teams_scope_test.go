package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

func (w *agentTeamsWorkspace) setWorkspace(value bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.workspace = &value
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
