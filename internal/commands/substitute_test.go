package commands

import (
	"testing"

	"github.com/charmbracelet/crush/internal/skills"
	"github.com/stretchr/testify/require"
)

func TestSubstituteReplacesEachPlaceholder(t *testing.T) {
	t.Parallel()
	got := Substitute("Fix $FILE then test $FILE with $MODE", map[string]string{"FILE": "a.go", "MODE": "race"})
	require.Equal(t, "Fix a.go then test a.go with race", got)
}

func TestSubstituteOnce(t *testing.T) {
	t.Parallel()
	got := Substitute("$A and $B", map[string]string{"A": "$B", "B": "bee"})
	require.Equal(t, "$B and bee", got, "a value is never expanded again")
}

func TestSubstituteLeavesUnknownAndLowercase(t *testing.T) {
	t.Parallel()
	require.Equal(t, "$UNKNOWN $lower", Substitute("$UNKNOWN $lower", map[string]string{"X": "y"}))
}

// TestPromptTextSkillIgnoresArgs pins that a skill command sends the
// loaded-skill block, the same text the TUI sends for one, whatever
// arguments came with it.
func TestPromptTextSkillIgnoresArgs(t *testing.T) {
	t.Parallel()
	cmd := CustomCommand{Content: "ignored $FILE", Skill: &skills.Skill{Name: "review", SkillFilePath: "/skills/review/SKILL.md"}}
	require.Equal(t, cmd.Skill.FormatInvocation(), cmd.PromptText(map[string]string{"FILE": "a.go"}))
}

func TestPromptTextSubstitutesCustomCommand(t *testing.T) {
	t.Parallel()
	cmd := CustomCommand{Content: "Fix $FILE"}
	require.Equal(t, "Fix $OTHER", cmd.PromptText(map[string]string{"FILE": "$OTHER"}))
	require.Equal(t, "Fix $FILE", cmd.PromptText(nil))
}
