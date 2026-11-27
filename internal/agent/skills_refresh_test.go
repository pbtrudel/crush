package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// A skills reload changes the skill list the system prompt carries, so the
// next turn must rebuild the prompt once. Without a reload or a toggle the
// prompt is left alone, keeping the provider's prompt cache warm.
func TestRefreshSkillsRebuildsThePromptOnce(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())

	skillsDir := t.TempDir()
	opts := coord.cfg.Config().Options
	opts.SkillsPaths = append(opts.SkillsPaths, skillsDir)

	p, err := loadPrompt(coord.cfg, "coder", coord.skillOverrideOptions()...)
	require.NoError(t, err)
	coord.prompts = map[string]*prompt.Prompt{config.AgentCoder: p}
	coord.skillKeys = map[string]string{config.AgentCoder: coord.skillToggleKey(t.Context())}

	agent := coord.mainAgent.(*sessionAgent)
	agent.SetSystemPrompt("stale")

	skillDir := filepath.Join(skillsDir, "reloaded-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: reloaded-skill\ndescription: Added while the session ran.\n---\n\nBody.\n"), 0o644))

	require.NoError(t, coord.refreshSkillPrompt(t.Context(), agent, config.AgentCoder))
	require.Equal(t, "stale", agent.systemPrompt.Get(), "a turn with no reload or toggle keeps the prompt")

	require.NoError(t, coord.RefreshSkills(t.Context()))
	require.NoError(t, coord.refreshSkillPrompt(t.Context(), agent, config.AgentCoder))
	require.Contains(t, agent.systemPrompt.Get(), "<name>reloaded-skill</name>",
		"the turn after a reload lists the skill it added")

	agent.SetSystemPrompt("stale again")
	require.NoError(t, coord.refreshSkillPrompt(t.Context(), agent, config.AgentCoder))
	require.Equal(t, "stale again", agent.systemPrompt.Get(), "a reload rebuilds the prompt once, not every turn")
}
