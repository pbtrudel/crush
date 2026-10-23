package dialog

import (
	"testing"

	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/stretchr/testify/require"
)

func commandTitle(c *Commands, id string) string {
	for _, item := range c.defaultCommands() {
		if item.ID() == id {
			return item.title
		}
	}
	return ""
}

// TestReloadSkillsCommandAlwaysVisible pins the palette entry that
// re-discovers skills without restarting.
func TestReloadSkillsCommandAlwaysVisible(t *testing.T) {
	t.Parallel()

	com := &common.Common{Workspace: &rcCommandsWorkspace{}}
	c := &Commands{com: com, windowWidth: 200}

	require.NotEmpty(t, commandTitle(c, "reload_skills"))
}

// TestSideQuestionCommandVisibleWithSession pins the palette entry that
// opens the side-question prompt. It needs a session to ask about.
func TestSideQuestionCommandVisibleWithSession(t *testing.T) {
	t.Parallel()

	com := &common.Common{Workspace: &rcCommandsWorkspace{}}
	c := &Commands{com: com, hasSession: true, windowWidth: 200}

	require.Equal(t, "Ask Side Question", commandTitle(c, "ask_side_question"))

	c.hasSession = false
	require.Empty(t, commandTitle(c, "ask_side_question"))
}

// TestSecretRevealCommandLabelReflectsState verifies the reveal toggle
// advertises the action it will perform.
func TestSecretRevealCommandLabelReflectsState(t *testing.T) {
	t.Parallel()

	com := &common.Common{Workspace: &rcCommandsWorkspace{}}
	c := &Commands{com: com, hasSession: true, windowWidth: 200}

	require.Equal(t, "Reveal Secrets", commandTitle(c, "toggle_secret_reveal"))

	c.revealSecrets = true
	require.Equal(t, "Hide Secrets", commandTitle(c, "toggle_secret_reveal"))
}
