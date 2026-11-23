package web

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

const fixturePath = "../../web/src/state/__fixtures__/events.json"

func fixtureEvents() map[string]any {
	return map[string]any{
		"message_user": pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
			ID: "u-1", SessionID: "s1", Role: message.User, CreatedAt: 100, UpdatedAt: 100,
			Parts: []message.ContentPart{message.TextContent{Text: "Refactor the parser"}},
		}},
		"message_assistant": pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: message.Message{
			ID: "a-1", SessionID: "s1", Role: message.Assistant, CreatedAt: 101, UpdatedAt: 102, Model: "claude", Provider: "anthropic",
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "Look at parser.go first."},
				message.TextContent{Text: "Reading **parser.go**."},
				message.ToolCall{ID: "tc-1", Name: "view", Input: `{"file_path":"parser.go"}`, Finished: true},
				message.Finish{Reason: message.FinishReasonToolUse, Time: 102},
			},
		}},
		"message_tool_result": pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
			ID: "t-1", SessionID: "s1", Role: message.Tool, CreatedAt: 103, UpdatedAt: 103,
			Parts: []message.ContentPart{message.ToolResult{ToolCallID: "tc-1", Name: "view", Content: "package parser"}},
		}},
		"message_child": pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: message.Message{
			ID: "sub-a-1", SessionID: "sub-1", Role: message.Assistant, CreatedAt: 105, UpdatedAt: 106,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Scanning the tokenizer."},
				message.ToolCall{ID: "sub-tc-1", Name: "grep", Input: `{"pattern":"func Parse"}`},
			},
		}},
		"session": pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: session.Session{
			ID: "s1", Title: "Parser refactor", PromptTokens: 52000, CompletionTokens: 4100, Cost: 0.42,
			Todos: []session.Todo{
				{Content: "Map the parser", Status: session.TodoStatusCompleted, ActiveForm: "Mapping the parser"},
				{Content: "Port the parser", Status: session.TodoStatusInProgress, ActiveForm: "Porting the parser"},
				{Content: "Write parser tests", Status: session.TodoStatusPending, ActiveForm: "Writing parser tests"},
			},
		}},
		"permission_request": pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
			ID: "p-1", SessionID: "s1", ToolCallID: "tc-2", ToolName: "bash", Description: "Run tests",
			Action: "execute", Params: map[string]string{"command": "go test ./..."}, Path: "/repo", Danger: "matches block list: rm -rf",
		}},
		"permission_notification": pubsub.Event[permission.PermissionNotification]{Type: pubsub.UpdatedEvent, Payload: permission.PermissionNotification{
			SessionID: "s1", ToolCallID: "tc-2", Granted: true,
		}},
		"question_batch_request": pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: question.Request{
			ID: "q-1", SessionID: "s1", ToolCallID: "tc-3", Questions: []question.Question{{
				ID: "flavor", Type: question.TypeSingleChoice, Text: "Which theme?",
				Choices: []question.Choice{{ID: "cyan", Label: "Cyan"}},
			}},
		}},
		"question_batch_notification": pubsub.Event[question.Notification]{Type: pubsub.UpdatedEvent, Payload: question.Notification{
			BatchID: "q-1", SessionID: "s1",
		}},
		"permission_mode_changed": pubsub.Event[permission.ModeChangedEvent]{Type: pubsub.UpdatedEvent, Payload: permission.ModeChangedEvent{
			Mode: permission.PermissionModeYolo,
		}},
		"run_complete": pubsub.Event[notify.RunComplete]{Type: pubsub.CreatedEvent, Payload: notify.RunComplete{
			SessionID: "s1", RunID: "r-1", MessageID: "a-1", Text: "done",
		}},
		"lsp_event": pubsub.Event[app.LSPEvent]{Type: pubsub.UpdatedEvent, Payload: app.LSPEvent{
			Type: app.LSPEventStateChanged, Name: "gopls", DiagnosticCount: 2,
		}},
		"file": pubsub.Event[history.File]{Type: pubsub.CreatedEvent, Payload: history.File{
			ID: "f-1", SessionID: "sub-1", Path: "/repo/parser.go", Content: "package parser", Version: 1, CreatedAt: 104, UpdatedAt: 104,
		}},
	}
}

// fixtureTeam is the roster and task board the stage and the team tab show.
func fixtureTeam() TeamState {
	return TeamState{
		TeamID: "team-1",
		Members: []agentteam.Member{{
			ID: "m-1", Name: "alice", Role: agentteam.RoleTeammate, SessionID: "team-m-1", ParentID: "s1",
			Status: agentteam.StatusRunning, Model: "claude", ModelProvider: "anthropic",
		}},
		Tasks: []TeamTask{
			{ID: "task-1", Revision: 2, Subject: "Port the parser", Status: agentteam.TaskInProgress, OwnerID: "m-1", OwnerName: "alice", BlockedBy: []string{}, SessionID: "s1"},
			{ID: "task-2", Revision: 1, Subject: "Write parser tests", Status: agentteam.TaskPending, BlockedBy: []string{"task-1"}, SessionID: "s1"},
		},
	}
}

// fixtureRuns is the live agent registry: one subagent and one teammate.
func fixtureRuns() []agentrun.Run {
	started := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	return []agentrun.Run{
		{ID: "sub-1", Kind: agentrun.KindSubagent, Name: "Map the parser", SessionID: "sub-1", ParentSessionID: "s1", Status: agentrun.StatusRunning, StartedAt: started},
		{ID: "team-m-1", Kind: agentrun.KindTeammate, Name: "alice", SessionID: "team-m-1", ParentSessionID: "s1", Status: agentrun.StatusRunning, StartedAt: started},
	}
}

// gatewayFixtureFrames builds the frames the gateway originates itself,
// which never pass through encode.
func gatewayFixtureFrames(t *testing.T) map[string]frame {
	t.Helper()
	mk := func(typ string, v any) frame {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		return frame{Seq: 1, Type: typ, Payload: raw}
	}
	return map[string]frame{
		"team_state": mk(frameTeamState, fixtureTeam()),
		"agent_runs": mk(frameAgentRuns, fixtureRuns()),
		// A detached prompt that failed shows the fixed message, never the
		// workspace error.
		"web_error": mk(frameError, promptFailure("s1", errors.New("dial unix /tmp/crush.sock: refused"))),
		"shell_output": mk(frameShellOutput, shellOutputPayload{
			RunID: "run-1", SessionID: "s1", Chunk: "line 1\n",
		}),
		// A model switch made in the browser or the terminal: the HUD and
		// the selector render the new model from this payload.
		"model_changed": mk(frameModelChanged, ModelInfo{
			ID: "claude-sonnet", Name: "Claude Sonnet", Provider: "anthropic", ContextWindow: 200000,
			CanReason: true, EffortLevels: []string{"low", "medium", "high"},
			ReasoningEffort: "high", Think: true,
			Small:      &ModelRef{ID: "claude-haiku", Provider: "anthropic"},
			AgentReady: true,
		}),
	}
}

// snapshotFrame builds the snapshot the gateway sends before any live frame.
// A snapshot carries the hub head as its seq, which is 0 for a fresh hub.
func snapshotFrame(t *testing.T) frame {
	t.Helper()
	msg := innerPayload(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
		ID: "a-0", SessionID: "s1", Role: message.Assistant, CreatedAt: 1, UpdatedAt: 2,
		Model: "claude", Provider: "anthropic",
		Parts: []message.ContentPart{
			message.ToolCall{ID: "tc-0", Name: "bash", Input: "{}", Finished: true},
			message.Finish{Reason: message.FinishReasonEndTurn, Time: 2},
		},
	}})
	require.NotNil(t, msg)
	raw, err := json.Marshal(Snapshot{
		Version: "test", WorkingDir: "/repo", CurrentSessionID: "s1",
		Messages: []json.RawMessage{msg}, Permissions: []json.RawMessage{},
		Files: []FileSummary{{
			Path: "/repo/parser.go", Versions: 2, VersionIDs: []int64{0, 1}, Additions: 3, Removals: 1,
			Diagnostics: FileDiagnostics{Error: 1}, UpdatedAt: 104, SessionIDs: []string{"s1", "sub-1"},
		}},
		PermissionMode: "normal", InputMode: "code",
		Model: ModelInfo{
			ID: "claude", Name: "Claude", Provider: "anthropic", ContextWindow: 200000,
			CanReason: true, EffortLevels: []string{"low", "medium", "high"},
			ReasoningEffort: "high", Think: true,
			Small:      &ModelRef{ID: "claude-haiku", Provider: "anthropic"},
			AgentReady: true,
		},
		AgentReady: true,
		Queued:     []string{}, LSP: map[string]ServerInfo{}, MCP: map[string]ServerInfo{},
		Team:      fixtureTeam(),
		AgentRuns: fixtureRuns(),
	})
	require.NoError(t, err)
	return frame{Seq: 0, Type: frameSnapshot, Payload: raw}
}

// TestWireFixtures keeps the frontend fixture in sync with real wire output.
// Regenerate with CRUSH_WEB_FIXTURES=1 go test ./internal/web -run TestWireFixtures.
func TestWireFixtures(t *testing.T) {
	t.Parallel()
	out := map[string]frame{}
	for name, ev := range fixtureEvents() {
		typ, raw, ok := encode(ev)
		require.True(t, ok, name)
		out[name] = frame{Seq: 1, Type: typ, Payload: raw}
	}
	out["snapshot"] = snapshotFrame(t)
	for name, f := range gatewayFixtureFrames(t) {
		out[name] = f
	}
	got, err := json.MarshalIndent(out, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')

	if os.Getenv("CRUSH_WEB_FIXTURES") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(fixturePath), 0o755))
		require.NoError(t, os.WriteFile(fixturePath, got, 0o644))
		return
	}
	want, err := os.ReadFile(fixturePath)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing %s; run with CRUSH_WEB_FIXTURES=1", fixturePath)
	}
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
}
