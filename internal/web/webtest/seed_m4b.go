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
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/charmbracelet/crush/internal/workspace"
)

// SeedM4b adds the M4b surfaces on top of the M4 seeds; the runs of other
// seeds extend this function.
func SeedM4b(f *Fake, done <-chan struct{}, clock time.Time) (M4Seams, error) {
	seams, err := SeedM4(f, done, clock)
	if err != nil {
		return M4Seams{}, err
	}
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
	if err := seedHudServers(f); err != nil {
		return M4Seams{}, err
	}
	f.AddDiscoveredSkill(skills.CatalogEntry{
		ID:            "crush-config",
		Name:          "crush-config",
		Description:   "Read and change Crush's own configuration",
		Label:         "project:crush-config",
		Source:        skills.SourceProject,
		UserInvocable: true,
	})
	if f.Cfg.Options == nil {
		f.Cfg.Options = &config.Options{}
	}
	f.Cfg.Options.DisabledSkills = append(f.Cfg.Options.DisabledSkills, "crush-config")
	seams.DockerMCPAvailable = func() bool { return true }
	seams.Automation = m4bAutomation
	return seams, nil
}

// seedHudServers widens the HUD's server groups to a realistic count when
// WEBFAKE_HUD_SERVERS=full: nine MCP servers and four language servers, the
// load that once pushed the strip past a 1440px window. It replaces the
// smaller M4b server maps, so only the run that asks for it sees them.
func seedHudServers(f *Fake) error {
	switch v := os.Getenv("WEBFAKE_HUD_SERVERS"); v {
	case "":
		return nil
	case "full":
		f.MCP = make(map[string]mcp.ClientInfo, 9)
		for i := 1; i <= 9; i++ {
			name := fmt.Sprintf("mcp-%d", i)
			f.MCP[name] = mcp.ClientInfo{Name: name, State: mcp.StateConnected, Counts: mcp.Counts{Tools: i}}
		}
		f.LSP = make(map[string]workspace.LSPClientInfo, 4)
		for i := 1; i <= 4; i++ {
			name := fmt.Sprintf("lsp-%d", i)
			f.LSP[name] = workspace.LSPClientInfo{Name: name, State: lsp.StateReady, DiagnosticCount: i}
		}
		return nil
	default:
		return fmt.Errorf("invalid WEBFAKE_HUD_SERVERS: %q", v)
	}
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
