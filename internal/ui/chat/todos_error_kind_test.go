package chat

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// A failed todos call carries no todo metadata, only stamps such as its
// error kind, so it renders the same with or without them.
func TestTodosErrorRendersWithoutItsMetadata(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	call := message.ToolCall{
		ID:       "1",
		Name:     "todos",
		Input:    `{"todos":[{"content":"Write tests","status":"completed","active_form":"Writing tests"},{"content":"Ship","status":"in_progress","active_form":"Shipping"}]}`,
		Finished: true,
	}
	render := func(metadata string) string {
		result := &message.ToolResult{ToolCallID: "1", Content: "Error: user cancelled assistant tool calling", IsError: true, Metadata: metadata}
		return NewTodosToolMessageItem(&sty, call, result, false).Render(100)
	}

	require.Equal(t, render(""), render(`{"error_kind":"interrupted"}`))
}
