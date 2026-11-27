package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// startModels starts a gateway whose fake has the shared model config and
// an agent running its main model.
func startModels(t *testing.T) (*Gateway, *webtest.Fake) {
	t.Helper()
	return startModelConfig(t, webtest.ModelConfig())
}

// startModelConfig starts a gateway over cfg, with the fake's agent model
// matching the config's main model.
func startModelConfig(t *testing.T, cfg *config.Config) (*Gateway, *webtest.Fake) {
	t.Helper()
	fake := webtest.NewFake()
	fake.Cfg = cfg
	main := cfg.Models[config.SelectedModelTypeMain]
	model := cfg.GetModel(main.Provider, main.Model)
	require.NotNil(t, model)
	fake.SetAgentModel(workspace.AgentModel{ModelCfg: main, CatwalkCfg: *model})
	return startFake(t, fake), fake
}

// startModelAgent starts a gateway over a fake with cfg (which may be nil
// or have no providers) and an agent model set by hand, the states a reload
// in flight leaves behind.
func startModelAgent(t *testing.T, cfg *config.Config) (*Gateway, *webtest.Fake) {
	t.Helper()
	fake := webtest.NewFake()
	fake.Cfg = cfg
	fake.SetAgentModel(workspace.AgentModel{
		ModelCfg:   config.SelectedModel{Provider: "p1", Model: "m1"},
		CatwalkCfg: catwalk.Model{ID: "m1", Name: "Model One", ContextWindow: 200_000},
	})
	return startFake(t, fake), fake
}

// startFake starts a gateway over fake and shuts it down when the test ends.
func startFake(t *testing.T, fake *webtest.Fake) *Gateway {
	t.Helper()
	g, err := Start(t.Context(), Options{Workspace: fake})
	require.NoError(t, err)
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	return g
}

// decodeModelInfo reads a ModelInfo answer body.
func decodeModelInfo(t *testing.T, body []byte) ModelInfo {
	t.Helper()
	var info ModelInfo
	require.NoError(t, json.Unmarshal(body, &info))
	return info
}

func TestProvidersListsChoices(t *testing.T) {
	t.Parallel()
	g, _ := startModels(t)

	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/providers", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))

	var answer struct {
		Choices []workspace.ModelChoice `json:"choices"`
	}
	require.NoError(t, json.Unmarshal(body, &answer))
	require.Len(t, answer.Choices, 2, "the disabled provider's models are left out")

	first := answer.Choices[0]
	require.Equal(t, "p1", first.Provider)
	require.Equal(t, "Provider One", first.ProviderName)
	require.Equal(t, "m1", first.ID)
	require.Equal(t, "Model One", first.Name)
	require.Equal(t, int64(200_000), first.ContextWindow)
	require.True(t, first.CanReason)
	require.Equal(t, []string{"low", "high"}, first.EffortLevels)
	require.Equal(t, "low", first.DefaultEffort)

	second := answer.Choices[1]
	require.Equal(t, "m2", second.ID)
	require.False(t, second.CanReason)
	require.Empty(t, second.EffortLevels)
}

func TestSelectModelSwitchesAndPublishes(t *testing.T) {
	t.Parallel()
	g, fake := startModels(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/select",
		`{"type":"main","provider":"p1","model":"m2"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	info := decodeModelInfo(t, body)
	require.Equal(t, "m2", info.ID)
	require.Equal(t, "p1", info.Provider)
	require.True(t, info.AgentReady)
	require.NotNil(t, info.Small)
	require.Equal(t, "m2", info.Small.ID)

	calls := fake.CallLog()
	preferred := slices.Index(calls, "UpdatePreferredModel:global:main:p1/m2")
	require.GreaterOrEqual(t, preferred, 0, "the choice is saved before the agent reloads: %v", calls)
	require.Contains(t, calls[preferred+1:], "UpdateAgentModel")

	f := readFrame(t, c)
	require.Equal(t, "model_changed", f.Type)
	var changed ModelInfo
	require.NoError(t, json.Unmarshal(f.Payload, &changed))
	require.Equal(t, "m2", changed.ID)
	require.Equal(t, "Model Two", changed.Name)
	require.True(t, changed.AgentReady)

	// The switch was announced once: the watch must not repeat it.
	quiet, stop := context.WithTimeout(t.Context(), 2500*time.Millisecond)
	defer stop()
	_, _, err := c.Read(quiet)
	require.Error(t, err, "a switch is announced once, not again by the watch")
}

// TestSelectSmallModelSwitches pins that the small model is a model type of
// its own: the main model stays, and the new small model reaches every tab.
func TestSelectSmallModelSwitches(t *testing.T) {
	t.Parallel()
	g, fake := startModels(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.

	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/select",
		`{"type":"small","provider":"p1","model":"m1"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	info := decodeModelInfo(t, body)
	require.Equal(t, "m1", info.ID, "the main model is untouched")
	require.NotNil(t, info.Small)
	require.Equal(t, "m1", info.Small.ID)
	require.Contains(t, fake.CallLog(), "UpdatePreferredModel:global:small:p1/m1")

	f := readFrame(t, c)
	require.Equal(t, "model_changed", f.Type)
	var changed ModelInfo
	require.NoError(t, json.Unmarshal(f.Payload, &changed))
	require.Equal(t, "m1", changed.ID)
	require.NotNil(t, changed.Small)
	require.Equal(t, "m1", changed.Small.ID)
}

// TestSnapshotModelInfo pins the model fields a tab loads with and the one
// readiness call both snapshot fields share.
func TestSnapshotModelInfo(t *testing.T) {
	t.Parallel()
	g, fake := startModels(t)

	snap := bootstrapOf(t, g)
	require.Equal(t, "m1", snap.Model.ID)
	require.True(t, snap.Model.CanReason)
	require.Equal(t, []string{"low", "high"}, snap.Model.EffortLevels)
	require.NotNil(t, snap.Model.Small)
	require.Equal(t, "m2", snap.Model.Small.ID)
	require.Equal(t, "p1", snap.Model.Small.Provider)
	require.True(t, snap.Model.AgentReady)
	require.True(t, snap.AgentReady)

	// A starting agent says so in both places, from one check.
	fake.SetReadyErr(workspace.ErrAgentNotInitialized)
	snap = bootstrapOf(t, g)
	require.False(t, snap.Model.AgentReady)
	require.False(t, snap.AgentReady)
}

func TestSelectModelErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		body   string
		setup  func(*webtest.Fake)
		status int
		code   string
		// message is the user-facing text the answer must carry.
		message string
		// calls is the whole fake call log the attempt may leave.
		calls []string
	}{
		{
			name:   "busy agent",
			body:   `{"type":"main","provider":"p1","model":"m2"}`,
			setup:  func(f *webtest.Fake) { f.SetAgentBusy(true) },
			status: http.StatusConflict,
			code:   "agent_busy",
			calls:  []string{"AgentIsBusy"},
		},
		{
			name:    "model needs a sign-in",
			body:    `{"type":"main","provider":"p1","model":"m9"}`,
			status:  http.StatusBadRequest,
			code:    "unknown_model",
			message: "Choose this model in the terminal; it needs a sign-in",
			calls:   []string{"AgentIsBusy"},
		},
		{
			name:   "unknown type",
			body:   `{"type":"tiny","provider":"p1","model":"m2"}`,
			status: http.StatusBadRequest,
			code:   "bad_type",
		},
		{
			name:   "empty type",
			body:   `{"type":"","provider":"p1","model":"m2"}`,
			status: http.StatusBadRequest,
			code:   "bad_type",
		},
		{
			name:   "large type",
			body:   `{"type":"large","provider":"p1","model":"m2"}`,
			status: http.StatusBadRequest,
			code:   "bad_type",
		},
		{
			name:   "starting agent",
			body:   `{"type":"main","provider":"p1","model":"m2"}`,
			setup:  func(f *webtest.Fake) { f.SetReadyErr(workspace.ErrAgentNotInitialized) },
			status: http.StatusServiceUnavailable,
			code:   "agent_starting",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g, fake := startModels(t)
			if tt.setup != nil {
				tt.setup(fake)
			}
			rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/select", tt.body)
			rsp.Body.Close()
			require.Equal(t, tt.status, rsp.StatusCode, string(body))
			require.Contains(t, string(body), `"code":"`+tt.code+`"`)
			if tt.message != "" {
				require.Contains(t, string(body), tt.message)
			}
			require.Equal(t, tt.calls, fake.CallLog(), "a refused switch makes no workspace change")
		})
	}
}

func TestReasoningRequiresOneField(t *testing.T) {
	t.Parallel()

	t.Run("no field", func(t *testing.T) {
		t.Parallel()
		g, fake := startModels(t)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning", `{}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"bad_reasoning"`)
		require.Empty(t, fake.CallLog(), "the request is refused before the helper runs")
	})

	t.Run("both fields", func(t *testing.T) {
		t.Parallel()
		g, fake := startModels(t)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning",
			`{"effort":"low","think":true}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"bad_reasoning"`)
		require.Empty(t, fake.CallLog(), "the request is refused before the helper runs")
	})

	t.Run("effort", func(t *testing.T) {
		t.Parallel()
		g, fake := startModels(t)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning", `{"effort":"high"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
		require.Equal(t, "high", decodeModelInfo(t, body).ReasoningEffort)
		require.Contains(t, fake.CallLog(), "UpdatePreferredModel:global:main:p1/m1")
	})

	t.Run("think", func(t *testing.T) {
		t.Parallel()
		g, fake := startModels(t)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning", `{"think":true}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
		require.True(t, decodeModelInfo(t, body).Think)
		require.Contains(t, fake.CallLog(), "UpdatePreferredModel:global:main:p1/m1")
	})
}

// TestReasoningErrors pins the states the reasoning route refuses before it
// touches the model: an agent that is starting, a busy one, and a config
// reload in flight.
func TestReasoningErrors(t *testing.T) {
	t.Parallel()

	t.Run("starting agent", func(t *testing.T) {
		t.Parallel()
		g, fake := startModels(t)
		fake.SetReadyErr(workspace.ErrAgentNotInitialized)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning", `{"effort":"high"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"agent_starting"`)
		require.Empty(t, fake.CallLog())
	})

	t.Run("busy agent", func(t *testing.T) {
		t.Parallel()
		g, fake := startModels(t)
		fake.SetAgentBusy(true)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning", `{"effort":"high"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusConflict, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"agent_busy"`)
		require.Equal(t, []string{"AgentIsBusy"}, fake.CallLog())
	})

	t.Run("no config", func(t *testing.T) {
		t.Parallel()
		g, fake := startModelAgent(t, nil)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning", `{"effort":"high"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"agent_starting"`)
		require.Empty(t, fake.CallLog(), "the reload state is read before any helper runs")
	})

	t.Run("config without providers", func(t *testing.T) {
		t.Parallel()
		g, fake := startModelAgent(t, &config.Config{
			Models: map[config.SelectedModelType]config.SelectedModel{
				config.SelectedModelTypeMain: {Provider: "p1", Model: "m1"},
			},
			Agents: map[string]config.Agent{
				config.AgentCoder: {Model: config.SelectedModelTypeMain},
			},
		})
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning", `{"effort":"high"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusServiceUnavailable, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"agent_starting"`)
		require.Empty(t, fake.CallLog(), "the reload state is read before any helper runs")
	})
}

// TestReasoningValidatesTheCurrentModel pins the checks the helper would
// otherwise answer with a 500: an effort the model does not offer and
// thinking on a model that cannot reason.
func TestReasoningValidatesTheCurrentModel(t *testing.T) {
	t.Parallel()

	t.Run("unknown effort", func(t *testing.T) {
		t.Parallel()
		g, fake := startModels(t)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning", `{"effort":"medium"}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"bad_reasoning"`)
		require.Empty(t, fake.CallLog(), "the request is refused before the helper runs")
	})

	t.Run("no reasoning", func(t *testing.T) {
		t.Parallel()
		cfg := webtest.ModelConfig()
		cfg.Models[config.SelectedModelTypeMain] = config.SelectedModel{Provider: "p1", Model: "m2"}
		g, fake := startModelConfig(t, cfg)
		rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/reasoning", `{"think":true}`)
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
		require.Contains(t, string(body), `"code":"bad_reasoning"`)
		require.Empty(t, fake.CallLog(), "the request is refused before the helper runs")
	})
}

func TestModelWatchPublishesTerminalChanges(t *testing.T) {
	t.Parallel()
	g, fake := startModels(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.

	// The terminal switches models, out of the gateway's sight.
	fake.SetAgentModel(workspace.AgentModel{
		ModelCfg:   config.SelectedModel{Provider: "p1", Model: "m2"},
		CatwalkCfg: catwalk.Model{ID: "m2", Name: "Model Two", ContextWindow: 100_000},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	f := nextFrameOfType(ctx, t, c, "model_changed")
	var changed ModelInfo
	require.NoError(t, json.Unmarshal(f.Payload, &changed))
	require.Equal(t, "m2", changed.ID)
	require.Equal(t, "Model Two", changed.Name)
	require.True(t, changed.AgentReady)

	// Nothing changed since, so the watch must stay quiet.
	quiet, stop := context.WithTimeout(t.Context(), 2500*time.Millisecond)
	defer stop()
	_, _, err := c.Read(quiet)
	require.Error(t, err, "an unchanged model must not publish a frame")
}

// TestModelWatchReportsReadiness pins that a tab learns the agent became
// ready through model_changed, without reloading the snapshot.
func TestModelWatchReportsReadiness(t *testing.T) {
	t.Parallel()
	fake := webtest.NewFake()
	fake.Cfg = webtest.ModelConfig()
	main := fake.Cfg.Models[config.SelectedModelTypeMain]
	model := fake.Cfg.GetModel(main.Provider, main.Model)
	require.NotNil(t, model)
	fake.SetAgentModel(workspace.AgentModel{ModelCfg: main, CatwalkCfg: *model})
	fake.SetReadyErr(workspace.ErrAgentNotInitialized)
	g := startFake(t, fake)

	c := dialStream(t, g, "")
	snap := readFrame(t, c) // Snapshot, taken while the agent starts.
	var loaded Bootstrap
	require.NoError(t, json.Unmarshal(snap.Payload, &loaded))
	require.False(t, loaded.AgentReady)
	require.False(t, loaded.Model.AgentReady)

	fake.SetReadyErr(nil)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	f := nextFrameOfType(ctx, t, c, "model_changed")
	var changed ModelInfo
	require.NoError(t, json.Unmarshal(f.Payload, &changed))
	require.True(t, changed.AgentReady)
}

// TestModelWatchSkipsUnreachableWorkspace pins that a client workspace the
// gateway cannot reach is not reported as a model switch: AgentModel
// answers an empty model while the server is away, and publishing it would
// replace every tab's model with nothing.
func TestModelWatchSkipsUnreachableWorkspace(t *testing.T) {
	t.Parallel()
	g, fake := startModels(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.

	fake.SetReadyErr(fmt.Errorf("get agent info: %w", workspace.ErrServerUnreachable))
	fake.SetAgentModel(workspace.AgentModel{})

	quiet, stop := context.WithTimeout(t.Context(), 2500*time.Millisecond)
	defer stop()
	_, _, err := c.Read(quiet)
	require.Error(t, err, "an unreachable workspace must not publish an empty model")

	// The watch must resume once the workspace answers again: the quiet
	// read closed the first tab's stream, so a second tab watches the
	// recovery.
	b := dialStream(t, g, "")
	readFrame(t, b) // Snapshot, taken while the workspace is still gone.
	fake.SetReadyErr(nil)
	fake.SetAgentModel(workspace.AgentModel{
		ModelCfg:   config.SelectedModel{Provider: "p1", Model: "m2"},
		CatwalkCfg: catwalk.Model{ID: "m2", Name: "Model Two", ContextWindow: 100_000},
	})
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	f := nextFrameOfType(ctx, t, b, "model_changed")
	var recovered ModelInfo
	require.NoError(t, json.Unmarshal(f.Payload, &recovered))
	require.Equal(t, "m2", recovered.ID, "the watch still reports model changes after an outage")
}

// TestStaleModelWatchCannotOverwriteASelect pins the generation guard: a
// watch tick that read the model before a browser switch must drop its
// publish, or the tab would flash the model the select just replaced.
func TestStaleModelWatchCannotOverwriteASelect(t *testing.T) {
	t.Parallel()
	g, fake := startModels(t)
	c := dialStream(t, g, "")
	readFrame(t, c) // Bootstrap.

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	fake.SetAgentModelGate(entered, release)
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("the model watch never read the model")
	}
	// The watch holds a read of the old model while the browser switches.
	rsp, body := doJSON(t, authedClient(t, g), http.MethodPost, g.URL()+"/api/models/select",
		`{"type":"main","provider":"p1","model":"m2"}`)
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	f := nextFrameOfType(ctx, t, c, "model_changed")
	var changed ModelInfo
	require.NoError(t, json.Unmarshal(f.Payload, &changed))
	require.Equal(t, "m2", changed.ID)

	// Releasing the stale read must publish nothing: the announced model
	// is newer than what the watch read.
	unblock()
	quiet, stop := context.WithTimeout(t.Context(), 600*time.Millisecond)
	defer stop()
	_, data, err := c.Read(quiet)
	require.Error(t, err, "a stale watch read must not publish a model: %s", data)
}

// nextFrameOfType reads frames until one of the wanted type arrives. It
// runs on the test goroutine, so failures stop the test directly.
func nextFrameOfType(ctx context.Context, t *testing.T, c *websocket.Conn, want string) testFrame {
	t.Helper()
	for {
		_, data, err := c.Read(ctx)
		require.NoError(t, err, "waiting for a %s frame", want)
		var f testFrame
		require.NoError(t, json.Unmarshal(data, &f))
		if want == "" || f.Type == want {
			return f
		}
	}
}
