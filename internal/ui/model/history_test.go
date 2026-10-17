package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

type historyWorkspace struct {
	workspace.Workspace
}

func (historyWorkspace) Config() *config.Config {
	return &config.Config{}
}

func (historyWorkspace) PermissionSkipRequests() bool {
	return false
}

func TestHistoryBangCommandStripsPrefixWhileAlreadyInBangMode(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.com.Workspace = historyWorkspace{}
	u.promptHistory.messages = []string{"!echo one", "!echo two"}
	u.promptHistory.index = -1

	require.True(t, u.historyPrev())
	require.True(t, u.bangMode)
	require.Equal(t, "echo one", u.textarea.Value())

	require.True(t, u.historyPrev())
	require.True(t, u.bangMode)
	require.Equal(t, "echo two", u.textarea.Value())
}

// TestHistoryEntriesSkipsHiddenMessages pins that generated or synthetic
// user messages (the per-session environment snapshot, hidden
// continuations) never become recallable prompt history: up-arrow must not
// paste the raw snapshot blob into the composer.
func TestHistoryEntriesSkipsHiddenMessages(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "first prompt"}}},
		{Role: message.User, Parts: []message.ContentPart{message.TextContent{
			Text:   "<env_snapshot>\nToday's date: 1/1/2025\n</env_snapshot>",
			Hidden: true,
		}}},
		{Role: message.User, Parts: []message.ContentPart{message.ShellCommand{Command: "echo hi", Output: "hi", ExitCode: 0}}},
	}

	require.Equal(t, []string{"first prompt", "!echo hi"}, historyEntries(msgs),
		"hidden messages must not be recallable in prompt history")
}
