package dialog

import (
	"os"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// CommandsID is the identifier for the commands dialog.
const CommandsID = "commands"

// CommandType represents the type of commands being displayed.
type CommandType uint

// String returns the string representation of the CommandType.
func (c CommandType) String() string { return []string{"System", "User", "MCP"}[c] }

const (
	sidebarCompactModeBreakpoint = 120
)

// commandDeckBorder is the terminal bezel used by the command palette.
var commandDeckBorder = lipgloss.Border{
	Top:         "═",
	Bottom:      "═",
	Left:        "║",
	Right:       "║",
	TopLeft:     "╔",
	TopRight:    "╗",
	BottomLeft:  "╚",
	BottomRight: "╝",
}

const (
	SystemCommands CommandType = iota
	UserCommands
	MCPPrompts
)

// Commands represents a dialog that shows available commands.
type dockerMCPAvailabilityCheckedMsg struct {
	available bool
}

type Commands struct {
	com    *common.Common
	keyMap struct {
		Select,
		UpDown,
		Next,
		Previous,
		Tab,
		ShiftTab,
		Close key.Binding
	}

	sessionID     string
	hasSession    bool
	hasTodos      bool
	hasQueue      bool
	revealSecrets bool
	// transparent is whether the background color is off on screen,
	// which a host theme can make differ from the config.
	transparent bool
	selected    CommandType

	spinner spinner.Model
	loading bool

	help  help.Model
	input textinput.Model
	list  *list.FilterableList

	windowWidth int

	customCommands []commands.CustomCommand
	mcpPrompts     []commands.MCPPrompt

	dockerMCPAvailable     *bool
	dockerMCPCheckInFlight bool
}

var _ Dialog = (*Commands)(nil)

// NewCommands creates a new commands dialog. transparent is whether the
// background color is currently off on screen.
func NewCommands(com *common.Common, sessionID string, hasSession, hasTodos, hasQueue, revealSecrets, transparent bool, customCommands []commands.CustomCommand, mcpPrompts []commands.MCPPrompt) (*Commands, error) {
	c := &Commands{
		com:            com,
		selected:       SystemCommands,
		sessionID:      sessionID,
		hasSession:     hasSession,
		hasTodos:       hasTodos,
		hasQueue:       hasQueue,
		revealSecrets:  revealSecrets,
		transparent:    transparent,
		customCommands: customCommands,
		mcpPrompts:     mcpPrompts,
	}

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()

	c.help = help

	c.list = list.NewFilterableList()
	c.list.Focus()
	c.list.SetSelected(0)
	c.list.SetEmptyMessage(com.Styles.Tool.ResultEmpty.Render("No results"))

	c.input = textinput.New()
	c.input.SetVirtualCursor(false)
	c.input.Placeholder = "Search commands"
	c.input.Prompt = "⌕ "
	c.input.SetStyles(com.Styles.TextInput)
	c.input.Focus()

	c.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
	)
	c.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	c.keyMap.Next = key.NewBinding(
		key.WithKeys("down"),
		key.WithHelp("↓", "next item"),
	)
	c.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	c.keyMap.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch selection"),
	)
	c.keyMap.ShiftTab = key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("shift+tab", "switch selection prev"),
	)
	closeKey := CloseKey
	closeKey.SetHelp("esc", "cancel")
	c.keyMap.Close = closeKey

	if available, known := config.DockerMCPAvailabilityCached(); known {
		c.dockerMCPAvailable = &available
	}

	// Set initial commands
	c.setCommandItems(c.selected)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = com.Styles.Dialog.Spinner
	c.spinner = s

	return c, nil
}

// ID implements Dialog.
func (c *Commands) ID() string {
	return CommandsID
}

// HandleMsg implements [Dialog].
func (c *Commands) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case dockerMCPAvailabilityCheckedMsg:
		c.dockerMCPAvailable = &msg.available
		c.dockerMCPCheckInFlight = false
		if c.selected == SystemCommands {
			// Preserve the current selection across the rebuild to avoid reset
			var prevID string
			if item, ok := c.list.SelectedItem().(*CommandItem); ok && item != nil {
				prevID = item.id
			}
			c.setCommandItems(c.selected)
			if prevID != "" {
				for i, it := range c.list.FilteredItems() {
					if ci, ok := it.(*CommandItem); ok && ci != nil && ci.id == prevID {
						c.list.SetSelected(i)
						c.list.ScrollToSelected()
						break
					}
				}
			}
		}
		return nil
	case spinner.TickMsg:
		if c.loading {
			var cmd tea.Cmd
			c.spinner, cmd = c.spinner.Update(msg)
			return ActionCmd{Cmd: cmd}
		}
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, c.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, c.keyMap.Previous):
			c.list.Focus()
			if c.list.IsSelectedFirst() {
				c.list.SelectLast()
			} else {
				c.list.SelectPrev()
			}
			c.list.ScrollToSelected()
		case key.Matches(msg, c.keyMap.Next):
			c.list.Focus()
			if c.list.IsSelectedLast() {
				c.list.SelectFirst()
			} else {
				c.list.SelectNext()
			}
			c.list.ScrollToSelected()
		case key.Matches(msg, c.keyMap.Select):
			if selectedItem := c.list.SelectedItem(); selectedItem != nil {
				if item, ok := selectedItem.(*CommandItem); ok && item != nil {
					return item.Action()
				}
			}
		case key.Matches(msg, c.keyMap.Tab):
			if len(c.customCommands) > 0 || len(c.mcpPrompts) > 0 {
				c.selected = c.nextCommandType()
				c.setCommandItems(c.selected)
			}
		case key.Matches(msg, c.keyMap.ShiftTab):
			if len(c.customCommands) > 0 || len(c.mcpPrompts) > 0 {
				c.selected = c.previousCommandType()
				c.setCommandItems(c.selected)
			}
		default:
			var cmd tea.Cmd
			for _, item := range c.list.FilteredItems() {
				if item, ok := item.(*CommandItem); ok && item != nil {
					if msg.String() == item.Shortcut() {
						return item.Action()
					}
				}
			}
			prevValue := c.input.Value()
			c.input, cmd = c.input.Update(msg)
			value := c.input.Value()
			if value != prevValue {
				c.list.SetFilter(value)
				c.list.ScrollToTop()
				c.list.SetSelected(0)
			}
			return ActionCmd{cmd}
		}
	}
	return nil
}

func checkDockerMCPAvailabilityCmd() tea.Cmd {
	return func() tea.Msg {
		return dockerMCPAvailabilityCheckedMsg{available: config.RefreshDockerMCPAvailability()}
	}
}

func (c *Commands) InitialCmd() tea.Cmd {
	if c.dockerMCPAvailable != nil || c.dockerMCPCheckInFlight {
		return nil
	}
	c.dockerMCPCheckInFlight = true
	return checkDockerMCPAvailabilityCmd()
}

// Cursor returns the cursor position relative to the dialog.
func (c *Commands) Cursor() *tea.Cursor {
	cur := InputCursor(c.com.Styles, c.input.Cursor())
	if cur != nil {
		// The palette draws a gradient rule between the title and the
		// search bar. The generic dialog cursor offset does not know
		// about that extra row.
		cur.Y++
	}
	return cur
}

// commandsRadioView generates the command type selector radio buttons.
// commandsTabsView renders the command category tabs as filled pills.
func commandsTabsView(sty *styles.Styles, selected CommandType, hasUserCmds bool, hasMCPPrompts bool) string {
	if !hasUserCmds && !hasMCPPrompts {
		return ""
	}

	tabs := []CommandType{SystemCommands}
	if hasUserCmds {
		tabs = append(tabs, UserCommands)
	}
	if hasMCPPrompts {
		tabs = append(tabs, MCPPrompts)
	}

	parts := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		label := " " + strings.ToUpper(tab.String()) + " "
		if tab == selected {
			parts = append(parts, sty.Dialog.Palette.TabActive.Render(label))
			continue
		}
		parts = append(parts, sty.Dialog.Palette.TabInactive.Render(label))
	}
	return " " + strings.Join(parts, " ")
}

// Draw implements [Dialog].
func (c *Commands) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := c.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	// The palette adds a rule, a preview, and a second rule around the
	// list, so it needs three more rows than a standard dialog.
	height := max(0, min(defaultDialogHeight+3, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	if area.Dx() != c.windowWidth && c.selected == SystemCommands {
		c.windowWidth = area.Dx()
		// since some items in the list depend on width (e.g. toggle sidebar command),
		// we need to reset the command items when width changes
		c.setCommandItems(c.selected)
	}

	innerWidth := width - c.com.Styles.Dialog.View.GetHorizontalFrameSize()
	// The palette adds three rows around the list: a rule under the
	// title, a selection preview, and a rule above the help.
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize() + 3

	c.input.SetWidth(dialogInputTextWidth(t, c.input, innerWidth))

	c.list.SetSize(innerWidth, max(0, height-heightOffset))

	// Hide the shortcut hints uniformly when the widest would crowd names.
	applyInfoColumnVisibility(c.list.FilteredItems(), innerWidth, commandInfoMaxPercent)

	rc := NewRenderContext(t, width)
	rc.ViewStyle = t.Dialog.View.Border(commandDeckBorder).BorderForeground(t.Chrome.SidebarRailFrom)
	rc.Title = "> COMMAND DECK"
	rc.TitleInfo = commandsTabsView(t, c.selected, len(c.customCommands) > 0, len(c.mcpPrompts) > 0)

	rc.AddPart(styles.ApplyForegroundGrad(
		t.Dialog.Palette.Rule,
		strings.Repeat("─", innerWidth),
		t.Dialog.Palette.RuleFromColor,
		t.Dialog.Palette.RuleToColor,
	))

	inputView := t.Dialog.InputPrompt.Render(c.input.View())
	rc.AddPart(inputView)

	if item, ok := c.list.SelectedItem().(*CommandItem); ok && item != nil {
		previewText := item.description
		if previewText == "" {
			previewText = c.selected.String() + " command · enter to run"
		}
		previewText = ansi.Truncate(previewText, max(0, innerWidth-2), "…")
		rc.AddPart(t.Dialog.Palette.PreviewIcon.Render("▸") + " " + t.Dialog.Palette.Preview.Render(previewText))
	}

	listView := t.Dialog.List.Height(c.list.Height()).Render(c.list.Render())
	rc.AddPart(listView)

	rc.AddPart(styles.ApplyForegroundGrad(
		t.Dialog.Palette.Rule,
		strings.Repeat("─", innerWidth),
		t.Dialog.Palette.RuleToColor,
		t.Dialog.Palette.RuleFromColor,
	))
	rc.Help = renderDialogHelp(t, &c.help, c, innerWidth)

	if c.loading {
		rc.Help = t.Dialog.HelpView.Width(innerWidth).Render(c.spinner.View() + " Generating Prompt...")
	}

	view := rc.Render()

	cur := c.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (c *Commands) ShortHelp() []key.Binding {
	return []key.Binding{
		c.keyMap.Tab,
		c.keyMap.UpDown,
		c.keyMap.Select,
		c.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (c *Commands) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{c.keyMap.Select, c.keyMap.Next, c.keyMap.Previous, c.keyMap.Tab},
		{c.keyMap.Close},
	}
}

// nextCommandType returns the next command type in the cycle.
func (c *Commands) nextCommandType() CommandType {
	switch c.selected {
	case SystemCommands:
		if len(c.customCommands) > 0 {
			return UserCommands
		}
		if len(c.mcpPrompts) > 0 {
			return MCPPrompts
		}
		fallthrough
	case UserCommands:
		if len(c.mcpPrompts) > 0 {
			return MCPPrompts
		}
		fallthrough
	case MCPPrompts:
		return SystemCommands
	default:
		return SystemCommands
	}
}

// previousCommandType returns the previous command type in the cycle.
func (c *Commands) previousCommandType() CommandType {
	switch c.selected {
	case SystemCommands:
		if len(c.mcpPrompts) > 0 {
			return MCPPrompts
		}
		if len(c.customCommands) > 0 {
			return UserCommands
		}
		return SystemCommands
	case UserCommands:
		return SystemCommands
	case MCPPrompts:
		if len(c.customCommands) > 0 {
			return UserCommands
		}
		return SystemCommands
	default:
		return SystemCommands
	}
}

// setCommandItems sets the command items based on the specified command type.
func (c *Commands) setCommandItems(commandType CommandType) {
	c.selected = commandType

	commandItems := []list.FilterableItem{}
	switch c.selected {
	case SystemCommands:
		for _, cmd := range c.defaultCommands() {
			commandItems = append(commandItems, cmd)
		}
	case UserCommands:
		for _, cmd := range c.customCommands {
			var action Action
			if cmd.Skill != nil {
				action = ActionAttachSkill{ID: cmd.Skill.SkillFilePath, Name: cmd.Skill.Name}
			} else {
				action = ActionRunCustomCommand{
					Content:   cmd.Content,
					Arguments: cmd.Arguments,
					Skill:     cmd.Skill,
				}
			}
			item := NewCommandItem(c.com.Styles, "custom_"+cmd.ID, cmd.Name, "", action)
			if cmd.Skill != nil {
				item = item.WithDescription(cmd.Skill.Description)
			}
			commandItems = append(commandItems, item)
		}
	case MCPPrompts:
		for _, cmd := range c.mcpPrompts {
			action := ActionRunMCPPrompt{
				Title:       cmd.Title,
				Description: cmd.Description,
				PromptID:    cmd.PromptID,
				ClientID:    cmd.ClientID,
				Arguments:   cmd.Arguments,
			}
			commandItems = append(commandItems, NewCommandItem(c.com.Styles, "mcp_"+cmd.ID, cmd.PromptID, "", action))
		}
	}

	c.list.SetItems(commandItems...)
	c.list.SetFilter("")
	c.list.ScrollToTop()
	c.list.SetSelected(0)
	c.input.SetValue("")
}

// defaultCommands returns the list of default system commands.
func (c *Commands) defaultCommands() []*CommandItem {
	commands := []*CommandItem{
		NewCommandItem(c.com.Styles, "new_session", "New Session", "ctrl+n", ActionNewSession{}).WithAliases("clear").WithIcon("✦"),
		NewCommandItem(c.com.Styles, "switch_session", "Sessions", "ctrl+s", ActionOpenDialog{SessionsID}).WithIcon("▤"),
	}
	if c.hasSession {
		commands = append(commands, NewCommandItem(c.com.Styles, "view_agents", "View Agents", "", ActionOpenDialog{AgentsID}).WithAliases("agents", "subagents", "teammates").WithIcon("⧉"))
	}
	commands = append(commands,
		NewCommandItem(c.com.Styles, "switch_model", "Switch Model", "ctrl+l", ActionOpenDialog{ModelsID}).WithIcon("◇"),
	)

	// Only show compact command if there's an active session
	if c.hasSession {
		commands = append(commands, NewCommandItem(c.com.Styles, "compact", "Compact Session", "", ActionSummarize{SessionID: c.sessionID}).WithAliases("summarize").WithIcon("⊟"))
		commands = append(commands, NewCommandItem(c.com.Styles, "export_session", "Export Session as Markdown", "", ActionExportSession{SessionID: c.sessionID}).WithAliases("export", "markdown").WithIcon("↧"))
		commands = append(commands, NewCommandItem(c.com.Styles, "ask_side_question", "Ask Side Question", "", ActionAskSideQuestion{SessionID: c.sessionID}).WithAliases("btw", "side").WithIcon("?"))
		commands = append(commands, NewCommandItem(c.com.Styles, "goal", "Set Goal", "", ActionGoal{}).WithAliases("goals", "objective").WithIcon("◎"))
		commands = append(commands, NewCommandItem(c.com.Styles, "loop", "Start Loop", "", ActionLoop{}).WithAliases("loops", "schedule").WithIcon("↻"))
	}

	commands = append(commands,
		NewCommandItem(c.com.Styles, "reload_config", "Reload Configuration", "", ActionReloadConfig{}).WithAliases("reload", "reload-config").WithIcon("⟳"),
		NewCommandItem(c.com.Styles, "reload_skills", "Reload Skills", "", ActionReloadSkills{}).WithAliases("reload-skills").WithIcon("⟳"),
	)

	teamLabel := "Enable Agent Teams"
	if c.com.Config().Options.GetAgentTeams() {
		teamLabel = "Disable Agent Teams"
	}
	commands = append(commands,
		NewCommandItem(c.com.Styles, "toggle_agent_teams", teamLabel, "", ActionToggleAgentTeams{}).WithAliases("teams", "agent-teams").WithIcon("⌬"),
	)

	// Add reasoning toggle for models that support it
	cfg := c.com.Config()
	if agentCfg, ok := cfg.Agents[config.AgentCoder]; ok {
		providerCfg := cfg.GetProviderForModel(agentCfg.Model)
		model := cfg.GetModelByType(agentCfg.Model)
		if providerCfg != nil && model != nil && model.CanReason() {
			selectedModel := cfg.Models[agentCfg.Model]
			effortLevels := model.ReasoningEffortLevels()

			// Anthropic models: thinking toggle
			if len(effortLevels) == 0 {
				status := "Enable"
				if selectedModel.Think {
					status = "Disable"
				}
				commands = append(commands, NewCommandItem(c.com.Styles, "toggle_thinking", status+" Thinking Mode", "", ActionToggleThinking{}).WithIcon("◇"))
			}

			// OpenAI models: reasoning effort dialog
			if len(effortLevels) > 0 {
				commands = append(commands, NewCommandItem(c.com.Styles, "select_reasoning_effort", "Select Reasoning Effort", "", ActionOpenDialog{
					DialogID: ReasoningID,
				}).WithIcon("◈"))
			}
		}
	}
	// Only show toggle compact mode command if window width is larger than compact breakpoint (120)
	if c.windowWidth >= sidebarCompactModeBreakpoint && c.hasSession {
		commands = append(commands, NewCommandItem(c.com.Styles, "toggle_sidebar", "Toggle Sidebar", "ctrl+b", ActionToggleCompactMode{}).WithIcon("▥"))
	}
	if c.hasSession {
		revealLabel := "Reveal Secrets"
		if c.revealSecrets {
			revealLabel = "Hide Secrets"
		}
		commands = append(commands, NewCommandItem(c.com.Styles, "toggle_secret_reveal", revealLabel, "", ActionToggleSecretReveal{}).WithAliases("secrets").WithIcon("◉"))
	}
	if c.hasSession {
		cfgPrime := c.com.Config()
		agentCfg := cfgPrime.Agents[config.AgentCoder]
		model := cfgPrime.GetModelByType(agentCfg.Model)
		if model != nil && model.Capabilities.Vision {
			commands = append(commands, NewCommandItem(c.com.Styles, "file_picker", "Open File Picker", "ctrl+f", ActionOpenDialog{
				DialogID: FilePickerID,
			}).WithIcon("▤"))
		}
	}

	// Add external editor command if $EDITOR is available.
	//
	// TODO: Use [tea.EnvMsg] to get environment variable instead of os.Getenv;
	// because os.Getenv does IO is breaks the TEA paradigm and is generally an
	// antipattern.
	if os.Getenv("EDITOR") != "" {
		commands = append(commands, NewCommandItem(c.com.Styles, "open_external_editor", "Open External Editor", "ctrl+o", ActionExternalEditor{}).WithIcon("✎"))
	}

	// Add Docker MCP command if available and not already enabled.
	if !cfg.IsDockerMCPEnabled() && c.dockerMCPAvailable != nil && *c.dockerMCPAvailable {
		commands = append(commands, NewCommandItem(c.com.Styles, "enable_docker_mcp", "Enable Docker MCP Catalog", "", ActionEnableDockerMCP{}).WithIcon("⬡"))
	}

	// Add disable Docker MCP command if it's currently enabled
	if cfg.IsDockerMCPEnabled() {
		commands = append(commands, NewCommandItem(c.com.Styles, "disable_docker_mcp", "Disable Docker MCP Catalog", "", ActionDisableDockerMCP{}).WithIcon("⬡"))
	}

	// Toggle MCP servers for this repository when any are configured.
	if len(cfg.MCP) > 0 {
		commands = append(commands, NewCommandItem(c.com.Styles, "toggle_mcps", "Toggle MCPs", "", ActionOpenDialog{
			DialogID: MCPTogglesID,
		}))
	}

	if c.hasTodos || c.hasQueue {
		var label string
		switch {
		case c.hasTodos && c.hasQueue:
			label = "Toggle To-Dos/Queue"
		case c.hasQueue:
			label = "Toggle Queue"
		default:
			label = "Toggle To-Dos"
		}
		commands = append(commands, NewCommandItem(c.com.Styles, "toggle_pills", label, "ctrl+t", ActionTogglePills{}))
	}

	// Add a command for selecting notification style via picker dialog.
	notificationLabel := "Notification Style"
	commands = append(commands, NewCommandItem(c.com.Styles, "select_notifications", notificationLabel, "", ActionOpenDialog{DialogID: NotificationsID}))

	// Remote control is always listed: if no session is active yet, the
	// action creates one before mirroring it.
	commands = append(commands, NewCommandItem(c.com.Styles, "remote_control", "Toggle Remote Control (Discord)", "", ActionToggleRemoteControl{}).WithAliases("rc", "discord"))

	commands = append(
		commands,
		NewCommandItem(c.com.Styles, "toggle_yolo", "Toggle Yolo Mode", "ctrl+y", ActionToggleYoloMode{}),
		NewCommandItem(c.com.Styles, "toggle_sysadmin", "Toggle Sysadmin Mode", "", ActionToggleSysadminMode{}),
		NewCommandItem(c.com.Styles, "toggle_help", "Toggle Help", "ctrl+g", ActionToggleHelp{}),
		NewCommandItem(c.com.Styles, "init", "Initialize Project", "", ActionInitializeProject{}),
	)

	// Add transparent background toggle. The label follows what is on
	// screen, which the toggle flips.
	transparentLabel := "Disable Background Color"
	if c.transparent {
		transparentLabel = "Enable Background Color"
	}
	commands = append(commands, NewCommandItem(c.com.Styles, "toggle_transparent", transparentLabel, "", ActionToggleTransparentBackground{}))

	commands = append(commands, NewCommandItem(c.com.Styles, "switch_theme", "Themes", "", ActionOpenDialog{ThemeID}))

	// Add mouse support toggle.
	mouseLabel := "Disable Mouse"
	if cfg != nil && cfg.Options != nil && cfg.Options.TUI.Mouse != nil && !*cfg.Options.TUI.Mouse {
		mouseLabel = "Enable Mouse"
	}
	commands = append(commands, NewCommandItem(c.com.Styles, "toggle_mouse", mouseLabel, "", ActionToggleMouseSupport{}))

	commands = append(
		commands,
		NewCommandItem(c.com.Styles, "quit", "Quit", "ctrl+c", tea.QuitMsg{}).WithAliases("exit").WithIcon("⏻"),
	)

	return commands
}

// SetCustomCommands sets the custom commands and refreshes the view if user commands are currently displayed.
func (c *Commands) SetCustomCommands(customCommands []commands.CustomCommand) {
	c.customCommands = customCommands
	if c.selected == UserCommands {
		c.setCommandItems(c.selected)
	}
}

// SetMCPPrompts sets the MCP prompts and refreshes the view if MCP prompts are currently displayed.
func (c *Commands) SetMCPPrompts(mcpPrompts []commands.MCPPrompt) {
	c.mcpPrompts = mcpPrompts
	if c.selected == MCPPrompts {
		c.setCommandItems(c.selected)
	}
}

// StartLoading implements [LoadingDialog].
func (c *Commands) StartLoading() tea.Cmd {
	if c.loading {
		return nil
	}
	c.loading = true
	return c.spinner.Tick
}

// StopLoading implements [LoadingDialog].
func (c *Commands) StopLoading() {
	c.loading = false
}
