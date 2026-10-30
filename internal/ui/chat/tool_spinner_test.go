package chat

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/stretchr/testify/require"
)

// A tool call with a result is not still running, whether or not it was ever
// marked finished.
func TestToolItemStopsSpinningOnceItHasAResult(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	result := &message.ToolResult{
		ToolCallID: "tc1",
		Name:       "write",
		Content:    "the model ran out of output tokens",
		IsError:    true,
	}

	tests := []struct {
		name     string
		toolCall message.ToolCall
		result   *message.ToolResult
		want     bool
	}{
		{
			name:     "running call with no result spins",
			toolCall: message.ToolCall{ID: "tc1", Name: "write", Input: "{}", Finished: false},
			want:     true,
		},
		{
			name:     "unfinished call with a result does not spin",
			toolCall: message.ToolCall{ID: "tc1", Name: "write", Input: "{}", Finished: false},
			result:   result,
			want:     false,
		},
		{
			name:     "finished call with a result does not spin",
			toolCall: message.ToolCall{ID: "tc1", Name: "write", Input: "{}", Finished: true},
			result:   result,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			item := NewToolMessageItem(&sty, "msg", tt.toolCall, tt.result, false, "")
			spinner, ok := item.(interface{ isSpinning() bool })
			require.True(t, ok, "tool items must expose their spinning state")
			require.Equal(t, tt.want, spinner.isSpinning())
		})
	}
}

func TestPendingToolsShowPartialArguments(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("bash command", func(t *testing.T) {
		t.Parallel()
		item := NewBashToolMessageItem(&sty, message.ToolCall{
			ID: "tc1", Name: "bash", Input: `{"command":"echo hel`, Finished: false,
		}, nil, false, "")
		require.Contains(t, item.Render(80), "echo hel")
	})

	t.Run("view path", func(t *testing.T) {
		t.Parallel()
		item := NewViewToolMessageItem(&sty, message.ToolCall{
			ID: "tc2", Name: "view", Input: `{"file_path":"/tmp/par`, Finished: false,
		}, nil, false)
		require.Contains(t, item.Render(80), "/tmp/par")
	})

	t.Run("edit path", func(t *testing.T) {
		t.Parallel()
		item := NewEditToolMessageItem(&sty, message.ToolCall{
			ID: "tc3", Name: "edit", Input: `{"file_path":"/tmp/edi`, Finished: false,
		}, nil, false)
		require.Contains(t, item.Render(80), "/tmp/edi")
	})
}
