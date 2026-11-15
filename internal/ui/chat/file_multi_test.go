package chat

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// A multi-file view call has no single path and no response metadata, so it
// must still render as an ordinary finished call: first path, count, body.
func TestViewToolRendersMultiFileCall(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	content := "==> /tmp/a.go <==\n<file>\n     1|package a\n</file>\n\n" +
		"==> /tmp/b.go <==\n<file>\n     1|package b\n</file>\n"

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "several files",
			input: `{"file_paths":["/tmp/a.go","/tmp/b.go","/tmp/c.go"]}`,
			want:  "a.go +2 more",
		},
		{
			name:  "one path in file_paths",
			input: `{"file_paths":["/tmp/a.go"]}`,
			want:  "a.go",
		},
		{
			name:  "single file_path is unchanged",
			input: `{"file_path":"/tmp/a.go"}`,
			want:  "a.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			item := NewViewToolMessageItem(&sty, message.ToolCall{
				ID:       "tc1",
				Name:     "view",
				Input:    tt.input,
				Finished: true,
			}, &message.ToolResult{
				ToolCallID: "tc1",
				Name:       "view",
				Content:    content,
			}, false)

			out := ansi.Strip(item.Render(80))
			require.Contains(t, out, tt.want)
			require.NotContains(t, out, "Invalid parameters")
			require.Contains(t, out, "package a")
		})
	}
}
