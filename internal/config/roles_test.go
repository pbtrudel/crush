package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/roles"
)

func TestSetupAgentsAddsRoles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs-writer.md"),
		[]byte("---\nname: docs-writer\ndescription: Writes docs.\ntools: view, bogus\n---\nWrite docs.\n"), 0o644))

	cfg := &Config{Options: &Options{}, RoleDirs: []roles.Dir{{Path: dir, Source: roles.SourceGlobal}}}
	cfg.SetupAgents()

	for _, name := range []string{AgentCoder, AgentTask, AgentPlan} {
		require.Nil(t, cfg.Agents[name].Role, name)
	}
	for _, name := range []string{"architect", "researcher", "reviewer", "implementer", "verifier", "docs-writer"} {
		agent, ok := cfg.Agents[name]
		require.True(t, ok, name)
		require.NotNil(t, agent.Role, name)
		require.Equal(t, name, agent.ID)
		require.Equal(t, cfg.Agents[AgentCoder].AllowedTools, agent.AllowedTools, name)
	}
	require.Equal(t, []string{"view"}, cfg.Agents["docs-writer"].Role.Tools)
	require.Len(t, cfg.RoleDiagnostics, 1)
	require.Contains(t, cfg.RoleDiagnostics[0].String(), `unknown tool "bogus" dropped`)
}

func TestBuiltinRoleToolsAreRealTools(t *testing.T) {
	t.Parallel()
	_, diags := roles.Discover(nil, AllToolNames())
	require.Empty(t, diags, "every exact tool name in a built-in role must exist")
}

func TestRoleDirsOrder(t *testing.T) {
	wd := t.TempDir()
	t.Setenv("CRUSH_AGENTS_DIR", filepath.Join(wd, "global-agents"))
	dirs := RoleDirs(wd)
	require.Equal(t, roles.Dir{Path: filepath.Join(wd, "global-agents"), Source: roles.SourceGlobal}, dirs[0])
	require.Equal(t, roles.Dir{Path: filepath.Join(wd, ".crush", "agents"), Source: roles.SourceProject}, dirs[len(dirs)-1],
		"the working directory is the highest priority")
}
