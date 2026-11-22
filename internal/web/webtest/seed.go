package webtest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

const parserV2 = "package parser\n\n// Parse reads one token stream.\nfunc Parse(src string) []Token {\n\treturn tokenize(src)\n}\n"

func (f *Fake) parserPath() string {
	return filepath.Join(f.WorkingDir(), "internal", "parser", "parser.go")
}

// SeedHolodeck gives the fake a teammate, a subagent, edited files and
// todos, so every M2 surface has something to show. teamDir receives
// team.json and agents.json for Options.AgentTeamsDir. Messages are dated
// from clock so a pinned browser clock shows stable elapsed times; the
// registry runs use the wall clock because agentrun.Load prunes by it.
func SeedHolodeck(f *Fake, teamDir string, clock time.Time) error {
	ts := clock.Add(-42 * time.Second).Unix()
	app := filepath.Join(f.WorkingDir(), "web", "src", "app.ts")

	root, err := f.GetSession(context.Background(), "s1")
	if err != nil {
		return err
	}
	root.Todos = []session.Todo{
		{Content: "Map the parser", Status: session.TodoStatusCompleted, ActiveForm: "Mapping the parser"},
		{Content: "Port the parser", Status: session.TodoStatusInProgress, ActiveForm: "Porting the parser"},
		{Content: "Write parser tests", Status: session.TodoStatusPending, ActiveForm: "Writing parser tests"},
	}
	f.AddSession(root)
	f.AddSession(session.Session{ID: "sub-1", ParentSessionID: "s1", Title: "Map the parser", CreatedAt: ts, PromptTokens: 3100, CompletionTokens: 220, Cost: 0.004})
	f.AddSession(session.Session{ID: "team-m-1", ParentSessionID: "s1", Title: "Teammate alice", CreatedAt: ts, PromptTokens: 8200, CompletionTokens: 640, Cost: 0.031})

	// Seed IDs must not look like nextID output ("<prefix>-<n>"), or the
	// scripted agent's first message or tool call reuses one.
	f.AddMessage(message.Message{ID: "sub-seed-a", SessionID: "sub-1", Role: message.Assistant, CreatedAt: ts, UpdatedAt: ts, Model: "claude-haiku", Provider: "anthropic", Parts: []message.ContentPart{
		message.TextContent{Text: "Scanning the parser package for the tokenizer entry points."},
		message.ToolCall{ID: "sub-seed-tc", Name: "grep", Input: `{"pattern":"func Parse"}`},
	}})
	f.AddMessage(message.Message{ID: "alice-a-1", SessionID: "team-m-1", Role: message.Assistant, CreatedAt: ts, UpdatedAt: ts, Model: "claude-sonnet", Provider: "anthropic", Parts: []message.ContentPart{
		message.TextContent{Text: "Porting the parser to the new token stream."},
		message.ToolCall{ID: "alice-tc-1", Name: "edit", Input: `{"file_path":"internal/parser/parser.go"}`, Finished: true},
		message.Finish{Reason: message.FinishReasonToolUse, Time: ts},
	}})
	f.AddMessage(message.Message{ID: "alice-t-1", SessionID: "team-m-1", Role: message.Tool, CreatedAt: ts, UpdatedAt: ts, Parts: []message.ContentPart{
		message.ToolResult{ToolCallID: "alice-tc-1", Name: "edit", Content: "Edited internal/parser/parser.go"},
	}})

	for _, h := range []history.File{
		{ID: "h-seed-0", SessionID: "s1", Path: f.parserPath(), Content: "package parser\n\nfunc Parse() {}\n", Version: 0, CreatedAt: ts, UpdatedAt: ts},
		{ID: "h-seed-1", SessionID: "s1", Path: f.parserPath(), Content: "package parser\n\nfunc Parse(src string) {}\n", Version: 1, CreatedAt: ts + 1, UpdatedAt: ts + 1},
		{ID: "h-seed-2", SessionID: "team-m-1", Path: f.parserPath(), Content: parserV2, Version: 2, CreatedAt: ts + 2, UpdatedAt: ts + 2},
		{ID: "h-seed-3", SessionID: "team-m-1", Path: app, Content: "", Version: 0, CreatedAt: ts + 3, UpdatedAt: ts + 3},
		{ID: "h-seed-4", SessionID: "team-m-1", Path: app, Content: "export const app = mount();\n", Version: 1, CreatedAt: ts + 4, UpdatedAt: ts + 4},
	} {
		f.AddHistory(h)
	}
	f.SetDiagnostics(map[string]lsp.DiagnosticCounts{f.parserPath(): {Error: 1}})

	team := agentteam.State{
		Version: 1, TeamID: "team-holodeck", CreatedAt: clock,
		Members: []agentteam.Member{
			{ID: "lead", Name: agentteam.LeadName, Role: agentteam.RoleLead, SessionID: "s1"},
			{ID: "m-1", Name: "alice", Role: agentteam.RoleTeammate, Description: "Ports the parser", SessionID: "team-m-1", ParentID: "s1", Status: agentteam.StatusRunning, Model: "claude-sonnet", ModelProvider: "anthropic"},
		},
		Tasks: []agentteam.Task{
			{ID: "task-1", Revision: 2, Subject: "Port the parser", Description: "Move Parse onto the token stream.", Status: agentteam.TaskInProgress, OwnerID: "m-1", BlockedBy: []string{}, WriteScopes: []string{"internal/parser"}, SessionID: "s1"},
			{ID: "task-2", Revision: 1, Subject: "Write parser tests", Description: "Cover the new token stream.", Status: agentteam.TaskPending, BlockedBy: []string{"task-1"}, WriteScopes: []string{}, SessionID: "s1"},
			{ID: "task-3", Revision: 3, Subject: "Map the parser", Description: "List the entry points.", Status: agentteam.TaskCompleted, BlockedBy: []string{}, WriteScopes: []string{}, SessionID: "s1"},
		},
	}
	now := time.Now()
	runs := map[string]any{"version": 1, "runs": []agentrun.Run{
		{ID: "sub-1", Kind: agentrun.KindSubagent, Name: "Map the parser", SessionID: "sub-1", ParentSessionID: "s1", Status: agentrun.StatusRunning, StartedAt: now},
		{ID: "team-m-1", Kind: agentrun.KindTeammate, Name: "alice", SessionID: "team-m-1", ParentSessionID: "s1", Status: agentrun.StatusRunning, StartedAt: now},
	}}
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		return err
	}
	for name, v := range map[string]any{"team.json": team, "agents.json": runs} {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Join(teamDir, name), data); err != nil {
			return err
		}
	}
	return nil
}

// writeFileAtomic replaces path with data so the team watcher never reads a
// half-written file.
func writeFileAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".webtest-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// delegate plays the subagent picking up work: a new edit call on sub-1
// and the next version of parser.go. The record is dated after every
// seeded version so it is the latest position even when WEBFAKE_CLOCK runs
// ahead of the wall clock.
func (f *Fake) delegate() {
	version, now := f.nextHistory(f.parserPath())
	call := message.ToolCall{ID: f.nextID("sub-tc"), Name: "edit", Input: `{"file_path":"internal/parser/parser.go"}`}
	m := message.Message{ID: f.nextID("sub-a"), SessionID: "sub-1", Role: message.Assistant, CreatedAt: now, UpdatedAt: now, Parts: []message.ContentPart{
		message.TextContent{Text: "Rewriting the tokenizer loop."}, call,
	}}
	f.AddMessage(m)
	f.Emit(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: m})
	h := history.File{ID: f.nextID("h"), SessionID: "sub-1", Path: f.parserPath(), Content: parserV2 + "\nfunc tokenize(string) []Token { return nil }\n", Version: version, CreatedAt: now, UpdatedAt: now}
	f.AddHistory(h)
	f.Emit(pubsub.Event[history.File]{Type: pubsub.CreatedEvent, Payload: h})
}

// nextHistory returns the version after the highest stored version of
// path, and a creation time later than every stored version of it.
func (f *Fake) nextHistory(path string) (version, at int64) {
	at = time.Now().Unix()
	sessions, _ := f.ListSessions(context.Background())
	for _, s := range sessions {
		files, _ := f.ListSessionHistory(context.Background(), s.ID)
		for _, file := range files {
			if file.Path != path {
				continue
			}
			if file.Version >= version {
				version = file.Version + 1
			}
			if file.CreatedAt >= at {
				at = file.CreatedAt + 1
			}
		}
	}
	return version, at
}
