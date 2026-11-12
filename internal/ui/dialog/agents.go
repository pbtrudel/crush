package dialog

import (
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
)

// AgentsID is the identifier for the agent selector dialog.
const AgentsID = "agents"

// AgentDescriptor describes one selectable agent in the current session.
type AgentDescriptor struct {
	SessionID       string
	ParentSessionID string
	Name            string
	Kind            agentrun.Kind
	Status          agentrun.Status
	StartedAt       time.Time
	FinishedAt      time.Time
}

// Agents is an agent selector dialog.
type Agents struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*Agents)(nil)

// NewAgents creates a new Agents dialog.
func NewAgents(com *common.Common, agents []AgentDescriptor) *Agents {
	a := &Agents{com: com}

	helpModel := help.New()
	helpModel.Styles = com.Styles.DialogHelpStyles()
	a.help = helpModel

	a.list = list.NewFilterableList(agentItems(com.Styles, agents)...)
	a.list.Focus()
	a.list.SetSelected(0)
	a.list.SetEmptyMessage(com.Styles.Tool.ResultEmpty.Render("No agents for this session"))

	a.input = textinput.New()
	a.input.SetVirtualCursor(false)
	a.input.Placeholder = "Search agents"
	a.input.SetStyles(com.Styles.TextInput)
	a.input.Focus()

	a.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "choose"),
	)
	a.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	a.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	a.keyMap.Close = CloseKey

	return a
}

// agentItems builds the filterable list entries for the agents.
func agentItems(t *styles.Styles, agents []AgentDescriptor) []list.FilterableItem {
	items := make([]list.FilterableItem, 0, len(agents))
	for _, agent := range agents {
		action := ActionSelectAgent{Agent: agent}
		item := NewCommandItem(t, agent.SessionID, agent.Name, string(agent.Status), action).
			WithDescription(string(agent.Kind)).
			WithAliases(string(agent.Kind), string(agent.Status))
		items = append(items, item)
	}
	return items
}

// ID implements Dialog.
func (a *Agents) ID() string {
	return AgentsID
}

// HandleMsg implements Dialog.
func (a *Agents) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, a.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, a.keyMap.Previous):
			a.list.Focus()
			if a.list.IsSelectedFirst() {
				a.list.SelectLast()
			} else {
				a.list.SelectPrev()
			}
			a.list.ScrollToSelected()
		case key.Matches(msg, a.keyMap.Next):
			a.list.Focus()
			if a.list.IsSelectedLast() {
				a.list.SelectFirst()
			} else {
				a.list.SelectNext()
			}
			a.list.ScrollToSelected()
		case key.Matches(msg, a.keyMap.Select):
			if item, ok := a.list.SelectedItem().(*CommandItem); ok && item != nil {
				return item.Action()
			}
		default:
			prevValue := a.input.Value()
			var cmd tea.Cmd
			a.input, cmd = a.input.Update(msg)
			value := a.input.Value()
			if value != prevValue {
				a.list.SetFilter(value)
				a.list.ScrollToTop()
				a.list.SetSelected(0)
			}
			return ActionCmd{Cmd: cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (a *Agents) Cursor() *tea.Cursor {
	return InputCursor(a.com.Styles, a.input.Cursor())
}

// Draw implements Dialog.
func (a *Agents) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := a.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	a.input.SetWidth(dialogInputTextWidth(t, a.input, innerWidth))
	listHeight, listTotalHeight, listWidth := sizeDialogList(t, a.list, innerWidth, height)
	applyInfoColumnVisibility(a.list.FilteredItems(), listWidth, sessionInfoMaxPercent)
	rc := NewRenderContext(t, width)
	rc.Title = "Agents"
	rc.AddPart(t.Dialog.InputPrompt.Render(a.input.View()))
	body := t.Dialog.List.Height(a.list.Height()).Render(a.list.Render())
	body = joinScrollbar(t, body, listHeight, listTotalHeight, listHeight, a.list.Offset())
	rc.AddPart(body)
	rc.Help = renderDialogHelp(t, &a.help, a, innerWidth)
	DrawCenterCursor(scr, area, rc.Render(), a.Cursor())
	return a.Cursor()
}

// ShortHelp implements help.KeyMap.
func (a *Agents) ShortHelp() []key.Binding {
	return []key.Binding{a.keyMap.Previous, a.keyMap.Next, a.keyMap.Select, a.keyMap.Close}
}

// FullHelp implements help.KeyMap.
func (a *Agents) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{a.keyMap.Select, a.keyMap.Next, a.keyMap.Previous},
		{a.keyMap.Close},
	}
}
