package prompt

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// The Agent Teams policy tells the model to coordinate through team tools. An
// agent whose palette has none of them (because they are disabled, or because
// a subagent never gets them) was still being told to use them, at 3.6 KB per
// request. The policy is kept when the caller does not say which tools the
// agent has, so an unset list can never silently drop it.
func TestTeamPolicyRenderedOnlyWithTeamTools(t *testing.T) {
	t.Parallel()

	enabled := true
	store := config.NewTestStore(&config.Config{
		Options: &config.Options{AgentTeams: &enabled},
	})
	const template = "{{if .TeamPolicy}}POLICY-PRESENT{{end}}"

	tests := []struct {
		name      string
		toolNames []string
		want      string
	}{
		{
			name: "no tool names given",
			want: "POLICY-PRESENT",
		},
		{
			name:      "a team tool is present",
			toolNames: []string{"bash", "view", "spawn_teammate"},
			want:      "POLICY-PRESENT",
		},
		{
			name:      "a team task tool is present",
			toolNames: []string{"bash", "team_task_update"},
			want:      "POLICY-PRESENT",
		},
		{
			name:      "no team tool",
			toolNames: []string{"bash", "view", "edit"},
			want:      "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, err := NewPrompt("test", template, WithToolNames(test.toolNames))
			require.NoError(t, err)

			built, err := p.Build(t.Context(), "", "", store)
			require.NoError(t, err)
			require.Equal(t, test.want, built)
		})
	}
}

// With the option off the policy stays out of the prompt whatever the tool
// palette holds.
func TestTeamPolicyAbsentWhenTeamsDisabled(t *testing.T) {
	t.Parallel()

	disabled := false
	store := config.NewTestStore(&config.Config{
		Options: &config.Options{AgentTeams: &disabled},
	})

	p, err := NewPrompt("test", "{{if .TeamPolicy}}POLICY-PRESENT{{end}}", WithToolNames([]string{"spawn_teammate"}))
	require.NoError(t, err)

	built, err := p.Build(t.Context(), "", "", store)
	require.NoError(t, err)
	require.Empty(t, built)
}
