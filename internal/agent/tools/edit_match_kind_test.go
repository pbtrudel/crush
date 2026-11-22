package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	notFoundMessage      = "old_string not found in file. Make sure it matches exactly, including whitespace and line breaks"
	multipleMatchMessage = "old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true"
)

func TestFindAndReplaceKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		old        string
		replaceAll bool
		want       ErrorKind
		message    string
	}{
		{name: "not found", content: "alpha\n", old: "gamma", want: ErrorKindTextNotFound, message: notFoundMessage},
		{name: "not found with replace_all", content: "alpha\n", old: "gamma", replaceAll: true, want: ErrorKindTextNotFound, message: notFoundMessage},
		{name: "multiple matches", content: "alpha\nalpha\n", old: "alpha", want: ErrorKindMultipleMatches, message: multipleMatchMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := findAndReplace(tt.content, tt.old, "x", tt.replaceAll)
			require.Error(t, err)
			require.True(t, strings.HasPrefix(err.Error(), tt.message), "the model reads the same text as before: %q", err.Error())
			require.Equal(t, tt.want, errorKindOf(err))
		})
	}
}

func TestErrorKindOf(t *testing.T) {
	t.Parallel()

	require.Equal(t, ErrorKind(""), errorKindOf(errors.New("failed to write file")))
	require.Equal(t, ErrorKind(""), errorKindOf(nil))
	wrapped := fmt.Errorf("edit 2: %w", &editMatchError{kind: ErrorKindTextNotFound, msg: "missing"})
	require.Equal(t, ErrorKindTextNotFound, errorKindOf(wrapped))
}

// A file not read first whose text matches zero or several times keeps the
// match error: the read guard is not the reason the edit failed, and the
// refusal must not invite a read that would change nothing.
func TestEditMatchFailureKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		delete      bool
		withoutRead bool
		old         string
		want        ErrorKind
		message     string
	}{
		{name: "replace, not found", old: "gamma", want: ErrorKindTextNotFound},
		{name: "replace, multiple matches", old: "alpha\n", want: ErrorKindMultipleMatches},
		{name: "delete, not found", delete: true, old: "gamma", want: ErrorKindTextNotFound},
		{name: "delete, multiple matches", delete: true, old: "alpha\n", want: ErrorKindMultipleMatches},
		{name: "replace, not found, not read", withoutRead: true, old: "gamma", want: ErrorKindTextNotFound, message: notFoundMessage},
		{name: "replace, multiple matches, not read", withoutRead: true, old: "alpha\n", want: ErrorKindMultipleMatches, message: multipleMatchMessage},
		{name: "delete, not found, not read", withoutRead: true, delete: true, old: "gamma", want: ErrorKindTextNotFound, message: notFoundMessage},
		{name: "delete, multiple matches, not read", withoutRead: true, delete: true, old: "alpha\n", want: ErrorKindMultipleMatches, message: multipleMatchMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := filepath.Join(dir, "a.txt")
			require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\nalpha\n"), 0o644))
			// A zero last-read time is a file the session never read.
			lastRead := time.Now().Add(time.Second)
			if tt.withoutRead {
				lastRead = time.Time{}
			}
			edit := editContextForKind(t, dir, lastRead)
			edit.withoutRead = tt.withoutRead

			var resp fantasy.ToolResponse
			var err error
			if tt.delete {
				resp, err = deleteContent(edit, filePath, tt.old, false, fantasy.ToolCall{ID: "call"})
			} else {
				resp, err = replaceContent(edit, filePath, tt.old, "x", false, fantasy.ToolCall{ID: "call"})
			}
			require.NoError(t, err)
			require.True(t, resp.IsError)
			require.Equal(t, string(tt.want), gjson.Get(resp.Metadata, "error_kind").String())
			if tt.message != "" {
				require.True(t, strings.HasPrefix(resp.Content, tt.message), resp.Content)
				require.NotContains(t, resp.Content, "Use the View tool first", "a match error must not send the model to read")
			}
		})
	}
}

func TestMultiEditFailedEditKinds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\ntwo\ntwo\n"), 0o644))

	resp, err := processMultiEditExistingFile(editContextForKind(t, dir, time.Now().Add(time.Second)), MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "one", NewString: "ONE"},
			{OldString: "missing", NewString: "MISSING"},
			{OldString: "two", NewString: "TWO"},
		},
	}, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	var meta MultiEditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Len(t, meta.EditsFailed, 2)
	require.Equal(t, 2, meta.EditsFailed[0].Index)
	require.Equal(t, ErrorKindTextNotFound, meta.EditsFailed[0].Kind)
	require.Equal(t, 3, meta.EditsFailed[1].Index)
	require.Equal(t, ErrorKindMultipleMatches, meta.EditsFailed[1].Kind)
	require.False(t, gjson.Get(resp.Metadata, "error_kind").Exists(), "a call that applied an edit has no kind of its own")
}

func TestMultiEditAllFailedTakesFirstKind(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\none\n"), 0o644))

	resp, err := processMultiEditExistingFile(editContextForKind(t, dir, time.Now().Add(time.Second)), MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "one", NewString: "ONE"},
			{OldString: "missing", NewString: "MISSING"},
		},
	}, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.True(t, strings.HasPrefix(resp.Content, "no changes made - all 2 edit(s) failed"), resp.Content)
	require.Equal(t, string(ErrorKindMultipleMatches), gjson.Get(resp.Metadata, "error_kind").String())
	require.Equal(t, int64(2), gjson.Get(resp.Metadata, "edits_failed.#").Int(), "the kind must not replace the batch's metadata")
	require.Equal(t, string(ErrorKindTextNotFound), gjson.Get(resp.Metadata, "edits_failed.1.error_kind").String())
}
