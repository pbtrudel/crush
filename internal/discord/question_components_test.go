package discord

import (
	"errors"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/question"
	"github.com/stretchr/testify/require"
)

// componentInteraction builds a button press on the control thread's
// question card.
func componentInteraction(customID string) gatewayInteraction {
	in := gatewayInteraction{
		ID:        "i2",
		Token:     "ctok",
		Type:      gwInteractionMessageComponent,
		ChannelID: "thread-1",
	}
	in.Data.CustomID = customID
	in.Member.User.ID = "u1"
	return in
}

// singleChoiceRequest builds a one-question batch with two described
// choices.
func singleChoiceRequest() question.Request {
	return question.Request{
		ID:        "req-1",
		SessionID: "sess-1",
		Questions: []question.Question{{
			ID:   "q1",
			Type: question.TypeSingleChoice,
			Text: "Which database?",
			Choices: []question.Choice{
				{ID: "pg", Label: "PostgreSQL", Description: "Relational, sturdy"},
				{ID: "mongo", Label: "MongoDB", Description: "Documents, flexible"},
			},
		}},
	}
}

func TestRenderQuestionCardShowsChoiceDescriptions(t *testing.T) {
	t.Parallel()

	card := renderQuestionCard(singleChoiceRequest(), questionHintButtons)
	require.Contains(t, card, "**1.** PostgreSQL")
	require.Contains(t, card, "Relational, sturdy")
	require.Contains(t, card, "**2.** MongoDB")
	require.Contains(t, card, "Documents, flexible")
	require.Contains(t, card, "press a button to answer")
	require.NotContains(t, card, "/answer", "a buttoned card does not need the text hint")

	textOnly := renderQuestionCard(singleChoiceRequest(), questionHintAnswer)
	require.Contains(t, textOnly, "/answer", "a non-buttoned card keeps the text hint")
}

func TestQuestionRowsSingleChoicePressAnswersImmediately(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())
	env.answerOK = true

	req := singleChoiceRequest()
	b.handleQuestionRequest(req)
	require.NotEmpty(t, b.questionMessage[req.ID], "the card is posted")
	require.NotNil(t, questionRows(req, b.questionSelections[req.ID]), "the card carries buttons")

	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 0, 1)))

	require.Equal(t, []question.Answer{
		{QuestionID: "q1", SelectedIDs: []string{"mongo"}},
	}, env.answers, "pressing a choice answers the question")

	updates := api.updatesSnapshot()
	require.Len(t, updates, 1)
	require.Contains(t, updates[0].content, "✅ answered")
	require.Empty(t, updates[0].components, "the answered card drops its buttons")
	require.Empty(t, b.questionSelections[req.ID], "the batch state is cleared")
	require.Empty(t, b.questions[req.ID], "the batch is no longer pending")
}

func TestQuestionRowsYesNoPressAnswers(t *testing.T) {
	t.Parallel()
	b, env, _, _ := testBridge(t, testConfig())
	env.answerOK = true

	req := question.Request{
		ID:        "req-yn",
		SessionID: "sess-1",
		Questions: []question.Question{{
			ID:   "q1",
			Type: question.TypeYesNo,
			Text: "Proceed?",
		}},
	}
	b.handleQuestionRequest(req)
	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 0, 0)))

	require.Len(t, env.answers, 1)
	require.NotNil(t, env.answers[0].Yes)
	require.True(t, *env.answers[0].Yes)
}

func TestQuestionRowsMultiChoiceToggleAndSubmit(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())
	env.answerOK = true

	req := question.Request{
		ID:        "req-multi",
		SessionID: "sess-1",
		Questions: []question.Question{{
			ID:   "q1",
			Type: question.TypeMultiChoice,
			Text: "Pick any",
			Choices: []question.Choice{
				{ID: "a", Label: "A"},
				{ID: "b", Label: "B"},
			},
		}},
	}
	b.handleQuestionRequest(req)

	// Toggling does not answer; it redraws the card with the selection.
	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 0, 0)))
	require.Empty(t, env.answers, "a toggle must not answer the question")
	updates := api.updatesSnapshot()
	require.Len(t, updates, 1)
	require.Equal(t, buttonStyleSuccess, updates[0].components[0].Components[0].Style,
		"the selected choice is highlighted")

	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 0, 1)))
	b.handleQuestionComponent(componentInteraction(submitCustomID(req.ID)))

	require.Equal(t, []question.Answer{
		{QuestionID: "q1", SelectedIDs: []string{"a", "b"}},
	}, env.answers)
	updates = api.updatesSnapshot()
	require.Contains(t, updates[len(updates)-1].content, "✅ answered")
}

func TestQuestionRowsMultiQuestionBatchNeedsSubmit(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())
	env.answerOK = true

	req := question.Request{
		ID:        "req-batch",
		SessionID: "sess-1",
		Questions: []question.Question{
			{
				ID:   "q1",
				Type: question.TypeSingleChoice,
				Text: "First?",
				Choices: []question.Choice{
					{ID: "a", Label: "A"},
					{ID: "b", Label: "B"},
				},
			},
			{
				ID:   "q2",
				Type: question.TypeSingleChoice,
				Text: "Second?",
				Choices: []question.Choice{
					{ID: "c", Label: "C"},
					{ID: "d", Label: "D"},
				},
			},
		},
	}
	b.handleQuestionRequest(req)

	rows := questionRows(req, b.questionSelections[req.ID])
	require.Len(t, rows, 3, "one row per question plus the submit row")
	submit := rows[2].Components[0]
	require.True(t, submit.Disabled, "submit starts disabled while the batch is incomplete")

	// Selecting in the first question only redraws the card.
	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 0, 1)))
	require.Empty(t, env.answers)

	// Pressing submit while incomplete is refused, still no answer.
	b.handleQuestionComponent(componentInteraction(submitCustomID(req.ID)))
	require.Empty(t, env.answers)
	require.Len(t, api.callbacksSnapshot(), 1, "the refusal is an ephemeral reply")

	// Selecting in the second question completes the batch.
	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 1, 0)))
	b.handleQuestionComponent(componentInteraction(submitCustomID(req.ID)))

	require.Equal(t, []question.Answer{
		{QuestionID: "q1", SelectedIDs: []string{"b"}},
		{QuestionID: "q2", SelectedIDs: []string{"c"}},
	}, env.answers, "submit sends every question's answer in one call")
}

func TestQuestionComponentStalePressAnswered(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	b.handleQuestionComponent(componentInteraction(selectCustomID("gone", 0, 0)))

	require.Empty(t, env.answers)
	callbacks := api.callbacksSnapshot()
	require.Len(t, callbacks, 1)
	require.True(t, callbacks[0].ephemeral)
	require.Contains(t, callbacks[0].content, "already resolved")
}

func TestQuestionComponentForeignUserRejected(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.AllowedUserIDs = []string{"someone-else"}
	b, env, api, _ := testBridge(t, cfg)
	req := singleChoiceRequest()
	b.handleQuestionRequest(req)

	// A press from a user allowed by the card but not the config.
	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 0, 0)))

	require.Empty(t, env.answers)
	callbacks := api.callbacksSnapshot()
	require.Len(t, callbacks, 1)
	require.True(t, callbacks[0].ephemeral)
	require.Contains(t, callbacks[0].content, "Not authorized")
}

func TestQuestionRowsFallBackWhenOverBudget(t *testing.T) {
	t.Parallel()

	// Five questions with five choices each need six rows including
	// submit, over Discord's five-row cap.
	req := question.Request{ID: "req-big", SessionID: "sess-1"}
	for qi := range maxActionRows {
		q := question.Question{
			ID:   "q" + string(rune('1'+qi)),
			Type: question.TypeSingleChoice,
			Text: "Question",
		}
		for range maxRowButtons {
			q.Choices = append(q.Choices, question.Choice{ID: "c", Label: "Choice"})
		}
		req.Questions = append(req.Questions, q)
	}

	require.Nil(t, questionRows(req, nil), "an over-budget batch stays text-only")
	require.Contains(t, renderQuestionCard(req, questionHintAnswer), "answer in the terminal",
		"the fallback must not promise a command that refuses multi-question batches")
}

func TestQuestionRowsFallBackForFreeText(t *testing.T) {
	t.Parallel()

	req := question.Request{
		ID:        "req-text",
		SessionID: "sess-1",
		Questions: []question.Question{{
			ID:   "q1",
			Type: question.TypeFreeText,
			Text: "What should it be called?",
		}},
	}
	require.Nil(t, questionRows(req, nil), "free-text batches stay text-only")
}

func TestParseQuestionCustomID(t *testing.T) {
	t.Parallel()

	ref, ok := parseQuestionCustomID(selectCustomID("req-1", 2, 4))
	require.True(t, ok)
	require.Equal(t, questionButtonRef{requestID: "req-1", questionIndex: 2, choiceIndex: 4}, ref)

	ref, ok = parseQuestionCustomID(submitCustomID("req-1"))
	require.True(t, ok)
	require.True(t, ref.submit)
	require.Equal(t, "req-1", ref.requestID)

	for _, bad := range []string{"", "x:y", "cq:", "cq:req", "cq:req:x:1", "cq:req:1:x"} {
		_, ok := parseQuestionCustomID(bad)
		require.False(t, ok, "malformed custom id %q must be ignored", bad)
	}
}

// TestQuestionFallbackHintIsAnswerable pins that the text fallback only
// promises /answer when /answer can actually deliver: multi-question
// batches cannot be answered with the command, so their card must point at
// the terminal instead of a guaranteed failure.
func TestQuestionFallbackHintIsAnswerable(t *testing.T) {
	t.Parallel()

	single := question.Request{
		ID: "req-text", SessionID: "sess-1",
		Questions: []question.Question{{ID: "q1", Type: question.TypeFreeText, Text: "Name?"}},
	}
	require.Contains(t, renderQuestionCard(single, questionHintAnswer), "/answer",
		"a single free-text question is answerable with /answer")

	multi := question.Request{
		ID: "req-multi-text", SessionID: "sess-1",
		Questions: []question.Question{
			{ID: "q1", Type: question.TypeFreeText, Text: "Name?"},
			{ID: "q2", Type: question.TypeFreeText, Text: "Color?"},
		},
	}
	card := renderQuestionCard(multi, questionHintAnswer)
	require.Contains(t, card, "answer in the terminal",
		"a multi-question fallback must not promise /answer")
	require.NotContains(t, card, "/answer")
}

// TestQuestionResolvedCardDropsTheAnswerHint pins that a resolved card does
// not keep telling the user to press a button that no longer exists.
func TestQuestionResolvedCardDropsTheAnswerHint(t *testing.T) {
	t.Parallel()

	card := renderQuestionCard(singleChoiceRequest(), questionHintNone)
	require.NotContains(t, card, "press a button")
	require.NotContains(t, card, "/answer")
}

// TestQuestionSubmitFallsBackToEditWhenCallbackFails pins that a failed
// interaction callback still marks the card answered over REST, instead of
// leaving live-looking buttons on a resolved question.
func TestQuestionSubmitFallsBackToEditWhenCallbackFails(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())
	env.answerOK = true
	api.updateErr = errors.New("interaction expired")

	req := singleChoiceRequest()
	b.handleQuestionRequest(req)
	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 0, 0)))

	require.Len(t, env.answers, 1, "the answer still lands")
	require.True(t, api.hasEditContaining("✅ answered"),
		"the card is resolved over REST when the callback fails")
	require.Empty(t, b.questions[req.ID])
}

// TestQuestionComponentArrivesThroughInputLoop pins the gateway-to-handler
// routing: a component interaction enqueued by the gateway is dispatched to
// the question handler, not the slash-command handler.
func TestQuestionComponentArrivesThroughInputLoop(t *testing.T) {
	t.Parallel()
	b, env, _, _ := testBridge(t, testConfig())
	env.answerOK = true

	req := singleChoiceRequest()
	b.handleQuestionRequest(req)
	in := componentInteraction(selectCustomID(req.ID, 0, 0))
	b.enqueueInput(inputEvent{interaction: &in})

	require.Eventually(t, func() bool { return len(env.answersSnapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, []question.Answer{{QuestionID: "q1", SelectedIDs: []string{"pg"}}}, env.answersSnapshot())
}
