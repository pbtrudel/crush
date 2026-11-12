package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

type fakeFusionLanguageModel struct {
	id    string
	parts []fantasy.StreamPart
	calls []fantasy.Call
}

func (f *fakeFusionLanguageModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	f.calls = append(f.calls, call)
	return replayStreamParts(f.parts), nil
}

func (f *fakeFusionLanguageModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeFusionLanguageModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeFusionLanguageModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeFusionLanguageModel) Provider() string { return "fake" }
func (f *fakeFusionLanguageModel) Model() string    { return f.id }

type fakeFusionRouter struct {
	plan    *fusionPlan
	prompts []string
}

func (f *fakeFusionRouter) route(_ context.Context, _ string, prompt string) (*fusionPlan, error) {
	f.prompts = append(f.prompts, prompt)
	return f.plan, nil
}

func fusionTextParts(text string) []fantasy.StreamPart {
	return []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeTextStart, ID: "text"},
		{Type: fantasy.StreamPartTypeTextDelta, ID: "text", Delta: text},
		{Type: fantasy.StreamPartTypeTextEnd, ID: "text"},
		{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop},
	}
}

func newTestFusionModel(plan *fusionPlan, models map[string]*fakeFusionLanguageModel) (*fusionModel, *fakeFusionRouter) {
	router := &fakeFusionRouter{plan: plan}
	cfg := FusionConfig{
		Tier:    fusionTierHigh,
		ModelID: "fusion-high",
		Router:  router,
		Factory: func(_ context.Context, modelID, _ string) (fantasy.LanguageModel, error) {
			model, ok := models[modelID]
			if !ok {
				return nil, errors.New("no fake for " + modelID)
			}
			return model, nil
		},
	}
	return newFusionModel(cfg, nil, "the original task"), router
}

func collectFusionText(t *testing.T, stream fantasy.StreamResponse) string {
	t.Helper()
	var b strings.Builder
	for part := range stream {
		if part.Type == fantasy.StreamPartTypeTextDelta {
			b.WriteString(part.Delta)
		}
	}
	return b.String()
}

func TestFusionModel_Solo(t *testing.T) {
	t.Parallel()

	primary := &fakeFusionLanguageModel{id: "gpt-6-luna", parts: fusionTextParts("solo answer")}
	model, router := newTestFusionModel(&fusionPlan{
		Pattern: "solo",
		Steps:   []fusionStep{{Role: "generation", ModelID: "gpt-6-luna"}},
	}, map[string]*fakeFusionLanguageModel{"gpt-6-luna": primary})

	stream, err := model.Stream(context.Background(), fantasy.Call{})
	require.NoError(t, err)
	require.Equal(t, "solo answer", collectFusionText(t, stream))
	require.Equal(t, []string{"the original task"}, router.prompts)
}

func TestFusionModel_CascadeAcceptsDraft(t *testing.T) {
	t.Parallel()

	draft := &fakeFusionLanguageModel{id: "claude-opus-5.5", parts: fusionTextParts("draft answer")}
	judge := &fakeFusionLanguageModel{id: "gpt-6-sol", parts: fusionTextParts(`{"score":4,"rationale":"complete"}`)}
	repair := &fakeFusionLanguageModel{id: "gpt-6-astra", parts: fusionTextParts("repair answer")}
	model, _ := newTestFusionModel(&fusionPlan{
		Pattern: "cascade",
		Steps: []fusionStep{
			{Role: "draft", ModelID: "claude-opus-5.5", ReasoningEffort: "xhigh"},
			{Role: "escalation", ModelID: "gpt-6-sol", ReasoningEffort: "high"},
			{Role: "repair", ModelID: "gpt-6-astra", ReasoningEffort: "xhigh"},
		},
	}, map[string]*fakeFusionLanguageModel{
		"claude-opus-5.5": draft,
		"gpt-6-sol":       judge,
		"gpt-6-astra":     repair,
	})

	stream, err := model.Stream(context.Background(), fantasy.Call{})
	require.NoError(t, err)
	require.Equal(t, "draft answer", collectFusionText(t, stream))
	require.Len(t, judge.calls, 1)
	require.Empty(t, repair.calls)
}

func TestFusionModel_CascadeRepairsRejectedDraft(t *testing.T) {
	t.Parallel()

	draft := &fakeFusionLanguageModel{id: "claude-opus-5.5", parts: fusionTextParts("draft answer")}
	judge := &fakeFusionLanguageModel{id: "gpt-6-sol", parts: fusionTextParts(`{"score":1,"rationale":"wrong fix"}`)}
	repair := &fakeFusionLanguageModel{id: "gpt-6-astra", parts: fusionTextParts("repair answer")}
	model, _ := newTestFusionModel(&fusionPlan{
		Pattern: "cascade",
		Steps: []fusionStep{
			{Role: "draft", ModelID: "claude-opus-5.5"},
			{Role: "escalation", ModelID: "gpt-6-sol"},
			{Role: "repair", ModelID: "gpt-6-astra"},
		},
	}, map[string]*fakeFusionLanguageModel{
		"claude-opus-5.5": draft,
		"gpt-6-sol":       judge,
		"gpt-6-astra":     repair,
	})

	stream, err := model.Stream(context.Background(), fantasy.Call{})
	require.NoError(t, err)
	require.Equal(t, "repair answer", collectFusionText(t, stream))
	require.Len(t, repair.calls, 1)

	handoff := fusionPromptFromCall(repair.calls[0])
	require.Contains(t, handoff, "repair handoff")
	require.Contains(t, handoff, "draft answer")
	require.Contains(t, handoff, "wrong fix")
}

func TestFusionModel_CritiqueApprovesDraft(t *testing.T) {
	t.Parallel()

	draft := &fakeFusionLanguageModel{id: "claude-opus-5.5", parts: fusionTextParts("draft answer")}
	critic := &fakeFusionLanguageModel{id: "gpt-6-luna", parts: fusionTextParts(`{"assessment":"approve","feedback":"fine","defect":null}`)}
	revision := &fakeFusionLanguageModel{id: "gpt-6-astra", parts: fusionTextParts("revision answer")}
	model, _ := newTestFusionModel(&fusionPlan{
		Pattern: "critique",
		Steps: []fusionStep{
			{Role: "draft", ModelID: "claude-opus-5.5"},
			{Role: "critic", ModelID: "gpt-6-luna"},
			{Role: "revision", ModelID: "gpt-6-astra"},
		},
	}, map[string]*fakeFusionLanguageModel{
		"claude-opus-5.5": draft,
		"gpt-6-luna":      critic,
		"gpt-6-astra":     revision,
	})

	stream, err := model.Stream(context.Background(), fantasy.Call{})
	require.NoError(t, err)
	require.Equal(t, "draft answer", collectFusionText(t, stream))
	require.Empty(t, revision.calls)
}

func TestFusionModel_CritiqueRevises(t *testing.T) {
	t.Parallel()

	draft := &fakeFusionLanguageModel{id: "claude-opus-5.5", parts: fusionTextParts("draft answer")}
	critic := &fakeFusionLanguageModel{id: "gpt-6-luna", parts: fusionTextParts(`{"assessment":"revise","feedback":"missing edge case","defect":"nil map"}`)}
	revision := &fakeFusionLanguageModel{id: "gpt-6-astra", parts: fusionTextParts("revision answer")}
	model, _ := newTestFusionModel(&fusionPlan{
		Pattern: "critique",
		Steps: []fusionStep{
			{Role: "draft", ModelID: "claude-opus-5.5"},
			{Role: "critic", ModelID: "gpt-6-luna"},
			{Role: "revision", ModelID: "gpt-6-astra"},
		},
	}, map[string]*fakeFusionLanguageModel{
		"claude-opus-5.5": draft,
		"gpt-6-luna":      critic,
		"gpt-6-astra":     revision,
	})

	stream, err := model.Stream(context.Background(), fantasy.Call{})
	require.NoError(t, err)
	require.Equal(t, "revision answer", collectFusionText(t, stream))
	require.Len(t, revision.calls, 1)
	require.Contains(t, fusionPromptFromCall(revision.calls[0]), "missing edge case")
}

func TestFusionModel_ToolCallStepIsReplayed(t *testing.T) {
	t.Parallel()

	draft := &fakeFusionLanguageModel{id: "claude-opus-5.5", parts: []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeToolInputStart, ID: "call-1", ToolCallName: "view"},
		{Type: fantasy.StreamPartTypeToolInputDelta, ID: "call-1", Delta: `{"file_path":"main.go"}`},
		{Type: fantasy.StreamPartTypeToolInputEnd, ID: "call-1"},
		{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls},
	}}
	judge := &fakeFusionLanguageModel{id: "gpt-6-sol", parts: fusionTextParts(`{"score":5,"rationale":"ok"}`)}
	model, _ := newTestFusionModel(&fusionPlan{
		Pattern: "cascade",
		Steps: []fusionStep{
			{Role: "draft", ModelID: "claude-opus-5.5"},
			{Role: "escalation", ModelID: "gpt-6-sol"},
			{Role: "repair", ModelID: "gpt-6-astra"},
		},
	}, map[string]*fakeFusionLanguageModel{
		"claude-opus-5.5": draft,
		"gpt-6-sol":       judge,
	})

	stream, err := model.Stream(context.Background(), fantasy.Call{})
	require.NoError(t, err)
	var toolParts int
	for part := range stream {
		if part.Type == fantasy.StreamPartTypeToolInputStart {
			toolParts++
		}
	}
	require.Equal(t, 1, toolParts)
	require.Empty(t, judge.calls, "a tool-call step is not the draft's final answer")
}

func TestParseJudgeVerdict(t *testing.T) {
	t.Parallel()

	score, rationale := parseJudgeVerdict("```json\n{\"score\":1,\"rationale\":\"wrong\"}\n```")
	require.Equal(t, 1, score)
	require.Equal(t, "wrong", rationale)

	score, _ = parseJudgeVerdict("not json")
	require.Equal(t, fusionAcceptScore, score, "a malformed verdict accepts the draft")
}

func TestParseCriticVerdict(t *testing.T) {
	t.Parallel()

	approved, _ := parseCriticVerdict(`{"assessment":"approve","feedback":"fine","defect":null}`)
	require.True(t, approved)

	approved, feedback := parseCriticVerdict(`{"assessment":"revise","feedback":"fix it","defect":"bug"}`)
	require.False(t, approved)
	require.Equal(t, "fix it", feedback)

	approved, _ = parseCriticVerdict("not json")
	require.True(t, approved, "a malformed verdict accepts the draft")
}

func TestFusionPromptFromCall(t *testing.T) {
	t.Parallel()

	call := fantasy.Call{Prompt: fantasy.Prompt{
		fantasy.NewSystemMessage("system"),
		fantasy.NewUserMessage("first"),
		fantasy.NewUserMessage("second"),
	}}
	require.Equal(t, "second", fusionPromptFromCall(call))
	require.Empty(t, fusionPromptFromCall(fantasy.Call{}))
}
