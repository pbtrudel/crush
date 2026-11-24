package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/stretchr/testify/require"
)

type mockEditFileTracker struct {
	lastRead time.Time
	reads    []string
	// recorded counts RecordRead calls, so a test can assert an edit did
	// not claim a read it never made.
	recorded int
}

func (m *mockEditFileTracker) RecordRead(ctx context.Context, sessionID, path string) {
	m.reads = append(m.reads, path)
	m.recorded++
}

func (m *mockEditFileTracker) LastReadTime(ctx context.Context, sessionID, path string) time.Time {
	return m.lastRead
}

func (m *mockEditFileTracker) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return m.reads, nil
}

func TestReplaceContentPreservesCRLFAndMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\r\nbeta\r\n"), 0o644))

	tracker := &mockEditFileTracker{lastRead: time.Now().Add(time.Second)}
	edit := editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: tracker,
		workingDir:  dir,
	}

	resp, err := replaceContent(edit, filePath, "beta", "BETA", false, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Equal(t, "Content replaced in file: "+filePath, resp.Content)

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "alpha\r\nBETA\r\n", string(content))
	require.Equal(t, []string{filePath}, tracker.reads)

	var meta EditResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, "alpha\nbeta\n", meta.OldContent)
	require.Equal(t, "alpha\r\nBETA\r\n", meta.NewContent)
}

func TestEditToolExpandsHomeDirectory(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(home.Dir(), "tilde-edit.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha"), 0o644))

	tool := NewEditTool(nil, &mockPermissionService{}, &mockHistoryService{}, &mockEditFileTracker{lastRead: time.Now().Add(time.Second)}, t.TempDir(), false)
	input, err := json.Marshal(EditParams{FilePath: "~/tilde-edit.txt", OldString: "alpha", NewString: "beta"})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  EditToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "beta", string(content))
}

func TestDeleteContentRejectsMultipleMatchesWithoutReplaceAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\nalpha\n"), 0o644))

	edit := editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: &mockEditFileTracker{lastRead: time.Now().Add(time.Second)},
		workingDir:  dir,
	}

	resp, err := deleteContent(edit, filePath, "alpha\n", false, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "old_string appears multiple times")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "alpha\nbeta\nalpha\n", string(content))
}

func TestDeleteContentReportsMatchCountAndLines(t *testing.T) {
	t.Parallel()

	// The model needs the count and the lines to add enough context on
	// its next call instead of guessing again.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\ngamma\nalpha\ndelta\nzeta\nalpha\n"), 0o644))

	edit := editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: &mockEditFileTracker{lastRead: time.Now().Add(time.Second)},
		workingDir:  dir,
	}

	resp, err := deleteContent(edit, filePath, "alpha", false, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Equal(t, "old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true. It matches 3 times, starting on lines 1, 4 and 7.", resp.Content)
}

// The line list is capped, so the message has to say it lists only the
// first matches: a model reading a short list under "starting on lines"
// would take it for the whole set.
func TestDeleteContentSaysHowManyMatchesItLists(t *testing.T) {
	t.Parallel()

	// Twelve matches, each followed by one filler line, so the matches
	// start on the odd lines 1 to 23.
	var sb strings.Builder
	wantLines := make([]string, 0, 12)
	for i := range 12 {
		if i > 0 {
			sb.WriteString("filler\n")
		}
		wantLines = append(wantLines, strconv.Itoa(2*i+1))
		sb.WriteString("alpha\n")
	}
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(sb.String()), 0o644))

	edit := editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: &mockEditFileTracker{lastRead: time.Now().Add(time.Second)},
		workingDir:  dir,
	}

	resp, err := deleteContent(edit, filePath, "alpha", false, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	listed := strings.Join(wantLines[:9], ", ") + " and " + wantLines[9]
	require.Equal(t, "old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true. It matches 12 times, the first 10 on lines "+listed+".", resp.Content)
}

// refusalTracker records the reads an edit refuses, and answers the next
// guard check the way a real tracker would after that record.
type refusalTracker struct {
	reads    []string
	lastRead time.Time
}

func (m *refusalTracker) RecordRead(ctx context.Context, sessionID, path string) {
	m.reads = append(m.reads, path)
	m.lastRead = time.Now().Add(time.Second)
}

func (m *refusalTracker) LastReadTime(context.Context, string, string) time.Time { return m.lastRead }

func (m *refusalTracker) ListReadFiles(context.Context, string) ([]string, error) {
	return m.reads, nil
}

// runReplaceContent runs one edit against the file, the way the tool does.
func runReplaceContent(t *testing.T, dir, filePath, oldString, newString string, tracker filetracker.Service) fantasy.ToolResponse {
	t.Helper()

	edit := editContext{
		ctx:         context.WithValue(t.Context(), SessionIDContextKey, "session"),
		permissions: &mockPermissionService{},
		files:       &mockHistoryService{},
		filetracker: tracker,
		workingDir:  dir,
	}
	resp, err := replaceContent(edit, filePath, oldString, newString, false, fantasy.ToolCall{ID: "call"})
	require.NoError(t, err)
	return resp
}

func TestUnreadEditRefusalShowsFileAndRecordsRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.go")
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(lines, "\n")+"\n"), 0o644))

	tracker := &refusalTracker{}
	resp := runReplaceContent(t, dir, filePath, "line 12", "x", tracker)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "you must read the file before editing it")
	require.Contains(t, resp.Content, "    12|line 12")
	require.Equal(t, []string{filePath}, tracker.reads)

	resp = runReplaceContent(t, dir, filePath, "line 12", "x", tracker)
	require.False(t, resp.IsError, resp.Content)
}

func TestRefusalContentIsCapped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "big.go")
	var lines []string
	for i := 1; i <= 10000; i++ {
		lines = append(lines, fmt.Sprintf("l%d", i))
	}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(lines, "\n")+"\n"), 0o644))

	resp := runReplaceContent(t, dir, filePath, "l4000", "x", &refusalTracker{})
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "4000|l4000")
	require.Contains(t, resp.Content, "Showing lines")
	require.Less(t, len(resp.Content), 60_000)
}

func TestChangedSinceReadRefusalShowsCurrentFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.go")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\n"), 0o644))

	tracker := &refusalTracker{lastRead: time.Now().Add(-time.Hour)}
	resp := runReplaceContent(t, dir, filePath, "beta", "gamma", tracker)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "has been modified since it was last read")
	require.Contains(t, resp.Content, "    2|beta")
}

func TestRepeatedEditIsAlreadyApplied(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.go")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\n"), 0o644))
	tracker := &refusalTracker{lastRead: time.Now().Add(time.Second)}

	applied := "gamma, and a line long enough to be a real change"
	resp := runReplaceContent(t, dir, filePath, "beta", applied, tracker)
	require.False(t, resp.IsError, resp.Content)

	resp = runReplaceContent(t, dir, filePath, "beta", applied, tracker)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "No change needed: this call changed nothing because the file already contains new_string once and no longer contains old_string.")

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "alpha\n"+applied+"\n", string(content))
}

// A short new_string that happens to appear once proves nothing: the edit may
// simply have the wrong old_string, so it stays the ordinary not-found error.
func TestShortNewStringWithAStaleOldStringStillFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.go")
	require.NoError(t, os.WriteFile(filePath, []byte("func f() int {\n\treturn nil\n}\n"), 0o644))
	tracker := &refusalTracker{lastRead: time.Now().Add(time.Second)}

	resp := runReplaceContent(t, dir, filePath, "stale old string", "return nil", tracker)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "old_string not found")
	require.NotContains(t, resp.Content, "No change needed")
}

// A multi-line new_string is specific enough to prove the edit is applied,
// however short its lines are.
func TestMultiLineNewStringIsAlreadyApplied(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.go")
	require.NoError(t, os.WriteFile(filePath, []byte("func f() int {\n\treturn 1\n}\n"), 0o644))
	tracker := &refusalTracker{lastRead: time.Now().Add(time.Second)}

	resp := runReplaceContent(t, dir, filePath, "a stale line\nand another", "return 1\n}", tracker)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "No change needed")
}

func TestEditWithAnAbsentOldStringStillFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.go")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\n"), 0o644))
	tracker := &refusalTracker{lastRead: time.Now().Add(time.Second)}

	resp := runReplaceContent(t, dir, filePath, "missing", "gamma", tracker)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "old_string not found")
}

// Edit refuses before it asks for any permission, and view needs one to read
// outside the working directory, so a refusal there must not show the file.
func TestRefusalOutsideWorkingDirShowsNoContent(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("token=abc\n"), 0o644))

	tracker := &refusalTracker{}
	resp := runReplaceContent(t, workingDir, outside, "token=abc", "x", tracker)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "you must read the file before editing it")
	require.NotContains(t, resp.Content, "token=abc")
	require.Empty(t, tracker.reads)
}
