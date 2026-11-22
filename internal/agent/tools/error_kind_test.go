package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/permission/testutil"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSetErrorKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata string
		want     string
	}{
		{name: "no metadata", metadata: "", want: `{"error_kind":"not_read"}`},
		{name: "keeps other keys", metadata: `{"additions":2}`, want: `{"additions":2,"error_kind":"not_read"}`},
		{name: "replaces an earlier kind", metadata: `{"error_kind":"changed_since_read"}`, want: `{"error_kind":"not_read"}`},
		{name: "not an object", metadata: `["a"]`, want: `["a"]`},
		{name: "not JSON", metadata: "plain text", want: "plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, SetErrorKind(tt.metadata, ErrorKindNotRead))
		})
	}
}

// editContextForKind builds an edit context whose tracker reports lastRead
// for every file; the zero time means the file was never read.
func editContextForKind(t *testing.T, dir string, lastRead time.Time) editContext {
	t.Helper()
	return editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: &mockEditFileTracker{lastRead: lastRead},
		workingDir:  dir,
	}
}

// The read guard refuses an unread or stale file with the option off. With
// the option on, the same file applies when old_string matches exactly
// once.
func TestEditReadGuardKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		lastRead    time.Time
		withoutRead bool
		refused     bool
		want        ErrorKind
		message     string
	}{
		{name: "never read, option off", refused: true, want: ErrorKindNotRead, message: "you must read the file before editing it. Use the View tool first"},
		{name: "changed since read, option off", lastRead: time.Now().Add(-time.Hour), refused: true, want: ErrorKindChangedSinceRead, message: "has been modified since it was last read"},
		{name: "never read, option on", withoutRead: true},
		{name: "changed since read, option on", lastRead: time.Now().Add(-time.Hour), withoutRead: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := filepath.Join(dir, "a.txt")
			// Both tools start from the same unread file.
			restore := func() {
				require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))
			}
			edit := editContextForKind(t, dir, tt.lastRead)
			edit.withoutRead = tt.withoutRead

			restore()
			resp, err := replaceContent(edit, filePath, "alpha", "beta", false, fantasy.ToolCall{ID: "call"})
			require.NoError(t, err)
			require.Equal(t, tt.refused, resp.IsError, resp.Content)
			if tt.refused {
				require.Contains(t, resp.Content, tt.message)
				require.Equal(t, string(tt.want), gjson.Get(resp.Metadata, "error_kind").String())
			} else {
				require.True(t, gjson.Get(resp.Metadata, "unread_edit").Bool())
			}

			// multiedit goes through the same guard.
			restore()
			multi, err := processMultiEditExistingFile(edit, MultiEditParams{
				FilePath: filePath,
				Edits:    []MultiEditOperation{{OldString: "alpha", NewString: "beta"}},
			}, fantasy.ToolCall{ID: "call"})
			require.NoError(t, err)
			require.Equal(t, tt.refused, multi.IsError, multi.Content)
			if tt.refused {
				require.Equal(t, string(tt.want), gjson.Get(multi.Metadata, "error_kind").String())
			}
		})
	}
}

func TestWriteReadGuardKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lastRead time.Time
		want     ErrorKind
		message  string
		absent   string
	}{
		{
			name:    "never read",
			want:    ErrorKindNotRead,
			message: "exists but has not been read in this session. Read it with view before overwriting it",
			absent:  "0001-01-01",
		},
		{
			name:     "changed since read",
			lastRead: time.Now().Add(-time.Hour),
			want:     ErrorKindChangedSinceRead,
			message:  "has been modified since it was last read",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := filepath.Join(dir, "a.txt")
			require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))
			tool := NewWriteTool(nil, &testutil.MockPermissionService{}, &mockHistoryService{}, &mockEditFileTracker{lastRead: tt.lastRead}, dir)
			input, err := json.Marshal(WriteParams{FilePath: "a.txt", Content: "beta\n"})
			require.NoError(t, err)

			resp, err := tool.Run(context.WithValue(t.Context(), SessionIDContextKey, "session"), fantasy.ToolCall{
				ID:    "call",
				Name:  WriteToolName,
				Input: string(input),
			})
			require.NoError(t, err)
			require.True(t, resp.IsError)
			// Each case gets wording that matches what happened: a file with
			// no read in this session was not modified since a read.
			require.Contains(t, resp.Content, tt.message)
			if tt.absent != "" {
				require.NotContains(t, resp.Content, tt.absent)
			}
			require.Equal(t, string(tt.want), gjson.Get(resp.Metadata, "error_kind").String())

			content, err := os.ReadFile(filePath)
			require.NoError(t, err)
			require.Equal(t, "alpha\n", string(content))
		})
	}
}
