package agent

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
)

// busySessionAgent answers only what the tree asks of the main agent. The
// embedded interface is nil, so any other call panics.
type busySessionAgent struct {
	SessionAgent
	busy   atomic.Bool
	queued atomic.Int64
}

func (a *busySessionAgent) IsSessionBusy(string) bool { return a.busy.Load() }
func (a *busySessionAgent) QueuedPrompts(string) int  { return int(a.queued.Load()) }

type treeWait struct {
	tree SessionTree
	err  error
}

func waitForTree(t *testing.T, c *coordinator, sessionID string) <-chan treeWait {
	t.Helper()
	waited := make(chan treeWait, 1)
	go func() {
		tree, err := c.WaitForSessionTree(t.Context(), sessionID)
		waited <- treeWait{tree: tree, err: err}
	}()
	return waited
}

func requireStillWaiting(t *testing.T, waited <-chan treeWait) {
	t.Helper()
	select {
	case got := <-waited:
		t.Fatalf("the wait returned while the tree was busy: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func requireTreeIdle(t *testing.T, waited <-chan treeWait) SessionTree {
	t.Helper()
	select {
	case got := <-waited:
		require.NoError(t, got.err)
		require.True(t, got.tree.Idle())
		return got.tree
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not return once the tree was idle")
		return SessionTree{}
	}
}

func TestWaitForSessionTreeFollowsTheSessionsRuns(t *testing.T) {
	t.Parallel()
	agent := &busySessionAgent{}
	agent.busy.Store(true)
	c := &coordinator{mainAgent: agent}

	waited := waitForTree(t, c, "session-root")
	requireStillWaiting(t, waited)

	agent.busy.Store(false)
	c.runEnded.notify()
	tree := requireTreeIdle(t, waited)
	require.False(t, tree.Busy)
}

// A run counts from the moment the coordinator starts it, before the agent
// reports the session busy. A teammate settling in that window must not end
// the wait.
func TestWaitForSessionTreeCountsARunInFlight(t *testing.T) {
	t.Parallel()
	agent := &busySessionAgent{}
	c := &coordinator{mainAgent: agent}

	c.runStarted("session-root")
	waited := waitForTree(t, c, "session-root")
	requireStillWaiting(t, waited)

	c.runFinished("session-root")
	tree := requireTreeIdle(t, waited)
	require.False(t, tree.Busy)
}

// A prompt queued behind a turn the agent has not picked up yet holds the
// wait, though the agent reads the session as not busy.
func TestWaitForSessionTreeCountsAQueuedPrompt(t *testing.T) {
	t.Parallel()
	agent := &busySessionAgent{}
	agent.queued.Store(1)
	c := &coordinator{mainAgent: agent}

	waited := waitForTree(t, c, "session-root")
	requireStillWaiting(t, waited)

	agent.queued.Store(0)
	c.runEnded.notify()
	tree := requireTreeIdle(t, waited)
	require.False(t, tree.Busy)
}

// A signal cancels the command's context; the wait returns at once with
// what it last read.
func TestWaitForSessionTreeStopsWithItsContext(t *testing.T) {
	t.Parallel()
	agent := &busySessionAgent{}
	agent.busy.Store(true)
	c := &coordinator{mainAgent: agent}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	tree, err := c.WaitForSessionTree(ctx, "session-root")
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, tree.Busy)
}

// Every run the coordinator starts signals when it returns, so a wait reads
// the tree again after a run that woke the session (a background job).
func TestRunSignalsTheSessionTreeWhenItEnds(t *testing.T) {
	coord := newGateTestCoordinator(t, true)
	require.NoError(t, coord.readyWg.Wait())
	coord.mainAgent = &mockSessionAgent{
		model: coord.mainModels().Large,
		runFunc: func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
			return &fantasy.AgentResult{}, nil
		},
	}
	changed := coord.runEnded.changed()

	// The run answers without calling a tool; it signals when it returns.
	_, _ = coord.run(t.Context(), nil, nil, "test-session", "hello")

	select {
	case <-changed:
	default:
		t.Fatal("the run returned without signalling the session tree")
	}
}

// gatedModel holds every agent step until release is closed, then answers
// with its text. It stands in for a teammate still working after the Lead's
// turn has ended.
type gatedModel struct {
	finishStreamModel
	release <-chan struct{}
}

func (m *gatedModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if len(call.Tools) > 0 {
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.finishStreamModel.Stream(ctx, call)
}

// The Lead starts a teammate and ends its own turn first, which is where
// `crush run` used to exit. The wait lasts until the teammate's turn is
// over, and the report the Lead never read is counted.
func TestWaitForSessionTreeOutlastsTheLeadsTurn(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	require.NoError(t, coord.readyWg.Wait())
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, nil)
	require.NoError(t, err)
	service.SetRunner(&teamRunner{c: coord})
	coord.team = service

	release := make(chan struct{})
	releaseTeammate := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseTeammate)

	spawn := scriptedCall{parts: []fantasy.StreamPart{
		{
			Type:          fantasy.StreamPartTypeToolCall,
			ID:            "call-spawn",
			ToolCallName:  SpawnTeammateToolName,
			ToolCallInput: `{"name":"worker","description":"Writes the report","prompt":"Write the report."}`,
		},
		{
			Type:         fantasy.StreamPartTypeFinish,
			FinishReason: fantasy.FinishReasonToolCalls,
			Usage:        fantasy.Usage{InputTokens: 1000, OutputTokens: 10, TotalTokens: 1010},
		},
	}}
	// Teammates run on the subagent model, so the two get different models.
	leadModel := coord.mainModels().Large
	leadModel.CatwalkCfg.ContextWindow = 200_000
	teammateModel := leadModel
	leadModel.Model = &scriptedModel{script: []scriptedCall{spawn, answer("The worker is on it.", fantasy.FinishReasonStop, 1000)}}
	teammateModel.Model = &gatedModel{finishStreamModel: finishStreamModel{text: "Report: all done."}, release: release}
	coord.models.Set(coordinatorModels{large: leadModel, subLarge: teammateModel, small: leadModel})

	// The gate coordinator built the Lead's tools before the Team service
	// existed; production builds them after.
	lead := coord.coderAgent()
	leadTools, err := coord.buildTools(t.Context(), coord.cfg.Config().Agents[config.AgentCoder], roleLead, lead.Model)
	require.NoError(t, err)
	lead.SetTools(leadTools)

	sess, err := coord.sessions.Create(t.Context(), "Lead")
	require.NoError(t, err)
	_, err = lead.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "Start the worker.", MaxOutputTokens: 1000})
	require.NoError(t, err)
	require.Equal(t, 1, service.Tree(sess.ID).Active, "the teammate outlives the Lead's turn")

	waited := waitForTree(t, coord, sess.ID)
	requireStillWaiting(t, waited)

	releaseTeammate()
	tree := requireTreeIdle(t, waited)
	require.Equal(t, 1, tree.Team.UnreadByLead, "the Lead never read the teammate's report")
}
