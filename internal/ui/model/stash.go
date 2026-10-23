package model

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/crush/internal/ui/util"
)

// toggleInputStash sets the prompt text aside and clears the input when there
// is something to stash, or restores the most recently stashed text when the
// input is empty. Like git stash, stashing is reversible: the text survives
// running commands and navigating prompt history until it is popped again.
func (m *UI) toggleInputStash() tea.Cmd {
	value := m.promptValue()
	if strings.TrimSpace(value) == "" {
		if len(m.inputStash) == 0 {
			return util.ReportInfo("Nothing to unstash")
		}
		last := len(m.inputStash) - 1
		value = m.inputStash[last]
		m.inputStash = m.inputStash[:last]
		return tea.Batch(m.setPromptValue(value), util.ReportInfo("Input unstashed"))
	}

	m.inputStash = append(m.inputStash, value)
	return tea.Batch(m.setPromptValue(""), util.ReportInfo("Input stashed"))
}

// promptValue returns the full prompt, including the "!" prefix when bang
// mode is active. Bang mode is tracked outside the textarea value, so it has
// to be folded back in for the value to round-trip through the stash.
func (m *UI) promptValue() string {
	value := m.textarea.Value()
	if m.bangMode && !strings.HasPrefix(value, "!") {
		value = "!" + value
	}
	return value
}

// setPromptValue replaces the prompt text, re-deriving bang mode and draft
// history from the new value. It returns a command when the textarea height
// changed and the layout needs to be recalculated.
func (m *UI) setPromptValue(value string) tea.Cmd {
	prevHeight := m.textarea.Height()
	m.bangMode = false
	m.bangWasEmpty = false
	m.textarea.Reset()
	m.textarea.InsertString(value)
	m.textarea.MoveToEnd()
	m.syncBangModeFromTextarea()
	m.historyReset()
	m.updateHistoryDraft("")
	m.closeCompletions()
	return m.updateTextareaWithPrevHeight(nil, prevHeight)
}
