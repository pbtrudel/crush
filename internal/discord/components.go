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

	buttonStylePrimary   = 1
	buttonStyleSecondary = 2
	buttonStyleSuccess   = 3

	maxActionRows  = 5
	maxRowButtons  = 5
	maxButtonLabel = 80

	questionCustomIDPrefix = "cq"
	submitCustomIDSuffix   = "submit"
)

// Synthetic choice IDs for yes/no questions so selections, button styling,
// and answer building share one path with choice questions.
const (
	yesChoiceID = "y"
	noChoiceID  = "n"
)

// discordComponent is one message component: an action row of buttons.
type discordComponent struct {
	Type       int             `json:"type"`
	Components []discordButton `json:"components,omitempty"`
}

// discordButton is one interactive button inside an action row.
type discordButton struct {
	Type     int    `json:"type"`
	Style    int    `json:"style"`
	Label    string `json:"label"`
	CustomID string `json:"custom_id"`
	Disabled bool   `json:"disabled,omitempty"`
}

// questionButtonRef identifies what a question button press selects.
type questionButtonRef struct {
	requestID     string
	submit        bool
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
	ci, err := strconv.Atoi(parts[3])
	if err != nil {
		return questionButtonRef{}, false
	}
	ref.questionIndex, ref.choiceIndex = qi, ci
	return ref, true
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

// selectionsComplete reports whether every question holds at least one
// selected choice.
func selectionsComplete(req question.Request, selections map[string]map[string]bool) bool {
	for _, q := range req.Questions {
		if len(selections[q.ID]) == 0 {
			return false
		}
	}
	return true
}

// questionNeedsSubmit reports whether a batch requires an explicit submit
// press: anything multi-turn (a batch with several questions or a
// multi-choice question). A lone yes/no or single-choice question answers
// on the button press itself.
func questionNeedsSubmit(req question.Request) bool {
	if len(req.Questions) > 1 {
		return true
	}
	return len(req.Questions) == 1 && req.Questions[0].Type == question.TypeMultiChoice
}

// questionRows builds the action rows for a question batch, or nil when the
// batch cannot be presented as buttons: a free-text question is present, or
// the buttons would need more than Discord's five action rows. A nil result
// means the card stays text-only and is answered with /answer.
func questionRows(req question.Request, selections map[string]map[string]bool) []discordComponent {
	var rows []discordComponent
	for qi, q := range req.Questions {
		choices := questionChoices(q)
		if len(choices) == 0 {
			return nil
		}
		for start := 0; start < len(choices); start += maxRowButtons {
			row := discordComponent{Type: componentTypeActionRow}
			for ci := start; ci < start+maxRowButtons && ci < len(choices); ci++ {
				style := buttonStyleSecondary
				if selections[q.ID][choices[ci].ID] {
					style = buttonStyleSuccess
				}
				row.Components = append(row.Components, discordButton{
					Type:     componentTypeButton,
					Style:    style,
					Label:    compactLine(choices[ci].Label, maxButtonLabel),
					CustomID: selectCustomID(req.ID, qi, ci),
				})
			}
			rows = append(rows, row)
		}
	}
	if questionNeedsSubmit(req) {
		rows = append(rows, discordComponent{
			Type: componentTypeActionRow,
			Components: []discordButton{{
				Type:     componentTypeButton,
				Style:    buttonStylePrimary,
				Label:    "Submit",
				CustomID: submitCustomID(req.ID),
				Disabled: !selectionsComplete(req, selections),
			}},
		})
	}
	if len(rows) > maxActionRows {
		return nil
	}
	return rows
}

// answersFromSelections renders the current selections as the batch's
// answers, preserving choice order so the payload is deterministic.
func answersFromSelections(req question.Request, selections map[string]map[string]bool) []question.Answer {
	answers := make([]question.Answer, 0, len(req.Questions))
	for _, q := range req.Questions {
		selected := selections[q.ID]
		if q.Type == question.TypeYesNo {
			yes := selected[yesChoiceID]
			answers = append(answers, question.Answer{QuestionID: q.ID, Yes: &yes})
			continue
		}
		var ids []string
		for _, choice := range questionChoices(q) {
			if selected[choice.ID] {
				ids = append(ids, choice.ID)
			}
		}
		answers = append(answers, question.Answer{QuestionID: q.ID, SelectedIDs: ids})
	}
	return answers
}
