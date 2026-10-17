package chat

import (
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

func userMessage(parts ...message.ContentPart) *message.Message {
	return &message.Message{
		ID:    "user-1",
		Role:  message.User,
		Parts: parts,
	}
}

// TestUserMessageItemMasksSecrets pins the display half of secret
// protection: pasted credentials render masked in the transcript.
func TestUserMessageItemMasksSecrets(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz012345"
	msg := userMessage(message.TextContent{Text: "my key is " + secret + " ok"})

	item := NewUserMessageItem(&sty, msg, nil)
	rendered := ansi.Strip(item.Render(100))

	require.Contains(t, rendered, "my key is")
	require.Contains(t, rendered, "sk-p…")
	require.NotContains(t, rendered, secret)
}

// TestUserMessageItemWithoutSecretUnchanged verifies plain messages are
// rendered as-is.
func TestUserMessageItemWithoutSecretUnchanged(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	msg := userMessage(message.TextContent{Text: "no credentials here"})

	item := NewUserMessageItem(&sty, msg, nil)
	require.Contains(t, ansi.Strip(item.Render(100)), "no credentials here")
}

// TestUserMessageItemRevealSecrets verifies the reveal toggle flips
// between masked and original text in both directions.
func TestUserMessageItemRevealSecrets(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	secret := "AKIAIOSFODNN7EXAMPLE"
	msg := userMessage(message.TextContent{Text: "key " + secret + " end"})

	item := NewUserMessageItem(&sty, msg, nil).(*UserMessageItem)

	require.NotContains(t, ansi.Strip(item.Render(100)), secret, "masked by default")

	item.SetRevealSecrets(true)
	require.Contains(t, ansi.Strip(item.Render(100)), secret, "revealed after toggle")

	item.SetRevealSecrets(false)
	require.NotContains(t, ansi.Strip(item.Render(100)), secret, "masked again after toggling back")
}
