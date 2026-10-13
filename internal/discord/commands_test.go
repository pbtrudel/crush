package discord

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/charmbracelet/crush/internal/commands"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/question"
	"github.com/stretchr/testify/require"
)

// newCommandBridge builds a launched-free bridge for command tests.
func newCommandBridge(t *testing.T) (*Bridge, *fakeEnv, *fakeAPI) {
	t.Helper()
	env := newFakeEnv()
	env.cfg.Options = &config.Options{DataDirectory: t.TempDir()}
	api := newFakeAPI()
	b := newBridge(env, nil, "sess-1", Config{Token: "t", ChannelIDs: []string{"chan-1"}}, "code", bridgeDeps{
		api:          api,
		catalog:      env.catalogFn,
		now:          time.Now,
		tick:         10 * time.Millisecond,
		editThrottle: time.Millisecond,
	})
	b.threadID = "thread-1"
	b.ctx = context.Background()
	b.cancel = func() {}
	return b, env, api
}

func TestParseCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		name string
		args string
		ok   bool
	}{
		{"", "", "", false},
		{"hello world", "", "", false},
		{"/help", "help", "", true},
		{"/model gpt-4o", "model", "gpt-4o", true},
		{"  /MODEL   gpt 4o  ", "model", "gpt 4o", true},
		{"/", "", "", false},
		{"//path/to/file", "/path/to/file", "", true},
	}
	for _, tt := range tests {
		name, args, ok := parseCommand(tt.in)
		require.Equal(t, tt.ok, ok, "input %q", tt.in)
		require.Equal(t, tt.name, name, "input %q", tt.in)
		require.Equal(t, tt.args, args, "input %q", tt.in)
	}
}

func TestCommandHelp(t *testing.T) {
	t.Parallel()
	b, _, _ := newCommandBridge(t)
	help := b.handleCommand(b.ctx, "help", "")
	require.Contains(t, help, "/model")
	require.Contains(t, help, "/yolo")
	require.Contains(t, help, "sent to the agent as a prompt")
}

func TestCommandStatus(t *testing.T) {
	t.Parallel()
	b, env, _ := newCommandBridge(t)
	env.setBusy(true, "queued prompt")

	status := b.handleCommand(b.ctx, "status", "")
	require.Contains(t, status, "Test Session")
	require.Contains(t, status, "openai/gpt-4o")
	require.Contains(t, status, "mode `code`")
	require.Contains(t, status, "queue 1")
	require.Contains(t, status, "1.2k")
}

func TestCommandModelToggles(t *testing.T) {
	t.Parallel()
	b, env, _ := newCommandBridge(t)

	reply := b.handleCommand(b.ctx, "model", "gpt-5")
	require.Contains(t, reply, "Model changed")
	require.Equal(t, []config.SelectedModel{{Provider: "openai", Model: "gpt-5"}}, env.preferred)
	require.Equal(t, 1, env.modelUpdate)

	// Exact provider/model form.
	reply = b.handleCommand(b.ctx, "model", "openai/gpt-4o")
	require.Contains(t, reply, "Model changed")
	require.Len(t, env.preferred, 2)

	// Ambiguous query lists candidates.
	reply = b.handleCommand(b.ctx, "model", "gpt")
	require.Contains(t, reply, "Multiple models match")
	require.Contains(t, reply, "openai/gpt-4o")
	require.Contains(t, reply, "openai/gpt-5")
	require.Len(t, env.preferred, 2, "ambiguous query must not switch")

	// Unconfigured providers are never offered.
	reply = b.handleCommand(b.ctx, "model", "claude")
	require.Contains(t, reply, "no configured model matches")

	// Unknown query.
	reply = b.handleCommand(b.ctx, "model", "zzz")
	require.Contains(t, reply, "no configured model matches")
}

func TestCommandModelsList(t *testing.T) {
	t.Parallel()
	b, _, _ := newCommandBridge(t)
	reply := b.handleCommand(b.ctx, "models", "")
	require.Contains(t, reply, "openai/gpt-4o")
	require.Contains(t, reply, "GPT-5")
	require.NotContains(t, reply, "claude", "unconfigured providers are hidden")

	reply = b.handleCommand(b.ctx, "models", "gpt-5")
	require.Contains(t, reply, "openai/gpt-5")
	require.NotContains(t, reply, "gpt-4o")
}

func TestCommandModeSwitch(t *testing.T) {
	t.Parallel()
	b, env, _ := newCommandBridge(t)

	require.Contains(t, b.handleCommand(b.ctx, "plan", ""), "Plan mode enabled")
	require.Equal(t, config.AgentPlan, env.agent)
	require.Equal(t, "plan", b.modeName())

	require.Contains(t, b.handleCommand(b.ctx, "code", ""), "Code mode enabled")
	require.Equal(t, config.AgentCoder, env.agent)
	require.Equal(t, "code", b.modeName())
}

func TestCommandYolo(t *testing.T) {
	t.Parallel()
	b, env, _ := newCommandBridge(t)

	require.Contains(t, b.handleCommand(b.ctx, "yolo", "on"), "enabled")
	require.True(t, env.yolo)

	require.Contains(t, b.handleCommand(b.ctx, "yolo", "off"), "disabled")
	require.False(t, env.yolo)

	// Bare /yolo toggles.
	require.Contains(t, b.handleCommand(b.ctx, "yolo", ""), "enabled")
	require.True(t, env.yolo)

	require.Contains(t, b.handleCommand(b.ctx, "yolo", "maybe"), "Usage")
}

func TestCommandStop(t *testing.T) {
	t.Parallel()
	b, env, _ := newCommandBridge(t)
	reply := b.handleCommand(b.ctx, "stop", "")
	require.Contains(t, reply, "Cancelling")
	require.Equal(t, 1, env.cancelCount())
}

func TestCommandRcOff(t *testing.T) {
	t.Parallel()
	b, _, api := newCommandBridge(t)
	require.Empty(t, b.handleCommand(b.ctx, "rc", "off"))
	require.True(t, api.hasPostContaining("Remote control ended"))
	require.Contains(t, b.handleCommand(b.ctx, "rc", ""), "Usage")
}

func TestCommandQueueTokensTitle(t *testing.T) {
	t.Parallel()
	b, env, _ := newCommandBridge(t)
	env.setBusy(true, "first prompt", "second prompt")

	reply := b.handleCommand(b.ctx, "queue", "")
	require.Contains(t, reply, "2")
	require.Contains(t, reply, "first prompt")

	require.Contains(t, b.handleCommand(b.ctx, "tokens", ""), "1.2k")
	require.Contains(t, b.handleCommand(b.ctx, "title", ""), "Test Session")
}

func TestCommandPermissionFlow(t *testing.T) {
	t.Parallel()
	b, env, _ := newCommandBridge(t)

	require.Contains(t, b.handleCommand(b.ctx, "deny", "bash"), "no permission requests are pending")

	b.permissions["p1"] = permission.PermissionRequest{ID: "p1", SessionID: "sess-1", ToolCallID: "tc1", ToolName: "bash"}
	b.permissionOrder = []string{"p1"}
	require.Contains(t, b.handleCommand(b.ctx, "allow", ""), "Approved")
	require.Equal(t, 1, env.grantCount())

	// The resolution notification clears the pending entry.
	b.handleEvent(pubsub.Event[tea.Msg]{Payload: pubsub.Event[permission.PermissionNotification]{
		Type: pubsub.CreatedEvent, Payload: permission.PermissionNotification{ToolCallID: "tc1", Granted: true},
	}})
	require.Contains(t, b.handleCommand(b.ctx, "deny", "bash"), "no permission requests are pending")

	// Re-seed and deny by tool name.
	b.permissions["p2"] = permission.PermissionRequest{ID: "p2", SessionID: "sess-1", ToolCallID: "tc2", ToolName: "write"}
	b.permissionOrder = append(b.permissionOrder, "p2")
	require.Contains(t, b.handleCommand(b.ctx, "deny", "write"), "Denied")
	require.Len(t, env.denies, 1)

	// Newest is the default target.
	b.permissions["p3"] = permission.PermissionRequest{ID: "p3", SessionID: "sess-1", ToolCallID: "tc3", ToolName: "bash"}
	b.permissionOrder = append(b.permissionOrder, "p3")
	require.Contains(t, b.handleCommand(b.ctx, "allow-session", ""), "session")
	require.Len(t, env.grantSessions, 1)
}

func TestCommandAnswer(t *testing.T) {
	t.Parallel()
	b, env, _ := newCommandBridge(t)

	require.Contains(t, b.handleCommand(b.ctx, "answer", "yes"), "No question is pending")

	b.questions["q1"] = question.Request{
		ID:        "q1",
		SessionID: "sess-1",
		Questions: []question.Question{{ID: "qq1", Type: question.TypeYesNo, Text: "Continue?"}},
	}
	b.questionOrder = []string{"q1"}
	require.Contains(t, b.handleCommand(b.ctx, "answer", "yes"), "Answer sent")
	require.NotNil(t, env.answers[0].Yes)
	require.True(t, *env.answers[0].Yes)

	// Multi-question forms stay in the TUI.
	b.questions["q2"] = question.Request{
		ID:        "q2",
		SessionID: "sess-1",
		Questions: []question.Question{
			{ID: "a", Type: question.TypeFreeText, Text: "one"},
			{ID: "b", Type: question.TypeFreeText, Text: "two"},
		},
	}
	b.questionOrder = append(b.questionOrder, "q2")
	require.Contains(t, b.handleCommand(b.ctx, "answer", "x"), "multiple parts")
}

func TestAnswerForQuestion(t *testing.T) {
	t.Parallel()
	choice := question.Question{
		ID:      "q",
		Type:    question.TypeSingleChoice,
		Choices: []question.Choice{{ID: "c1", Label: "One"}, {ID: "c2", Label: "Two"}},
	}
	answer, err := answerForQuestion(choice, "#2")
	require.NoError(t, err)
	require.Equal(t, []string{"c2"}, answer.SelectedIDs)

	_, err = answerForQuestion(choice, "#9")
	require.ErrorContains(t, err, "out of range")

	answer, err = answerForQuestion(choice, "some text")
	require.NoError(t, err)
	require.Equal(t, "some text", answer.FillInText)

	multi := question.Question{
		ID:      "m",
		Type:    question.TypeMultiChoice,
		Choices: []question.Choice{{ID: "a"}, {ID: "b"}, {ID: "c"}},
	}
	answer, err = answerForQuestion(multi, "1, 3")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "c"}, answer.SelectedIDs)

	_, err = answerForQuestion(question.Question{ID: "y", Type: question.TypeYesNo}, "banana")
	require.ErrorContains(t, err, "yes")
}

func TestExpandCustomCommand(t *testing.T) {
	t.Parallel()
	cmd := commands.CustomCommand{
		Name:    "deploy",
		Content: "Deploy $TARGET with $FLAGS please",
		Arguments: []commands.Argument{
			{ID: "TARGET"}, {ID: "FLAGS"},
		},
	}

	out, err := expandCustomCommand(cmd, "prod --dry-run")
	require.NoError(t, err)
	require.Equal(t, "Deploy prod with --dry-run please", out)

	// The last declared argument absorbs the remainder.
	out, err = expandCustomCommand(cmd, "prod --dry run")
	require.NoError(t, err)
	require.Contains(t, out, "with --dry run")

	// name=value overrides positionally.
	out, err = expandCustomCommand(cmd, "FLAGS=fast prod")
	require.NoError(t, err)
	require.Equal(t, "Deploy prod with fast please", out)

	_, err = expandCustomCommand(cmd, "prod")
	require.ErrorContains(t, err, "$FLAGS")

	noArgs := commands.CustomCommand{Name: "ping", Content: "pong"}
	out, err = expandCustomCommand(noArgs, "extra")
	require.NoError(t, err)
	require.Contains(t, out, "pong")
	require.Contains(t, out, "extra")
}

func TestCustomCommandMatches(t *testing.T) {
	t.Parallel()
	cmd := commands.CustomCommand{ID: "user:deploy", Name: "user:deploy"}
	require.True(t, customCommandMatches(cmd, "deploy"))
	require.True(t, customCommandMatches(cmd, "user:deploy"))
	require.False(t, customCommandMatches(cmd, "other"))
}

func TestCommandUnknown(t *testing.T) {
	t.Parallel()
	b, env, _ := newCommandBridge(t)
	reply := b.handleCommand(b.ctx, "frobnicate", "")
	require.Contains(t, reply, "Unknown command")
	require.Contains(t, reply, "escape")
	require.Zero(t, env.runCount(), "unknown commands must not reach the agent")
}

func TestResolveModelQuery(t *testing.T) {
	t.Parallel()
	providers := []catwalk.Provider{
		{
			ID: catwalk.InferenceProvider("openai"),
			Models: []catwalk.Model{
				{ID: "gpt-4o", Name: "GPT-4o"},
				{ID: "gpt-5", Name: "GPT-5"},
			},
		},
		{
			ID: catwalk.InferenceProvider("anthropic"),
			Models: []catwalk.Model{
				{ID: "claude-4", Name: "Claude 4"},
			},
		},
	}
	configured := func(id string) bool { return id == "openai" }

	sel, candidates, err := resolveModelQuery(providers, "gpt-5", configured)
	require.NoError(t, err)
	require.Empty(t, candidates)
	require.Equal(t, config.SelectedModel{Provider: "openai", Model: "gpt-5"}, sel)

	sel, _, err = resolveModelQuery(providers, "GPT-5", configured)
	require.NoError(t, err)
	require.Equal(t, "gpt-5", sel.Model)

	_, candidates, err = resolveModelQuery(providers, "gpt", configured)
	require.NoError(t, err)
	require.Len(t, candidates, 2)

	_, _, err = resolveModelQuery(providers, "claude", configured)
	require.Error(t, err)

	sel, _, err = resolveModelQuery(providers, "anchor-name", func(string) bool { return true })
	require.Error(t, err, "unknown substrings must not resolve")
	require.Equal(t, config.SelectedModel{}, sel)
}
