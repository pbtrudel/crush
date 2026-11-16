package dialog

import (
	"cmp"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
)

// ArgumentsID is the identifier for the arguments dialog.
const ArgumentsID = "arguments"

// Dialog sizing for arguments.
const (
	maxInputWidth         = 120
	minInputWidth         = 30
	minReadableInputWidth = 60
	maxInputHeight        = 8
	maxViewportHeight     = 20
	argumentsLabelHeight  = 1
)

// Arguments represents a dialog for collecting command arguments.
type Arguments struct {
	com       *common.Common
	title     string
	arguments []commands.Argument
	inputs    []textarea.Model
	focused   int
	spinner   spinner.Model
	loading   bool

	// fieldOffsets and fieldHeights describe the rendered field layout in
	// rows from the top of the scrollable fields content.
	fieldOffsets []int
	fieldHeights []int
	// inputWidths caches the width applied to each textarea so a resize can
	// rebuild its wrapped content before the cursor is positioned.
	inputWidths []int

	description  string
	resultAction Action

	help   help.Model
	keyMap struct {
		Confirm,
		Next,
		Previous,
		UpDown,
		ScrollUp,
		ScrollDown,
		Close key.Binding
	}

	viewport viewport.Model
}

var _ Dialog = (*Arguments)(nil)

// NewArguments creates a new arguments dialog.
func NewArguments(com *common.Common, title, description string, arguments []commands.Argument, resultAction Action) *Arguments {
	a := &Arguments{
		com:          com,
		title:        title,
		description:  description,
		arguments:    arguments,
		resultAction: resultAction,
	}

	a.help = help.New()
	a.help.Styles = com.Styles.DialogHelpStyles()

	a.keyMap.Confirm = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "confirm"),
	)
	a.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "tab"),
		key.WithHelp("↓/tab", "next"),
	)
	a.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "shift+tab"),
		key.WithHelp("↑/shift+tab", "previous"),
	)
	a.keyMap.UpDown = key.NewBinding(key.WithKeys("up", "down"))
	a.keyMap.Close = CloseKey

	// Create input fields for each argument. Arguments can be long-form
	// text, so they wrap and scroll instead of scrolling horizontally.
	a.inputs = make([]textarea.Model, len(arguments))
	a.inputWidths = make([]int, len(arguments))
	for i, arg := range arguments {
		input := textarea.New()
		input.SetVirtualCursor(false)
		input.SetStyles(questionTextareaStyles(com.Styles))
		input.ShowLineNumbers = false
		input.DynamicHeight = true
		input.MinHeight = 1
		input.MaxHeight = maxInputHeight
		input.SetHeight(1)
		input.SetPromptFunc(2, func(info textarea.PromptInfo) string {
			if info.LineNumber == 0 {
				return "> "
			}
			return "  "
		})
		input.KeyMap.InsertNewline = key.NewBinding(key.WithDisabled())
		// Use description as placeholder if available, otherwise title
		if arg.Description != "" {
			input.Placeholder = arg.Description
		} else {
			input.Placeholder = arg.Title
		}

		if i == 0 {
			input.Focus()
		} else {
			input.Blur()
		}

		a.inputs[i] = input
	}
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = com.Styles.Dialog.Spinner
	a.spinner = s

	return a
}

// ID implements Dialog.
func (a *Arguments) ID() string {
	return ArgumentsID
}

// focusInput changes focus to a new input by index with wrap-around.
func (a *Arguments) focusInput(newIndex int) {
	a.inputs[a.focused].Blur()

	// Wrap around: Go's modulo can return negative, so add len first.
	n := len(a.inputs)
	a.focused = ((newIndex % n) + n) % n

	a.inputs[a.focused].Focus()

	// Ensure the newly focused field is visible in the viewport
	a.ensureFieldVisible(a.focused)
}

// isFieldVisible checks if a field at the given index is visible in the viewport.
func (a *Arguments) isFieldVisible(fieldIndex int) bool {
	if fieldIndex < 0 || fieldIndex >= len(a.fieldHeights) {
		return true
	}
	fieldStart := a.fieldOffsets[fieldIndex]
	fieldEnd := fieldStart + a.fieldHeights[fieldIndex] - 1
	viewportTop := a.viewport.YOffset()
	viewportBottom := viewportTop + a.viewport.Height() - 1

	return fieldStart >= viewportTop && fieldEnd <= viewportBottom
}

// ensureFieldVisible scrolls the viewport to make the field visible.
func (a *Arguments) ensureFieldVisible(fieldIndex int) {
	if fieldIndex < 0 || fieldIndex >= len(a.fieldHeights) {
		return
	}
	viewportHeight := a.viewport.Height()
	if viewportHeight <= 0 {
		return
	}

	fieldStart := a.fieldOffsets[fieldIndex]
	fieldEnd := fieldStart + a.fieldHeights[fieldIndex] - 1
	viewportTop := a.viewport.YOffset()
	viewportBottom := viewportTop + viewportHeight - 1

	// A field can be taller than the viewport. Align on the textarea caret
	// instead of the field's top or bottom so the cursor stays visible while
	// typing and after a resize.
	if a.fieldHeights[fieldIndex] > viewportHeight {
		caretRow := fieldStart + argumentsLabelHeight
		if cur := a.inputs[fieldIndex].Cursor(); cur != nil {
			caretRow += cur.Y
		}
		if caretRow < viewportTop {
			a.viewport.SetYOffset(caretRow)
		} else if caretRow > viewportBottom {
			a.viewport.SetYOffset(caretRow - viewportHeight + 1)
		}
		return
	}

	// If field is above viewport, scroll up to show it at top
	if fieldStart < viewportTop {
		a.viewport.SetYOffset(fieldStart)
		return
	}

	// If field is below viewport, scroll down to show it at bottom
	if fieldEnd > viewportBottom {
		a.viewport.SetYOffset(fieldEnd - viewportHeight + 1)
	}
}

// findVisibleFieldByOffset returns the field index closest to the given viewport offset.
func (a *Arguments) findVisibleFieldByOffset(fromTop bool) int {
	offset := a.viewport.YOffset()
	if !fromTop {
		offset += max(0, a.viewport.Height()-1)
	}

	for i := len(a.fieldOffsets) - 1; i >= 0; i-- {
		if a.fieldOffsets[i] <= offset {
			return i
		}
	}
	return 0
}

// HandleMsg implements Dialog.
func (a *Arguments) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if a.loading {
			var cmd tea.Cmd
			a.spinner, cmd = a.spinner.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, a.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, a.keyMap.Confirm):
			// If we're on the last input or there's only one input, submit.
			if a.focused == len(a.inputs)-1 || len(a.inputs) == 1 {
				args := make(map[string]string)
				var warning tea.Cmd
				for i, arg := range a.arguments {
					args[arg.ID] = a.inputs[i].Value()
					if arg.Required && strings.TrimSpace(a.inputs[i].Value()) == "" {
						warning = util.ReportWarn("Required argument '" + arg.Title + "' is missing.")
						break
					}
				}
				if warning != nil {
					return ActionCmd{Cmd: warning}
				}

				switch action := a.resultAction.(type) {
				case ActionRunCustomCommand:
					action.Args = args
					return action
				case ActionRunMCPPrompt:
					action.Args = args
					return action
				case ActionAskSideQuestion:
					action.Question = args["question"]
					return action
				case ActionGoal:
					action.Objective = args["objective"]
					return action
				case ActionLoop:
					action.Task = args["task"]
					return action
				}
			}
			a.focusInput(a.focused + 1)
		case len(a.inputs) == 1 && key.Matches(msg, a.keyMap.UpDown):
			// With a single field there is nowhere to move focus, so the
			// arrows move the cursor through the wrapped text instead.
			var cmd tea.Cmd
			a.inputs[a.focused], cmd = a.inputs[a.focused].Update(msg)
			return ActionCmd{Cmd: cmd}
		case key.Matches(msg, a.keyMap.Next):
			a.focusInput(a.focused + 1)
		case key.Matches(msg, a.keyMap.Previous):
			a.focusInput(a.focused - 1)
		default:
			var cmd tea.Cmd
			a.inputs[a.focused], cmd = a.inputs[a.focused].Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case common.CoalescedWheelMsg:
		a.viewport, _ = a.viewport.Update(tea.MouseWheelMsg(msg.Mouse))
		// If focused field scrolled out of view, focus the visible field
		if !a.isFieldVisible(a.focused) {
			a.focusInput(a.findVisibleFieldByOffset(msg.DeltaY > 0))
		}
	case tea.PasteMsg:
		var cmd tea.Cmd
		a.inputs[a.focused], cmd = a.inputs[a.focused].Update(msg)
		return ActionCmd{Cmd: cmd}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (a *Arguments) Cursor(contentStyle lipgloss.Style, titleHeight, descriptionHeight int) *tea.Cursor {
	if a.focused < 0 || a.focused >= len(a.inputs) || a.focused >= len(a.fieldOffsets) {
		return nil
	}
	// A zero-height field viewport renders no input rows, so there is no
	// visible position for the cursor.
	if a.viewport.Height() <= 0 {
		return nil
	}
	cursor := a.inputs[a.focused].Cursor()
	if cursor == nil {
		return nil
	}

	dialogStyle := a.com.Styles.Dialog.View
	cursor.X += dialogStyle.GetBorderLeftSize() + contentStyle.GetPaddingLeft()
	cursor.Y += dialogStyle.GetBorderTopSize() +
		titleHeight +
		contentStyle.GetPaddingTop() +
		descriptionHeight +
		a.fieldOffsets[a.focused] - a.viewport.YOffset() +
		argumentsLabelHeight
	return cursor
}

// Draw implements Dialog.
func (a *Arguments) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	s := a.com.Styles

	dialogContentStyle := s.Dialog.Arguments.Content
	possibleWidth := max(0, area.Dx()-s.Dialog.View.GetHorizontalFrameSize()-dialogContentStyle.GetHorizontalFrameSize())
	// Build fields with label and input.
	caser := cases.Title(language.English)

	a.fieldOffsets = a.fieldOffsets[:0]
	a.fieldHeights = a.fieldHeights[:0]
	fieldOffset := 0
	var fields []string
	for i, arg := range a.arguments {
		isFocused := i == a.focused

		// Try to pretty up the title for the label.
		title := strings.ReplaceAll(arg.Title, "_", " ")
		title = strings.ReplaceAll(title, "-", " ")
		titleParts := strings.Fields(title)
		for i, part := range titleParts {
			titleParts[i] = caser.String(strings.ToLower(part))
		}
		labelText := strings.Join(titleParts, " ")

		markRequiredStyle := s.Dialog.Arguments.InputRequiredMarkBlurred

		labelStyle := s.Dialog.Arguments.InputLabelBlurred
		if isFocused {
			labelStyle = s.Dialog.Arguments.InputLabelFocused
			markRequiredStyle = s.Dialog.Arguments.InputRequiredMarkFocused
		}
		if arg.Required {
			labelText += markRequiredStyle.String()
		}
		label := labelStyle.Render(labelText)

		labelWidth := lipgloss.Width(labelText)
		placeholderWidth := lipgloss.Width(a.inputs[i].Placeholder)

		maxWidth := max(0, min(possibleWidth, maxInputWidth))
		inputWidth := max(placeholderWidth, labelWidth, minInputWidth)
		inputWidth = min(inputWidth, maxWidth)
		// Long-form arguments need room to wrap; a dialog as narrow as
		// the placeholder makes a goal or task hard to read.
		inputWidth = max(inputWidth, min(minReadableInputWidth, maxWidth))
		if a.inputWidths[i] != inputWidth {
			a.inputWidths[i] = inputWidth
			a.inputs[i].SetWidth(inputWidth)
			// SetWidth re-wraps the textarea, but its viewport keeps the
			// previous scroll offset until the view is rebuilt. Refresh the
			// content, then reposition so the cursor stays inside the field
			// after a terminal resize.
			_ = a.inputs[i].View()
			a.inputs[i].SetHeight(a.inputs[i].Height())
		}

		inputLine := a.inputs[i].View()
		labelHeight := lipgloss.Height(label)
		fieldHeight := labelHeight + lipgloss.Height(inputLine) + 1
		a.fieldOffsets = append(a.fieldOffsets, fieldOffset)
		a.fieldHeights = append(a.fieldHeights, fieldHeight)
		fieldOffset += fieldHeight

		field := lipgloss.JoinVertical(lipgloss.Left, label, inputLine, "")
		fields = append(fields, field)
	}

	renderedFields := lipgloss.JoinVertical(lipgloss.Left, fields...)

	// Anchor width to the longest field, capped at maxInputWidth.
	const scrollbarWidth = 1
	width := lipgloss.Width(renderedFields)
	height := lipgloss.Height(renderedFields)

	// Use standard header
	titleStyle := s.Dialog.Title

	titleText := cmp.Or(a.title, "Arguments")

	header := common.DialogTitle(s, titleText, width, s.Dialog.TitleGradFromColor, s.Dialog.TitleGradToColor)

	// Add description if available.
	var description string
	if a.description != "" {
		descStyle := s.Dialog.Arguments.Description.Width(width)
		description = descStyle.Render(a.description)
	}

	helpView := renderDialogHelp(s, &a.help, a, width)
	if a.loading {
		helpView = s.Dialog.HelpView.Width(width).Render(a.spinner.View() + " Generating Prompt...")
	}

	availableHeight := max(0, area.Dy()-s.Dialog.View.GetVerticalFrameSize()-dialogContentStyle.GetVerticalFrameSize()-lipgloss.Height(header)-lipgloss.Height(description)-lipgloss.Height(helpView)-2) // extra spacing
	viewportHeight := max(0, min(height, maxViewportHeight, availableHeight))

	a.viewport.SetWidth(width) // -1 for scrollbar
	a.viewport.SetHeight(viewportHeight)
	a.viewport.SetContent(renderedFields)
	// A field grows as the user types; keep the focused one visible when
	// the dialog is too short to show the whole form.
	a.ensureFieldVisible(a.focused)

	content := joinScrollbar(s, a.viewport.View(), viewportHeight, a.viewport.TotalLineCount(), viewportHeight, a.viewport.YOffset())
	var contentParts []string
	if description != "" {
		contentParts = append(contentParts, description)
	}
	contentParts = append(contentParts, content)

	view := lipgloss.JoinVertical(
		lipgloss.Left,
		titleStyle.Render(header),
		dialogContentStyle.Render(lipgloss.JoinVertical(lipgloss.Left, contentParts...)),
		helpView,
	)

	dialog := s.Dialog.View.Render(view)

	titleHeight := lipgloss.Height(titleStyle.Render(header))
	descriptionHeight := 0
	if a.description != "" {
		descriptionHeight = lipgloss.Height(description)
	}
	cur := a.Cursor(dialogContentStyle, titleHeight, descriptionHeight)

	DrawCenterCursor(scr, area, dialog, cur)
	return cur
}

// StartLoading implements [LoadingDialog].
func (a *Arguments) StartLoading() tea.Cmd {
	if a.loading {
		return nil
	}
	a.loading = true
	return a.spinner.Tick
}

// StopLoading implements [LoadingDialog].
func (a *Arguments) StopLoading() {
	a.loading = false
}

// ShortHelp implements help.KeyMap.
func (a *Arguments) ShortHelp() []key.Binding {
	bindings := []key.Binding{a.keyMap.Confirm}
	if len(a.inputs) > 1 {
		bindings = append(bindings, a.keyMap.Next)
	}
	return append(bindings, a.keyMap.Close)
}

// FullHelp implements help.KeyMap.
func (a *Arguments) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{a.keyMap.Confirm, a.keyMap.Next, a.keyMap.Previous},
		{a.keyMap.Close},
	}
}
