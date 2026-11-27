package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

type mockBashPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
}

func (m *mockBashPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	return true, nil
}

func (m *mockBashPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *mockBashPermissionService) AutoApproveSession(sessionID string) {}

func (m *mockBashPermissionService) IsSessionAutoApproved(sessionID string) bool { return false }

func (m *mockBashPermissionService) InheritAutoApproval(parentSessionID, childSessionID string) {}

func (m *mockBashPermissionService) ActiveRequests() []permission.PermissionRequest {
	return nil
}

func (m *mockBashPermissionService) SetSessionScope(scope permission.SessionScope) {}

func (m *mockBashPermissionService) SetSkipRequests(skip bool) {}

func (m *mockBashPermissionService) SkipRequests() bool {
	return false
}

func (m *mockBashPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func (m *mockBashPermissionService) SetPermissionMode(mode permission.PermissionMode) {}

func (m *mockBashPermissionService) PermissionMode() permission.PermissionMode {
	return permission.PermissionModeNormal
}

func (m *mockBashPermissionService) SubscribeModeChanges(ctx context.Context) <-chan pubsub.Event[permission.ModeChangedEvent] {
	return make(<-chan pubsub.Event[permission.ModeChangedEvent])
}

func TestBashTool_DefaultAutoBackgroundThreshold(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "default threshold",
		Command:     "echo done",
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.False(t, meta.Background)
	require.Empty(t, meta.ShellID)
	require.Contains(t, meta.Output, "done")
}

func TestBashTool_BackgroundJobFastCompletionStaysReadable(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:     "quick background",
		Command:         "echo quick",
		RunInBackground: true,
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.NotEmpty(t, meta.ShellID)
	require.Contains(t, meta.Output, "quick")

	bgManager := shell.GetBackgroundShellManager()
	defer bgManager.Kill(context.Background(), meta.ShellID)

	// The completion notice for this job points at its ID, so the ID must
	// still resolve once the fast-failure probe has seen it finish.
	bgShell, ok := bgManager.Get(meta.ShellID)
	require.True(t, ok, "a job the model is told to read must stay readable")
	require.True(t, bgShell.IsDone())
}

func TestBashTool_CustomAutoBackgroundThreshold(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:         "custom threshold",
		Command:             "sleep 1.5 && echo done",
		AutoBackgroundAfter: 1,
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.True(t, meta.Background)
	require.NotEmpty(t, meta.ShellID)
	require.Contains(t, resp.Content, "moved to background")

	bgManager := shell.GetBackgroundShellManager()
	require.NoError(t, bgManager.Kill(t.Context(), meta.ShellID))
}

type recordingPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
	requestCount int
	allow        bool
	mode         permission.PermissionMode
	lastDanger   string
	lastRequest  permission.CreatePermissionRequest
}

func (m *recordingPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	m.requestCount++
	m.lastDanger = req.Danger
	m.lastRequest = req
	return m.allow, nil
}

func (m *recordingPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *recordingPermissionService) AutoApproveSession(sessionID string) {}

func (m *recordingPermissionService) IsSessionAutoApproved(sessionID string) bool { return false }

func (m *recordingPermissionService) InheritAutoApproval(parentSessionID, childSessionID string) {}

func (m *recordingPermissionService) ActiveRequests() []permission.PermissionRequest {
	return nil
}

func (m *recordingPermissionService) SetSessionScope(scope permission.SessionScope) {}

func (m *recordingPermissionService) SetSkipRequests(skip bool) {}

func (m *recordingPermissionService) SkipRequests() bool {
	return false
}

func (m *recordingPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func (m *recordingPermissionService) SetPermissionMode(mode permission.PermissionMode) { m.mode = mode }

func (m *recordingPermissionService) PermissionMode() permission.PermissionMode { return m.mode }

func (m *recordingPermissionService) SubscribeModeChanges(ctx context.Context) <-chan pubsub.Event[permission.ModeChangedEvent] {
	return make(<-chan pubsub.Event[permission.ModeChangedEvent])
}

func newBashToolForTest(workingDir string) fantasy.AgentTool {
	permissions := &mockBashPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(permissions, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil)
}

func newBashToolWithRecordingPerms(workingDir string, allow bool) (fantasy.AgentTool, *recordingPermissionService) {
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  allow,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil), perms
}

func TestBashTool_ChainedCommandsRequirePermission(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, true)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// A chain is judged by its parts, so joining two read-only commands is
	// no more of a prompt than either one alone would be.
	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained ls",
		Command:     "ls && echo done",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 0, perms.requestCount, "a chain of read-only commands should not prompt")

	// One part that writes is enough to require approval for the whole chain.
	perms.requestCount = 0
	resp = runBashTool(t, tool, ctx, BashParams{
		Description: "chained touch",
		Command:     "ls && touch newfile",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 1, perms.requestCount, "a chain containing a write must prompt")

	// Plain ls should NOT trigger permission check.
	perms.requestCount = 0
	resp = runBashTool(t, tool, ctx, BashParams{
		Description: "plain ls",
		Command:     "ls -la",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 0, perms.requestCount, "plain ls should not trigger permission request")
}

func TestBashTool_ChainedCommandsDenied(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, false)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained ls denied",
		Command:     "ls && rm -rf /",
	})

	require.Equal(t, 1, perms.requestCount)
	require.Contains(t, resp.Content, "User denied permission")
}

func TestIsDangerousCommand(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		dangerous bool
	}{
		{
			name:      "simple banned command - curl",
			command:   "curl https://example.com",
			dangerous: true,
		},
		{
			name:      "simple banned command - sudo",
			command:   "sudo apt-get update",
			dangerous: true,
		},
		{
			name:      "npm global install with --global",
			command:   "npm install --global typescript",
			dangerous: true,
		},
		{
			name:      "npm global install with -g",
			command:   "npm install -g typescript",
			dangerous: true,
		},
		{
			name:      "npm local install",
			command:   "npm install typescript",
			dangerous: false,
		},
		{
			name:      "go test with -exec",
			command:   "go test -exec ./malicious ./...",
			dangerous: true,
		},
		{
			name:      "go test without -exec",
			command:   "go test ./...",
			dangerous: false,
		},
		{
			name:      "safe command - ls",
			command:   "ls -la",
			dangerous: false,
		},
		{
			name:      "safe command - echo",
			command:   "echo hello",
			dangerous: false,
		},
		{
			name:      "safe command - git",
			command:   "git status",
			dangerous: false,
		},
		{
			name:      "pip install with --user",
			command:   "pip install --user requests",
			dangerous: true,
		},
		{
			name:      "pip install without --user",
			command:   "pip install requests",
			dangerous: false,
		},
		{
			name:      "brew install",
			command:   "brew install wget",
			dangerous: true,
		},
		{
			// Quoting the command name must not evade detection.
			name:      "quoted dangerous command name",
			command:   `"brew" install wget`,
			dangerous: true,
		},
		{
			// Quoted arguments must still be seen by argument blockers.
			name:      "quoted dangerous argument",
			command:   `go test "-exec" ./malicious ./...`,
			dangerous: true,
		},
		{
			// Command substitution can't be resolved without executing, so
			// it is treated conservatively as dangerous.
			name:      "command substitution is conservative",
			command:   "$(echo brew) install wget",
			dangerous: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shell.IsCommandBlocked(tt.command, blockFuncs(defaultBlockedCommands))
			require.Equal(t, tt.dangerous, result, "command: %s", tt.command)
		})
	}
}

func runBashTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params BashParams) fantasy.ToolResponse {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{
		ID:    "test-call",
		Name:  BashToolName,
		Input: string(input),
	}

	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	return resp
}

func TestTruncateOutputValidUTF8(t *testing.T) {
	t.Parallel()
	// CJK characters are 2 cells wide; this string is far wider than
	// MaxOutputLength so TruncateOutput must truncate it.
	content := strings.Repeat("你好世界", MaxOutputLength)

	out := TruncateOutput(content, t.TempDir())
	require.True(t, utf8.ValidString(out), "truncated output must stay valid UTF-8")
	require.Contains(t, out, "lines truncated")
}

func TestTruncateOutputShortContent(t *testing.T) {
	t.Parallel()
	content := "short output"
	require.Equal(t, content, TruncateOutput(content, t.TempDir()))
}

func TestTruncateOutputEmoji(t *testing.T) {
	t.Parallel()
	// Emoji with ZWJ sequences should not be split.
	content := strings.Repeat("👨‍👩‍👧‍👦", MaxOutputLength)

	out := TruncateOutput(content, t.TempDir())
	require.True(t, utf8.ValidString(out), "truncated output must stay valid UTF-8")
	require.Contains(t, out, "lines truncated")
}

func TestResolveBlockedCommands(t *testing.T) {
	t.Parallel()

	t.Run("nil config uses the defaults", func(t *testing.T) {
		t.Parallel()
		require.ElementsMatch(t, defaultBlockedCommands, resolveBlockedCommands(nil))
	})

	t.Run("blocked_commands adds to the defaults", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"kubectl", "terraform"},
		})
		require.Contains(t, got, "kubectl")
		require.Contains(t, got, "terraform")
		require.Contains(t, got, "curl", "defaults must still apply")
	})

	t.Run("allowed_commands removes defaults", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			AllowedCommands: []string{"curl", "wget"},
		})
		require.NotContains(t, got, "curl")
		require.NotContains(t, got, "wget")
		require.Contains(t, got, "sudo", "unrelated defaults must stay")
	})

	t.Run("allowed_commands wins over blocked_commands", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"kubectl"},
			AllowedCommands: []string{"kubectl"},
		})
		require.NotContains(t, got, "kubectl")
	})

	t.Run("built-in order is preserved and duplicates collapsed", func(t *testing.T) {
		t.Parallel()
		got := resolveBlockedCommands(&config.Permissions{
			BlockedCommands: []string{"curl", "kubectl", "kubectl"},
		})
		// The list is embedded verbatim in the tool description, so the
		// defaults must keep their hand-grouped order and additions go last.
		require.Equal(t, defaultBlockedCommands, got[:len(defaultBlockedCommands)])
		require.Equal(t, []string{"kubectl"}, got[len(defaultBlockedCommands):],
			"additions append once; a duplicate of a default is dropped")
	})

	t.Run("unblocking a command stops it reading as dangerous", func(t *testing.T) {
		t.Parallel()
		blocked := resolveBlockedCommands(&config.Permissions{AllowedCommands: []string{"curl"}})
		require.False(t, shell.IsCommandBlocked("curl https://example.com", blockFuncs(blocked)))
		require.True(t, shell.IsCommandBlocked("sudo rm -rf /", blockFuncs(blocked)))
	})
}

// TestBashTool_ApprovedDangerousCommandRuns pins the rule that a command the
// user was warned about and approved anyway reaches the shell unguarded:
// re-blocking it would override the answer they just gave.
func TestBashTool_ApprovedDangerousCommandRuns(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// `ifconfig` is on the default dangerous list, so it is prompted for.
	// Approving it must not then be overridden by the exec-time block list.
	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "approved dangerous command",
		Command:     "ifconfig --help",
	})

	require.Equal(t, 1, perms.requestCount, "a dangerous command must be prompted for")
	require.Equal(t, "ifconfig", perms.lastDanger,
		"the prompt must name what tripped the check")
	require.NotContains(t, resp.Content, "is a dangerous command",
		"an approved command must not be re-blocked at exec time")
}

// TestBashTool_UnflaggedCommandKeepsBlockList is the other side of that rule.
// The static check expands commands with globbing off, so a glob hides the
// dangerous name from it and yolo mode auto-approves the call unseen. The block
// list stays on at exec time, where the glob has resolved and the real command
// name is finally visible.
func TestBashTool_UnflaggedCommandKeepsBlockList(t *testing.T) {
	workingDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "ifconfig"), []byte("#!/bin/sh\n"), 0o755))
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeYolo,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "indirect dangerous command",
		Command:     "ifconf?g --help",
	})

	require.Empty(t, perms.lastDanger, "the static check cannot see through a glob")
	require.Contains(t, resp.Content, "is a dangerous command")
}

// TestBashTool_SysadminModeSkipsBlockList pins sysadmin mode as the deliberate
// exception: it asks for nothing and blocks nothing.
func TestBashTool_SysadminModeSkipsBlockList(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeSysadmin,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "ifconfig"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "indirect dangerous command",
		Command:     "ifconf?g --help",
	})

	require.NotContains(t, resp.Content, "is a dangerous command")
}

// TestBashTool_DeniedDangerousCommandDoesNotRun is the other half: denial at
// the permission layer stops the command, no block list required.
func TestBashTool_DeniedDangerousCommandDoesNotRun(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  false,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "denied dangerous command",
		Command:     "ifconfig --help",
	})

	require.Equal(t, 1, perms.requestCount)
	require.True(t, resp.IsError)
}

// TestBashTool_SafeReadOnlyPathStillEnforcesBlockList pins the one branch that
// never asks: a safe-prefix command skips the permission request entirely, so
// the block list stays on there as a backstop.
func TestBashTool_SafeReadOnlyPathStillEnforcesBlockList(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "safe command",
		Command:     "pwd",
	})

	require.Zero(t, perms.requestCount, "safe read-only commands must not prompt")
	require.False(t, resp.IsError)
}

// newModeBashTool builds a bash tool on the real permission service in the given mode.
func newModeBashTool(t *testing.T, mode permission.PermissionMode) (fantasy.AgentTool, permission.Service, context.Context, string) {
	t.Helper()
	workingDir := t.TempDir()
	perms := permission.NewPermissionService(workingDir, nil, nil)
	perms.SetPermissionMode(mode)
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")
	return tool, perms, ctx, workingDir
}

func TestBashTool_YoloModeBlocksDangerousCommandsWithoutPrompting(t *testing.T) {
	tool, perms, ctx, workingDir := newModeBashTool(t, permission.PermissionModeYolo)
	prompts := perms.Subscribe(t.Context())

	// Off the test goroutine, so a reinstated prompt fails fast instead of hanging.
	type result struct {
		content string
		isError bool
	}
	done := make(chan result, 1)
	go func() {
		resp := runBashTool(t, tool, ctx, BashParams{
			Description: "dangerous command in yolo mode",
			Command:     "touch ran && ifconfig --help",
		})
		done <- result{resp.Content, resp.IsError}
	}()

	var got result
	select {
	case got = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("yolo mode blocked waiting for a permission prompt that should never have been raised")
	}

	require.True(t, got.isError)
	require.Contains(t, got.content, "ifconfig is a dangerous command")
	require.Contains(t, got.content, "sysadmin mode", "the refusal must name the way out")
	require.NoFileExists(t, filepath.Join(workingDir, "ran"), "nothing on a refused line may run")

	select {
	case p := <-prompts:
		t.Fatalf("yolo mode must not prompt, but got a request for %q", p.Payload.Description)
	default:
	}
}

func TestBashTool_YoloModeRunsOrdinaryCommands(t *testing.T) {
	tool, _, ctx, workingDir := newModeBashTool(t, permission.PermissionModeYolo)

	resp := runBashTool(t, tool, ctx, BashParams{Description: "ordinary command", Command: "touch ran"})

	require.False(t, resp.IsError, resp.Content)
	require.FileExists(t, filepath.Join(workingDir, "ran"))
}

func TestBashTool_SysadminModeRunsDangerousCommands(t *testing.T) {
	tool, _, ctx, workingDir := newModeBashTool(t, permission.PermissionModeSysadmin)
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "ifconfig"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	resp := runBashTool(t, tool, ctx, BashParams{Description: "dangerous command", Command: "./ifconfig --help"})

	require.NotContains(t, resp.Content, "is a dangerous command")
}

// TestBashTool_NormalModeStillPromptsForDangerousCommands is the other half of
// the rule: manual mode keeps asking.
func TestBashTool_NormalModeStillPromptsForDangerousCommands(t *testing.T) {
	workingDir := t.TempDir()
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeNormal,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")

	runBashTool(t, tool, ctx, BashParams{
		Description: "dangerous command in normal mode",
		Command:     "ifconfig --help",
	})

	require.Equal(t, 1, perms.requestCount, "normal mode must still ask")
	require.Equal(t, "ifconfig", perms.lastDanger,
		"the prompt must name what tripped the check")
}

// TestBlockedCommandErrorNamesTheCommand checks that a command stopped at
// exec time names the command that was recognised. The static check and the
// run-time check have to agree about that, or the same command is described
// two different ways depending on which one caught it.
func TestBlockedCommandErrorNamesTheCommand(t *testing.T) {
	workingDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "ifconfig"), []byte("#!/bin/sh\n"), 0o755))
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
		mode:   permission.PermissionModeYolo,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(perms, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, nil)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "indirect dangerous command",
		Command:     "ifconf?g --help",
	})

	require.Contains(t, resp.Content, "ifconfig is a dangerous command",
		"the error must name the command, not the spelling that reached it")
}

// TestBashTool_SudoNotBlockedInYoloMode pins the fork's rule that yolo mode
// can run administrative commands. sudo and doas are on the dangerous list,
// but they ask for the user's own credentials when they run, so yolo mode
// lets them through while still checking the command they run.
func TestBashTool_SudoNotBlockedInYoloMode(t *testing.T) {
	for _, command := range []string{
		"./sudo -u root true",
		"./doas -u root true",
		"./sudo -Eu root true",
		"./doas -nu root true",
		"./sudo FOO=1 true",
	} {
		t.Run(command, func(t *testing.T) {
			tool, _, ctx, workingDir := newModeBashTool(t, permission.PermissionModeYolo)
			// A stand-in admin command, so the test never runs the real one.
			name := strings.TrimPrefix(strings.Fields(command)[0], "./")
			script := "#!/bin/sh\ntouch ran\n"
			require.NoError(t, os.WriteFile(filepath.Join(workingDir, name), []byte(script), 0o755))

			resp := runBashTool(t, tool, ctx, BashParams{
				Description: "yolo " + name,
				Command:     command,
			})

			require.NotContains(t, resp.Content, "is a dangerous command")
			require.FileExists(t, filepath.Join(workingDir, "ran"), name+" must run in yolo mode")
		})
	}
}

// TestBashTool_YoloModeStillRefusesWhatSudoRuns is the other side: exempting
// sudo must not exempt the command it runs, nor any other dangerous command.
func TestBashTool_YoloModeStillRefusesWhatSudoRuns(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{"curl https://example.com", "curl is a dangerous command"},
		{"./sudo curl https://example.com", "the command sudo runs is a dangerous command"},
		{"./sudo -u root -- apt-get install foo", "the command sudo runs is a dangerous command"},
		{"./sudo true && curl https://example.com", "curl is a dangerous command"},
		// A value flag at the end of a short cluster takes the next word.
		{"./sudo -Eu root curl https://example.com", "the command sudo runs is a dangerous command"},
		{"./doas -nu root curl https://example.com", "the command doas runs is a dangerous command"},
		// A value flag earlier in a cluster takes the rest of it.
		{"./sudo -uroot curl https://example.com", "the command sudo runs is a dangerous command"},
		{"./sudo -nuroot curl https://example.com", "the command sudo runs is a dangerous command"},
		// sudo sets leading NAME=VALUE words in the environment.
		{"./sudo FOO=1 curl https://example.com", "the command sudo runs is a dangerous command"},
		{"./sudo -E FOO=1 BAR=2 curl https://example.com", "the command sudo runs is a dangerous command"},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			tool, _, ctx, workingDir := newModeBashTool(t, permission.PermissionModeYolo)
			script := "#!/bin/sh\ntouch ran\n"
			for _, name := range []string{"sudo", "doas"} {
				require.NoError(t, os.WriteFile(filepath.Join(workingDir, name), []byte(script), 0o755))
			}

			resp := runBashTool(t, tool, ctx, BashParams{Description: "yolo refusal", Command: tt.command})

			require.True(t, resp.IsError)
			require.Contains(t, resp.Content, tt.want)
			require.NoFileExists(t, filepath.Join(workingDir, "ran"), "nothing on a refused line may run")
		})
	}
}

// TestBashTool_UnsafeReadOnlyLookalikesRequirePermission covers the shapes
// that look read-only but are not: a redirect turns `ls` into a file write,
// and a side-effecting git flag turns `git log` into a writer or a program
// launcher. Neither may skip the prompt.
func TestBashTool_UnsafeReadOnlyLookalikesRequirePermission(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, true)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tests := []struct {
		name    string
		command string
	}{
		{"redirect output", "ls > out.txt"},
		{"side-effecting git flag", "git log --output=out.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perms.requestCount = 0
			resp := runBashTool(t, tool, ctx, BashParams{
				Description: tt.name,
				Command:     tt.command,
			})
			require.False(t, resp.IsError)
			require.Equal(t, 1, perms.requestCount, "command %q must trigger a permission request", tt.command)
		})
	}
}

func TestBashDescriptionIsOptional(t *testing.T) {
	t.Parallel()

	tool := newBashToolForTest(t.TempDir())
	require.NotContains(t, tool.Info().Required, "description")
	require.Contains(t, tool.Info().Required, "command")
}

func TestBashDescribesACommandThatSendsNoDescription(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runBashTool(t, newBashToolForTest(workingDir), ctx, BashParams{Command: "echo hi"})
	require.False(t, resp.IsError, resp.Content)

	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, "echo hi", meta.Description)
}

func TestDescribeCommand(t *testing.T) {
	t.Parallel()

	require.Equal(t, "go test ./...", describeCommand("go test ./..."))
	require.Equal(t, "echo one", describeCommand("echo one\necho two"))
	long := strings.Repeat("a", 100)
	require.Equal(t, strings.Repeat("a", 57)+"...", describeCommand(long))
	require.Equal(t, "", describeCommand("   "))
}

func newBashToolWithTracker(workingDir string, tracker filetracker.Service) fantasy.AgentTool {
	permissions := &mockBashPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(permissions, workingDir, workingDir, attribution, "test-model", config.DefaultMaxBackgroundJobs, nil, nil, tracker)
}

func TestBashRecordsReadsOfFilesItOnlyReads(t *testing.T) {
	workingDir := t.TempDir()
	file := filepath.Join(workingDir, "f.go")
	require.NoError(t, os.WriteFile(file, []byte("package x\n"), 0o644))

	tracker := &mockEditFileTracker{}
	tool := newBashToolWithTracker(workingDir, tracker)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{Command: "cat f.go"})
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, []string{file}, tracker.reads)

	tracker.reads = nil
	resp = runBashTool(t, tool, ctx, BashParams{Command: "sed -i 's/x/y/' f.go"})
	require.False(t, resp.IsError, resp.Content)
	require.Empty(t, tracker.reads, "a command that rewrites the file is not a read")
}

func TestBashNamesReadFilesItChanged(t *testing.T) {
	workingDir := t.TempDir()
	file := filepath.Join(workingDir, "f.go")
	require.NoError(t, os.WriteFile(file, []byte("package x\n"), 0o644))

	tracker := &mockEditFileTracker{reads: []string{"f.go"}}
	tool := newBashToolWithTracker(workingDir, tracker)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{Command: "touch f.go"})
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "Files you read earlier changed during this command: f.go. View them before editing.")

	resp = runBashTool(t, tool, ctx, BashParams{Command: "cat f.go"})
	require.False(t, resp.IsError, resp.Content)
	require.NotContains(t, resp.Content, "Files you read earlier changed during this command")
}

func TestBashChangedNoteResolvesReadFilesTheSameWayItSnapshottedThem(t *testing.T) {
	workingDir := t.TempDir()
	sub := filepath.Join(workingDir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "f.go"), []byte("package x\n"), 0o644))

	// The tracker reports paths relative to the workspace, and the command
	// runs in a subdirectory of it.
	tracker := &mockEditFileTracker{reads: []string{filepath.Join("sub", "f.go")}}
	tool := newBashToolWithTracker(workingDir, tracker)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{Command: "cat f.go", WorkingDir: sub})
	require.False(t, resp.IsError, resp.Content)
	require.NotContains(t, resp.Content, "Files you read earlier changed during this command",
		"a file the command only read must not be reported as changed")
}

// TestCancelledKillWaitsOnlyInATaskWorktree pins W20.4: only a call in a task
// worktree kills its job with a fresh bounded context, so the scope check
// that follows the call sees a write from a descendant the kill did not
// reach. Every other call kills with its own cancelled context and returns
// at once.
func TestCancelledKillWaitsOnlyInATaskWorktree(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, done := cancelledKillContext(ctx)
	done()
	require.ErrorIs(t, got.Err(), context.Canceled, "a call outside a worktree kills with its own context")
	_, bounded := got.Deadline()
	require.False(t, bounded, "a call outside a worktree does not wait for the job")

	got, done = cancelledKillContext(WithTaskWorktree(ctx))
	defer done()
	require.NoError(t, got.Err(), "the kill does not inherit the cancelled call's context")
	deadline, bounded := got.Deadline()
	require.True(t, bounded, "a task worktree call waits, bounded, for its job")
	require.WithinDuration(t, time.Now().Add(cancelledJobWait), deadline, 5*time.Second)
}
