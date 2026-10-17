package model

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/styles"
)

const testSecret = "AKIAIOSFODNN7EXAMPLE"

func secretTestUserItem(sty *styles.Styles, text string) *chat.UserMessageItem {
	msg := &message.Message{
		ID:    "user-1",
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: text}},
	}
	return chat.NewUserMessageItem(sty, msg, nil).(*chat.UserMessageItem)
}

// TestChatRevealSecretsToggle verifies toggling reveal flips every
// already-loaded user message in the chat list.
func TestChatRevealSecretsToggle(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetMessages(secretTestUserItem(u.com.Styles, "key "+testSecret+" end"))

	item, ok := u.chat.list.ItemAt(0).(*chat.UserMessageItem)
	require.True(t, ok, "expected a user message item in the list")

	require.NotContains(t, ansi.Strip(item.Render(100)), testSecret, "masked by default")
	require.False(t, u.chat.RevealSecrets())

	u.chat.SetRevealSecrets(true)
	require.Contains(t, ansi.Strip(item.Render(100)), testSecret, "revealed after toggle")
	require.True(t, u.chat.RevealSecrets())

	u.chat.SetRevealSecrets(false)
	require.NotContains(t, ansi.Strip(item.Render(100)), testSecret, "masked again after toggling back")
}

// TestChatRevealSecretsAppliesToNewItems verifies items loaded or
// appended while reveal is enabled start out revealed, so the list never
// mixes states.
func TestChatRevealSecretsAppliesToNewItems(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.chat.SetRevealSecrets(true)

	loaded := secretTestUserItem(u.com.Styles, "key "+testSecret+" end")
	u.chat.SetMessages(loaded)
	require.Contains(t, ansi.Strip(loaded.Render(100)), testSecret, "loaded item must inherit reveal")

	appended := secretTestUserItem(u.com.Styles, "key "+testSecret+" end")
	u.chat.AppendMessages(appended)
	require.Contains(t, ansi.Strip(appended.Render(100)), testSecret, "appended item must inherit reveal")
}
