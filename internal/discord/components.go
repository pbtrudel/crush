package discord

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/crush/internal/question"
)

// Discord message-hosted component limits.
const (
	componentTypeActionRow = 1
	componentTypeButton    = 2
	componentTypeTextInput = 4

	buttonStylePrimary   = 1
	buttonStyleSecondary = 2
	buttonStyleSuccess   = 3

	textInputStyleParagraph = 2

	maxActionRows  = 5
	maxRowButtons  = 5
	maxButtonLabel = 80
	maxModalTitle  = 45
	// maxCustomAnswerLength matches the TUI's "Something else?" textarea.
	maxCustomAnswerLength = 500

	questionCustomIDPrefix = "cq"
	modalCustomIDPrefix    = "cqm"
	submitCustomIDSuffix   = "submit"
	customCustomIDSuffix   = "custom"
	modalInputCustomID     = "answer"
)

// Synthetic choice IDs for yes/no questions so selections, button styling,
// and answer building share one path with choice questions.
const (
	yesChoiceID = "y"
	noChoiceID  = "n"
)

// discordComponent is one message component: an action row of buttons or a
// single text input inside a modal's action row.
type discordComponent struct {
	Type       int             `json:"type"`
	Components []discordButton `json:"components,omitempty"`
}

// discordButton is one interactive component. It is used for both buttons
// and modal text inputs; only the fields relevant to the type are encoded.
type discordButton struct {
	Type        int    `json:"type"`
	Style       int    `json:"style,omitempty"`
	Label       string `json:"label,omitempty"`
	CustomID    string `json:"custom_id"`
	Disabled    bool   `json:"disabled,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Value       string `json:"value,omitempty"`
	Required    bool   `json:"required,omitempty"`
	MinLength   int    `json:"min_length,omitempty"`
	MaxLength   int    `json:"max_length,omitempty"`
}

// discordModal is the response payload for a modal (callback type 9).
type discordModal struct {
	CustomID   string             `json:"custom_id"`
	Title      string             `json:"title"`
	Components []discordComponent `json:"components"`
}

// questionButtonRef identifies what a question button press selects.
type questionButtonRef struct {
	requestID     string
	submit        bool
	custom        bool
	questionIndex int
	choiceIndex   int
}

// selectCustomID builds the custom id for a choice button.
func selectCustomID(requestID string, questionIndex, choiceIndex int) string {
	return questionCustomIDPrefix + ":" + requestID + ":" +
		strconv.Itoa(questionIndex) + ":" + strconv.Itoa(choiceIndex)
}

// submitCustomID builds the custom id for a batch's submit button.
func submitCustomID(requestID string) string {
	return questionCustomIDPrefix + ":" + requestID + ":" + submitCustomIDSuffix
}

// customCustomID builds the custom id for a question's "Something else"
// button, which opens a modal for a typed answer.
func customCustomID(requestID string, questionIndex int) string {
	return questionCustomIDPrefix + ":" + requestID + ":" +
		strconv.Itoa(questionIndex) + ":" + customCustomIDSuffix
}

// modalCustomID builds the custom id for the modal opened by a question's
// "Something else" button.
func modalCustomID(requestID string, questionIndex int) string {
	return modalCustomIDPrefix + ":" + requestID + ":" + strconv.Itoa(questionIndex)
}

// parseQuestionCustomID decodes a button press, reporting whether the id
// belongs to a question card.
func parseQuestionCustomID(customID string) (questionButtonRef, bool) {
	parts := strings.Split(customID, ":")
	if len(parts) < 3 || parts[0] != questionCustomIDPrefix {
		return questionButtonRef{}, false
	}
	ref := questionButtonRef{requestID: parts[1]}
	if parts[2] == submitCustomIDSuffix && len(parts) == 3 {
		ref.submit = true
		return ref, true
	}
	if len(parts) != 4 {
		return questionButtonRef{}, false
	}
	qi, err := strconv.Atoi(parts[2])
	if err != nil {
		return questionButtonRef{}, false
	}
	ref.questionIndex = qi
	if parts[3] == customCustomIDSuffix {
		ref.custom = true
		return ref, true
	}
	ci, err := strconv.Atoi(parts[3])
	if err != nil {
		return questionButtonRef{}, false
	}
	ref.choiceIndex = ci
	return ref, true
}

// parseModalCustomID decodes a submitted custom-answer modal into the
// question it answers.
func parseModalCustomID(customID string) (requestID string, questionIndex int, ok bool) {
	parts := strings.Split(customID, ":")
	if len(parts) != 3 || parts[0] != modalCustomIDPrefix {
		return "", 0, false
	}
	qi, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, false
	}
	return parts[1], qi, true
}

// questionChoices returns a question's button-addressable choices, with
// yes/no modeled as a two-choice question. Free-text questions return nil.
func questionChoices(q question.Question) []question.Choice {
	switch q.Type {
	case question.TypeYesNo:
		return []question.Choice{
			{ID: yesChoiceID, Label: "Yes"},
			{ID: noChoiceID, Label: "No"},
		}
	case question.TypeSingleChoice, question.TypeMultiChoice:
		return q.Choices
	default:
		return nil
	}
}

// newQuestionSelections builds an empty per-question selection set.
func newQuestionSelections(req question.Request) map[string]map[string]bool {
	selections := make(map[string]map[string]bool, len(req.Questions))
	for _, q := range req.Questions {
		selections[q.ID] = map[string]bool{}
	}
	return selections
}

// newQuestionFillIns builds an empty per-question custom-answer set.
func newQuestionFillIns(req question.Request) map[string]string {
	fillIns := make(map[string]string, len(req.Questions))
	for _, q := range req.Questions {
		fillIns[q.ID] = ""
	}
	return fillIns
}

// questionAnswered reports whether a question has a choice selected or a
// typed custom answer.
func questionAnswered(q question.Question, selections map[string]bool, fillIn string) bool {
	return len(selections) > 0 || strings.TrimSpace(fillIn) != ""
}

// selectionsComplete reports whether every question is answered, whether by
// a button choice or by the custom-answer modal.
func selectionsComplete(req question.Request, selections map[string]map[string]bool, fillIns map[string]string) bool {
	for _, q := range req.Questions {
		if !questionAnswered(q, selections[q.ID], fillIns[q.ID]) {
			return false
		}
	}
	return true
}

// questionNeedsSubmit reports whether a batch requires an explicit submit
// press: anything multi-turn (a batch with several questions or a
// multi-choice question). A lone yes/no, single-choice, or free-text
// question answers on the button or modal press itself.
func questionNeedsSubmit(req question.Request) bool {
	if len(req.Questions) > 1 {
		return true
	}
	return len(req.Questions) == 1 && req.Questions[0].Type == question.TypeMultiChoice
}

// questionRows builds the action rows for one question. A batch's submit
// row rides on the last question's message so the form's controls stay
// under the question they belong to.
func questionRows(req question.Request, questionIndex int, selections map[string]map[string]bool, fillIns map[string]string, includeSubmit bool) []discordComponent {
	if questionIndex < 0 || questionIndex >= len(req.Questions) {
		return nil
	}
	q := req.Questions[questionIndex]

	var rows []discordComponent
	row := discordComponent{Type: componentTypeActionRow}
	for ci, choice := range questionChoices(q) {
		if len(row.Components) == maxRowButtons {
			rows = append(rows, row)
			row = discordComponent{Type: componentTypeActionRow}
		}
		style := buttonStyleSecondary
		if selections[q.ID][choice.ID] {
			style = buttonStyleSuccess
		}
		row.Components = append(row.Components, discordButton{
			Type:     componentTypeButton,
			Style:    style,
			Label:    compactLine(choice.Label, maxButtonLabel),
			CustomID: selectCustomID(req.ID, questionIndex, ci),
		})
	}
	// Every question offers a typed answer, matching the TUI's
	// "Something else?" row.
	if len(row.Components) == maxRowButtons {
		rows = append(rows, row)
		row = discordComponent{Type: componentTypeActionRow}
	}
	row.Components = append(row.Components, discordButton{
		Type:     componentTypeButton,
		Style:    buttonStyleSecondary,
		Label:    "Something else",
		CustomID: customCustomID(req.ID, questionIndex),
	})
	rows = append(rows, row)

	if includeSubmit && questionNeedsSubmit(req) {
		rows = append(rows, discordComponent{
			Type: componentTypeActionRow,
			Components: []discordButton{{
				Type:     componentTypeButton,
				Style:    buttonStylePrimary,
				Label:    "Submit",
				CustomID: submitCustomID(req.ID),
				Disabled: !selectionsComplete(req, selections, fillIns),
			}},
		})
	}
	if len(rows) > maxActionRows {
		return nil
	}
	return rows
}

// customAnswerModal builds the modal opened by a question's "Something
// else" button.
func customAnswerModal(req question.Request, questionIndex int) discordModal {
	title := "Answer"
	if questionIndex >= 0 && questionIndex < len(req.Questions) {
		q := req.Questions[questionIndex]
		title = q.Text
		if q.Label != "" {
			title = q.Label
		}
	}
	label := "Your answer"
	return discordModal{
		CustomID: modalCustomID(req.ID, questionIndex),
		Title:    truncateRunes(title, maxModalTitle),
		Components: []discordComponent{{
			Type: componentTypeActionRow,
			Components: []discordButton{{
				Type:        componentTypeTextInput,
				Style:       textInputStyleParagraph,
				Label:       label,
				CustomID:    modalInputCustomID,
				Placeholder: "Type your answer",
				Required:    true,
				MaxLength:   maxCustomAnswerLength,
			}},
		}},
	}
}

// answersFromSelections renders the current selections as the batch's
// answers, preserving choice order so the payload is deterministic.
func answersFromSelections(req question.Request, selections map[string]map[string]bool, fillIns map[string]string) []question.Answer {
	answers := make([]question.Answer, 0, len(req.Questions))
	for _, q := range req.Questions {
		selected := selections[q.ID]
		if q.Type == question.TypeYesNo {
			yes := selected[yesChoiceID]
			answers = append(answers, question.Answer{
				QuestionID: q.ID,
				Yes:        &yes,
				FillInText: strings.TrimSpace(fillIns[q.ID]),
			})
			continue
		}
		var ids []string
		for _, choice := range questionChoices(q) {
			if selected[choice.ID] {
				ids = append(ids, choice.ID)
			}
		}
		answers = append(answers, question.Answer{
			QuestionID:  q.ID,
			SelectedIDs: ids,
			FillInText:  strings.TrimSpace(fillIns[q.ID]),
		})
	}
	return answers
}
