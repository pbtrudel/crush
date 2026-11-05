package tools

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/stretchr/testify/require"
)

func TestQuestionParamsUnmarshalJSON_NativeArray(t *testing.T) {
	t.Parallel()
	input := `{"questions": [{"type": "yes_no", "question": "OK?", "description": "test"}]}`
	var p QuestionParams
	require.NoError(t, json.Unmarshal([]byte(input), &p))
	require.Len(t, p.Questions, 1)
	require.Equal(t, "OK?", p.Questions[0].Question)
}

func TestQuestionParamsUnmarshalJSON_StringEncodedArray(t *testing.T) {
	t.Parallel()
	// Simulates a model that double-serializes the questions field.
	inner := `[{"type":"yes_no","question":"OK?","description":"test"}]`
	encoded, _ := json.Marshal(inner)
	input := `{"questions": ` + string(encoded) + `}`
	var p QuestionParams
	require.NoError(t, json.Unmarshal([]byte(input), &p))
	require.Len(t, p.Questions, 1)
	require.Equal(t, "OK?", p.Questions[0].Question)
}

func TestQuestionParamsUnmarshalJSON_StringEncodedWithWhitespace(t *testing.T) {
	t.Parallel()
	inner := `  [{"type":"single_choice","question":"Pick","description":"d","choices":[{"id":"a","label":"A"}]}]  `
	encoded, _ := json.Marshal(inner)
	input := `{"questions": ` + string(encoded) + `, "confirm_title": "Go?"}`
	var p QuestionParams
	require.NoError(t, json.Unmarshal([]byte(input), &p))
	require.Len(t, p.Questions, 1)
	require.Equal(t, "Pick", p.Questions[0].Question)
	require.Equal(t, "Go?", p.ConfirmTitle)
}

func TestQuestionParamsUnmarshalJSON_InvalidString(t *testing.T) {
	t.Parallel()
	encoded, _ := json.Marshal("not valid json")
	input := `{"questions": ` + string(encoded) + `}`
	var p QuestionParams
	require.Error(t, json.Unmarshal([]byte(input), &p))
}

func TestFormatAnswer_MultiChoiceWithFillIn(t *testing.T) {
	answer := question.Answer{
		SelectedIDs: []string{"speed", "readability"},
		FillInText:  "maintainability",
	}
	resp, err := formatAnswer(&answer, question.TypeMultiChoice)
	require.NoError(t, err)
	require.Contains(t, resp.Content, `User selected: ["speed","readability"]`)
	require.Contains(t, resp.Content, "User provided: maintainability")
}

func TestFormatAnswer_SelectionsOnly(t *testing.T) {
	answer := question.Answer{SelectedIDs: []string{"gardening"}}
	resp, err := formatAnswer(&answer, question.TypeSingleChoice)
	require.NoError(t, err)
	require.Contains(t, resp.Content, `User selected: ["gardening"]`)
	require.NotContains(t, resp.Content, "User provided")
}

func TestFormatAnswer_Skipped(t *testing.T) {
	answer := question.Answer{}
	resp, err := formatAnswer(&answer, question.TypeFreeText)
	require.NoError(t, err)
	require.Contains(t, resp.Content, "User skipped this question")
	require.Contains(t, resp.Content, "Continue with the task")
	require.Contains(t, resp.Content, "do not ask the same question again")
	require.Contains(t, resp.Content, "not authorization")
}

// fakeQuestionService records whether the tool reached the user-facing
// service, which is what the teammate guard must prevent.
type fakeQuestionService struct {
	answers []question.Answer
	asked   bool
}

func (f *fakeQuestionService) Subscribe(context.Context) <-chan pubsub.Event[question.Request] {
	return nil
}

func (f *fakeQuestionService) SubscribeNotifications(context.Context) <-chan pubsub.Event[question.Notification] {
	return nil
}

func (f *fakeQuestionService) Ask(context.Context, question.Request) ([]question.Answer, error) {
	f.asked = true
	return f.answers, nil
}

func (f *fakeQuestionService) Answer([]question.Answer) bool { return false }

func (f *fakeQuestionService) Cancel() bool { return false }

func (f *fakeQuestionService) Pending() (question.Request, bool) { return question.Request{}, false }

func runQuestionTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context) fantasy.ToolResponse {
	t.Helper()
	params := QuestionParams{
		Questions: []QuestionItem{{
			Type:        string(question.TypeYesNo),
			Question:    "Proceed?",
			Description: "Confirm the next step.",
		}},
	}
	input, err := json.Marshal(params)
	require.NoError(t, err)
	response, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-1", Name: tool.Info().Name, Input: string(input)})
	require.NoError(t, err)
	return response
}

// TestQuestionToolRefusesInsideTeammate pins the runtime half of tool parity:
// the question tool stays in a teammate's palette so the tools block matches
// the Lead's, but only the Lead may put a question in front of the user.
func TestQuestionToolRefusesInsideTeammate(t *testing.T) {
	yes := true
	svc := &fakeQuestionService{answers: []question.Answer{{Yes: &yes}}}
	tool := NewQuestionTool(svc)

	teammateCtx := agentteam.WithMember(t.Context(), "reviewer")
	response := runQuestionTool(t, tool, teammateCtx)
	require.True(t, response.IsError)
	require.Contains(t, response.Content, "teammates cannot ask the user directly")
	require.False(t, svc.asked, "a teammate's question must never reach the user")

	response = runQuestionTool(t, tool, t.Context())
	require.False(t, response.IsError, response.Content)
	require.True(t, svc.asked)
	require.Contains(t, response.Content, "User answered: yes")
}
