package webtest

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/charmbracelet/crush/internal/workspace"
)

// m3ShellLines is what the seeded shell command streams, one progress call
// per line.
var m3ShellLines = []string{
	"building the parser\n",
	"linting the parser\n",
	"3 tests passed\n",
}

// M3Commands is the custom command catalog the M3 webfake scenario loads:
// one command with an argument and one without.
func M3Commands() []commands.CustomCommand {
	return []commands.CustomCommand{
		{
			ID:      "user:fix",
			Name:    "user:fix",
			Content: "Fix $FILE and make sure the tests still pass.",
			Arguments: []commands.Argument{
				{ID: "FILE", Title: "File", Description: "The file to fix", Required: true},
			},
		},
		{
			ID:      "project:hello",
			Name:    "project:hello",
			Content: "Say hello to the project and list its entry points.",
		},
	}
}

// SeedM3 adds what the M3 browser surfaces show: a model config, a command
// and skill catalog, MCP and LSP servers, a side-question answer, a shell
// command that streams, and three more sessions. webfake applies it after
// SeedHolodeck, only for the m3 scenario, so the default scenario and its
// screenshots stay as they were.
func SeedM3(f *Fake, clock time.Time) error {
	cfg := ModelConfig()
	f.Cfg = cfg
	agentModel := workspace.AgentModel{ModelCfg: cfg.Models[config.SelectedModelTypeMain]}
	if model := cfg.GetModel(agentModel.ModelCfg.Provider, agentModel.ModelCfg.Model); model != nil {
		agentModel.CatwalkCfg = *model
	}
	f.SetAgentModel(agentModel)

	for _, s := range []session.Session{
		{ID: "s-2", Title: "Tokenizer port", CreatedAt: clock.Unix(), UpdatedAt: clock.Unix()},
		{ID: "s-3", Title: "Diagnostics sweep", CreatedAt: clock.Unix(), UpdatedAt: clock.Unix()},
		{ID: "s-4", Title: "Release notes", CreatedAt: clock.Unix(), UpdatedAt: clock.Unix()},
	} {
		f.AddSession(s)
	}

	f.AddSkill(skills.CatalogEntry{
		ID:            "go-tests",
		Name:          "go-tests",
		Description:   "Run the Go tests and report the failures",
		Label:         "user:go-tests",
		Source:        skills.SourceProject,
		UserInvocable: true,
	}, "# Go tests\n\nRun `go test ./...` and report every failure.\n")

	f.AddMCPPrompt(commands.MCPPrompt{
		ID:          "github:triage",
		Title:       "Triage an issue",
		Description: "Sort an issue into a plan.",
		PromptID:    "triage",
		ClientID:    "github",
		Arguments: []commands.Argument{
			{ID: "topic", Title: "Topic", Description: "What to triage", Required: true},
		},
	}, "Triage the parser failures into a plan.")
	f.AddMCPPrompt(commands.MCPPrompt{
		ID:          "notes:summary",
		Title:       "Summarize the notes",
		Description: "Turn the session notes into a summary.",
		PromptID:    "summary",
		ClientID:    "notes",
	}, "Summarize the session notes.")

	f.MCP = map[string]mcp.ClientInfo{
		"github": {Name: "github", State: mcp.StateConnected, Counts: mcp.Counts{Tools: 12}},
		"notes":  {Name: "notes", State: mcp.StateNeedsAuth, Error: errors.New("connection refused")},
	}
	f.MCPPending = []mcp.PendingAuthServer{{Name: "notes", URL: "https://auth.example/notes"}}
	f.AuthURL = "https://auth.example/notes"

	f.LSP = map[string]workspace.LSPClientInfo{
		"gopls":    {Name: "gopls", State: lsp.StateReady, DiagnosticCount: 3},
		"tsserver": {Name: "tsserver", State: lsp.StateStopped},
	}
	f.LSPDiagnostics = map[string]lsp.DiagnosticCounts{
		"gopls": {Error: 2, Warning: 1},
	}

	f.AskAnswer = "The parser uses a recursive descent."
	f.OnShell = func(ctx context.Context, _, _ string, onProgress func(string), _ bool) (proto.ShellCommandResponse, error) {
		for _, line := range m3ShellLines {
			select {
			case <-ctx.Done():
				return proto.ShellCommandResponse{ExitCode: 1}, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			onProgress(line)
		}
		return proto.ShellCommandResponse{Output: strings.Join(m3ShellLines, ""), ExitCode: 0}, nil
	}
	return nil
}
