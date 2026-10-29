package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTruncateForChars(t *testing.T) {
	t.Parallel()

	t.Run("within budget unchanged", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "hello", TruncateForChars("hello", 10, ""))
	})

	t.Run("keeps head and tail with a spill path", func(t *testing.T) {
		t.Parallel()
		content := strings.Repeat("a", 2000) + "TAIL"
		got := TruncateForChars(content, 500, t.TempDir())
		require.LessOrEqual(t, len(got), 500)
		require.Contains(t, got, "tool output truncated")
		require.Contains(t, got, "full output:")
		require.True(t, strings.HasSuffix(got, "TAIL"))
	})

	t.Run("no spill dir still truncates", func(t *testing.T) {
		t.Parallel()
		got := TruncateForChars(strings.Repeat("x", 1000), 200, "")
		require.LessOrEqual(t, len(got), 200)
		require.Contains(t, got, "tool output truncated")
		require.NotContains(t, got, "full output:")
	})

	t.Run("never splits UTF-8", func(t *testing.T) {
		t.Parallel()
		got := TruncateForChars(strings.Repeat("你好世界", 500), 301, "")
		require.LessOrEqual(t, len(got), 301)
		require.True(t, strings.Contains(got, "tool output truncated"))
	})

	t.Run("zero disables", func(t *testing.T) {
		t.Parallel()
		content := strings.Repeat("x", 1000)
		require.Equal(t, content, TruncateForChars(content, 0, ""))
	})
}

func TestHasSpillNotice(t *testing.T) {
	t.Parallel()
	require.True(t, HasSpillNotice("x [... tool output truncated: 5 bytes omitted ...] y"))
	require.False(t, HasSpillNotice("plain output"))
}
