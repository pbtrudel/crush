package web

import (
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/agentrun"
	"github.com/charmbracelet/crush/internal/agentteam"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

// goldenPath is the wire contract the frontend validates with zod. Every
// frame type the gateway constructs has one example here.
const goldenPath = "testdata/frames.golden.json"

var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// TestFrameGolden keeps the contract in step with the gateway. Regenerate
// with: go test ./internal/web -run TestFrameGolden -update
func TestFrameGolden(t *testing.T) {
	frames := goldenFrames(t)
	data, err := json.MarshalIndent(frames, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	if *updateGolden {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(goldenPath, data, 0o644))
		return
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "run with -update to write the golden file")
	require.JSONEq(t, string(want), string(data))
}

// goldenFrames builds the contract: one example per event frame type, keyed
// by that type, plus the gateway-originated frames. The key of an event frame
// is its type and not the fixture name, because two fixtures for one type
// would make the file depend on map iteration order; the require below fails
// loudly if that ever happens again. A frame type with two shapes (a shell
// chunk and its end) is keyed "type" and "type_done".
func goldenFrames(t *testing.T) map[string]frame {
	t.Helper()
	out := make(map[string]frame)
	for name, ev := range fixtureEvents() {
		typ, raw, ok := encode(ev)
		require.True(t, ok, name)
		require.NotContains(t, out, typ, "two fixtures share the frame type %s", typ)
		out[typ] = frame{Seq: 1, Type: typ, Session: eventSessionID(ev), Payload: raw}
	}
	for name, f := range gatewayFrames(t) {
		out[name] = f
	}
	return out
}

// TestGoldenCoversEveryFrameType keeps the contract complete: a frame type the
// gateway can put on the wire without an example in the golden file fails
// here, which is what stops the frontend's zod boundary from meeting an
// unknown type at runtime.
func TestGoldenCoversEveryFrameType(t *testing.T) {
	t.Parallel()
	frames := goldenFrames(t)
	for _, typ := range frameTypes() {
		require.Contains(t, frames, typ)
	}
}

// frameTypes is every frame type this package can publish, gateway-originated
// or forwarded from the workspace wire.
func frameTypes() []string {
	return []string{
		frameBootstrap,
		frameSessionSnapshot,
		frameSessionStatus,
		frameFollow,
		frameInputMode,
		framePlanReady,
		frameTeamState,
		frameAgentRuns,
		frameModelChanged,
		frameClosing,
		frameError,
		frameSecurePrompt,
		frameSignIn,
		frameShellOutput,
		pubsub.PayloadTypeMessage,
		pubsub.PayloadTypeFile,
		pubsub.PayloadTypeSession,
		pubsub.PayloadTypePermissionRequest,
		pubsub.PayloadTypePermissionNotification,
		pubsub.PayloadTypePermissionModeChanged,
		pubsub.PayloadTypeQuestionRequest,
		pubsub.PayloadTypeQuestionNotification,
		pubsub.PayloadTypeRunComplete,
		pubsub.PayloadTypeAgentEvent,
		pubsub.PayloadTypeLSPEvent,
		pubsub.PayloadTypeMCPEvent,
		pubsub.PayloadTypeConfigStale,
		pubsub.PayloadTypeConfigChanged,
		pubsub.PayloadTypeUpdateAvailable,
	}
}

// fixtureEvents is one example of every workspace event the gateway
// forwards.
func fixtureEvents() map[string]any {
	return map[string]any{
		// One message fixture, the richest: the user shape is the same
		// message with one text part.
		"message": pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: message.Message{
			ID: "a-1", SessionID: "s1", Role: message.Assistant, CreatedAt: 101, UpdatedAt: 102,
			Model: "claude", Provider: "anthropic",
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "Look at parser.go first."},
				message.TextContent{Text: "Reading parser.go."},
				message.ToolCall{ID: "tc-1", Name: "view", Input: `{"file_path":"parser.go"}`, Finished: true},
				message.Finish{Reason: message.FinishReasonToolUse, Time: 102},
			},
		}},
		"session": pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: session.Session{
			ID: "s1", Title: "Parser refactor", MessageCount: 4, UpdatedAt: 104,
		}},
		"file": pubsub.Event[history.File]{Type: pubsub.CreatedEvent, Payload: history.File{
			ID: "f-1", SessionID: "s1", Path: "/repo/parser.go", Content: "package parser",
			Version: 1, CreatedAt: 104, UpdatedAt: 104,
		}},
		"permission_request": pubsub.Event[permission.PermissionRequest]{Type: pubsub.CreatedEvent, Payload: permission.PermissionRequest{
			ID: "p-1", SessionID: "s1", ToolCallID: "tc-2", ToolName: "bash",
			Description: "Run tests", Action: "execute", Params: map[string]string{"command": "go test ./..."},
		}},
		"permission_notification": pubsub.Event[permission.PermissionNotification]{Type: pubsub.UpdatedEvent, Payload: permission.PermissionNotification{
			SessionID: "s1", ToolCallID: "tc-2", Granted: true,
		}},
		"permission_mode_changed": pubsub.Event[permission.ModeChangedEvent]{Type: pubsub.UpdatedEvent, Payload: permission.ModeChangedEvent{
			Mode: permission.PermissionModeYolo,
		}},
		"question_batch_request": pubsub.Event[question.Request]{Type: pubsub.CreatedEvent, Payload: question.Request{
			ID: "q-1", SessionID: "s1", ToolCallID: "tc-3",
			Questions: []question.Question{{
				ID: "flavor", Type: question.TypeSingleChoice, Text: "Which theme?",
				Choices: []question.Choice{{ID: "cyan", Label: "Cyan"}},
			}},
		}},
		"question_batch_notification": pubsub.Event[question.Notification]{Type: pubsub.UpdatedEvent, Payload: question.Notification{
			BatchID: "q-1", SessionID: "s1",
		}},
		"run_complete": pubsub.Event[notify.RunComplete]{Type: pubsub.CreatedEvent, Payload: notify.RunComplete{
			SessionID: "s1", RunID: "r-1", MessageID: "a-1", Text: "done",
		}},
		"lsp_event": pubsub.Event[app.LSPEvent]{Type: pubsub.UpdatedEvent, Payload: app.LSPEvent{
			Type: app.LSPEventStateChanged, Name: "gopls", DiagnosticCount: 2,
		}},
		"mcp_event": pubsub.Event[mcp.Event]{Type: pubsub.UpdatedEvent, Payload: mcp.Event{
			Type: mcp.EventStateChanged, Name: "fs", State: mcp.StateConnected,
			Counts: mcp.Counts{Tools: 3},
		}},
		"agent_event": pubsub.Event[notify.Notification]{Type: pubsub.CreatedEvent, Payload: notify.Notification{
			SessionID: "s1", SessionTitle: "Parser refactor", Type: notify.TypeAgentError,
			ProviderID: "anthropic", Message: "The provider refused the request",
		}},
		"config_stale":     app.ConfigStaleMsg{Changed: []string{"/repo/crushrc"}, Missing: []string{}},
		"update_available": app.UpdateAvailableMsg{CurrentVersion: "0.30.0", LatestVersion: "0.31.0"},
		"config_changed":   pubsub.Event[proto.ConfigChanged]{Type: pubsub.UpdatedEvent, Payload: proto.ConfigChanged{WorkspaceID: "ws-1"}},
	}
}

// gatewayFrames builds one example of every frame the gateway originates
// itself, which never pass through encode.
func gatewayFrames(t *testing.T) map[string]frame {
	t.Helper()
	mk := func(typ string, v any) frame {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		return frame{Seq: 1, Type: typ, Payload: raw}
	}
	mkSession := func(typ, sessionID string, v any) frame {
		f := mk(typ, v)
		f.Session = sessionID
		return f
	}
	return map[string]frame{
		frameBootstrap: mk(frameBootstrap, fixtureBootstrap()),
		frameSessionSnapshot: mkSession(frameSessionSnapshot, "s1", SessionSnapshot{
			SessionID: "s1",
			Session:   fixtureSessionEntity(),
			Messages:  []json.RawMessage{fixtureUserMessage(t)},
			HasMore:   true,
			Files: []FileSummary{{
				Path: "/repo/parser.go", Versions: 1, VersionIDs: []int64{0},
				Additions: 3, Removals: 1, UpdatedAt: 104, SessionIDs: []string{"s1"},
			}},
			Busy:   true,
			Queued: []string{"and the tests"},
		}),
		frameSessionStatus: mk(frameSessionStatus, map[string]string{"session_id": "s1", "status": statusRunning}),
		frameFollow:        mk(frameFollow, map[string]string{"session_id": "s1"}),
		frameInputMode:     mk(frameInputMode, map[string]string{"mode": "plan"}),
		framePlanReady:     mk(framePlanReady, map[string]string{"session_id": "s1"}),
		frameTeamState:     mk(frameTeamState, fixtureTeam()),
		frameAgentRuns:     mk(frameAgentRuns, fixtureRuns()),
		frameModelChanged:  mk(frameModelChanged, fixtureModelInfo()),
		frameClosing:       mk(frameClosing, map[string]string{"reason": "crush exited"}),
		frameError:         mk(frameError, webErrorPayload("s1", "agent_starting", "Crush is starting")),
		frameSecurePrompt: mk(frameSecurePrompt, map[string][]SecurePrompt{"prompts": {{
			Source: sourcePinentry, ID: "p1", Kind: "passphrase", Title: "GPG passphrase",
			Message: "Unlock the signing key", Details: []string{"Key: ABCDEF0123456789"},
		}}}),
		frameSignIn: mk(frameSignIn, SignInState{
			FlowID: "f1", Provider: "copilot", ProviderName: "GitHub Copilot",
			State: signInWaiting, Kind: signInKindDevice,
			UserCode: "WDJB-MJHT", VerificationURL: "https://github.com/login/device",
		}),
		frameShellOutput: mkSession(frameShellOutput, "s1", shellOutputPayload{
			RunID: "run-1", SessionID: "s1", Chunk: "line 1\n",
		}),
		// The same frame type with the run's last payload: a client must
		// accept either shape.
		frameShellOutput + "_done": mkSession(frameShellOutput, "s1", shellDonePayload{
			RunID: "run-1", SessionID: "s1", Done: true, ExitCode: 0,
		}),
	}
}

func fixtureBootstrap() Bootstrap {
	return Bootstrap{
		Version:          "test",
		WorkingDir:       "/repo",
		Branch:           "main",
		CurrentSessionID: "s1",
		Sessions: []sessionRow{{
			Session: proto.Session{ID: "s1", Title: "Parser refactor", MessageCount: 4, UpdatedAt: 104},
			Status:  statusRunning,
		}},
		Model:              fixtureModelInfo(),
		AgentReady:         true,
		PermissionMode:     "normal",
		InputMode:          "code",
		PlanReadySessionID: "s1",
		LSP:                map[string]ServerInfo{"gopls": {State: "2", DiagnosticCount: 2}},
		MCP:                map[string]ServerInfo{"fs": {State: "connected", ToolCount: 3}},
		Team:               fixtureTeam(),
		AgentRuns:          fixtureRuns(),
		Permissions:        []json.RawMessage{},
	}
}

func fixtureModelInfo() ModelInfo {
	return ModelInfo{
		ID: "claude-sonnet", Name: "Claude Sonnet", Provider: "anthropic", ContextWindow: 200000,
		CanReason: true, EffortLevels: []string{"low", "medium", "high"},
		ReasoningEffort: "high", Think: true,
		Small:      &ModelRef{ID: "claude-haiku", Provider: "anthropic"},
		AgentReady: true,
	}
}

func fixtureSessionEntity() json.RawMessage {
	return innerPayload(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: session.Session{
		ID: "s1", Title: "Parser refactor", MessageCount: 4, UpdatedAt: 104,
	}})
}

func fixtureUserMessage(t *testing.T) json.RawMessage {
	t.Helper()
	raw := innerPayload(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent, Payload: message.Message{
		ID: "u-1", SessionID: "s1", Role: message.User, CreatedAt: 100, UpdatedAt: 100,
		Parts: []message.ContentPart{message.TextContent{Text: "Refactor the parser"}},
	}})
	require.NotNil(t, raw)
	return raw
}

func fixtureTeam() TeamState {
	return TeamState{
		TeamID: "team-1",
		Members: []agentteam.Member{{
			ID: "m-1", Name: "alice", Role: agentteam.RoleTeammate, SessionID: "team-m-1",
			ParentID: "s1", Status: agentteam.StatusRunning, Model: "claude", ModelProvider: "anthropic",
		}},
		Tasks: []TeamTask{{
			ID: "task-1", Revision: 2, Subject: "Port the parser", Status: agentteam.TaskInProgress,
			OwnerID: "m-1", OwnerName: "alice", BlockedBy: []string{}, SessionID: "s1",
		}},
	}
}

func fixtureRuns() []agentrun.Run {
	started := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	return []agentrun.Run{{
		ID: "sub-1", Kind: agentrun.KindSubagent, Name: "Map the parser", SessionID: "sub-1",
		ParentSessionID: "s1", Status: agentrun.StatusRunning, StartedAt: started,
	}}
}
