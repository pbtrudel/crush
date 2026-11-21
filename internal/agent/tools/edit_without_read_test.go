package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// todayNotRead is the refusal an unread file gets with the option off, as
// the model reads it today.
const todayNotRead = "you must read the file before editing it. Use the View tool first"

// unreadEditContext builds an edit context whose tracker reports lastRead
// for every file: the zero time means the file was never read in the
// session. It returns the tracker so a test can assert the read record.
func unreadEditContext(t *testing.T, dir string, lastRead time.Time, withoutRead bool) (editContext, *mockEditFileTracker) {
	t.Helper()
	tracker := &mockEditFileTracker{lastRead: lastRead}
	return editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: tracker,
		workingDir:  dir,
		withoutRead: withoutRead,
	}, tracker
}

// writeFile writes content into a new file under dir and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	filePath := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(filePath, []byte(content), 0o644))
	return filePath
}

// An exact, unique match applies to a file the guard would refuse, and the
// model is told the file was not read first. The edit must not record the
// file as read, because the model never read it.
func TestEditWithoutReadAppliesAnExactUniqueMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lastRead time.Time
	}{
		{name: "never read"},
		{name: "changed since read", lastRead: time.Now().Add(-time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := writeFile(t, dir, "a.txt", "alpha\nbeta\n")
			edit, tracker := unreadEditContext(t, dir, tt.lastRead, true)

			resp, err := replaceContent(edit, filePath, "beta", "BETA", false, fantasy.ToolCall{ID: "call"})
			require.NoError(t, err)
			require.False(t, resp.IsError, resp.Content)

			content, err := os.ReadFile(filePath)
			require.NoError(t, err)
			require.Equal(t, "alpha\nBETA\n", string(content))
			require.True(t, gjson.Get(resp.Metadata, "unread_edit").Bool(), "the edit is counted as made without a read")
			require.Zero(t, tracker.recorded, "an unread edit must not claim a read")
			require.Contains(t, resp.Content, "Content replaced in file: "+filePath)
			require.Contains(t, resp.Content, "had not been read in this session, or changed since it was read")
		})
	}
}

// A match the guard cannot prove unique and exact keeps today's refusal,
// and the refusal says why the option did not apply.
func TestEditWithoutReadRefusesTheRest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		old        string
		replaceAll bool
	}{
		{name: "replace_all", content: "alpha\n", old: "alpha", replaceAll: true},
		{name: "a whitespace-only match", content: "alpha\nbeta\n", old: "alpha\n  beta\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := writeFile(t, dir, "a.txt", tt.content)
			edit, tracker := unreadEditContext(t, dir, time.Time{}, true)

			resp, err := replaceContent(edit, filePath, tt.old, "X\n", tt.replaceAll, fantasy.ToolCall{ID: "call"})
			require.NoError(t, err)
			require.True(t, resp.IsError)
			require.Contains(t, resp.Content, todayNotRead)
			require.Contains(t, resp.Content, "Reading the file first is still recommended")
			require.Equal(t, string(ErrorKindNotRead), gjson.Get(resp.Metadata, "error_kind").String())
			require.False(t, gjson.Get(resp.Metadata, "unread_edit").Exists())

			content, err := os.ReadFile(filePath)
			require.NoError(t, err)
			require.Equal(t, tt.content, string(content))
			require.Zero(t, tracker.recorded)
		})
	}
}

// A delete takes the same path: an exact, unique match deletes, and a match
// that needs the whitespace fallback stays refused.
func TestDeleteContentWithoutRead(t *testing.T) {
	t.Parallel()

	t.Run("exact unique match deletes", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := writeFile(t, dir, "a.txt", "alpha\nbeta\n")
		edit, tracker := unreadEditContext(t, dir, time.Time{}, true)

		resp, err := deleteContent(edit, filePath, "beta\n", false, fantasy.ToolCall{ID: "call"})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		require.Equal(t, "alpha\n", string(content))
		require.True(t, gjson.Get(resp.Metadata, "unread_edit").Bool())
		require.Zero(t, tracker.recorded)
	})

	t.Run("whitespace-only match stays refused", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := writeFile(t, dir, "a.txt", "alpha\nbeta\n")
		edit, tracker := unreadEditContext(t, dir, time.Time{}, true)

		resp, err := deleteContent(edit, filePath, "alpha\n  beta\n", false, fantasy.ToolCall{ID: "call"})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, todayNotRead)
		require.Equal(t, string(ErrorKindNotRead), gjson.Get(resp.Metadata, "error_kind").String())
		require.Zero(t, tracker.recorded)
	})
}

// With the option off, both refusals keep the exact text they have today.
func TestReadGuardOptionOffKeepsTodaysRefusal(t *testing.T) {
	t.Parallel()

	t.Run("never read", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := writeFile(t, dir, "a.txt", "alpha\nbeta\n")
		edit, tracker := unreadEditContext(t, dir, time.Time{}, false)

		resp, err := replaceContent(edit, filePath, "beta", "BETA", false, fantasy.ToolCall{ID: "call"})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Equal(t, todayNotRead, resp.Content)
		require.Equal(t, `{"error_kind":"not_read"}`, resp.Metadata)
		require.Zero(t, tracker.recorded)

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		require.Equal(t, "alpha\nbeta\n", string(content))
	})

	t.Run("changed since read", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := writeFile(t, dir, "a.txt", "alpha\nbeta\n")
		lastRead := time.Now().Add(-time.Hour)
		edit, tracker := unreadEditContext(t, dir, lastRead, false)

		resp, err := replaceContent(edit, filePath, "beta", "BETA", false, fantasy.ToolCall{ID: "call"})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		info, err := os.Stat(filePath)
		require.NoError(t, err)
		want := fmt.Sprintf("file %s has been modified since it was last read (mod time: %s, last read: %s)",
			filePath, info.ModTime().Truncate(time.Second).Format(time.RFC3339), lastRead.Format(time.RFC3339))
		require.Equal(t, want, resp.Content)
		require.Equal(t, string(ErrorKindChangedSinceRead), gjson.Get(resp.Metadata, "error_kind").String())
		require.Zero(t, tracker.recorded)
	})
}

// Creating a file that exists keeps its own refusal, with the option on.
func TestEditWithoutReadStillRefusesToCreateAnExistingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := writeFile(t, dir, "a.txt", "alpha\n")
	edit, _ := unreadEditContext(t, dir, time.Time{}, true)

	resp, err := createNewFile(edit, filePath, "beta\n", fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Equal(t, "file already exists: "+filePath, resp.Content)
}

// resetEditWithoutReadEnv restores the once-value so a test can change the
// variable it reads.
func resetEditWithoutReadEnv(t *testing.T) {
	t.Helper()
	previous := editWithoutReadEnv
	editWithoutReadEnv = sync.OnceValue(readEditWithoutReadEnv)
	t.Cleanup(func() { editWithoutReadEnv = previous })
}

// The option is off by default, and either switch turns it on.
func TestEditWithoutReadEnabled(t *testing.T) {
	t.Run("option off and no override", func(t *testing.T) {
		resetEditWithoutReadEnv(t)
		t.Setenv("CRUSH_EDIT_WITHOUT_READ", "")
		require.False(t, editWithoutReadEnabled(false))
	})
	t.Run("option on and no override", func(t *testing.T) {
		resetEditWithoutReadEnv(t)
		t.Setenv("CRUSH_EDIT_WITHOUT_READ", "")
		require.True(t, editWithoutReadEnabled(true))
	})
	t.Run("override one", func(t *testing.T) {
		resetEditWithoutReadEnv(t)
		t.Setenv("CRUSH_EDIT_WITHOUT_READ", "1")
		require.True(t, editWithoutReadEnabled(false))
	})
	t.Run("override true", func(t *testing.T) {
		resetEditWithoutReadEnv(t)
		t.Setenv("CRUSH_EDIT_WITHOUT_READ", "true")
		require.True(t, editWithoutReadEnabled(false))
	})
	t.Run("override zero", func(t *testing.T) {
		resetEditWithoutReadEnv(t)
		t.Setenv("CRUSH_EDIT_WITHOUT_READ", "0")
		require.False(t, editWithoutReadEnabled(false))
	})
	t.Run("unparseable override follows the option", func(t *testing.T) {
		resetEditWithoutReadEnv(t)
		t.Setenv("CRUSH_EDIT_WITHOUT_READ", "sometimes")
		require.False(t, editWithoutReadEnabled(false))
	})
}
