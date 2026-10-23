package dialog

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
)

// SideAnswerID is the identifier for the side-question answer dialog.
const SideAnswerID = "side_answer"

// sideAnswerMaxWidth is the widest the answer dialog grows.
const sideAnswerMaxWidth = 80

// SideAnswer shows the answer to a side question. The exchange is not
// part of the conversation, so it lives in a dialog that disappears on
// close instead of in the chat.
type SideAnswer struct {
	com      *common.Common
	question string
	answer   string

	help     help.Model
	viewport viewport.Model

	keyMap struct {
		Scroll,
		Close key.Binding
	}
}

var _ Dialog = (*SideAnswer)(nil)

// NewSideAnswer creates a dialog showing the answer to question.
func NewSideAnswer(com *common.Common, question, answer string) *SideAnswer {
	s := &SideAnswer{
		com:      com,
		question: question,
		answer:   answer,
	}
	s.help = help.New()
	s.help.Styles = com.Styles.DialogHelpStyles()
	s.keyMap.Scroll = key.NewBinding(
		key.WithKeys("up", "down", "pgup", "pgdown"),
		key.WithHelp("↑/↓", "scroll"),
	)
	s.keyMap.Close = CloseKey
	s.viewport = viewport.New()
	s.viewport.MouseWheelEnabled = true
	return s
}

// ID implements Dialog.
func (s *SideAnswer) ID() string {
	return SideAnswerID
}

// HandleMsg implements Dialog.
func (s *SideAnswer) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if key.Matches(msg, s.keyMap.Close) {
			return ActionClose{}
		}
		var cmd tea.Cmd
		s.viewport, cmd = s.viewport.Update(msg)
		return ActionCmd{Cmd: cmd}
	case common.CoalescedWheelMsg:
		s.viewport, _ = s.viewport.Update(tea.MouseWheelMsg(msg.Mouse))
	}
	return nil
}

// content renders the question and answer at the given width.
func (s *SideAnswer) content(width int) string {
	t := s.com.Styles

	var parts []string
	if question := strings.TrimSpace(s.question); question != "" {
		quote := t.Dialog.Arguments.Description.Width(width).Render(question)
		parts = append(parts, quote, "")
	}

	answer := strings.TrimSpace(s.answer)
	if answer == "" {
		answer = "(the model returned no answer)"
	}
	renderer := common.MarkdownRenderer(t, width)
	mu := common.LockMarkdownRenderer(renderer)
	mu.Lock()
	rendered, err := renderer.Render(answer)
	mu.Unlock()
	if err != nil {
		rendered = answer
	}
	parts = append(parts, strings.Trim(rendered, "\n"))

	hint := t.Dialog.Arguments.Description.Render("Not added to the conversation.")
	parts = append(parts, "", hint)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// Draw implements Dialog.
func (s *SideAnswer) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := s.com.Styles
	dialogStyle := t.Dialog.View
	width := max(0, min(sideAnswerMaxWidth, area.Dx()-dialogStyle.GetHorizontalFrameSize()))
	innerWidth := max(0, width-dialogStyle.GetHorizontalFrameSize())

	header := t.Dialog.Title.Render(common.DialogTitle(
		t,
		"Side Question",
		innerWidth,
		t.Dialog.TitleGradFromColor,
		t.Dialog.TitleGradToColor,
	))
	helpView := renderDialogHelp(t, &s.help, s, innerWidth)

	rendered := s.content(innerWidth)
	contentHeight := lipgloss.Height(rendered)
	fixedHeight := lipgloss.Height(header) + lipgloss.Height(helpView) +
		dialogStyle.GetVerticalFrameSize() + 2
	maxHeight := max(0, area.Dy()*3/4)
	viewportHeight := min(contentHeight, max(3, maxHeight-fixedHeight))

	needsScrollbar := contentHeight > viewportHeight
	viewportWidth := innerWidth
	if needsScrollbar {
		viewportWidth = max(0, innerWidth-1)
		rendered = s.content(viewportWidth)
	}
	s.viewport.SetWidth(viewportWidth)
	s.viewport.SetHeight(viewportHeight)
	s.viewport.SetContent(rendered)

	panel := s.viewport.View()
	if needsScrollbar {
		panel = joinScrollbar(t, panel, viewportHeight, s.viewport.TotalLineCount(), viewportHeight, s.viewport.YOffset())
	}

	view := dialogStyle.Width(width).Render(lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		panel,
		helpView,
	))
	DrawCenter(scr, area, view)
	return nil
}

// ShortHelp implements help.KeyMap.
func (s *SideAnswer) ShortHelp() []key.Binding {
	return []key.Binding{s.keyMap.Scroll, s.keyMap.Close}
}

// FullHelp implements help.KeyMap.
func (s *SideAnswer) FullHelp() [][]key.Binding {
	return [][]key.Binding{s.ShortHelp()}
}
