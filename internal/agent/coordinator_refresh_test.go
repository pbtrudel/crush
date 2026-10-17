package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/skills"
)

// TestCoordinatorRefreshSkills verifies a reload re-reads the skill
// manager snapshot and rebuilds each agent's tools against it, while
// keeping loaded-skill bookkeeping for skills that survive.
func TestCoordinatorRefreshSkills(t *testing.T) {
	t.Parallel()

	env := testEnv(t)

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()
	coderCfg := cfg.Config().Agents[config.AgentCoder]
	coderCfg.AllowedTools = []string{tools.CrushInfoToolName}
	cfg.Config().Agents[config.AgentCoder] = coderCfg

	oldSkill := &skills.Skill{Name: "old-skill", Description: "old"}
	newSkill := &skills.Skill{Name: "new-skill", Description: "new"}

	mgr := skills.NewManager([]*skills.Skill{oldSkill}, []*skills.Skill{oldSkill}, nil)
	t.Cleanup(mgr.Shutdown)

	oldTracker := skills.NewTracker([]*skills.Skill{oldSkill})
	oldTracker.MarkLoaded("old-skill")

	agent := &mockSessionAgent{}
	coord := &coordinator{
		cfg:          cfg,
		sessions:     env.sessions,
		messages:     env.messages,
		permissions:  env.permissions,
		history:      env.history,
		filetracker:  *env.filetracker,
		skillsMgr:    mgr,
		allSkills:    []*skills.Skill{oldSkill},
		activeSkills: []*skills.Skill{oldSkill},
		skillTracker: oldTracker,
		agents:       map[string]SessionAgent{config.AgentCoder: agent},
	}

	// A reload swaps discovery to a set that keeps old-skill and adds
	// new-skill.
	mgr.Replace(
		[]*skills.Skill{oldSkill, newSkill},
		[]*skills.Skill{oldSkill, newSkill},
		nil,
		nil,
		env.workingDir,
	)

	require.NoError(t, coord.RefreshSkills(t.Context()))

	require.Len(t, coord.allSkills, 2)
	require.Len(t, coord.activeSkills, 2)

	// Loaded bookkeeping survives for skills that are still active.
	require.True(t, coord.skillTracker.IsLoaded("old-skill"))
	require.False(t, coord.skillTracker.IsLoaded("new-skill"))

	// The agent's tools were rebuilt against the new snapshot.
	var names []string
	for _, tool := range agent.tools {
		names = append(names, tool.Info().Name)
	}
	require.Contains(t, names, tools.CrushInfoToolName)
}

// TestCoordinatorRefreshSkillsWithoutManager verifies the no-op path for
// coordinators constructed without a skills manager.
func TestCoordinatorRefreshSkillsWithoutManager(t *testing.T) {
	t.Parallel()

	c := &coordinator{}
	require.NoError(t, c.RefreshSkills(t.Context()))
}
