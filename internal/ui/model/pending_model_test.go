package model

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/v3/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/ui/util"
	"github.com/charmbracelet/crush/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

// pendingModelWorkspace records the model selections written while busy and
// reflects them in its config, the way the real workspace does once a write
// lands.
type pendingModelWorkspace struct {
	workspace.Workspace

	cfg    *config.Config
	writes int
}

func (w *pendingModelWorkspace) Config() *config.Config { return w.cfg }

func (w *pendingModelWorkspace) UpdatePreferredModel(_ config.Scope, typ config.SelectedModelType, model config.SelectedModel) error {
	w.writes++
	w.cfg.Models[typ] = model
	return nil
}

func (w *pendingModelWorkspace) UpdateAgentModel(context.Context) error { return nil }

// AgentIsBusy reports idle: these tests drive the UI's busy state through its
// memoized cache, as the real probes do.
func (w *pendingModelWorkspace) AgentIsBusy() bool { return false }

// pendingModelConfig is a config whose provider offers m2 alongside the
// active m1, plus the active theme, so a switch does not touch component
// styles a bare UI does not have.
func pendingModelConfig() *config.Config {
	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("p1", config.ProviderConfig{
		ID: "p1",
		Models: []catwalk.Model{
			{ID: "m1", Name: "Model One", Reasoning: catwalk.Reasoning{
				EffortLevels: catwalk.NewEffortLevels("low", "high"),
			}},
			{ID: "m2", Name: "Model Two", Reasoning: catwalk.Reasoning{
				EffortLevels: catwalk.NewEffortLevels("low", "high"),
			}},
			{ID: "m3", Name: "Model Three"},
		},
	})
	return &config.Config{
		Options:   &config.Options{TUI: &config.TUIOptions{ActiveTheme: "charmtone-pantera"}},
		Providers: providers,
		Models: map[config.SelectedModelType]config.SelectedModel{
			config.SelectedModelTypeMain:  {Provider: "p1", Model: "m1"},
			config.SelectedModelTypeSmall: {Provider: "p1", Model: "m1"},
		},
		Agents: map[string]config.Agent{
			config.AgentCoder: {Model: config.SelectedModelTypeMain},
		},
	}
}

// newPendingModelUI builds a UI whose workspace records the writes the queued
// selections make.
func newPendingModelUI(t *testing.T, cfg *config.Config) (*pendingModelWorkspace, *UI) {
	t.Helper()
	ws := &pendingModelWorkspace{cfg: cfg}
	u := newTestUI()
	u.com.Workspace = ws
	u.dialog = dialog.NewOverlay()
	return ws, u
}

// pendingIdleEdge probes the busy state as idle, the way the off-thread probe
// does once the agent finishes, and runs the resulting commands.
func pendingIdleEdge(u *UI) []util.InfoMsg {
	cmds := u.applyBusyState(busyStateMsg{
		gen:      u.busyFetchGen,
		permMode: permission.PermissionModeNormal,
	})
	return pendingInfoMsgs(runWebCmds(tea.Batch(cmds...)))
}

// pendingInfoMsgs keeps the status messages of a command run.
func pendingInfoMsgs(msgs []tea.Msg) []util.InfoMsg {
	var out []util.InfoMsg
	for _, msg := range msgs {
		if info, ok := msg.(util.InfoMsg); ok {
			out = append(out, info)
		}
	}
	return out
}

// TestSelectModelWhileBusyIsQueued pins that a model chosen while the agent
// works is remembered and confirmed instead of refused.
func TestSelectModelWhileBusyIsQueued(t *testing.T) {
	t.Parallel()

	ws, u := newPendingModelUI(t, pendingModelConfig())
	u.agentBusyCache.set(true)

	cmd := u.handleSelectModel(dialog.ActionSelectModel{
		Model:     config.SelectedModel{Provider: "p1", Model: "m2"},
		ModelType: config.SelectedModelTypeMain,
	})
	require.NotNil(t, u.pendingModelSelect, "the selection is remembered")
	require.Zero(t, ws.writes, "nothing is written while the agent is busy")
	require.Nil(t, u.pendingReasoningEffort)

	infos := pendingInfoMsgs(runWebCmds(cmd))
	require.Len(t, infos, 1)
	require.Equal(t, util.InfoTypeInfo, infos[0].Type, "the queued selection is good news, not a warning")
	require.Contains(t, infos[0].Msg, "when the current turn ends")
	require.Contains(t, infos[0].Msg, "Model Two")
}

// TestQueuedModelAppliesWhenIdle pins that the queued selection lands on the
// next idle probe, with the usual success message.
func TestQueuedModelAppliesWhenIdle(t *testing.T) {
	t.Parallel()

	ws, u := newPendingModelUI(t, pendingModelConfig())
	u.agentBusyCache.set(true)
	u.handleSelectModel(dialog.ActionSelectModel{
		Model:     config.SelectedModel{Provider: "p1", Model: "m2"},
		ModelType: config.SelectedModelTypeMain,
	})
	require.Zero(t, ws.writes)

	infos := pendingIdleEdge(u)

	require.Nil(t, u.pendingModelSelect, "the idle probe consumes the selection")
	require.Equal(t, 1, ws.writes, "the queued selection is written once")
	require.Equal(t, config.SelectedModel{Provider: "p1", Model: "m2"},
		ws.cfg.Models[config.SelectedModelTypeMain])

	var hasSuccess bool
	for _, info := range infos {
		require.NotEqual(t, util.InfoTypeWarn, info.Type, "the busy refusal is gone")
		require.NotContains(t, info.Msg, "busy")
		if info.Type == util.InfoTypeInfo && info.Msg == "Main model changed to Model Two" {
			hasSuccess = true
		}
	}
	require.True(t, hasSuccess, "got %v", infos)
}

// TestSelectModelWhileBusyQueuesWithoutProviders pins that the queued
// confirmation falls back to the raw model ID when the config has no
// provider catalog to resolve a display name from.
func TestSelectModelWhileBusyQueuesWithoutProviders(t *testing.T) {
	t.Parallel()

	cfg := pendingModelConfig()
	cfg.Providers = nil
	ws, u := newPendingModelUI(t, cfg)
	u.agentBusyCache.set(true)

	cmd := u.handleSelectModel(dialog.ActionSelectModel{
		Model:     config.SelectedModel{Provider: "p1", Model: "m2"},
		ModelType: config.SelectedModelTypeMain,
	})
	require.NotNil(t, u.pendingModelSelect)
	require.Zero(t, ws.writes)

	infos := pendingInfoMsgs(runWebCmds(cmd))
	require.Len(t, infos, 1)
	require.Contains(t, infos[0].Msg, "m2")
}

// TestReasoningEffortWhileBusyIsQueued pins that an effort chosen while the
// agent works is remembered and confirmed instead of refused.
func TestReasoningEffortWhileBusyIsQueued(t *testing.T) {
	t.Parallel()

	ws, u := newPendingModelUI(t, pendingModelConfig())
	u.agentBusyCache.set(true)

	cmd := u.handleSelectReasoningEffort(dialog.ActionSelectReasoningEffort{Effort: "high"})
	require.NotNil(t, u.pendingReasoningEffort, "the effort is remembered")
	require.Zero(t, ws.writes, "nothing is written while the agent is busy")

	infos := pendingInfoMsgs(runWebCmds(cmd))
	require.Len(t, infos, 1)
	require.Equal(t, util.InfoTypeInfo, infos[0].Type)
	require.Contains(t, infos[0].Msg, "when the current turn ends")
	require.Contains(t, infos[0].Msg, "high")
}

// TestQueuedReasoningEffortAppliesWhenIdle pins that the queued effort lands
// on the next idle probe.
func TestQueuedReasoningEffortAppliesWhenIdle(t *testing.T) {
	t.Parallel()

	ws, u := newPendingModelUI(t, pendingModelConfig())
	u.agentBusyCache.set(true)
	u.handleSelectReasoningEffort(dialog.ActionSelectReasoningEffort{Effort: "high"})
	require.Zero(t, ws.writes)

	pendingIdleEdge(u)

	require.Nil(t, u.pendingReasoningEffort, "the idle probe consumes the effort")
	require.Equal(t, 1, ws.writes, "the queued effort is written once")
	main := ws.cfg.Models[config.SelectedModelTypeMain]
	require.Equal(t, "high", main.ReasoningEffort)
	require.Equal(t, "p1", main.Provider, "the effort stays on the model it was queued for")
}

// TestQueuedModelThenEffortApplyInOrder pins that a queued model switch lands
// before the effort queued with it, so both apply to the same model.
func TestQueuedModelThenEffortApplyInOrder(t *testing.T) {
	t.Parallel()

	ws, u := newPendingModelUI(t, pendingModelConfig())
	u.agentBusyCache.set(true)
	u.handleSelectModel(dialog.ActionSelectModel{
		Model:     config.SelectedModel{Provider: "p1", Model: "m2"},
		ModelType: config.SelectedModelTypeMain,
	})
	u.handleSelectReasoningEffort(dialog.ActionSelectReasoningEffort{Effort: "high"})

	infos := pendingIdleEdge(u)

	require.Equal(t, 2, ws.writes, "the model write and the effort write both land")
	for _, info := range infos {
		require.NotEqual(t, util.InfoTypeError, info.Type, "got %v", infos)
	}
	require.Equal(t, config.SelectedModel{Provider: "p1", Model: "m2", ReasoningEffort: "high"},
		ws.cfg.Models[config.SelectedModelTypeMain])
}

// TestQueuedModelSelectionIsReplacedByNewerOne pins that a second choice while
// busy replaces the first instead of piling up.
func TestQueuedModelSelectionIsReplacedByNewerOne(t *testing.T) {
	t.Parallel()

	ws, u := newPendingModelUI(t, pendingModelConfig())
	u.agentBusyCache.set(true)
	u.handleSelectModel(dialog.ActionSelectModel{
		Model:     config.SelectedModel{Provider: "p1", Model: "m2"},
		ModelType: config.SelectedModelTypeMain,
	})
	u.handleSelectModel(dialog.ActionSelectModel{
		Model:     config.SelectedModel{Provider: "p1", Model: "m3"},
		ModelType: config.SelectedModelTypeMain,
	})
	require.NotNil(t, u.pendingModelSelect)
	require.Equal(t, "m3", u.pendingModelSelect.Model.Model, "the latest choice wins")

	pendingIdleEdge(u)

	require.Nil(t, u.pendingModelSelect)
	require.Equal(t, 1, ws.writes, "only the latest choice is written")
	require.Equal(t, "m3", ws.cfg.Models[config.SelectedModelTypeMain].Model)
}

// TestQueuedEffortRejectedWhenNewModelLacksIt pins that an effort queued with
// a model switch is dropped, with an error, when the new model does not offer
// it: the model switch still lands.
func TestQueuedEffortRejectedWhenNewModelLacksIt(t *testing.T) {
	t.Parallel()

	ws, u := newPendingModelUI(t, pendingModelConfig())
	u.agentBusyCache.set(true)
	u.handleSelectModel(dialog.ActionSelectModel{
		Model:     config.SelectedModel{Provider: "p1", Model: "m3"},
		ModelType: config.SelectedModelTypeMain,
	})
	u.handleSelectReasoningEffort(dialog.ActionSelectReasoningEffort{Effort: "high"})

	infos := pendingIdleEdge(u)

	require.Nil(t, u.pendingReasoningEffort)
	require.Equal(t, 1, ws.writes, "only the model switch lands")
	main := ws.cfg.Models[config.SelectedModelTypeMain]
	require.Equal(t, "m3", main.Model)
	require.Empty(t, main.ReasoningEffort, "the model does not offer that effort")

	var hasError bool
	for _, info := range infos {
		if info.Type == util.InfoTypeError && info.Msg == `the model does not offer reasoning effort "high"` {
			hasError = true
		}
	}
	require.True(t, hasError, "got %v", infos)
}

// oauthDialog stands in for the sign-in dialog, which must stay dismissible
// while the agent works.
type oauthDialog struct{}

func (oauthDialog) ID() string { return dialog.OAuthID }

func (oauthDialog) HandleMsg(tea.Msg) dialog.Action { return nil }

func (oauthDialog) Draw(uv.Screen, uv.Rectangle) *tea.Cursor { return nil }

// TestOAuthDialogSelectModelAppliesWhileBusy pins that a model chosen inside
// the sign-in flow still applies at once, since that dialog has to stay
// dismissible while the agent works.
func TestOAuthDialogSelectModelAppliesWhileBusy(t *testing.T) {
	t.Parallel()

	ws, u := newPendingModelUI(t, pendingModelConfig())
	u.dialog = dialog.NewOverlay(oauthDialog{})
	u.agentBusyCache.set(true)

	u.handleSelectModel(dialog.ActionSelectModel{
		Model:     config.SelectedModel{Provider: "p1", Model: "m2"},
		ModelType: config.SelectedModelTypeMain,
	})
	require.Nil(t, u.pendingModelSelect, "the sign-in flow applies at once")
	require.Equal(t, 1, ws.writes)
}
