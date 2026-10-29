package prompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBudgetContextFiles(t *testing.T) {
	t.Parallel()

	t.Run("within budget is unchanged", func(t *testing.T) {
		t.Parallel()
		files := []ContextFile{{Path: "AGENTS.md", Content: "hello"}}
		got, budget := budgetContextFiles(files, 100)
		require.Equal(t, files, got)
		require.Nil(t, budget)
	})

	t.Run("zero disables the budget", func(t *testing.T) {
		t.Parallel()
		files := []ContextFile{{Path: "AGENTS.md", Content: strings.Repeat("x", 1000)}}
		got, budget := budgetContextFiles(files, 0)
		require.Equal(t, files, got)
		require.Nil(t, budget)
	})

	t.Run("drops broader files before the canonical instruction", func(t *testing.T) {
		t.Parallel()
		files := []ContextFile{
			{Path: ".cursorrules", Content: strings.Repeat("a", 200)},
			{Path: "AGENTS.md", Content: strings.Repeat("b", 200)},
		}
		got, budget := budgetContextFiles(files, 300)
		require.Len(t, got, 1)
		require.Equal(t, "AGENTS.md", got[0].Path)
		require.Equal(t, files[1], got[0])
		require.NotNil(t, budget)
		require.Equal(t, []string{".cursorrules"}, budget.Omitted)
		require.Empty(t, budget.Truncated)
	})

	t.Run("truncates the most specific remaining file", func(t *testing.T) {
		t.Parallel()
		files := []ContextFile{
			{Path: "AGENTS.md", Content: strings.Repeat("a", 500)},
		}
		got, budget := budgetContextFiles(files, 200)
		require.Len(t, got, 1)
		require.LessOrEqual(t, len(got[0].Content), 200)
		require.Contains(t, got[0].Content, "context truncated")
		require.NotNil(t, budget)
		require.Equal(t, []string{"AGENTS.md"}, budget.Truncated)
	})

	t.Run("prefers deeper files", func(t *testing.T) {
		t.Parallel()
		files := []ContextFile{
			{Path: "AGENTS.md", Content: strings.Repeat("a", 100)},
			{Path: "internal/AGENTS.md", Content: strings.Repeat("b", 100)},
		}
		got, budget := budgetContextFiles(files, 150)
		require.Len(t, got, 1)
		require.Equal(t, "internal/AGENTS.md", got[0].Path)
		require.Equal(t, []string{"AGENTS.md"}, budget.Omitted)
	})
}

func TestTruncateContextContent(t *testing.T) {
	t.Parallel()

	t.Run("short content unchanged", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "hello", truncateContextContent("hello", 10))
	})

	t.Run("never splits UTF-8", func(t *testing.T) {
		t.Parallel()
		content := strings.Repeat("你好世界", 100)
		got := truncateContextContent(content, 101)
		require.LessOrEqual(t, len(got), 101)
		require.True(t, strings.Contains(got, "context truncated"))
	})

	t.Run("zero returns empty", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, truncateContextContent("content", 0))
	})
}
