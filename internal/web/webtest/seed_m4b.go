package webtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/skills"
)

// SeedM4b adds the M4b surfaces on top of the M4 seeds. The tab's display
// settings start on the Holodeck theme unless WEBFAKE_THEME names the one
// a screenshot needs; the runs of other seeds extend this function.
func SeedM4b(f *Fake, done <-chan struct{}, clock time.Time) (M4Seams, error) {
	seams, err := SeedM4(f, done, clock)
	if err != nil {
		return M4Seams{}, err
	}
	theme, err := m4bTheme()
	if err != nil {
		return M4Seams{}, err
	}
	if f.Cfg.Options == nil {
		f.Cfg.Options = &config.Options{}
	}
	f.Cfg.Options.WebDisplay = &config.WebDisplayOptions{Theme: theme}
	// The HUD layout test asks for a model name long enough to truncate.
	if name := os.Getenv("WEBFAKE_MODEL_NAME"); name != "" {
		model := f.AgentModel()
		model.CatwalkCfg.Name = name
		f.SetAgentModel(model)
	}
	// The browse spec opens s-2, so it carries a message to show. The id
	// must not look like nextID output, or the scripted agent reuses it.
	f.AddMessage(message.Message{ID: "browse-seed-a", SessionID: "s-2", Role: message.Assistant, CreatedAt: clock.Unix(), UpdatedAt: clock.Unix(), Model: "claude-haiku", Provider: "anthropic", Parts: []message.ContentPart{
		message.TextContent{Text: "Porting the tokenizer."},
	}})
	// The chronoline and the drawer's version links read two edits on s1:
	// one assistant message holding both calls, their results, and the two
	// versions of parser.go they wrote. The times stay after h-seed-2 to
	// h-seed-4 so the new versions sort last and the drawer's default range
	// names them.
	editAt := clock.Add(5 * time.Second)
	f.AddMessage(message.Message{ID: "m4b-a-1", SessionID: "s1", Role: message.Assistant, CreatedAt: editAt.Unix(), UpdatedAt: editAt.Unix(), Model: "claude-haiku", Provider: "anthropic", Parts: []message.ContentPart{
		message.TextContent{Text: "Linking the parser to its token stream."},
		message.ToolCall{ID: "tc-edit-1", Name: "edit", Input: `{"file_path":"internal/parser/parser.go"}`, Finished: true},
		message.ToolCall{ID: "tc-edit-2", Name: "edit", Input: `{"file_path":"internal/parser/parser.go"}`, Finished: true},
		message.Finish{Reason: message.FinishReasonToolUse, Time: editAt.Unix()},
	}})
	f.AddMessage(message.Message{ID: "m4b-t-1", SessionID: "s1", Role: message.Tool, CreatedAt: editAt.Unix(), UpdatedAt: editAt.Unix(), Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "tc-edit-1", Name: "edit", Content: "Edited internal/parser/parser.go"},
		message.ToolResult{ToolCallID: "tc-edit-2", Name: "edit", Content: "Edited internal/parser/parser.go"},
	}})
	for i, callID := range []string{"tc-edit-1", "tc-edit-2"} {
		at := clock.Add(time.Duration(5+i) * time.Second)
		f.AddHistory(history.File{
			ID: fmt.Sprintf("h-m4b-%d", i), SessionID: "s1", Path: f.parserPath(),
			Content: parserV2 + strings.Repeat("\n// Passed the parser tests.", i+1),
			Version: int64(2 + i), CreatedAt: at.Unix(), UpdatedAt: at.Unix(),
			ToolCallID: callID,
		})
	}
	// The m4b scenario carries a channel-capable MCP server, a skill the
	// global config disables, and a terminal that answers automation.
	if f.MCP == nil {
		f.MCP = make(map[string]mcp.ClientInfo)
	}
	f.MCP["discord"] = mcp.ClientInfo{
		Name:           "discord",
		State:          mcp.StateConnected,
		Counts:         mcp.Counts{Tools: 3},
		ChannelCapable: true,
	}
	f.AddDiscoveredSkill(skills.CatalogEntry{
		ID:            "crush-config",
		Name:          "crush-config",
		Description:   "Read and change Crush's own configuration",
		Label:         "project:crush-config",
		Source:        skills.SourceProject,
		UserInvocable: true,
	})
	f.Cfg.Options.DisabledSkills = append(f.Cfg.Options.DisabledSkills, "crush-config")
	seams.DockerMCPAvailable = func() bool { return true }
	seams.Automation = m4bAutomation
	return seams, nil
}

// m4bAutomation answers the two automation commands the m4b scenario runs,
// with the replies its tests expect.
func m4bAutomation(_ context.Context, _, name, args string) (string, error) {
	switch name {
	case "goal":
		return "Goal set: " + args, nil
	case "loop":
		return "Loops: 2 active", nil
	}
	return "", fmt.Errorf("unknown automation command %q", name)
}

// m4bTheme reads the theme a run starts on. Every other display field keeps
// its default, which is what an absent web_display block means.
func m4bTheme() (string, error) {
	switch theme := os.Getenv("WEBFAKE_THEME"); theme {
	case "", "holodeck":
		return "holodeck", nil
	case "solid":
		return "solid", nil
	default:
		return "", fmt.Errorf("invalid WEBFAKE_THEME: %q", theme)
	}
}
