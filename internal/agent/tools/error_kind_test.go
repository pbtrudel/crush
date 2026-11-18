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

			require.Equal(t, tt.want, setErrorKind(tt.metadata, ErrorKindNotRead))
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

func TestEditReadGuardKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lastRead time.Time
		want     ErrorKind
		message  string
	}{
		{name: "never read", want: ErrorKindNotRead, message: "you must read the file before editing it. Use the View tool first"},
		{name: "changed since read", lastRead: time.Now().Add(-time.Hour), want: ErrorKindChangedSinceRead, message: "has been modified since it was last read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := filepath.Join(dir, "a.txt")
			require.NoError(t, os.WriteFile(filePath, []byte("alpha\n"), 0o644))
			edit := editContextForKind(t, dir, tt.lastRead)

			resp, err := replaceContent(edit, filePath, "alpha", "beta", false, fantasy.ToolCall{ID: "call"})
			require.NoError(t, err)
			require.True(t, resp.IsError)
			require.Contains(t, resp.Content, tt.message)
			require.Equal(t, string(tt.want), gjson.Get(resp.Metadata, "error_kind").String())

			// multiedit goes through the same guard.
			multi, err := processMultiEditExistingFile(edit, MultiEditParams{
				FilePath: filePath,
				Edits:    []MultiEditOperation{{OldString: "alpha", NewString: "beta"}},
			}, fantasy.ToolCall{ID: "call"})
			require.NoError(t, err)
			require.True(t, multi.IsError)
			require.Equal(t, string(tt.want), gjson.Get(multi.Metadata, "error_kind").String())
		})
	}
}

func TestWriteReadGuardKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lastRead time.Time
		want     ErrorKind
	}{
		{name: "never read", want: ErrorKindNotRead},
		{name: "changed since read", lastRead: time.Now().Add(-time.Hour), want: ErrorKindChangedSinceRead},
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
			// The wording is kept for both cases; only the kind tells them apart.
			require.Contains(t, resp.Content, "has been modified since it was last read")
			require.Equal(t, string(tt.want), gjson.Get(resp.Metadata, "error_kind").String())

			content, err := os.ReadFile(filePath)
			require.NoError(t, err)
			require.Equal(t, "alpha\n", string(content))
		})
	}
}
