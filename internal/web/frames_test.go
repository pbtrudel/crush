package web

import (
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
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
// loudly if that ever happens again. A frame type with several shapes (a shell
// chunk, its end and its failure) is keyed "type" plus a suffix from
// shellGoldenKeys.
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

// TestGoldenCoversEveryFrameType keeps the contract complete and exact. The
// frame types are read from the source, not from a hand-written list, so a new
// frame constant or a new forwarded payload type without a golden example
// fails here, which is what stops the frontend's zod boundary from meeting an
// unknown type at runtime. A fixture keyed to no frame type fails here too,
// and TestFrameGolden pins the file to these frames, so the file's keys are
// exactly this set: a stale or misspelled key in the file fails there.
func TestGoldenCoversEveryFrameType(t *testing.T) {
	t.Parallel()
	frames := goldenFrames(t)
	want := goldenFrameKeys(t)
	for _, typ := range want {
		require.Contains(t, frames, typ, "the gateway can publish %q with no golden example", typ)
	}
	known := make(map[string]struct{}, len(want))
	for _, typ := range want {
		known[typ] = struct{}{}
	}
	for typ := range frames {
		require.Contains(t, known, typ, "the golden has a key no frame type explains")
	}
}

// goldenFrameKeys is the exact set of keys the golden carries: every frame
// type the source declares, plus the extra keys for a frame type with more
// than one payload shape.
func goldenFrameKeys(t *testing.T) []string {
	t.Helper()
	keys := append(sourceFrameTypes(t), shellGoldenKeys...)
	slices.Sort(keys)
	return keys
}

// shellGoldenKeys are the extra golden keys for a frame type with more than
// one payload shape. They build on the frame constant, so renaming the type
// renames them too.
var shellGoldenKeys = []string{
	frameShellOutput + "_done",
	frameShellOutput + "_failed",
}

// excludedFrameTypes are the forwarded payload types this package never puts
// on the wire, so the golden has no example for them. Every exclusion names a
// reason: an unexplained exclusion would let a frame type leave the contract
// silently.
var excludedFrameTypes = map[string]string{
	// Forwarded by the workspace wire, but constructed outside internal/web;
	// the systems page adds it in M6.
	pubsub.PayloadTypeSkillsEvent: "constructed outside internal/web; M6 adds it",
	// The terminal's theme handover, a client/server concern the browser
	// reads from its own theme state; M6 adds it.
	pubsub.PayloadTypeHostTheme: "client/server theme handover; M6 adds it",
	// Bookkeeping for the client/server workspace link, which web mode does
	// not run.
	pubsub.PayloadTypeClientSession: "client/server link bookkeeping; web mode never publishes it",
	// encode drops the pinentry and sshaskpass events on purpose: the browser
	// answers them through secure_prompt.
	pubsub.PayloadTypePinentryPromptRequest: "encode drops it; the browser answers through secure_prompt",
	pubsub.PayloadTypePinentryNotification:  "encode drops it; the browser answers through secure_prompt",
	pubsub.PayloadTypeSSHPromptRequest:      "encode drops it; the browser answers through secure_prompt",
	pubsub.PayloadTypeSSHNotification:       "encode drops it; the browser answers through secure_prompt",
}

// sourceFrameTypes derives every frame type the gateway can publish from the
// source: the "frame…" string constants this package declares, plus the pubsub
// payload types it forwards, less excludedFrameTypes.
func sourceFrameTypes(t *testing.T) []string {
	t.Helper()
	dir := sourceDir(t)

	var out []string
	for _, file := range parseGoFiles(t, dir, func(name string) bool {
		return !strings.HasSuffix(name, "_test.go")
	}) {
		out = append(out, stringConsts(t, file, "frame")...)
	}

	forwarded := stringConsts(t, parseGoFile(t, filepath.Join(dir, "..", "pubsub", "events.go")), "PayloadType")
	declared := make(map[string]struct{}, len(forwarded))
	for _, typ := range forwarded {
		declared[typ] = struct{}{}
		if _, excluded := excludedFrameTypes[typ]; !excluded {
			out = append(out, typ)
		}
	}
	// A renamed payload type would strand its exclusion and slip out of the
	// contract, so every exclusion must still name a declared type.
	for typ, reason := range excludedFrameTypes {
		require.Contains(t, declared, typ, "excluded %q (%s) is no longer a pubsub payload type", typ, reason)
	}
	return out
}

// sourceDir is this package's directory, so the parser reads the files the
// test was compiled from however the test binary was started.
func sourceDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Dir(file)
}

// parseGoFiles parses the Go files in dir that keep reports true.
func parseGoFiles(t *testing.T, dir string, keep func(name string) bool) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() || !keep(entry.Name()) {
			continue
		}
		files = append(files, parseGoFile(t, filepath.Join(dir, entry.Name())))
	}
	require.NotEmpty(t, files)
	return files
}

// parseGoFile parses one Go file.
func parseGoFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err)
	return file
}

// stringConsts returns the values of the string constants in file whose names
// start with prefix. An inherited constant expression has no literal to read,
// so it is skipped: every frame type is spelled out.
func stringConsts(t *testing.T, file *ast.File, prefix string) []string {
	t.Helper()
	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			values := spec.(*ast.ValueSpec)
			for i, name := range values.Names {
				if !strings.HasPrefix(name.Name, prefix) || i >= len(values.Values) {
					continue
				}
				lit, ok := values.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				out = append(out, val)
			}
		}
	}
	return out
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
		// And the failure shape handleShell publishes: the fixed sentence
		// shellFailedText, never the workspace error.
		frameShellOutput + "_failed": mkSession(frameShellOutput, "s1", shellDonePayload{
			RunID: "run-2", SessionID: "s1", Done: true, ExitCode: 1, Error: shellFailedText,
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
