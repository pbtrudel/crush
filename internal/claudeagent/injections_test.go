package claudeagent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// injectionMessage builds a user message of text, the shape Crush writes
// for both a prompt and a harness reminder.
func injectionMessage(text string) fantasy.Message {
	return fantasy.Message{
		Role:    fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{fantasy.TextPart{Text: text}},
	}
}

// A harness reminder that trails the tool results is separated from what the
// user typed, because the CLI reports every steered message as the user's.
func TestTrailingInjections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		trail   fantasy.Prompt
		harness []string
		user    []string
	}{
		{
			name:    "a reminder alone",
			trail:   fantasy.Prompt{injectionMessage("<system_reminder>Update the list.</system_reminder>")},
			harness: []string{"<system_reminder>Update the list.</system_reminder>"},
		},
		{
			name:  "a message the user typed alone",
			trail: fantasy.Prompt{injectionMessage("and also fix the tests")},
			user:  []string{"and also fix the tests"},
		},
		{
			name: "a reminder and then a message",
			trail: fantasy.Prompt{
				injectionMessage("<system_reminder>Update the list.</system_reminder>"),
				injectionMessage("and also fix the tests"),
			},
			harness: []string{"<system_reminder>Update the list.</system_reminder>"},
			user:    []string{"and also fix the tests"},
		},
		{
			name: "the gate and a message",
			trail: fantasy.Prompt{
				injectionMessage("<turn_end_gate>\nCheck the list.\n</turn_end_gate>"),
				injectionMessage("carry on"),
			},
			harness: []string{"<turn_end_gate>\nCheck the list.\n</turn_end_gate>"},
			user:    []string{"carry on"},
		},
		{
			name:  "nothing trailing",
			trail: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			harness, user := trailingInjections(tt.trail)
			require.Equal(t, tt.harness, harness)
			require.Len(t, user, len(tt.user))
			for i, want := range tt.user {
				require.Equal(t, want, user[i].Content[0].(fantasy.TextPart).Text)
			}
		})
	}
}
