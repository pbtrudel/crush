package claudeagent

import (
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// imageMessage builds a user message carrying an image, the shape a typed
// prompt with an attachment has.
func imageMessage() fantasy.Message {
	return fantasy.Message{
		Role: fantasy.MessageRoleUser,
		Content: []fantasy.MessagePart{
			fantasy.FilePart{Filename: "shot.png", MediaType: "image/png", Data: []byte("png")},
		},
	}
}

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
		user    fantasy.Prompt
	}{
		{
			name:    "a reminder alone",
			trail:   fantasy.Prompt{injectionMessage("<system_reminder>Update the list.</system_reminder>")},
			harness: []string{"<system_reminder>Update the list.</system_reminder>"},
		},
		{
			name:  "a message the user typed alone",
			trail: fantasy.Prompt{injectionMessage("and also fix the tests")},
			user:  fantasy.Prompt{injectionMessage("and also fix the tests")},
		},
		{
			name: "a reminder and then a message",
			trail: fantasy.Prompt{
				injectionMessage("<system_reminder>Update the list.</system_reminder>"),
				injectionMessage("and also fix the tests"),
			},
			harness: []string{"<system_reminder>Update the list.</system_reminder>"},
			user:    fantasy.Prompt{injectionMessage("and also fix the tests")},
		},
		{
			name: "the gate and a message",
			trail: fantasy.Prompt{
				injectionMessage("<turn_end_gate>\nCheck the list.\n</turn_end_gate>"),
				injectionMessage("carry on"),
			},
			harness: []string{"<turn_end_gate>\nCheck the list.\n</turn_end_gate>"},
			user:    fantasy.Prompt{injectionMessage("carry on")},
		},
		{
			name:  "a tag that is not the whole message",
			trail: fantasy.Prompt{injectionMessage("<system_reminder>Check the list.</system_reminder> and also fix the tests")},
			user:  fantasy.Prompt{injectionMessage("<system_reminder>Check the list.</system_reminder> and also fix the tests")},
		},
		{
			name:    "a reminder with whitespace after its close tag",
			trail:   fantasy.Prompt{injectionMessage("\n<system_reminder>Check the list.</system_reminder>\n")},
			harness: []string{"<system_reminder>Check the list.</system_reminder>"},
		},
		{
			name:  "a message with an image stays the user's",
			trail: fantasy.Prompt{imageMessage()},
			user:  fantasy.Prompt{imageMessage()},
		},
		{
			name: "a typed message before the reminder",
			trail: fantasy.Prompt{
				injectionMessage("and also fix the tests"),
				injectionMessage("<system_reminder>Update the list.</system_reminder>"),
			},
			harness: []string{"<system_reminder>Update the list.</system_reminder>"},
			user:    fantasy.Prompt{injectionMessage("and also fix the tests")},
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
			require.Equal(t, tt.user, user)
		})
	}
}

// resultBlocks returns the text blocks of a tool result, in order.
func resultBlocks(result toolResult) []string {
	var texts []string
	for _, block := range result.content {
		texts = append(texts, block.Text)
	}
	return texts
}

// Attaching a reminder to a failed result keeps the failure, so the model
// still reads the tool as having failed.
func TestAttachInjectionKeepsAnErrorResult(t *testing.T) {
	t.Parallel()

	result := attachInjection(mcpContent("boom", "", "", true),
		"<system_reminder>Update the list.</system_reminder>")

	require.True(t, result.isError)
	require.Equal(t, []string{
		"boom",
		"\n\n<system_reminder>Update the list.</system_reminder>",
	}, resultBlocks(result))
}

// A result carrying an image keeps it first and the reminder last, so the
// media block reaches the model as media.
func TestAttachInjectionKeepsMedia(t *testing.T) {
	t.Parallel()

	result := attachInjection(mcpContent("a screenshot", "cG5n", "image/png", false),
		"<system_reminder>Update the list.</system_reminder>")

	require.False(t, result.isError)
	require.Len(t, result.content, 3)
	require.Equal(t, "image", result.content[0].Type)
	require.Equal(t, "cG5n", result.content[0].Data)
	require.Equal(t, "a screenshot", result.content[1].Text)
	require.Contains(t, result.content[2].Text, "Update the list.")
}

// The reminder rides the last result of a continuation, which is the one the
// model reads last, whatever the earlier results carried.
func TestAttachToLastResult(t *testing.T) {
	t.Parallel()

	pairs := []toolResultPair{
		{id: "call-1", result: mcpContent("first", "", "", false)},
		{id: "call-2", result: mcpContent("second", "", "", false)},
	}
	attachToLastResult(pairs, []string{"<system_reminder>Check the list.</system_reminder>"})

	require.Equal(t, []string{"first"}, resultBlocks(pairs[0].result))
	require.Equal(t, []string{
		"second",
		"\n\n<system_reminder>Check the list.</system_reminder>",
	}, resultBlocks(pairs[1].result))
}

// Two reminders are separated by a blank line, so the model does not read them
// as one row.
func TestAttachToLastResultJoinsTwoReminders(t *testing.T) {
	t.Parallel()

	pairs := []toolResultPair{{id: "call-1", result: mcpContent("result", "", "", false)}}
	attachToLastResult(pairs, []string{
		"<system_reminder>Check the list.</system_reminder>",
		"<system_reminder>Update the list.</system_reminder>",
	})

	require.Equal(t, []string{
		"result",
		"\n\n<system_reminder>Check the list.</system_reminder>\n\n" +
			"<system_reminder>Update the list.</system_reminder>",
	}, resultBlocks(pairs[0].result))
}
