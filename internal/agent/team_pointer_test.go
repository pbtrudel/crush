package agent

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/config"
)

// TestTeamPointerSurvivesConcurrentSync toggles Agent Teams while a
// frontend sends team messages, the way a config reload races a browser
// request. It only fails under -race.
func TestTeamPointerSurvivesConcurrentSync(t *testing.T) {
	t.Parallel()
	const providerID = "test-provider"
	env := testEnv(t)
	c := newTestCoordinator(t, env, providerID, config.ProviderConfig{ID: providerID})
	opts := c.cfg.Config().Options
	var (
		wg      sync.WaitGroup
		syncErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 50 {
			on := i%2 == 0
			opts.AgentTeams = &on
			if err := c.syncAgentTeams(); err != nil {
				syncErr = err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			_ = c.TeamInterrupt(t.Context(), "s1", "alice")
		}
	}()
	wg.Wait()
	require.NoError(t, syncErr)
	off := false
	opts.AgentTeams = &off
	require.NoError(t, c.syncAgentTeams())
	require.Nil(t, c.teamService())
}

// TestTeamToolsAnswerAfterTheServiceIsRetired pins that a turn whose tool
// list was built while Agent Teams was on answers with the disabled error,
// not a nil dereference, once a reload retires the service.
func TestTeamToolsAnswerAfterTheServiceIsRetired(t *testing.T) {
	t.Parallel()
	c := &coordinator{}
	tools := c.teamTools("lead", func() Model { return Model{} })
	for _, name := range []string{
		ListAgentsToolName,
		WaitAgentToolName,
		TeamTaskListToolName,
		TeamTaskGetToolName,
	} {
		tool := toolByName(tools, name)
		require.NotNil(t, tool, name)
		response := runTeamToolRaw(t, tool, map[string]any{"task_id": "task-1"})
		require.True(t, response.IsError, name)
		require.Contains(t, response.Content, ErrAgentTeamsDisabled.Error(), name)
	}
}
