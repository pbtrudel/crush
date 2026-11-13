package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/catwalk/v3/pkg/catwalk"
	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/oauth/copilot"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func TestFusionPrepareBudgetsAndReuse(t *testing.T) {
	draft := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("answer")}
	judge := &fakeFusionLanguageModel{id: "judge", parts: fusionTextParts(`{"score":4}`)}
	repair := &fakeFusionLanguageModel{id: "repair"}
	model, router := newTestFusionModel(&fusionPlan{Pattern: "cascade", Steps: []fusionStep{{ModelID: "draft"}, {ModelID: "judge"}, {ModelID: "repair"}}}, map[string]*fakeFusionLanguageModel{"draft": draft, "judge": judge, "repair": repair})
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 272000}, "judge": {MaxPromptTokens: 50000}, "repair": {MaxPromptTokens: 100000}}
	}
	require.NoError(t, model.prepare(context.Background()))
	budget, known := model.PromptBudget()
	require.True(t, known)
	require.Equal(t, int64(100000), budget)
	budget, known = model.ReviewBudget()
	require.True(t, known)
	require.Equal(t, int64(50000), budget)
	stream, err := model.Stream(context.Background(), fantasy.Call{})
	require.NoError(t, err)
	require.Equal(t, "answer", collectFusionText(t, stream))
	require.Len(t, router.prompts, 1)
}

func TestFusionReservesRequestedOutput(t *testing.T) {
	model, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, nil)
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxContextWindowTokens: 10000, MaxOutputTokens: 1000}}
	}
	model.cfg.MaxOutput = func(string) int64 { return 2000 }
	model.requestedOutput = 4000
	require.NoError(t, model.prepare(context.Background()))
	budget, known := model.PromptBudget()
	require.True(t, known)
	require.Equal(t, int64(6000), budget)
	model.requestedOutput = 500
	budget, known = model.PromptBudget()
	require.True(t, known)
	require.Equal(t, int64(8000), budget)
	model.cfg.MaxOutput = nil
	budget, known = model.PromptBudget()
	require.True(t, known)
	require.Equal(t, int64(9000), budget)
	model.limits["draft"] = copilot.ModelLimits{MaxPromptTokens: 7000}
	model.requestedOutput = 4000
	budget, known = model.PromptBudget()
	require.True(t, known)
	require.Equal(t, int64(7000), budget)
}

func TestFusionPlanBudgetRespectsExecutedPhases(t *testing.T) {
	tests := []struct {
		pattern string
		steps   []fusionStep
		want    int64
		review  bool
	}{
		{"solo", []fusionStep{{ModelID: "draft"}, {ModelID: "repair"}, {ModelID: "repair"}}, 200000, false},
		{"cascade", []fusionStep{{ModelID: "draft"}, {ModelID: "repair", Role: "repair"}}, 100000, true},
	}
	for _, test := range tests {
		t.Run(test.pattern, func(t *testing.T) {
			model, _ := newTestFusionModel(&fusionPlan{Pattern: test.pattern, Steps: test.steps}, nil)
			model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
				return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 200000}, "repair": {MaxPromptTokens: 100000}}
			}
			require.NoError(t, model.prepare(context.Background()))
			budget, known := model.PromptBudget()
			require.True(t, known)
			require.Equal(t, test.want, budget)
			_, known = model.ReviewBudget()
			require.Equal(t, test.review, known)
		})
	}
}

func TestFusionUsesBaseTierLimits(t *testing.T) {
	model, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft[long]"}}}, nil)
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 100000}, "draft[long]": {MaxPromptTokens: 1000000}}
	}
	require.NoError(t, model.prepare(t.Context()))
	budget, known := model.PromptBudget()
	require.True(t, known)
	require.Equal(t, int64(100000), budget)
}

func TestFusionUnknownHistoryPhaseBudget(t *testing.T) {
	for _, knownID := range []string{"draft", "repair"} {
		t.Run(knownID, func(t *testing.T) {
			model, _ := newTestFusionModel(&fusionPlan{Pattern: "cascade", Steps: []fusionStep{{ModelID: "draft"}, {ModelID: "judge"}, {ModelID: "repair"}}}, nil)
			model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
				return map[string]copilot.ModelLimits{knownID: {MaxPromptTokens: 100000}}
			}
			require.NoError(t, model.prepare(context.Background()))
			_, known := model.PromptBudget()
			require.False(t, known)
		})
	}
}

func TestFusionRepairHandoffCanExceedBudget(t *testing.T) {
	draft := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts(strings.Repeat("d", 4000))}
	judge := &fakeFusionLanguageModel{id: "judge", parts: fusionTextParts(`{"score":1,"rationale":"wrong"}`)}
	repair := &fakeFusionLanguageModel{id: "repair", parts: fusionTextParts("repair answer")}
	model, _ := newTestFusionModel(&fusionPlan{Pattern: "cascade", Steps: []fusionStep{{ModelID: "draft"}, {ModelID: "judge"}, {ModelID: "repair"}}}, map[string]*fakeFusionLanguageModel{"draft": draft, "judge": judge, "repair": repair})
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 100000}, "judge": {MaxPromptTokens: 100000}, "repair": {MaxPromptTokens: 500}}
	}
	require.NoError(t, model.prepare(context.Background()))
	_, err := model.Stream(context.Background(), fantasy.Call{Prompt: fantasy.Prompt{fantasy.NewUserMessage("small history")}})
	require.ErrorIs(t, err, errFusionPhaseBudget)
	require.Len(t, judge.calls, 1)
	require.Empty(t, repair.calls)
}

func TestFusionOverBudgetPhasesFail(t *testing.T) {
	for _, phase := range []string{"review", "repair"} {
		t.Run(phase, func(t *testing.T) {
			draft := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("draft answer")}
			judge := &fakeFusionLanguageModel{id: "judge", parts: fusionTextParts(`{"score":1,"rationale":"wrong"}`)}
			repair := &fakeFusionLanguageModel{id: "repair", parts: fusionTextParts("repair answer")}
			model, _ := newTestFusionModel(&fusionPlan{Pattern: "cascade", Steps: []fusionStep{{ModelID: "draft"}, {ModelID: "judge"}, {ModelID: "repair"}}}, map[string]*fakeFusionLanguageModel{"draft": draft, "judge": judge, "repair": repair})
			limits := map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 100000}, "judge": {MaxPromptTokens: 100000}, "repair": {MaxPromptTokens: 100000}}
			limits[map[string]string{"review": "judge", "repair": "repair"}[phase]] = copilot.ModelLimits{MaxPromptTokens: 1}
			model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits { return limits }
			require.NoError(t, model.prepare(context.Background()))
			_, err := model.Stream(context.Background(), fantasy.Call{Prompt: fantasy.Prompt{fantasy.NewUserMessage(strings.Repeat("history ", 100))}})
			require.ErrorIs(t, err, errFusionPhaseBudget)
			require.ErrorContains(t, err, phase)
			require.Empty(t, repair.calls)
			if phase == "review" {
				require.Empty(t, judge.calls)
			} else {
				require.Len(t, judge.calls, 1)
			}
		})
	}
}

func TestFusionPreflightCompactsWithPreparedPrimary(t *testing.T) {
	env := testEnv(t)
	primary := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("answer")}
	plan := &fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}
	fusion, router := newTestFusionModel(plan, map[string]*fakeFusionLanguageModel{"draft": primary})
	fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 100000}}
	}
	agent := newCompactionAgent(env, primary, nil, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &fusion.cfg
	agent.largeModel.Set(selected)
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "preflight")
	require.NoError(t, err)
	_, err = env.messages.Create(t.Context(), sess.ID, message.CreateMessageParams{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: bigText(110000)}}})
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: "hello"})
	require.NoError(t, err)
	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, updated.SummaryMessageID)
	require.Len(t, router.prompts, 1)
	require.GreaterOrEqual(t, len(primary.calls), 2)
}

type growingFusionReviewer struct {
	fakeFusionLanguageModel
}

func (m *growingFusionReviewer) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	if len(m.calls) == 0 {
		m.parts = []fantasy.StreamPart{
			{Type: fantasy.StreamPartTypeToolCall, ID: "read", ToolCallName: "view", ToolCallInput: `{"path":"` + strings.Repeat("x", 12000) + `"}`},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls},
		}
	} else {
		m.parts = fusionTextParts(`{"score":4}`)
	}
	return m.fakeFusionLanguageModel.Stream(ctx, call)
}

func TestFusionReviewRechecksToolSteps(t *testing.T) {
	judge := &growingFusionReviewer{}
	model, _ := newTestFusionModel(&fusionPlan{Pattern: "cascade", Steps: []fusionStep{{ModelID: "draft"}, {ModelID: "judge"}, {ModelID: "repair"}}}, nil)
	model.cfg.Factory = func(context.Context, string, string) (fantasy.LanguageModel, error) { return judge, nil }
	model.tools = []fantasy.AgentTool{&recordingTool{name: "view"}}
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"judge": {MaxPromptTokens: 2000}}
	}
	require.NoError(t, model.prepare(t.Context()))
	_, _, err := model.review(t.Context(), model.plan, "draft")
	require.ErrorIs(t, err, errFusionPhaseBudget)
	require.Len(t, judge.calls, 1)
}

func TestFusionRepairRechecksEachStep(t *testing.T) {
	repair := &fakeFusionLanguageModel{id: "repair", parts: fusionTextParts("answer")}
	model, _ := newTestFusionModel(&fusionPlan{Pattern: "cascade", Steps: []fusionStep{{ModelID: "draft"}, {ModelID: "judge"}, {ModelID: "repair"}}}, map[string]*fakeFusionLanguageModel{"repair": repair})
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"repair": {MaxContextWindowTokens: 2000, MaxOutputTokens: 100}}
	}
	require.NoError(t, model.prepare(context.Background()))
	model.phase = "repair"
	model.handoff = "feedback"
	output := int64(1500)
	_, err := model.Stream(t.Context(), fantasy.Call{Prompt: fantasy.Prompt{fantasy.NewUserMessage(bigText(600))}, MaxOutputTokens: &output})
	require.ErrorIs(t, err, errFusionPhaseBudget)
	require.Empty(t, repair.calls)
}

func TestFusionRepairCountsToolSchema(t *testing.T) {
	draft := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("draft")}
	judge := &fakeFusionLanguageModel{id: "judge", parts: fusionTextParts(`{"score":1,"rationale":"wrong"}`)}
	repair := &fakeFusionLanguageModel{id: "repair", parts: fusionTextParts("repair")}
	model, _ := newTestFusionModel(&fusionPlan{Pattern: "cascade", Steps: []fusionStep{{ModelID: "draft"}, {ModelID: "judge"}, {ModelID: "repair"}}}, map[string]*fakeFusionLanguageModel{"draft": draft, "judge": judge, "repair": repair})
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 100000}, "judge": {MaxPromptTokens: 100000}, "repair": {MaxPromptTokens: 500}}
	}
	require.NoError(t, model.prepare(context.Background()))
	call := fantasy.Call{Prompt: fantasy.Prompt{fantasy.NewUserMessage("small")}, Tools: []fantasy.Tool{fantasy.FunctionTool{Name: "tool", Description: strings.Repeat("x", 4000)}}}
	_, err := model.Stream(context.Background(), call)
	require.ErrorIs(t, err, errFusionPhaseBudget)
	require.Empty(t, repair.calls)
}

func TestFusionDisabledThresholdDoesNotStopMidTurn(t *testing.T) {
	env := testEnv(t)
	cfg, err := config.Init(t.Context(), env.workingDir, "", false)
	require.NoError(t, err)
	off := 0.0
	cfg.Config().Options.AutoCompactThreshold = &off
	primary := &fakeFusionLanguageModel{id: "draft", parts: fusionTextParts("answer")}
	fusion, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, map[string]*fakeFusionLanguageModel{"draft": primary})
	fusion.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 10}}
	}
	agent := newCompactionAgent(env, primary, cfg, false)
	selected := agent.largeModel.Get()
	selected.Fusion = &fusion.cfg
	agent.models = func() ModelSet { return ModelSet{Large: selected, Small: selected} }
	sess, err := env.sessions.Create(t.Context(), "disabled")
	require.NoError(t, err)
	_, err = agent.Run(t.Context(), SessionAgentCall{SessionID: sess.ID, Prompt: strings.Repeat("a", 100)})
	require.NoError(t, err)
	updated, err := env.sessions.Get(t.Context(), sess.ID)
	require.NoError(t, err)
	require.Empty(t, updated.SummaryMessageID)
}

func TestFusionWindowAffectsCompaction(t *testing.T) {
	model, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, nil)
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 272000}}
	}
	require.NoError(t, model.prepare(context.Background()))
	selected := Model{Model: model, CatwalkCfg: catwalk.Model{ContextWindow: 200000}}
	agent := &sessionAgent{}
	require.False(t, agent.shouldAutoCompact(selected, session.Session{PromptTokens: 120000}))
	require.Equal(t, int64(272000), effectiveContextWindow(selected))
}

func TestFusionPreflightCountsToolSchemas(t *testing.T) {
	model, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, nil)
	model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
		return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: 1000}}
	}
	require.NoError(t, model.prepare(context.Background()))
	agent := &sessionAgent{}
	selected := Model{Model: model, CatwalkCfg: catwalk.Model{ContextWindow: 200000}}
	tool := &recordingTool{name: strings.Repeat("x", 3000)}
	require.True(t, agent.shouldCompactFusionPreflight(selected, session.Session{}, nil, "", "", []fantasy.AgentTool{tool}, nil))
}

func TestFusionPreflightBudget(t *testing.T) {
	tests := []struct {
		name            string
		budget          int64
		known, disabled bool
		history         int64
		last            int64
		want            bool
	}{
		{"shrink", 100000, true, false, 95000, 0, true},
		{"stale usage ignored", 100000, true, false, 1000, 95000, false},
		{"unknown", 0, false, false, 95000, 0, false},
		{"disabled", 100000, true, true, 95000, 0, false},
		{"large", 272000, true, false, 120000, 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, _ := newTestFusionModel(&fusionPlan{Pattern: "solo", Steps: []fusionStep{{ModelID: "draft"}}}, nil)
			if test.known {
				model.cfg.Limits = func(context.Context) map[string]copilot.ModelLimits {
					return map[string]copilot.ModelLimits{"draft": {MaxPromptTokens: test.budget}}
				}
			}
			require.NoError(t, model.prepare(context.Background()))
			agent := &sessionAgent{disableAutoSummarize: test.disabled}
			selected := Model{Model: model, CatwalkCfg: catwalk.Model{ContextWindow: 200000}}
			msgs := []message.Message{textMessage(message.User, bigText(test.history))}
			require.Equal(t, test.want, agent.shouldCompactFusionPreflight(selected, session.Session{PromptTokens: test.last}, msgs, "", "", nil, nil))
		})
	}
}
