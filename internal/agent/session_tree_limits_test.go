package agent

import (
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
)

// startLimitedTeam runs a Lead turn that spawns one teammate and then ends,
// with limits set on the Lead's session first, as `crush run` sets them.
// The teammate runs on teammateModel. It returns the coordinator, the
// Lead's session id and the teammate's.
func startLimitedTeam(t *testing.T, limits RunLimits, teammateModel fantasy.LanguageModel) (*coordinator, string, string) {
	t.Helper()
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())
	// Cancel reads the detached subagents, which the gate coordinator
	// leaves unset.
	coord.dispatched = csync.NewMap[string, *dispatchedAgent]()
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, nil)
	require.NoError(t, err)
	service.SetRunner(&teamRunner{c: coord})
	coord.team = service

	spawn := scriptedCall{parts: []fantasy.StreamPart{
		{
			Type:          fantasy.StreamPartTypeToolCall,
			ID:            "call-spawn",
			ToolCallName:  SpawnTeammateToolName,
			ToolCallInput: `{"name":"worker","description":"Does the work","prompt":"Do the work."}`,
		},
		{
			Type:         fantasy.StreamPartTypeFinish,
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        fantasy.Usage{InputTokens: 1000, OutputTokens: 10, TotalTokens: 1010},
		},
	}}
	leadModel := coord.mainModels().Large
	leadModel.CatwalkCfg.ContextWindow = 200_000
	worker := leadModel
	leadModel.Model = &scriptedModel{script: []scriptedCall{spawn, answer("The worker is on it.", fantasy.FinishReasonStop, 1000)}}
	worker.Model = teammateModel
	coord.models.Set(coordinatorModels{large: leadModel, subLarge: worker, small: leadModel})

	lead := coord.coderAgent()
	leadTools, err := coord.buildTools(t.Context(), coord.cfg.Config().Agents[config.AgentCoder], roleLead, lead.Model)
	require.NoError(t, err)
	lead.SetTools(leadTools)

	sess, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	coord.LimitSessionTree(sess.ID, limits)
	_, err = lead.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "Start the worker.", MaxOutputTokens: 1000})
	require.NoError(t, err)
	teammates := service.Tree(sess.ID).Sessions
	require.Len(t, teammates, 1)
	return coord, sess.ID, teammates[0]
}

// A run refused because the tree reached a limit writes nothing, so the
// wait reports the limit that refused it rather than nothing at all.
func TestWaitForSessionTreeReportsTheLimitThatRefusedARun(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())
	sess, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	coord.LimitSessionTree(sess.ID, RunLimits{MaxSteps: 1})
	coord.limits.forSession(t.Context(), coord.sessions, sess.ID).takeStep()

	_, err = coord.mainAgent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "Go."})
	require.ErrorIs(t, err, errTreeLimitReached)

	tree := requireTreeIdle(t, waitForTree(t, coord, sess.ID))
	require.Equal(t, message.TurnEndStepLimit, tree.Limit)
}

// A tree with limits that refused no run leaves the reason to the rows.
func TestWaitForSessionTreeReportsNoLimitWithoutARefusal(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())
	sess, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	coord.LimitSessionTree(sess.ID, RunLimits{MaxSteps: 2})

	tree := requireTreeIdle(t, waitForTree(t, coord, sess.ID))
	require.Empty(t, tree.Limit)
}

// The deadline passes while the command waits for a teammate. The
// teammate's detached turn is cancelled through the Team service, reads
// time_limit, and the wait ends.
func TestTimeLimitStopsATeammate(t *testing.T) {
	never := &gatedModel{finishStreamModel: finishStreamModel{text: "never"}, release: make(chan struct{})}
	coord, leadID, teammateID := startLimitedTeam(t, RunLimits{Timeout: time.Second}, never)
	env := fakeEnv{messages: coord.messages}

	tree := requireTreeIdle(t, waitForTree(t, coord, leadID))
	require.Equal(t, 1, tree.Team.UnreadByLead, "the stopped teammate still reports")
	require.Equal(t, []message.TurnEnd{message.TurnEndCompleted}, turnEnds(t, env, leadID))
	require.Equal(t, []message.TurnEnd{message.TurnEndTimeLimit}, turnEnds(t, env, teammateID))
}

// The budget is the tree's: the teammate draws on what the Lead left of it
// and stops once it is spent, after the Lead's turn completed.
func TestStepLimitSpansATeammate(t *testing.T) {
	busy := &scriptedModel{script: []scriptedCall{callTool(ListAgentsToolName, 1000)}}
	coord, leadID, teammateID := startLimitedTeam(t, RunLimits{MaxSteps: 4}, busy)
	env := fakeEnv{messages: coord.messages}

	requireTreeIdle(t, waitForTree(t, coord, leadID))
	require.Equal(t, []message.TurnEnd{message.TurnEndCompleted}, turnEnds(t, env, leadID))
	require.Equal(t, []message.TurnEnd{message.TurnEndStepLimit}, turnEnds(t, env, teammateID))
	// Runs that step at once each finish the step they started, so the
	// tree can go one step past the budget, never more.
	steps := stepCount(t, env, leadID) + stepCount(t, env, teammateID)
	require.GreaterOrEqual(t, steps, 4)
	require.LessOrEqual(t, steps, 5)
}
