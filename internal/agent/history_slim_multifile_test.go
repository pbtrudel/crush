package agent

import (
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestColdCutIgnoresMissingTimestamps(t *testing.T) {
	t.Parallel()

	t.Run("missing predecessor is not an idle gap", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(0, "old"), assistantAt(0, "answer"), userAt(10_000, "new")}
		require.Zero(t, coldCut(msgs, 0, testIdle))
	})

	t.Run("missing current message is not an idle gap", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(100, "old"), assistantAt(200, "answer"), userAt(0, "new")}
		require.Zero(t, coldCut(msgs, 0, testIdle))
	})

	t.Run("missing timestamp before the next prompt is not an idle gap", func(t *testing.T) {
		t.Parallel()
		msgs := []message.Message{userAt(0, "old"), assistantAt(0, "answer")}
		require.Zero(t, coldCut(msgs, 100_000, testIdle))
	})
}

func TestMultiFileHeader(t *testing.T) {
	t.Parallel()

	path, ok := multiFileHeader("==> internal/a.go <==")
	require.True(t, ok)
	require.Equal(t, "internal/a.go", path)

	_, ok = multiFileHeader("not a header")
	require.False(t, ok)
	_, ok = multiFileHeader("==> missing terminator")
	require.False(t, ok)
}

func TestStubStaleReadsMultiFile(t *testing.T) {
	t.Parallel()

	combined := "==> a.go <==\n" + strings.Repeat("a", 500) + "\n\n==> b.go <==\n" + strings.Repeat("b", 500)
	msgs := []message.Message{
		callAt(100, "read-1", "view", `{"file_paths":["a.go","b.go"]}`),
		resultAt(101, "read-1", "view", combined),
		callAt(200, "edit-1", "edit", `{"file_path":"a.go","old_string":"a","new_string":"c"}`),
		resultAt(201, "edit-1", "edit", "ok"),
	}

	stubStaleReads(msgs)

	got := msgs[1].ToolResults()[0].Content
	require.Contains(t, got, "==> a.go <==")
	require.Contains(t, got, "==> b.go <==")
	require.Contains(t, got, "[Earlier contents of a.go omitted")
	require.NotContains(t, got, strings.Repeat("a", 500), "the stale file's contents must be gone")
	require.Contains(t, got, strings.Repeat("b", 500), "the fresh file's contents must survive")
}

func TestStubStaleReadsMultiFileEditOfOtherFileKeepsEverything(t *testing.T) {
	t.Parallel()

	combined := "==> a.go <==\n" + strings.Repeat("a", 300) + "\n\n==> b.go <==\n" + strings.Repeat("b", 300)
	msgs := []message.Message{
		callAt(100, "read-1", "view", `{"file_paths":["a.go","b.go"]}`),
		resultAt(101, "read-1", "view", combined),
		callAt(200, "edit-1", "edit", `{"file_path":"other.go","old_string":"x","new_string":"y"}`),
		resultAt(201, "edit-1", "edit", "ok"),
	}

	stubStaleReads(msgs)
	require.Equal(t, combined, msgs[1].ToolResults()[0].Content)
}
