package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/skills"
)

const (
	// maxCommandArgs and maxCommandArgBytes bound what a run accepts, so a
	// pasted page cannot ride into a prompt or the MCP client.
	maxCommandArgs     = 32
	maxCommandArgBytes = 64 << 10
	// skillMimeType is how the TUI attaches a skill: as markdown.
	skillMimeType = "text/markdown"
)

// commandArgument is one argument as the palette shows it.
type commandArgument struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// commandInfo is one palette entry: a custom command or a user-invocable
// skill.
type commandInfo struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Kind        string            `json:"kind"`
	Args        []commandArgument `json:"args"`
}

// skillInfo is one skill as the browser lists it.
type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled"`
}

// mcpPromptInfo is one MCP prompt as the browser lists it.
type mcpPromptInfo struct {
	ID          string            `json:"id"`
	ClientID    string            `json:"client_id"`
	PromptID    string            `json:"prompt_id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Args        []commandArgument `json:"args"`
}

// skillAttachment is the markdown the deck attaches for a skill.
type skillAttachment struct {
	Name     string `json:"name"`
	MimeType string `json:"mime_type"`
	Content  string `json:"content"`
}

// registerCommandRoutes installs the custom command, skill and MCP prompt
// routes. Like every API route they register through g.route so the auth
// test walks them.
func (g *Gateway) registerCommandRoutes(api *http.ServeMux) {
	g.route(api, "GET /api/commands", g.handleListCommands)
	g.route(api, "POST /api/commands/run", g.handleRunCommand)
	g.route(api, "GET /api/mcp/prompts", g.handleListMCPPrompts)
	g.route(api, "POST /api/mcp/prompts/run", g.handleRunMCPPrompt)
	g.route(api, "GET /api/skills", g.handleListSkills)
	g.route(api, "POST /api/skills/reload", g.handleReloadSkills)
	g.route(api, "POST /api/skills/{name}/enable", g.handleEnableSkill)
	g.route(api, "POST /api/skills/{name}/disable", g.handleDisableSkill)
	g.route(api, "POST /api/skills/{name}/attach", g.handleAttachSkill)
}

// customCommands lists what the palette offers: the custom commands the
// config provides followed by the user-invocable skills, exactly as the
// TUI builds them. A broken skill catalog leaves the custom commands
// usable, the way the TUI keeps going after a failed skill load.
func (g *Gateway) customCommands(ctx context.Context) ([]commands.CustomCommand, error) {
	var cmds []commands.CustomCommand
	if g.loadCommandsFn != nil {
		list, err := g.loadCommandsFn(g.ws.Config())
		if err != nil {
			return nil, err
		}
		cmds = list
	} else if cfg := g.ws.Config(); cfg != nil && cfg.Options != nil {
		list, err := commands.LoadCustomCommands(cfg)
		if err != nil {
			return nil, err
		}
		cmds = list
	}
	entries, err := g.ws.ListSkills(ctx)
	if err != nil {
		slog.Warn("Failed to load skill commands", "error", err)
		return cmds, nil
	}
	return append(cmds, commands.FromSkillCatalog(entries)...), nil
}

func (g *Gateway) handleListCommands(w http.ResponseWriter, r *http.Request) {
	cmds, err := g.customCommands(r.Context())
	if err != nil {
		writeWorkspaceError(w, "list_commands", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]commandInfo{"commands": commandInfos(cmds)})
}

// handleRunCommand runs a custom command as a prompt through the same
// helper the TUI uses. Skills are refused: the TUI palette attaches a skill
// to the editor instead of running it, so the browser uses
// POST /api/skills/{name}/attach.
func (g *Gateway) handleRunCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string            `json:"session_id"`
		ID        string            `json:"id"`
		Args      map[string]string `json:"args"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := g.ws.AgentReadyErr(); err != nil {
		writeWorkspaceError(w, "run_command", err)
		return
	}
	if body.SessionID == "" {
		writeError(w, http.StatusBadRequest, "missing_session", "session_id is required")
		return
	}
	if !g.sessionExists(w, r, body.SessionID) {
		return
	}
	cmds, err := g.customCommands(r.Context())
	if err != nil {
		writeWorkspaceError(w, "run_command", err)
		return
	}
	cmd, ok := findCommand(cmds, body.ID)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_command", "no such command")
		return
	}
	if cmd.Skill != nil {
		writeError(w, http.StatusBadRequest, "skill_attach_only", "Skills are attached, not run")
		return
	}
	if !checkArgs(w, cmd.Arguments, body.Args) {
		return
	}
	g.runDetached(body.SessionID, cmd.PromptText(body.Args))
	w.WriteHeader(http.StatusAccepted)
}

func (g *Gateway) handleListMCPPrompts(w http.ResponseWriter, r *http.Request) {
	prompts, err := g.ws.ListMCPPrompts(r.Context())
	if err != nil {
		writeWorkspaceError(w, "list_mcp_prompts", err)
		return
	}
	out := make([]mcpPromptInfo, 0, len(prompts))
	for _, p := range prompts {
		out = append(out, mcpPromptInfo{
			ID:          p.ID,
			ClientID:    p.ClientID,
			PromptID:    p.PromptID,
			Title:       p.Title,
			Description: p.Description,
			Args:        commandArguments(p.Arguments),
		})
	}
	writeJSON(w, http.StatusOK, map[string][]mcpPromptInfo{"prompts": out})
}

// handleRunMCPPrompt runs the prompt text an MCP server returns, the way
// the TUI sends it. A prompt with no text has nothing to send.
func (g *Gateway) handleRunMCPPrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string            `json:"session_id"`
		ClientID  string            `json:"client_id"`
		PromptID  string            `json:"prompt_id"`
		Args      map[string]string `json:"args"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := g.ws.AgentReadyErr(); err != nil {
		writeWorkspaceError(w, "run_mcp_prompt", err)
		return
	}
	if body.SessionID == "" {
		writeError(w, http.StatusBadRequest, "missing_session", "session_id is required")
		return
	}
	if !g.sessionExists(w, r, body.SessionID) {
		return
	}
	prompts, err := g.ws.ListMCPPrompts(r.Context())
	if err != nil {
		writeWorkspaceError(w, "list_mcp_prompts", err)
		return
	}
	prompt, ok := findMCPPrompt(prompts, body.ClientID, body.PromptID)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_prompt", "no such prompt")
		return
	}
	if !checkArgs(w, prompt.Arguments, body.Args) {
		return
	}
	text, err := g.ws.GetMCPPrompt(body.ClientID, body.PromptID, body.Args)
	if err != nil {
		writeWorkspaceError(w, "run_mcp_prompt", err)
		return
	}
	if text == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	g.runDetached(body.SessionID, text)
	w.WriteHeader(http.StatusAccepted)
}

func (g *Gateway) handleListSkills(w http.ResponseWriter, r *http.Request) {
	entries, err := g.ws.ListSkills(r.Context())
	if err != nil {
		writeWorkspaceError(w, "list_skills", err)
		return
	}
	disabled, err := g.ws.SkillsDisabled(r.Context())
	if err != nil {
		writeWorkspaceError(w, "list_skills", err)
		return
	}
	off := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		off[name] = true
	}
	out := make([]skillInfo, 0, len(entries))
	for _, entry := range entries {
		out = append(out, skillInfo{Name: entry.Name, Description: entry.Description, Disabled: off[entry.Name]})
	}
	writeJSON(w, http.StatusOK, map[string][]skillInfo{"skills": out})
}

func (g *Gateway) handleReloadSkills(w http.ResponseWriter, r *http.Request) {
	if err := g.ws.ReloadSkills(r.Context()); err != nil {
		writeWorkspaceError(w, "reload_skills", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleEnableSkill(w http.ResponseWriter, r *http.Request) {
	g.setSkillDisabled(w, r, false)
}

func (g *Gateway) handleDisableSkill(w http.ResponseWriter, r *http.Request) {
	g.setSkillDisabled(w, r, true)
}

func (g *Gateway) setSkillDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	name := r.PathValue("name")
	entries, err := g.ws.ListSkills(r.Context())
	if err != nil {
		writeWorkspaceError(w, "toggle_skill", err)
		return
	}
	if _, ok := findSkill(entries, name); !ok {
		writeError(w, http.StatusNotFound, "unknown_skill", "no such skill")
		return
	}
	if err := g.ws.SkillSetDisabled(r.Context(), name, disabled); err != nil {
		writeWorkspaceError(w, "toggle_skill", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAttachSkill reads a skill from the catalog by name and answers its
// markdown, the way the TUI attaches a skill to the editor. Finding it in
// the catalog first keeps an unknown name a 404 instead of a workspace
// failure.
func (g *Gateway) handleAttachSkill(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entries, err := g.ws.ListSkills(r.Context())
	if err != nil {
		writeWorkspaceError(w, "list_skills", err)
		return
	}
	entry, ok := findSkill(entries, name)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_skill", "no such skill")
		return
	}
	content, result, err := g.ws.ReadSkill(r.Context(), entry.ID)
	if err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) {
			writeError(w, http.StatusNotFound, "unknown_skill", "no such skill")
			return
		}
		writeWorkspaceError(w, "read_skill", err)
		return
	}
	fileName := result.Name
	if fileName == "" {
		fileName = entry.Name
	}
	writeJSON(w, http.StatusOK, map[string]skillAttachment{
		"attachment": {Name: fileName, MimeType: skillMimeType, Content: string(content)},
	})
}

// commandInfos projects commands for the wire, naming a skill by its own
// description.
func commandInfos(cmds []commands.CustomCommand) []commandInfo {
	out := make([]commandInfo, 0, len(cmds))
	for _, cmd := range cmds {
		info := commandInfo{
			ID:    cmd.ID,
			Title: cmd.Name,
			Kind:  "custom",
			Args:  commandArguments(cmd.Arguments),
		}
		if cmd.Skill != nil {
			info.Kind = "skill"
			info.Description = cmd.Skill.Description
		}
		out = append(out, info)
	}
	return out
}

func commandArguments(args []commands.Argument) []commandArgument {
	out := make([]commandArgument, 0, len(args))
	for _, arg := range args {
		out = append(out, commandArgument{
			ID:          arg.ID,
			Title:       arg.Title,
			Description: arg.Description,
			Required:    arg.Required,
		})
	}
	return out
}

func findCommand(cmds []commands.CustomCommand, id string) (commands.CustomCommand, bool) {
	for _, cmd := range cmds {
		if cmd.ID == id {
			return cmd, true
		}
	}
	return commands.CustomCommand{}, false
}

// findSkill returns the catalog entry a skill name refers to.
func findSkill(entries []skills.CatalogEntry, name string) (skills.CatalogEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return skills.CatalogEntry{}, false
}

func findMCPPrompt(prompts []commands.MCPPrompt, clientID, promptID string) (commands.MCPPrompt, bool) {
	for _, p := range prompts {
		if p.ClientID == clientID && p.PromptID == promptID {
			return p, true
		}
	}
	return commands.MCPPrompt{}, false
}

// checkArgs enforces the argument dialog's rules: at most maxCommandArgs
// values of at most maxCommandArgBytes each, and every required argument
// filled in, named by its ID in the declared order.
func checkArgs(w http.ResponseWriter, args []commands.Argument, values map[string]string) bool {
	if len(values) > maxCommandArgs {
		writeError(w, http.StatusBadRequest, "args_too_large", "at most 32 arguments are accepted")
		return false
	}
	for _, value := range values {
		if len(value) > maxCommandArgBytes {
			writeError(w, http.StatusBadRequest, "args_too_large", "an argument is over 64 KiB")
			return false
		}
	}
	for _, arg := range args {
		if arg.Required && strings.TrimSpace(values[arg.ID]) == "" {
			writeError(w, http.StatusBadRequest, "missing_args", "argument "+arg.ID+" is required")
			return false
		}
	}
	return true
}
