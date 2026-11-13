package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/oauth/copilot"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

type failingPreflightRouter struct{}

func (failingPreflightRouter) route(context.Context, string, string) (*fusionPlan, error) {
	return nil, errors.New("route failed")
}

func TestFusionPreflightReservesRequestedOutput(t *testing.T) {
	env := testEnv(t)
	primary := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("answer")}
	fusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, map[string]*fakeFusionLanguageModel{"draft": primary})
	fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxContextWindowTokens: 200000, MaxOutputTokens: 1000}}
	}
	fusion.cfg.MaxOutput = func(string) int64 { return 1000 }
	agent := newCompactionAgent(env, primary, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &fusion.cfg
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "output reserve")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(70000)}}})
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello", MaxOutputTokens: 100000})
	require.NoError(t, err)
	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID)
}

func TestFusionSwitchCompactsWithPreviousModel(t *testing.T) {
	env := testEnv(t)
	primary := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("answer")}
	previous := &fakeFusionLanguageModel{id: "previous", parts: fusionTextParts("old summary")}
	fusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, map[string]*fakeFusionLanguageModel{"draft": primary})
	fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 100000}}
	}
	agent := newCompactionAgent(env, primary, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &fusion.cfg
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	agent.previousLarge = Model{Model: previous, ModelCfg: config.SelectedModel{Model: "previous"}, CatwalkCfg: catwalk.Model{ContextWindow: 400000}}
	agent.modelSwitchPending = true
	sess, err := env.sessions.Create(t.Context(), "switch")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(110000)}}})
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, previous.calls)
}

func TestConsecutiveFusionTurnCompactsBeforeSmallerRoute(t *testing.T) {
	env := testEnv(t)
	largePrimary := &fakeFusionLanguageModel{id: "large", parts: fusionTextParts("first answer")}
	smallPrimary := &fakeFusionLanguageModel{id: "small", parts: fusionTextParts("second answer")}
	largeFusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "large"}}}, map[string]*fakeFusionLanguageModel{"large": largePrimary})
	largeFusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"large": {MaxPromptTokens: 300000}}
	}
	smallFusion, router := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "small"}}}, map[string]*fakeFusionLanguageModel{"small": smallPrimary})
	smallFusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"small": {MaxPromptTokens: 100000}}
	}
	agent := newCompactionAgent(env, largePrimary, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &largeFusion.cfg
	agent.largeModel.Set(selected)
	agent.models = func() ModelSet {
		selected := agent.largeModel.Get()
		return ModelSet{Large: selected, Small: selected}
	}
	sess, err := env.sessions.Create(t.Context(), "consecutive fusion")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(110000)}}})
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "first"})
	require.NoError(t, err)
	agent.largeModel.Set(Model{Model: smallFusion, Fusion: &smallFusion.cfg, CatwalkCfg: catwalk.Model{ContextWindow: 200000}})
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "second"})
	require.NoError(t, err)
	require.Len(t, largePrimary.calls, 2, "previous routed model should summarize before the next primary runs")
	require.Len(t, smallPrimary.calls, 1)
	require.Len(t, router.prompts, 1)
	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID)
}

func TestFusionRouteCompactsAssembledPromptWithPreviousModel(t *testing.T) {
	for _, repairLimited := range []bool{false, true} {
		t.Run(fmt.Sprintf("repair_limited_%t", repairLimited), func(t *testing.T) {
			env := testEnv(t)
			previous := &fakeFusionLanguageModel{id: "large", parts: fusionTextParts("summary")}
			primary := &fakeFusionLanguageModel{id: "small", parts: fusionTextParts("answer")}
			repair := &fakeFusionLanguageModel{id: "repair", parts: fusionTextParts("repaired")}
			plan := &fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "small"}}}
			limits := map[string]copilot.ModelLimits{"small": {MaxPromptTokens: 100000}}
			if repairLimited {
				plan = &fusionPlan{Pattern: "review", Steps: []fusionStep{{ModelID: "small"}, {ModelID: "small"}, {ModelID: "repair"}}}
				limits = map[string]copilot.ModelLimits{"small": {MaxPromptTokens: 300000}, "repair": {MaxPromptTokens: 100000}}
			}
			fusion, _ := newTestFusionModel(plan, map[string]*fakeFusionLanguageModel{"small": primary, "repair": repair})
			fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits { return limits }
			agent := newCompactionAgent(env, previous, nil, false)
			selected := Model{Model: fusion, Fusion: &fusion.cfg, CatwalkCfg: catwalk.Model{ContextWindow: 300000}}
			agent.largeModel.Set(selected)
			agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
			sess, err := env.sessions.Create(t.Context(), "assembled")
			require.NoError(t, err)
			_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(20000)}}})
			require.NoError(t, err)
			agent.fusionCompactionModels.Set(sess.ID, fusionCompactionModel{model: Model{Model: previous, ModelCfg: config.SelectedModel{Provider: "copilot", Model: "large"}, CatwalkCfg: catwalk.Model{ContextWindow: 300000}}, budget: 300000})
			_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: bigText(35000)})
			require.NoError(t, err)
			require.Len(t, previous.calls, 1, "the previous model must summarize when the assembled request crosses the route budget")
			updated, err := env.sessions.Get(t.Context(), sess.ID)
			require.NoError(t, err)
			require.NotEmpty(t, updated.SummaryMessageID)
		})
	}
}

func TestFusionRouteSummaryUsesRetainedModelWhenCurrentIdentityMatches(t *testing.T) {
	env := testEnv(t)
	previous := &fakeFusionLanguageModel{id: "large", parts: fusionTextParts("retained summary")}
	primary := &fakeFusionLanguageModel{id: "small", parts: fusionTextParts("answer")}
	fusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "small"}}}, map[string]*fakeFusionLanguageModel{"small": primary})
	fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"small": {MaxPromptTokens: 100000}}
	}
	agent := newCompactionAgent(env, previous, nil, false)
	identity := config.SelectedModel{Provider: "copilot", Model: "large"}
	selected := Model{Model: fusion, Fusion: &fusion.cfg, ModelCfg: identity, CatwalkCfg: catwalk.Model{ContextWindow: 300000}}
	agent.largeModel.Set(selected)
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "retained model")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(60000)}}})
	require.NoError(t, err)
	agent.fusionCompactionModels.Set(sess.ID, fusionCompactionModel{model: Model{Model: previous, ModelCfg: identity, CatwalkCfg: catwalk.Model{ContextWindow: 300000}}, budget: 300000})
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "continue"})
	require.NoError(t, err)
	require.Len(t, previous.calls, 1, "summary must use the retained concrete model")
	require.Len(t, primary.calls, 1, "the new route must only receive the turn")
}

func TestNonFusionModelSwitchCompactsWhenAutoSummarizeDisabled(t *testing.T) {
	env := testEnv(t)
	previous := &fakeFusionLanguageModel{id: "previous", parts: fusionTextParts("summary")}
	current := &fakeFusionLanguageModel{id: "current", parts: fusionTextParts("answer")}
	agent := newCompactionAgent(env, current, nil, true)
	agent.previousLarge = Model{Model: previous, ModelCfg: config.SelectedModel{Model: "previous"}, CatwalkCfg: catwalk.Model{ContextWindow: 300000}}
	agent.modelSwitchPending = true
	selected := Model{Model: current, ModelCfg: config.SelectedModel{Model: "current"}, CatwalkCfg: catwalk.Model{ContextWindow: 100000}}
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "disabled auto compaction")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(60000)}}})
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "continue"})
	require.NoError(t, err)
	require.Len(t, previous.calls, 1)
	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID)
}

func TestFusionPreflightRejectsOversizePromptAfterCompaction(t *testing.T) {
	env := testEnv(t)
	primary := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("summary")}
	fusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, map[string]*fakeFusionLanguageModel{"draft": primary})
	fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 10000}}
	}
	agent := newCompactionAgent(env, primary, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &fusion.cfg
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "oversize")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(1000)}}})
	require.NoError(t, err)
	prompt := bigText(12000)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: prompt})
	require.ErrorContains(t, err, "budget")
	require.Len(t, primary.calls, 1)
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Equal(t, prompt, msgs[len(msgs)-1].Content().Text)
}

func TestFusionPreflightIgnoresStaleUsageWhenHistoryFits(t *testing.T) {
	model, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, nil)
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 100000}}
	}
	require.NoError(t, model.prepare(context.Background()))
	agent := &sessionAgent{}
	selected := Model{Model: model, CatwalkCfg: catwalk.Model{ContextWindow: 200000}}
	msgs := []message.Message{textMessage(message.User, bigText(1000))}
	require.False(t, agent.shouldCompactFusionPreflight(selected, session.Session{PromptTokens: 120000}, msgs, "", "", nil, nil))
}

func TestFusionExhaustedWindowFailsWithoutCompaction(t *testing.T) {
	env := testEnv(t)
	primary := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("answer")}
	fusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, map[string]*fakeFusionLanguageModel{"draft": primary})
	fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxContextWindowTokens: 1000, MaxOutputTokens: 1000}}
	}
	agent := newCompactionAgent(env, primary, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &fusion.cfg
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "exhausted")
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "retain this"})
	require.ErrorContains(t, err, "budget")
	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, updated.SummaryMessageID)
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "retain this", msgs[0].Content().Text)
}

type failingFusionSummaryModel struct{ fakeFusionLanguageModel }

func (*failingFusionSummaryModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	return nil, errors.New("summary failed")
}

func TestFusionPreflightSummaryFailurePreservesHistory(t *testing.T) {
	for _, name := range []string{"preflight", "switch"} {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t)
			primary := &failingFusionSummaryModel{}
			fusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, nil)
			fusion.cfg.Factory = func(context.Context, string, string) (fantasy.LanguageModel, error) { return primary, nil }
			fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
				return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 10000}}
			}
			agent := newCompactionAgent(env, primary, nil, false)
			selected := agent.largeModel.Get()
			selected.Fusion = &fusion.cfg
			agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
			if name == "switch" {
				agent.previousLarge = Model{Model: primary, CatwalkCfg: catwalk.Model{ContextWindow: 400000}}
				agent.modelSwitchPending = true
			}
			sess, err := env.sessions.Create(t.Context(), "failure")
			require.NoError(t, err)
			old, err := env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(6000)}}})
			require.NoError(t, err)
			_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "retain this"})
			require.ErrorContains(t, err, "summary failed")
			updated, err := env.sessions.Get(t.Context(), sess.ID)
			require.NoError(t, err)
			require.Empty(t, updated.SummaryMessageID)
			msgs, err := env.messages.List(t.Context(), sess.ID)
			require.NoError(t, err)
			require.Equal(t, old.ID, msgs[0].ID)
			require.Equal(t, "retain this", msgs[len(msgs)-1].Content().Text)
			if name == "switch" {
				require.Equal(t, agent.previousLarge, agent.largeModel.Get())
			}
		})
	}
}

type noteAddingSummaryModel struct {
	fakeFusionLanguageModel
	addNote func()
}

func (m *noteAddingSummaryModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if len(m.calls) == 0 {
		m.addNote()
	}
	return m.fakeFusionLanguageModel.Stream(ctx, call)
}

func TestFusionPreflightLeavesLateNotesQueued(t *testing.T) {
	env := testEnv(t)
	primary := &noteAddingSummaryModel{fakeFusionLanguageModel: fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("summary")}}
	fusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, nil)
	fusion.cfg.Factory = func(context.Context, string, string) (fantasy.LanguageModel, error) { return primary, nil }
	fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 10000}}
	}
	agent := newCompactionAgent(env, primary, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &fusion.cfg
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "late note")
	require.NoError(t, err)
	agent.AddNote(sess.ID, "early note")
	primary.addNote = func() { agent.AddNote(sess.ID, "late note") }
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(6000)}}})
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)
	require.Equal(t, []string{"late note"}, agent.takeNotes(sess.ID))
}

type canceledPreflightRouter struct{}

func (canceledPreflightRouter) route(context.Context, string, string) (*fusionPlan, error) {
	return nil, context.Canceled
}

func TestFusionPreflightCancellationPersistsMarker(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, nil, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &FusionConfig{Router: canceledPreflightRouter{}}
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "cancel preflight")
	require.NoError(t, err)
	agent.AddNote(sess.ID, "retained note")
	defer func() { require.Equal(t, []string{"retained note"}, agent.takeNotes(sess.ID)) }()
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "retain this prompt"})
	require.ErrorIs(t, err, context.Canceled)
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, message.FinishReasonCanceled, msgs[1].FinishReason())
}

func TestFusionPreflightRouteFailurePersistsPrompt(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, nil, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &FusionConfig{Router: failingPreflightRouter{}}
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "routing error")
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "retain this prompt"})
	require.ErrorContains(t, err, "route failed")
	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, message.User, msgs[0].Role)
	require.Equal(t, "retain this prompt", msgs[0].Content().Text)
}

func TestFusionPreflightCancellationRestoresNotes(t *testing.T) {
	env := testEnv(t)
	agent := newCompactionAgent(env, nil, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &FusionConfig{Router: canceledPreflightRouter{}}
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "canceled notes")
	require.NoError(t, err)
	agent.AddNote(sess.ID, "keep me")
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []string{"keep me"}, agent.takeNotes(sess.ID))
}

func TestFusionRouteRetentionIsSessionScoped(t *testing.T) {
	env := testEnv(t)
	largePrimary := &fakeFusionLanguageModel{id: "large", parts: fusionTextParts("first answer")}
	smallPrimary := &fakeFusionLanguageModel{id: "small", parts: fusionTextParts("second answer")}
	largeFusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "large"}}}, map[string]*fakeFusionLanguageModel{"large": largePrimary})
	largeFusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"large": {MaxPromptTokens: 300000}}
	}
	smallFusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "small"}}}, map[string]*fakeFusionLanguageModel{"small": smallPrimary})
	smallFusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"small": {MaxPromptTokens: 100000}}
	}
	agent := newCompactionAgent(env, largePrimary, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &largeFusion.cfg
	agent.largeModel.Set(selected)
	agent.models = func() ModelSet {
		selected := agent.largeModel.Get()
		return ModelSet{Large: selected, Small: selected}
	}
	first, err := env.sessions.Create(t.Context(), "first")
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: first.ID, Prompt: "first"})
	require.NoError(t, err)
	other, err := env.sessions.Create(t.Context(), "other")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), other.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(110000)}}})
	require.NoError(t, err)
	agent.largeModel.Set(Model{Model: smallFusion, Fusion: &smallFusion.cfg, CatwalkCfg: catwalk.Model{ContextWindow: 200000}})
	require.True(t, agent.titles.wait(5*time.Second))
	before := len(largePrimary.calls)
	_, _ = agent.Run(t.Context(), SessionAgentCall{SessionID: other.ID, Prompt: "second"})
	require.True(t, agent.titles.wait(5*time.Second))
	for _, call := range largePrimary.calls[before:] {
		require.Contains(t, fmt.Sprint(call.Prompt[0]), "generate a short title", "another session's routed model must not summarize this session")
	}
	require.NotEmpty(t, smallPrimary.calls)
}

func TestFusionRouteCompactionFailurePreservesHistory(t *testing.T) {
	env := testEnv(t)
	largePrimary := &fakeFusionLanguageModel{id: "large", parts: fusionTextParts("first answer")}
	smallPrimary := &fakeFusionLanguageModel{id: "small", parts: fusionTextParts("second answer")}
	largeFusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "large"}}}, map[string]*fakeFusionLanguageModel{"large": largePrimary})
	largeFusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"large": {MaxPromptTokens: 300000}}
	}
	smallFusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "small"}}}, map[string]*fakeFusionLanguageModel{"small": smallPrimary})
	smallFusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"small": {MaxPromptTokens: 100000}}
	}
	agent := newCompactionAgent(env, largePrimary, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &largeFusion.cfg
	agent.largeModel.Set(selected)
	agent.models = func() ModelSet {
		selected := agent.largeModel.Get()
		return ModelSet{Large: selected, Small: selected}
	}
	sess, err := env.sessions.Create(t.Context(), "failure")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(110000)}}})
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "first"})
	require.NoError(t, err)
	before, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	largePrimary.parts = []fantasy.StreamPart{{Type: fantasy.StreamPartTypeError, Error: errors.New("summary failed")}}
	agent.largeModel.Set(Model{Model: smallFusion, Fusion: &smallFusion.cfg, CatwalkCfg: catwalk.Model{ContextWindow: 200000}})
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "second"})
	require.Error(t, err)
	require.Empty(t, smallPrimary.calls)
	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, updated.SummaryMessageID)
	after, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(after), len(before))
	require.Equal(t, before[0].ID, after[0].ID)
}
