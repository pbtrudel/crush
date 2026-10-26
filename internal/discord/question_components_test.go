package discord

import (
	"errors"
	"strings"
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

	card := renderQuestionContent(singleChoiceRequest(), 0, questionHintButtons)
	require.Contains(t, card, "**1.** PostgreSQL")
	require.Contains(t, card, "Relational, sturdy")
	require.Contains(t, card, "**2.** MongoDB")
	require.Contains(t, card, "Documents, flexible")
	require.Contains(t, card, "press a button to answer")
	require.Contains(t, card, "Something else")
}

// TestRenderQuestionCardKeepsFullText pins that the question text,
// description, and choice descriptions are never truncated: Discord gets
// the whole card, split across messages when it is long.
func TestRenderQuestionCardKeepsFullText(t *testing.T) {
	t.Parallel()

	longQuestion := strings.Repeat("How should the mesh span regions? ", 4)
	longDescription := strings.Repeat("The architecture has residency zones; this must stay readable. ", 8)
	longChoice := strings.Repeat("One mesh per region with independent trust and explicit gateways. ", 6)

	req := question.Request{
		ID:        "req-long",
		SessionID: "sess-1",
		Questions: []question.Question{{
			ID:          "q1",
			Type:        question.TypeSingleChoice,
			Label:       "Mesh design",
			Text:        longQuestion,
			Description: longDescription,
			Choices: []question.Choice{
				{ID: "a", Label: "Mesh per region", Description: longChoice},
				{ID: "b", Label: "Multi-cluster mesh", Description: longChoice},
			},
		}},
	}
	card := renderQuestionContent(req, 0, questionHintButtons)
	require.Contains(t, card, strings.TrimSpace(longQuestion))
	require.Contains(t, card, plainLine(longDescription))
	require.Contains(t, card, plainLine(longChoice))
	require.NotContains(t, card, "…", "nothing is cut short")
}

func TestQuestionRowsSingleChoicePressAnswersImmediately(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())
	env.answerOK = true

	req := singleChoiceRequest()
	b.handleQuestionRequest(req)
	require.Len(t, b.questionMessages[req.ID], 1, "one message per question is posted")
	require.NotNil(t, questionRows(req, 0, b.questionSelections[req.ID], b.questionFillIns[req.ID], true), "the card carries buttons")

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

	require.Len(t, b.questionMessages[req.ID], 2, "each question is its own message")
	rows := questionRows(req, 1, b.questionSelections[req.ID], b.questionFillIns[req.ID], true)
	submit := rows[len(rows)-1].Components[0]
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

	for i := range req.Questions {
		rows := questionRows(req, i, nil, nil, i == len(req.Questions)-1)
		require.NotNil(t, rows, "each question message fits Discord's action-row cap")
		require.LessOrEqual(t, len(rows), maxActionRows)
	}
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
	rows := questionRows(req, 0, nil, nil, false)
	require.NotNil(t, rows, "a free-text question gets a Something else button")
	require.Equal(t, customCustomID(req.ID, 0), rows[0].Components[0].CustomID)
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

	ref, ok = parseQuestionCustomID(customCustomID("req-1", 3))
	require.True(t, ok)
	require.True(t, ref.custom)
	require.Equal(t, 3, ref.questionIndex)

	for _, bad := range []string{"", "x:y", "cq:", "cq:req", "cq:req:x:1", "cq:req:1:x"} {
		_, ok := parseQuestionCustomID(bad)
		require.False(t, ok, "malformed custom id %q must be ignored", bad)
	}
}

// TestQuestionResolvedCardDropsTheAnswerHint pins that a resolved card does
// not keep telling the user to press a button that no longer exists.
func TestQuestionResolvedCardDropsTheAnswerHint(t *testing.T) {
	t.Parallel()

	card := renderQuestionContent(singleChoiceRequest(), 0, questionHintNone)
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

// TestQuestionComponentArrivesThroughInteractionLoop pins the
// gateway-to-handler routing: a component interaction enqueued by the
// gateway is dispatched to the question handler, not the slash-command
// handler.
func TestQuestionComponentArrivesThroughInteractionLoop(t *testing.T) {
	t.Parallel()
	b, env, _, _ := testBridge(t, testConfig())
	env.answerOK = true

	req := singleChoiceRequest()
	b.handleQuestionRequest(req)
	b.enqueueInteraction(componentInteraction(selectCustomID(req.ID, 0, 0)))

	require.Eventually(t, func() bool { return len(env.answersSnapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, []question.Answer{{QuestionID: "q1", SelectedIDs: []string{"pg"}}}, env.answersSnapshot())
}

// modalInteraction builds a MODAL_SUBMIT interaction for a question's
// custom-answer modal.
func modalInteraction(customID, value string) gatewayInteraction {
	in := gatewayInteraction{
		ID:        "i3",
		Token:     "ctok",
		Type:      gwInteractionModalSubmit,
		ChannelID: "thread-1",
	}
	in.Data.CustomID = customID
	in.Data.Components = []gatewayInteractionComponent{{
		Type: componentTypeActionRow,
		Components: []gatewayInteractionComponent{{
			Type:     componentTypeTextInput,
			CustomID: modalInputCustomID,
			Value:    value,
		}},
	}}
	in.Member.User.ID = "u1"
	return in
}

// TestQuestionPostsOneMessagePerQuestion pins the layout fix: a batch is
// posted as one message per question, with that question's buttons directly
// under it and the submit row on the last question.
func TestQuestionPostsOneMessagePerQuestion(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	req := question.Request{
		ID:        "req-batch",
		SessionID: "sess-1",
		Questions: []question.Question{
			{
				ID: "q1", Type: question.TypeSingleChoice, Text: "First?",
				Choices: []question.Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
			},
			{
				ID: "q2", Type: question.TypeSingleChoice, Text: "Second?",
				Choices: []question.Choice{{ID: "c", Label: "C"}, {ID: "d", Label: "D"}},
			},
		},
	}
	b.handleQuestionRequest(req)

	require.Len(t, b.questionMessages[req.ID], 2, "one message per question")

	ids := b.questionMessages[req.ID]
	require.Len(t, ids, 2, "one message per question")
	firstID := ids[0][0]
	secondID := ids[1][0]
	require.Contains(t, api.messageContent(firstID), "Question 1/2")
	require.Contains(t, api.messageContent(firstID), "First?")
	require.NotContains(t, api.messageContent(firstID), "Second?")
	firstRows := api.messageComponents(firstID)
	require.NotEmpty(t, firstRows)
	require.Equal(t, customCustomID(req.ID, 0), firstRows[0].Components[len(firstRows[0].Components)-1].CustomID,
		"the first question's Something else button sits under it")
	require.Contains(t, api.messageContent(secondID), "Question 2/2")
	lastRows := api.messageComponents(secondID)
	require.NotEmpty(t, lastRows)
	require.Equal(t, submitCustomID(req.ID), lastRows[len(lastRows)-1].Components[0].CustomID,
		"submit rides on the last question's message")
}

// TestQuestionSomethingElseOpensModalAndAnswers pins the custom-answer
// path: the button opens a modal, and submitting it produces FillInText.
func TestQuestionSomethingElseOpensModalAndAnswers(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())

	req := question.Request{
		ID: "req-text", SessionID: "sess-1",
		Questions: []question.Question{{ID: "q1", Type: question.TypeFreeText, Text: "Name?"}},
	}
	b.handleQuestionRequest(req)

	b.handleQuestionComponent(componentInteraction(customCustomID(req.ID, 0)))
	modals := api.modalsSnapshot()
	require.Len(t, modals, 1, "the button opens a modal")
	require.Equal(t, modalCustomID(req.ID, 0), modals[0].modal.CustomID)
	require.Equal(t, "Name?", modals[0].modal.Title)

	b.handleQuestionModalSubmit(modalInteraction(modals[0].modal.CustomID, "a custom name"))

	require.Equal(t, []question.Answer{{QuestionID: "q1", FillInText: "a custom name"}}, env.answers)
	updates := api.updatesSnapshot()
	require.NotEmpty(t, updates)
	require.Contains(t, updates[len(updates)-1].content, "✅ answered")
	require.Empty(t, b.questions[req.ID])
}

// TestQuestionCustomAnswerInBatchWaitsForSubmit pins that a typed answer
// counts as an answer for completeness but still waits for Submit in a
// multi-question batch.
func TestQuestionCustomAnswerInBatchWaitsForSubmit(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())
	env.answerOK = true

	req := question.Request{
		ID:        "req-batch",
		SessionID: "sess-1",
		Questions: []question.Question{
			{ID: "q1", Type: question.TypeSingleChoice, Text: "First?", Choices: []question.Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}},
			{ID: "q2", Type: question.TypeSingleChoice, Text: "Second?", Choices: []question.Choice{{ID: "c", Label: "C"}, {ID: "d", Label: "D"}}},
		},
	}
	b.handleQuestionRequest(req)

	b.handleQuestionComponent(componentInteraction(customCustomID(req.ID, 0)))
	modals := api.modalsSnapshot()
	require.Len(t, modals, 1)
	b.handleQuestionModalSubmit(modalInteraction(modals[0].modal.CustomID, "neither"))
	require.Empty(t, env.answers, "a batch waits for Submit")

	rows := questionRows(req, 1, b.questionSelections[req.ID], b.questionFillIns[req.ID], true)
	submit := rows[len(rows)-1].Components[0]
	require.True(t, submit.Disabled, "the second question is still unanswered")

	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 1, 0)))
	b.handleQuestionComponent(componentInteraction(submitCustomID(req.ID)))

	require.Equal(t, []question.Answer{
		{QuestionID: "q1", FillInText: "neither"},
		{QuestionID: "q2", SelectedIDs: []string{"c"}},
	}, env.answers)
}

// TestQuestionComponentsRideOnLastChunk pins that a long question whose
// content is split into several Discord messages keeps its buttons on the
// final chunk instead of stranding them mid-text.
func TestQuestionComponentsRideOnLastChunk(t *testing.T) {
	t.Parallel()
	b, _, api, _ := testBridge(t, testConfig())

	content := strings.Repeat("long question text\n", 300)
	rows := []discordComponent{{Type: componentTypeActionRow, Components: []discordButton{{
		Type: componentTypeButton, Style: buttonStyleSecondary, Label: "Pick", CustomID: "cq:x:0:0",
	}}}}
	posted := b.postAboveWithComponents(content, rows)
	require.NotEmpty(t, posted.ids)
	require.NotEmpty(t, posted.componentID)

	var chunks []fakePost
	for _, p := range api.postsSnapshot() {
		if strings.HasPrefix(p.content, "long question text") {
			chunks = append(chunks, p)
		}
	}
	require.Greater(t, len(chunks), 1, "the content must split")
	require.Equal(t, len(chunks), len(posted.ids), "every chunk's message id is returned")
	require.Empty(t, chunks[0].components, "the first chunk must not carry the controls")
	require.NotEmpty(t, chunks[len(chunks)-1].components, "the controls ride on the last chunk")
	require.Equal(t, chunks[len(chunks)-1].messageID, posted.componentID, "the component message id is returned")
}

// TestLongQuestionCardSplitsAndResolves pins that a card too long for one
// Discord message keeps all its text across chunks, with the controls on
// the final chunk, and that answering resolves the card.
func TestLongQuestionCardSplitsAndResolves(t *testing.T) {
	t.Parallel()
	b, env, api, _ := testBridge(t, testConfig())
	env.answerOK = true

	desc := strings.Repeat("Residency zones, explicit gateways, independent trust. ", 40)
	req := question.Request{
		ID: "req-split", SessionID: "sess-1",
		Questions: []question.Question{{
			ID:          "q1",
			Type:        question.TypeSingleChoice,
			Text:        "How many clusters should the first mesh design cover?",
			Description: desc,
			Choices: []question.Choice{
				{ID: "a", Label: "Mesh per region", Description: desc},
				{ID: "b", Label: "Multi-cluster mesh", Description: desc},
			},
		}},
	}
	b.handleQuestionRequest(req)

	cards := b.questionMessages[req.ID]
	require.Len(t, cards, 1, "one card per question")
	require.Greater(t, len(cards[0]), 1, "a long card splits across messages")
	require.Contains(t, api.messageContent(cards[0][0]), "How many clusters should the first mesh design cover?")
	last := cards[0][len(cards[0])-1]
	require.NotEmpty(t, api.messageComponents(last), "the controls ride on the last chunk")

	b.handleQuestionComponent(componentInteraction(selectCustomID(req.ID, 0, 0)))
	require.Equal(t, []question.Answer{{QuestionID: "q1", SelectedIDs: []string{"a"}}}, env.answers)

	updates := api.updatesSnapshot()
	require.Len(t, updates, 1)
	require.Contains(t, updates[0].content, "✅ answered")
	require.NotContains(t, updates[0].content, "press a button", "the hint is replaced")
}

// TestQuestionComponentArrivesThroughInteractionQueue pins the gateway
// routing through the dedicated interaction queue.
func TestQuestionComponentArrivesThroughInteractionQueue(t *testing.T) {
	t.Parallel()
	b, env, _, _ := testBridge(t, testConfig())
	env.answerOK = true

	req := singleChoiceRequest()
	b.handleQuestionRequest(req)
	b.enqueueInteraction(componentInteraction(selectCustomID(req.ID, 0, 1)))

	require.Eventually(t, func() bool { return len(env.answersSnapshot()) == 1 }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, []question.Answer{{QuestionID: "q1", SelectedIDs: []string{"mongo"}}}, env.answersSnapshot())
}
