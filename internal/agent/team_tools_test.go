package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/config"
)

// teamTestSessionID is the Lead session every Team tool test runs as.
const teamTestSessionID = "session-team"

type teamFakeRun struct {
	done   chan struct{}
	output string
	cancel func()
}

func (r *teamFakeRun) SessionID() string     { return "session-fake" }
func (r *teamFakeRun) Done() <-chan struct{} { return r.done }
func (r *teamFakeRun) Output() string        { return r.output }
func (r *teamFakeRun) Err() error            { return nil }
func (r *teamFakeRun) Cancel()               { r.cancel() }

type teamFakeRunner struct {
	mu    sync.Mutex
	runs  map[string]*teamFakeRun
	count int
}

func newTeamFakeRunner() *teamFakeRunner {
	return &teamFakeRunner{runs: make(map[string]*teamFakeRun)}
}

func (r *teamFakeRunner) Start(_ context.Context, member agentteam.Member, _ string) (agentteam.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := &teamFakeRun{done: make(chan struct{})}
	run.cancel = func() { close(run.done) }
	r.runs[member.Name] = run
	r.count++
	return run, nil
}

func (r *teamFakeRunner) finish(name string) {
	r.mu.Lock()
	run := r.runs[name]
	r.mu.Unlock()
	run.cancel()
}

func newTeamTestCoordinator(t *testing.T) (*coordinator, *teamFakeRunner) {
	t.Helper()
	runner := newTeamFakeRunner()
	service, err := agentteam.New(filepath.Join(t.TempDir(), "team.json"), agentteam.Config{}, runner)
	require.NoError(t, err)

	// The spawn tool resolves model overrides against a real provider
	// catalog, so the coordinator needs the config a Lead would have.
	workingDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "crush.json"), []byte(overrideTestConfig), 0o644))
	cfg, err := config.Init(t.Context(), workingDir, "", false)
	require.NoError(t, err)
	c := &coordinator{cfg: cfg, team: service}
	require.NoError(t, c.rebuildModels(context.Background()))
	return c, runner
}

// testTeamTools returns the Team tools for a caller, with the parent model
// a Lead agent would pass along.
func testTeamTools(c *coordinator, caller string) []fantasy.AgentTool {
	return c.teamTools(caller, func() Model { return c.mainModels().Large })
}

func runTeamTool(t *testing.T, tool fantasy.AgentTool, input any) string {
	t.Helper()
	response := runTeamToolRaw(t, tool, input)
	require.False(t, response.IsError, response.Content)
	return response.Content
}

func runTeamToolRaw(t *testing.T, tool fantasy.AgentTool, input any) fantasy.ToolResponse {
	t.Helper()
	return runTeamToolIn(t, context.WithValue(t.Context(), tools.SessionIDContextKey, teamTestSessionID), tool, input)
}

func runTeamToolIn(t *testing.T, ctx context.Context, tool fantasy.AgentTool, input any) fantasy.ToolResponse {
	t.Helper()
	data, err := json.Marshal(input)
	require.NoError(t, err)
	response, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "call-1",
		Name:  tool.Info().Name,
		Input: string(data),
	})
	require.NoError(t, err)
	return response
}

func toolByName(tools []fantasy.AgentTool, name string) fantasy.AgentTool {
	for _, tool := range tools {
		if tool.Info().Name == name {
			return tool
		}
	}
	return nil
}

func TestTeamToolsSpawnListAndMessage(t *testing.T) {
	t.Parallel()
	c, runner := newTeamTestCoordinator(t)
	leadTools := testTeamTools(c, "lead")
	require.Len(t, leadTools, 9)

	spawned := runTeamTool(t, toolByName(leadTools, SpawnTeammateToolName), map[string]any{
		"name":        "reviewer",
		"description": "Reviews the diff",
		"prompt":      "Review the diff.",
	})
	require.Contains(t, spawned, `"target":"reviewer"`)
	require.Contains(t, spawned, `"status":"running"`)

	listed := runTeamTool(t, toolByName(leadTools, ListAgentsToolName), map[string]any{})
	require.Contains(t, listed, `"target":"lead"`)
	require.Contains(t, listed, `"target":"reviewer"`)

	sent := runTeamTool(t, toolByName(leadTools, SendMessageToolName), map[string]any{
		"target":  "reviewer",
		"message": "Focus on the tests.",
	})
	require.JSONEq(t, `{"sent":true}`, sent)

	// The queued message resumes the teammate once its turn settles.
	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		runner.mu.Lock()
		defer runner.mu.Unlock()
		return runner.count == 2
	}, time.Second, 10*time.Millisecond)

	runner.finish("reviewer")
	require.Eventually(t, func() bool {
		members, err := c.team.ListMembers("lead", teamTestSessionID)
		return err == nil && members[1].Status == agentteam.StatusInactive
	}, time.Second, 10*time.Millisecond)

	// A message to an inactive teammate resumes it.
	runTeamTool(t, toolByName(leadTools, SendMessageToolName), map[string]any{
		"target":  "reviewer",
		"message": "Resume please.",
	})
	runner.mu.Lock()
	defer runner.mu.Unlock()
	require.Equal(t, 3, runner.count)
}

// TestTeamToolsSpawnWithModelOverride pins that a spawn can pin its
// teammate to another model, and that the roster shows the model and
// provider the Lead asked for.
func TestTeamToolsSpawnWithModelOverride(t *testing.T) {
	t.Parallel()
	c, _ := newTeamTestCoordinator(t)
	leadTools := testTeamTools(c, "lead")

	spawnTool := toolByName(leadTools, SpawnTeammateToolName)
	require.Contains(t, spawnTool.Info().Description, "Models available to `model`",
		"the spawn tool must advertise the same model catalog as agent_dispatch")

	spawned := runTeamTool(t, spawnTool, map[string]any{
		"name":        "reviewer",
		"description": "Reviews the diff",
		"prompt":      "Review the diff.",
		"model":       "mock-reasoner",
		"reasoning":   "high",
	})
	require.Contains(t, spawned, `"model":"mock-reasoner"`)
	require.Contains(t, spawned, `"model_provider":"mock"`,
		"a model-only override inherits the Lead's provider")
	require.Contains(t, spawned, `"reasoning":"high"`)

	listed := runTeamTool(t, toolByName(leadTools, ListAgentsToolName), map[string]any{})
	require.Contains(t, listed, `"model":"mock-reasoner"`)
	require.Contains(t, listed, `"model_provider":"mock"`)
	require.Contains(t, listed, `"reasoning":"high"`)
}

// TestTeamToolsSpawnRejectsInvalidModelOverride pins that a refused
// override returns a tool error before any member exists, so the call can
// be corrected and retried with the same name.
func TestTeamToolsSpawnRejectsInvalidModelOverride(t *testing.T) {
	t.Parallel()
	c, runner := newTeamTestCoordinator(t)
	leadTools := testTeamTools(c, "lead")
	spawnTool := toolByName(leadTools, SpawnTeammateToolName)

	unknownModel := runTeamToolRaw(t, spawnTool, map[string]any{
		"name":        "reviewer",
		"description": "Reviews the diff",
		"prompt":      "Review the diff.",
		"model":       "gone-model",
	})
	require.True(t, unknownModel.IsError)
	require.Contains(t, unknownModel.Content, `model "gone-model" is not available from provider "mock"`)

	providerOnly := runTeamToolRaw(t, spawnTool, map[string]any{
		"name":        "reviewer",
		"description": "Reviews the diff",
		"prompt":      "Review the diff.",
		"provider":    "mock",
	})
	require.True(t, providerOnly.IsError)
	require.Contains(t, providerOnly.Content, "provider is given without model")

	runner.mu.Lock()
	started := runner.count
	runner.mu.Unlock()
	require.Zero(t, started, "a refused override must not start a member")

	listed := runTeamTool(t, toolByName(leadTools, ListAgentsToolName), map[string]any{})
	require.NotContains(t, listed, `"target":"reviewer"`)
}

func TestTeamToolsTaskBoard(t *testing.T) {
	t.Parallel()
	c, _ := newTeamTestCoordinator(t)
	leadTools := testTeamTools(c, "lead")

	created := runTeamTool(t, toolByName(leadTools, TeamTaskCreateToolName), map[string]any{
		"subject":      "Add rate limiting",
		"description":  "Implement the middleware.",
		"write_scopes": []string{"internal/middleware"},
	})
	require.Contains(t, created, `"id":"task-1"`)
	require.Contains(t, created, `"ready":true`)

	claimed := runTeamTool(t, toolByName(leadTools, TeamTaskUpdateToolName), map[string]any{
		"task_id":           "task-1",
		"expected_revision": 1,
		"action":            "claim",
	})
	require.Contains(t, claimed, `"status":"in_progress"`)
	require.Contains(t, claimed, `"ownerName":"lead"`)

	stale := runTeamToolRaw(t, toolByName(leadTools, TeamTaskUpdateToolName), map[string]any{
		"task_id":           "task-1",
		"expected_revision": 1,
		"action":            "complete",
	})
	require.Contains(t, stale.Content, "stale team task")

	listed := runTeamTool(t, toolByName(leadTools, TeamTaskListToolName), map[string]any{
		"status": "in_progress",
	})
	require.Contains(t, listed, `"id":"task-1"`)

	got := runTeamTool(t, toolByName(leadTools, TeamTaskGetToolName), map[string]any{
		"task_id": "task-1",
	})
	require.Contains(t, got, `"revision":2`)
}

func TestTeamToolsDeliverPeerMessages(t *testing.T) {
	t.Parallel()
	c, _ := newTeamTestCoordinator(t)
	leadTools := testTeamTools(c, "lead")
	runTeamTool(t, toolByName(leadTools, SpawnTeammateToolName), map[string]any{
		"name":        "reviewer",
		"description": "Reviews the diff",
		"prompt":      "Review the diff.",
	})

	teammateTools := testTeamTools(c, "reviewer")
	runTeamTool(t, toolByName(teammateTools, SendMessageToolName), map[string]any{
		"target":  "lead",
		"message": "Found a bug.",
	})

	listed := runTeamTool(t, toolByName(leadTools, ListAgentsToolName), map[string]any{})
	require.True(t, strings.Contains(listed, "Message from reviewer: Found a bug."))
	require.True(t, strings.HasPrefix(listed, "["))
}

func TestTeamToolsLeadOnly(t *testing.T) {
	t.Parallel()
	c, _ := newTeamTestCoordinator(t)
	leadTools := testTeamTools(c, "lead")
	runTeamTool(t, toolByName(leadTools, SpawnTeammateToolName), map[string]any{
		"name":        "reviewer",
		"description": "Reviews the diff",
		"prompt":      "Review the diff.",
	})

	teammateTools := testTeamTools(c, "reviewer")
	spawned := runTeamToolRaw(t, toolByName(teammateTools, SpawnTeammateToolName), map[string]any{
		"name":        "tester",
		"description": "Tests",
		"prompt":      "Test.",
	})
	require.Contains(t, spawned.Content, "only the Team Lead can create teammates")
}

// TestTeamToolsAreScopedToTheCallingSession pins that a tool call cannot see
// or reach a team another session spawned.
func TestTeamToolsAreScopedToTheCallingSession(t *testing.T) {
	t.Parallel()
	c, _ := newTeamTestCoordinator(t)
	leadTools := testTeamTools(c, "lead")
	runTeamTool(t, toolByName(leadTools, SpawnTeammateToolName), map[string]any{
		"name":        "reviewer",
		"description": "Reviews the diff",
		"prompt":      "Review the diff.",
	})

	other := context.WithValue(t.Context(), tools.SessionIDContextKey, "session-other")
	listed := runTeamToolIn(t, other, toolByName(leadTools, ListAgentsToolName), map[string]any{})
	require.NotContains(t, listed.Content, `"target":"reviewer"`, "another session must not see this session's teammate")

	sent := runTeamToolIn(t, other, toolByName(leadTools, SendMessageToolName), map[string]any{
		"target":  "reviewer",
		"message": "hi",
	})
	require.True(t, sent.IsError, "another session must not message this session's teammate")

	owned := runTeamTool(t, toolByName(leadTools, ListAgentsToolName), map[string]any{})
	require.Contains(t, owned, `"target":"reviewer"`, "the owning session still sees its teammate")
}
