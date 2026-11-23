package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type mockPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
	deny bool
}

func (m *mockPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	return !m.deny, nil
}

func (m *mockPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *mockPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *mockPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *mockPermissionService) AutoApproveSession(sessionID string) {}

func (m *mockPermissionService) IsSessionAutoApproved(sessionID string) bool { return false }

func (m *mockPermissionService) InheritAutoApproval(parentSessionID, childSessionID string) {}

func (m *mockPermissionService) ActiveRequests() []permission.PermissionRequest {
	return nil
}

func (m *mockPermissionService) SetSessionScope(scope permission.SessionScope) {}

func (m *mockPermissionService) SetSkipRequests(skip bool) {}

func (m *mockPermissionService) SkipRequests() bool {
	return false
}

func (m *mockPermissionService) SetPermissionMode(mode permission.PermissionMode) {}

func (m *mockPermissionService) PermissionMode() permission.PermissionMode {
	return permission.PermissionModeNormal
}

func (m *mockPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func (m *mockPermissionService) SubscribeModeChanges(ctx context.Context) <-chan pubsub.Event[permission.ModeChangedEvent] {
	return make(<-chan pubsub.Event[permission.ModeChangedEvent])
}

type mockHistoryService struct {
	*pubsub.Broker[history.File]
}

func (m *mockHistoryService) Create(ctx context.Context, sessionID, path, content string) (history.File, error) {
	return history.File{Path: path, Content: content}, nil
}

func (m *mockHistoryService) CreateVersion(ctx context.Context, sessionID, path, content string) (history.File, error) {
	return history.File{}, nil
}

func (m *mockHistoryService) GetByPathAndSession(ctx context.Context, path, sessionID string) (history.File, error) {
	return history.File{Path: path, Content: ""}, nil
}

func (m *mockHistoryService) Get(ctx context.Context, id string) (history.File, error) {
	return history.File{}, nil
}

func (m *mockHistoryService) ListBySession(ctx context.Context, sessionID string) ([]history.File, error) {
	return nil, nil
}

func (m *mockHistoryService) ListLatestSessionFiles(ctx context.Context, sessionID string) ([]history.File, error) {
	return nil, nil
}

func (m *mockHistoryService) Delete(ctx context.Context, id string) error {
	return nil
}

func (m *mockHistoryService) DeleteSessionFiles(ctx context.Context, sessionID string) error {
	return nil
}

func TestApplyEditToContentPartialSuccess(t *testing.T) {
	t.Parallel()

	content := "line 1\nline 2\nline 3\n"

	// Test successful edit.
	newContent, _, err := applyEditToContent(content, MultiEditOperation{
		OldString: "line 1",
		NewString: "LINE 1",
	})
	require.NoError(t, err)
	require.Contains(t, newContent, "LINE 1")
	require.Contains(t, newContent, "line 2")

	// Test failed edit (string not found).
	_, _, err = applyEditToContent(content, MultiEditOperation{
		OldString: "line 99",
		NewString: "LINE 99",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestApplyEditToContentReplacementModes(t *testing.T) {
	t.Parallel()

	content := "alpha\nbeta\nalpha\n"

	newContent, _, err := applyEditToContent(content, MultiEditOperation{
		OldString:  "alpha",
		NewString:  "ALPHA",
		ReplaceAll: true,
	})
	require.NoError(t, err)
	require.Equal(t, "ALPHA\nbeta\nALPHA\n", newContent)

	_, _, err = applyEditToContent(content, MultiEditOperation{
		OldString: "alpha",
		NewString: "ALPHA",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple times")

	newContent, _, err = applyEditToContent(content, MultiEditOperation{})
	require.NoError(t, err)
	require.Equal(t, content, newContent)
}

func TestMultiEditSequentialApplication(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	// Create test file.
	content := "line 1\nline 2\nline 3\nline 4\n"
	err := os.WriteFile(testFile, []byte(content), 0o644)
	require.NoError(t, err)

	// Manually test the sequential application logic.
	currentContent := content

	// Apply edits sequentially, tracking failures.
	edits := []MultiEditOperation{
		{OldString: "line 1", NewString: "LINE 1"},   // Should succeed
		{OldString: "line 99", NewString: "LINE 99"}, // Should fail - doesn't exist
		{OldString: "line 3", NewString: "LINE 3"},   // Should succeed
		{OldString: "line 2", NewString: "LINE 2"},   // Should succeed - still exists
	}

	var failedEdits []FailedEdit
	successCount := 0

	for i, edit := range edits {
		newContent, _, err := applyEditToContent(currentContent, edit)
		if err != nil {
			failedEdits = append(failedEdits, FailedEdit{
				Index: i + 1,
				Error: err.Error(),
				Edit:  edit,
			})
			continue
		}
		currentContent = newContent
		successCount++
	}

	// Verify results.
	require.Equal(t, 3, successCount, "Expected 3 successful edits")
	require.Len(t, failedEdits, 1, "Expected 1 failed edit")

	// Check failed edit details.
	require.Equal(t, 2, failedEdits[0].Index)
	require.Contains(t, failedEdits[0].Error, "not found")

	// Verify content changes.
	require.Contains(t, currentContent, "LINE 1")
	require.Contains(t, currentContent, "LINE 2")
	require.Contains(t, currentContent, "LINE 3")
	require.Contains(t, currentContent, "line 4") // Original unchanged
	require.NotContains(t, currentContent, "LINE 99")
}

func TestMultiEditAllEditsSucceed(t *testing.T) {
	t.Parallel()

	content := "line 1\nline 2\nline 3\n"

	edits := []MultiEditOperation{
		{OldString: "line 1", NewString: "LINE 1"},
		{OldString: "line 2", NewString: "LINE 2"},
		{OldString: "line 3", NewString: "LINE 3"},
	}

	currentContent := content
	successCount := 0

	for _, edit := range edits {
		newContent, _, err := applyEditToContent(currentContent, edit)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		currentContent = newContent
		successCount++
	}

	require.Equal(t, 3, successCount)
	require.Contains(t, currentContent, "LINE 1")
	require.Contains(t, currentContent, "LINE 2")
	require.Contains(t, currentContent, "LINE 3")
}

func TestMultiEditAllEditsFail(t *testing.T) {
	t.Parallel()

	content := "line 1\nline 2\n"

	edits := []MultiEditOperation{
		{OldString: "line 99", NewString: "LINE 99"},
		{OldString: "line 100", NewString: "LINE 100"},
	}

	currentContent := content
	var failedEdits []FailedEdit

	for i, edit := range edits {
		newContent, _, err := applyEditToContent(currentContent, edit)
		if err != nil {
			failedEdits = append(failedEdits, FailedEdit{
				Index: i + 1,
				Error: err.Error(),
				Edit:  edit,
			})
			continue
		}
		currentContent = newContent
	}

	require.Len(t, failedEdits, 2)
	require.Equal(t, content, currentContent, "Content should be unchanged")
}

func TestProcessMultiEditExistingFilePartialFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\ntwo\nthree\n"), 0o644))

	edit := editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: &mockEditFileTracker{lastRead: time.Now().Add(time.Second)},
		workingDir:  dir,
	}
	params := MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "two", NewString: "TWO"},
			{OldString: "missing", NewString: "MISSING"},
		},
	}

	resp, err := processMultiEditExistingFile(edit, params, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Applied 1 of 2 edits")
	require.Contains(t, resp.Content, "edit 2:")
	require.Contains(t, resp.Content, "old_string not found")
	require.NotContains(t, resp.Content, "edit 1:")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "one\nTWO\nthree\n", string(content))

	var meta MultiEditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, 1, meta.EditsApplied)
	require.Len(t, meta.EditsFailed, 1)
	require.Equal(t, 2, meta.EditsFailed[0].Index)
	require.Equal(t, "one\ntwo\nthree\n", meta.OldContent)
	require.Equal(t, "one\nTWO\nthree\n", meta.NewContent)
}

func TestFailedEditsText(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", failedEditsText(nil), "a batch with no failures adds nothing")
	require.Equal(t,
		"Failed edits:\n- edit 2: old_string not found in file. Make sure it matches exactly, including whitespace and line breaks\n\n  The closest text found in the file is:\n  alpha\n- edit 3: old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true",
		failedEditsText([]FailedEdit{
			{Index: 2, Error: "old_string not found in file. Make sure it matches exactly, including whitespace and line breaks\n\nThe closest text found in the file is:\nalpha"},
			{Index: 3, Error: "old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true"},
		}),
	)
}

func TestProcessMultiEditAllFailedReportsEdits(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("one\ntwo\n"), 0o644))

	edit := editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: &mockEditFileTracker{lastRead: time.Now().Add(time.Second)},
		workingDir:  dir,
	}
	resp, err := processMultiEditExistingFile(edit, MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "missing", NewString: "MISSING"},
			{OldString: "absent", NewString: "ABSENT"},
		},
	}, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no changes made - all 2 edit(s) failed")
	require.Contains(t, resp.Content, "edit 1:")
	require.Contains(t, resp.Content, "edit 2:")
	require.Contains(t, resp.Content, "old_string not found")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "one\ntwo\n", string(content))
}

func TestProcessMultiEditWithCreationPartialFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "nested", "test.txt")

	edit := editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: &mockEditFileTracker{},
		workingDir:  dir,
	}
	params := MultiEditParams{
		FilePath: filePath,
		Edits: []MultiEditOperation{
			{OldString: "", NewString: "one\ntwo\nthree\n"},
			{OldString: "two", NewString: "TWO"},
			{OldString: "missing", NewString: "MISSING"},
		},
	}

	resp, err := processMultiEditWithCreation(edit, params, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "File created with 2 of 3 edits")
	require.Contains(t, resp.Content, "edit 3:")
	require.Contains(t, resp.Content, "old_string not found")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "one\nTWO\nthree\n", string(content))

	var meta MultiEditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, 2, meta.EditsApplied)
	require.Len(t, meta.EditsFailed, 1)
	require.Equal(t, 3, meta.EditsFailed[0].Index)
	require.Equal(t, "", meta.OldContent)
	require.Equal(t, "one\nTWO\nthree\n", meta.NewContent)
}

// A batch applies to a file the guard would refuse only when every edit is
// an exact, single match against the content as it evolves there, and the
// file is then not recorded as read.
func TestProcessMultiEditExistingFileWithoutRead(t *testing.T) {
	t.Parallel()

	t.Run("an exact batch applies", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
		edit, tracker := unreadEditContext(t, dir, time.Time{}, true)

		resp, err := processMultiEditExistingFile(edit, MultiEditParams{
			FilePath: filePath,
			Edits: []MultiEditOperation{
				{OldString: "one", NewString: "ONE"},
				{OldString: "ONE\ntwo", NewString: "ONE\nTWO"},
			},
		}, fantasy.ToolCall{ID: "call"})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		require.Equal(t, "ONE\nTWO\nthree\n", string(content))
		var meta MultiEditResponseMetadata
		require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
		require.True(t, meta.UnreadEdit)
		require.Equal(t, 2, meta.EditsApplied)
		require.Zero(t, tracker.recorded, "an unread batch must not claim a read")
	})

	t.Run("a later edit may match the text an earlier edit wrote", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := writeFile(t, dir, "a.txt", "one\ntwo\n")
		edit, tracker := unreadEditContext(t, dir, time.Time{}, true)

		// "marker" is only in the first edit's new_string, so the second
		// edit matches the content as the first one leaves it, and nothing
		// in the file the session never read.
		resp, err := processMultiEditExistingFile(edit, MultiEditParams{
			FilePath: filePath,
			Edits: []MultiEditOperation{
				{OldString: "two", NewString: "inserted\nmarker"},
				{OldString: "marker", NewString: "MARKER"},
			},
		}, fantasy.ToolCall{ID: "call"})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		require.Equal(t, "one\ninserted\nMARKER\n", string(content))
		require.Zero(t, tracker.recorded)
	})

	t.Run("a whitespace-only edit refuses the batch", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		filePath := writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
		edit, tracker := unreadEditContext(t, dir, time.Time{}, true)

		resp, err := processMultiEditExistingFile(edit, MultiEditParams{
			FilePath: filePath,
			Edits: []MultiEditOperation{
				{OldString: "one", NewString: "ONE"},
				{OldString: "  two", NewString: "TWO"},
			},
		}, fantasy.ToolCall{ID: "call"})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, todayNotRead)
		require.Contains(t, resp.Content, "every edit in the batch must match exactly once")
		require.NotContains(t, resp.Content, "Failed edits:", "no edit of the batch failed to match")
		require.Equal(t, string(ErrorKindNotRead), gjson.Get(resp.Metadata, "error_kind").String())
		require.Zero(t, tracker.recorded)

		content, err := os.ReadFile(filePath)
		require.NoError(t, err)
		require.Equal(t, "one\ntwo\nthree\n", string(content))
	})
}

// A batch the guard refuses names the edits that failed, and takes the
// match kind when that is why it was refused.
func TestProcessMultiEditExistingFileWithoutReadReportsFailedEdits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		edits    []MultiEditOperation
		wantKind ErrorKind
		wantEdit string
	}{
		{
			name:     "no match",
			edits:    []MultiEditOperation{{OldString: "one", NewString: "ONE"}, {OldString: "missing", NewString: "MISSING"}},
			wantKind: ErrorKindTextNotFound,
			wantEdit: "- edit 2: old_string not found",
		},
		{
			name:     "several matches",
			edits:    []MultiEditOperation{{OldString: "o", NewString: "O"}},
			wantKind: ErrorKindMultipleMatches,
			wantEdit: "- edit 1: old_string appears multiple times",
		},
		{
			name:     "replace_all with no match",
			edits:    []MultiEditOperation{{OldString: "missing", NewString: "MISSING", ReplaceAll: true}},
			wantKind: ErrorKindTextNotFound,
			wantEdit: "- edit 1: old_string not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			filePath := writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
			edit, tracker := unreadEditContext(t, dir, time.Time{}, true)

			resp, err := processMultiEditExistingFile(edit, MultiEditParams{
				FilePath: filePath,
				Edits:    tt.edits,
			}, fantasy.ToolCall{ID: "call"})
			require.NoError(t, err)
			require.True(t, resp.IsError)
			require.Contains(t, resp.Content, todayNotRead)
			require.Contains(t, resp.Content, "every edit in the batch must match exactly once")
			require.Contains(t, resp.Content, "Failed edits:")
			require.Contains(t, resp.Content, tt.wantEdit)
			require.Equal(t, string(tt.wantKind), gjson.Get(resp.Metadata, "error_kind").String())
			require.Zero(t, tracker.recorded)

			content, err := os.ReadFile(filePath)
			require.NoError(t, err)
			require.Equal(t, "one\ntwo\nthree\n", string(content))
		})
	}
}
