package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTeamTaskRoleValidation pins the tool half of task roles: a role must
// resolve, with the refusal spawn_teammate gives, and a valid one is stored
// with its verify default.
func TestTeamTaskRoleValidation(t *testing.T) {
	coord := newTeammateTestCoordinator(t)
	create := coord.teamTaskCreateTool("lead")
	update := coord.teamTaskUpdateTool("lead")

	response := runTeamToolRaw(t, create, map[string]any{"subject": "Build", "description": "d", "role": "nope"})
	require.True(t, response.IsError)
	require.Contains(t, response.Content, `unknown role "nope"; available: `)

	created := runTeamTool(t, create, map[string]any{"subject": "Build", "description": "d", "role": "implementer"})
	require.Contains(t, created, `"role":"implementer"`)
	require.Contains(t, created, `"verify":true`)

	unverified := runTeamTool(t, create, map[string]any{"subject": "Look", "description": "d", "role": "researcher", "verify": false})
	require.NotContains(t, unverified, `"verify":true`)

	response = runTeamToolRaw(t, update, map[string]any{
		"task_id": "task-1", "expected_revision": 1, "action": "edit", "role": "nope",
	})
	require.True(t, response.IsError)
	require.Contains(t, response.Content, `unknown role "nope"`)
}
