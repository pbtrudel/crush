package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/permission/testutil"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestReadTextFileBoundaryCases(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")

	var allLines []string
	for i := range 5 {
		allLines = append(allLines, fmt.Sprintf("line %d", i+1))
	}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(allLines, "\n")), 0o644))

	tests := []struct {
		name        string
		offset      int
		limit       int
		wantContent string
		wantHasMore bool
	}{
		{
			name:        "exactly limit lines remaining",
			offset:      0,
			limit:       5,
			wantContent: "line 1\nline 2\nline 3\nline 4\nline 5",
			wantHasMore: false,
		},
		{
			name:        "limit plus one line remaining",
			offset:      0,
			limit:       4,
			wantContent: "line 1\nline 2\nline 3\nline 4",
			wantHasMore: true,
		},
		{
			name:        "offset at last line",
			offset:      4,
			limit:       3,
			wantContent: "line 5",
			wantHasMore: false,
		},
		{
			name:        "offset beyond eof",
			offset:      10,
			limit:       3,
			wantContent: "",
			wantHasMore: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotContent, gotHasMore, err := readTextFile(filePath, tt.offset, tt.limit, 0)
			require.NoError(t, err)
			require.Equal(t, tt.wantContent, gotContent)
			require.Equal(t, tt.wantHasMore, gotHasMore)
		})
	}
}

func TestReadTextFileTruncatesLongLines(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "longline.txt")

	longLine := strings.Repeat("a", MaxLineLength+10)
	require.NoError(t, os.WriteFile(filePath, []byte(longLine), 0o644))

	content, hasMore, err := readTextFile(filePath, 0, 1, 0)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Equal(t, strings.Repeat("a", MaxLineLength)+"...", content)
}

func TestReadTextFileLineExceeding1MB(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "huge_line.txt")

	hugeLine := strings.Repeat("A", 2*1024*1024) // 2MB — exceeds bufio.Scanner max
	require.NoError(t, os.WriteFile(filePath, []byte(hugeLine), 0o644))

	content, hasMore, err := readTextFile(filePath, 0, 1, 0)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Equal(t, strings.Repeat("A", MaxLineLength)+"...", content)
}

func TestViewToolAllowsSmallSectionsOfLargeFiles(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "large.txt")
	lines := []string{strings.Repeat("a", MaxViewSize+1), "target line", "after target"}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
		Offset:   1,
		Limit:    1,
	})

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "     2|target line")
	require.NotContains(t, resp.Content, "File is too large")

	var meta ViewResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, "target line", meta.Content)
}

func TestViewToolBlocksOversizedReturnedSections(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "large-section.txt")
	lines := make([]string, DefaultReadLimit)
	for i := range lines {
		lines[i] = strings.Repeat("a", MaxLineLength)
	}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Content section is too large")
	require.Contains(t, resp.Content, "lines fit", "the model needs to know a smaller limit would fit")
}

func TestViewToolBlocksOversizedImages(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "large.png")
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Repeat("a", MaxViewSize+1)), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	ctx = context.WithValue(ctx, SupportsImagesContextKey, true)
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Image file is too large")
}

func TestReadTextFileEnforcesMaxContentSize(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "oversized.txt")
	lines := []string{
		strings.Repeat("a", MaxLineLength),
		strings.Repeat("b", MaxLineLength),
		"target line",
	}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0o644))

	content, hasMore, err := readTextFile(filePath, 0, len(lines), MaxLineLength)
	require.ErrorAs(t, err, &contentTooLargeError{})
	require.Empty(t, content)
	require.False(t, hasMore)

	content, hasMore, err = readTextFile(filePath, 2, 1, MaxLineLength)
	require.NoError(t, err)
	require.Equal(t, "target line", content)
	require.False(t, hasMore)
}

func TestReadTextFileAllowsExactMaxContentSize(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "exact-size.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("abcd\nefgh"), 0o644))

	content, hasMore, err := readTextFile(filePath, 0, 2, len("abcd\nefgh"))
	require.NoError(t, err)
	require.Equal(t, "abcd\nefgh", content)
	require.False(t, hasMore)
}

func TestViewToolSingleFilePathResponseShape(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "single.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello\nworld"), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{FilePath: filePath})

	require.False(t, resp.IsError)
	require.Equal(t, "<file>\n     1|hello\n     2|world\n</file>\n", resp.Content)

	var meta ViewResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, filePath, meta.FilePath)
	require.Equal(t, "hello\nworld", meta.Content)
}

func TestViewToolReadsSeveralFilesInOneCall(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	first := filepath.Join(workingDir, "first.txt")
	second := filepath.Join(workingDir, "second.txt")
	missing := filepath.Join(workingDir, "absent.txt")
	require.NoError(t, os.WriteFile(first, []byte("alpha"), 0o644))
	require.NoError(t, os.WriteFile(second, []byte("beta"), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runViewTool(t, tool, ctx, ViewParams{FilePaths: []string{first, second}})
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "==> "+first+" <==\n")
	require.Contains(t, resp.Content, "==> "+second+" <==\n")
	require.Less(t,
		strings.Index(resp.Content, "alpha"),
		strings.Index(resp.Content, "beta"),
		"files must come back in the order they were asked for",
	)
	require.Empty(t, resp.Metadata, "a multi-file result describes no single file")

	resp = runViewTool(t, tool, ctx, ViewParams{FilePaths: []string{first, missing, second}})
	require.False(t, resp.IsError, "one unreadable file must not fail the whole call")
	require.Contains(t, resp.Content, "==> "+missing+" <==\n")
	require.Contains(t, resp.Content, "File not found")
	require.Contains(t, resp.Content, "alpha")
	require.Contains(t, resp.Content, "beta")
}

func TestViewToolRejectsInvalidMultiFileCalls(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	first := filepath.Join(workingDir, "first.txt")
	require.NoError(t, os.WriteFile(first, []byte("alpha"), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	eleven := make([]string, 0, 11)
	for i := range 11 {
		eleven = append(eleven, filepath.Join(workingDir, fmt.Sprintf("f%d.txt", i)))
	}

	t.Run("too many files", func(t *testing.T) {
		t.Parallel()
		resp := runViewTool(t, tool, ctx, ViewParams{FilePaths: eleven})
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "10")
	})

	t.Run("file_path with file_paths", func(t *testing.T) {
		t.Parallel()
		resp := runViewTool(t, tool, ctx, ViewParams{
			FilePath:  first,
			FilePaths: []string{first},
		})
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "file_path")
	})

	t.Run("offset with file_paths", func(t *testing.T) {
		t.Parallel()
		resp := runViewTool(t, tool, ctx, ViewParams{
			FilePaths: []string{first},
			Offset:    1,
		})
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "offset")
	})
}

func TestViewToolMultiFileStopsAtOutputBudget(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	paths := make([]string, 0, 3)
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		path := filepath.Join(workingDir, name)
		lines := make([]string, DefaultReadLimit)
		for i := range lines {
			lines[i] = strings.Repeat("a", 100)
		}
		require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))
		paths = append(paths, path)
	}

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{FilePaths: paths})

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "==> "+paths[0]+" <==\n", "the first file is always read")
	require.NotContains(t, resp.Content, "==> "+paths[1]+" <==\n")
	require.Contains(t, resp.Content, "Not read, output budget reached: ")
	require.Contains(t, resp.Content, paths[1])
	require.Contains(t, resp.Content, paths[2])
	require.Contains(t, resp.Content, "Read them in another call.")
}

// A single-image call inside file_paths must behave like a single-image
// file_path call: the image itself travels with the response.
func TestViewToolMultiFilePreservesFirstImage(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	first := filepath.Join(workingDir, "shot.png")
	second := filepath.Join(workingDir, "later.png")
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d}
	require.NoError(t, os.WriteFile(first, png, 0o644))
	require.NoError(t, os.WriteFile(second, png, 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	ctx = context.WithValue(ctx, SupportsImagesContextKey, true)

	t.Run("one image", func(t *testing.T) {
		t.Parallel()

		resp := runViewTool(t, tool, ctx, ViewParams{FilePaths: []string{first}})
		require.False(t, resp.IsError)
		require.Equal(t, "image/png", resp.MediaType)
		require.NotEmpty(t, resp.Data, "the image must stay attached")
		require.Contains(t, resp.Content, "==> "+first+" <==")
		require.Contains(t, resp.Content, "(image: image/png)")
		require.NotContains(t, resp.Content, "view this file on its own")
	})

	t.Run("later image becomes a note", func(t *testing.T) {
		t.Parallel()

		resp := runViewTool(t, tool, ctx, ViewParams{FilePaths: []string{first, second}})
		require.False(t, resp.IsError)
		require.Equal(t, "image/png", resp.MediaType)
		require.NotEmpty(t, resp.Data, "the first image must stay attached")
		require.Contains(t, resp.Content, "==> "+second+" <==\n(image: view this file on its own)")
	})
}

// A file the budget will push out must not be read at all: reading it would
// ask for permission, record it as read and load content nobody sees.
func TestViewToolMultiFileDoesNotReadFilesItSkips(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	outside := t.TempDir()
	paths := make([]string, 0, 3)
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		path := filepath.Join(outside, name)
		lines := make([]string, 300)
		for i := range lines {
			lines[i] = strings.Repeat("a", 100)
		}
		require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644))
		paths = append(paths, path)
	}

	permissions := &countingPermissionService{
		mockViewPermissionService: mockViewPermissionService{
			Broker: pubsub.NewBroker[permission.PermissionRequest](),
		},
	}
	tracker := &recordingFileTracker{}
	tool := NewViewTool(nil, permissions, tracker, nil, workingDir)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{FilePaths: paths})

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "==> "+paths[0]+" <==\n")
	require.NotContains(t, resp.Content, "==> "+paths[1]+" <==\n")
	require.Contains(t, resp.Content, "Not read, output budget reached: "+paths[1]+", "+paths[2]+".")

	require.Equal(t, []string{paths[0]}, tracker.recorded(),
		"only files that came back may be recorded as read")
	require.Len(t, permissions.requests, 2,
		"outside files are permission-checked before any sizing, but only until the budget stops the call")
	require.Equal(t, paths[0], permissions.requests[0].Path)
}

// Only the first DefaultReadLimit lines come back, so a long file whose raw
// size is large must not be skipped on that basis: the estimate is of the
// rendered section, not of the whole file.
func TestViewToolMultiFileReadsShortLineFileWithinBudget(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	outside := t.TempDir()
	first := filepath.Join(outside, "small.txt")
	require.NoError(t, os.WriteFile(first, []byte("small"), 0o644))

	manyShortLines := filepath.Join(outside, "many.txt")
	lines := make([]string, 4000)
	for i := range lines {
		lines[i] = "x"
	}
	require.NoError(t, os.WriteFile(manyShortLines, []byte(strings.Join(lines, "\n")), 0o644))

	permissions := &countingPermissionService{
		mockViewPermissionService: mockViewPermissionService{
			Broker: pubsub.NewBroker[permission.PermissionRequest](),
		},
	}
	tracker := &recordingFileTracker{}
	tool := NewViewTool(nil, permissions, tracker, nil, workingDir)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{FilePaths: []string{first, manyShortLines}})

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "==> "+first+" <==\n")
	require.Contains(t, resp.Content, "==> "+manyShortLines+" <==\n")
	require.NotContains(t, resp.Content, "Not read, output budget reached")
	require.Equal(t, []string{first, manyShortLines}, tracker.recorded())
	require.Len(t, permissions.requests, 2)
}

type mockViewPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
}

func (m *mockViewPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	return true, nil
}

func (m *mockViewPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *mockViewPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *mockViewPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *mockViewPermissionService) AutoApproveSession(sessionID string) {}

func (m *mockViewPermissionService) IsSessionAutoApproved(sessionID string) bool { return false }

func (m *mockViewPermissionService) InheritAutoApproval(parentSessionID, childSessionID string) {}

func (m *mockViewPermissionService) ActiveRequests() []permission.PermissionRequest {
	return nil
}

func (m *mockViewPermissionService) SetSessionScope(scope permission.SessionScope) {}

func (m *mockViewPermissionService) SetSkipRequests(skip bool) {}

func (m *mockViewPermissionService) SkipRequests() bool {
	return false
}

func (m *mockViewPermissionService) SetPermissionMode(mode permission.PermissionMode) {}

func (m *mockViewPermissionService) PermissionMode() permission.PermissionMode {
	return permission.PermissionModeNormal
}

func (m *mockViewPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func (m *mockViewPermissionService) SubscribeModeChanges(ctx context.Context) <-chan pubsub.Event[permission.ModeChangedEvent] {
	return make(<-chan pubsub.Event[permission.ModeChangedEvent])
}

// countingPermissionService records the permission requests a test made.
type countingPermissionService struct {
	mockViewPermissionService

	mu       sync.Mutex
	requests []permission.CreatePermissionRequest
}

func (c *countingPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	return true, nil
}

// recordingFileTracker records the paths a test marked as read.
type recordingFileTracker struct {
	mu    sync.Mutex
	paths []string
}

func (r *recordingFileTracker) RecordRead(ctx context.Context, sessionID, path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
}

func (r *recordingFileTracker) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.paths)
}

func (r *recordingFileTracker) LastReadTime(ctx context.Context, sessionID, path string) time.Time {
	return time.Time{}
}

func (r *recordingFileTracker) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return nil, nil
}

var _ filetracker.Service = (*recordingFileTracker)(nil)

type mockFileTracker struct{}

func (m mockFileTracker) RecordRead(ctx context.Context, sessionID, path string) {}

func (m mockFileTracker) LastReadTime(ctx context.Context, sessionID, path string) time.Time {
	return time.Time{}
}

func (m mockFileTracker) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return nil, nil
}

func newViewToolForTest(workingDir string) fantasy.AgentTool {
	permissions := &testutil.MockPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	return NewViewTool(nil, permissions, mockFileTracker{}, nil, workingDir)
}

func runViewTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params ViewParams) fantasy.ToolResponse {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{
		ID:    "test-call",
		Name:  ViewToolName,
		Input: string(input),
	}

	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	return resp
}

var _ filetracker.Service = mockFileTracker{}

func TestViewToolExpandsHomeDirectory(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(home.Dir(), "tilde-view.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello from home"), 0o644))

	tool := newViewToolForTest(t.TempDir())
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: "~/tilde-view.txt",
	})
	require.Contains(t, resp.Content, "hello from home")
}

func TestReadBuiltinFile(t *testing.T) {
	t.Parallel()

	t.Run("reads crush-config skill", func(t *testing.T) {
		t.Parallel()

		resp, err := readBuiltinFile(ViewParams{
			FilePath: "crush://skills/crush-config/SKILL.md",
		}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, resp.Content)
		require.Contains(t, resp.Content, "Crush Configuration")
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		resp, err := readBuiltinFile(ViewParams{
			FilePath: "crush://skills/nonexistent/SKILL.md",
		}, nil)
		require.NoError(t, err)
		require.True(t, resp.IsError)
	})

	t.Run("metadata has skill info", func(t *testing.T) {
		t.Parallel()

		resp, err := readBuiltinFile(ViewParams{
			FilePath: "crush://skills/crush-config/SKILL.md",
		}, nil)
		require.NoError(t, err)

		var meta ViewResponseMetadata
		require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
		require.Equal(t, ViewResourceSkill, meta.ResourceType)
		require.Equal(t, "crush-config", meta.ResourceName)
		require.NotEmpty(t, meta.ResourceDescription)
	})

	t.Run("respects offset", func(t *testing.T) {
		t.Parallel()

		resp, err := readBuiltinFile(ViewParams{
			FilePath: "crush://skills/crush-config/SKILL.md",
			Offset:   5,
		}, nil)
		require.NoError(t, err)
		require.NotContains(t, resp.Content, "     1|")
	})
}

func TestSniffImageMimeType(t *testing.T) {
	t.Parallel()

	jpegMagic := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	pngMagic := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	gifMagic := []byte("GIF89a")
	// Minimal RIFF/WEBP header.
	webpMagic := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 16)...)
	random := []byte("not an image at all, just text")

	cases := []struct {
		name     string
		data     []byte
		fallback string
		want     string
	}{
		{"jpeg bytes in .png file uses sniffed", jpegMagic, "image/png", "image/jpeg"},
		{"png bytes in .jpg file uses sniffed", pngMagic, "image/jpeg", "image/png"},
		{"gif bytes uses sniffed", gifMagic, "image/png", "image/gif"},
		{"webp bytes uses sniffed", webpMagic, "image/png", "image/webp"},
		{"matching extension and content keeps sniffed", pngMagic, "image/png", "image/png"},
		{"unsniffable content falls back", random, "image/png", "image/png"},
		{"empty content falls back", nil, "image/jpeg", "image/jpeg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, sniffImageMimeType(tc.data, tc.fallback))
		})
	}
}

// A path deleted along with its parent folder used to report only "File not
// found". Pointing at the nearest folder that still exists, and listing what
// it holds, is what lets the next try land.
func TestViewNotFoundSuggestsNearestFolder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "kept.go"), nil, 0o644))

	tool := newViewToolForTest(root)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filepath.Join(root, "deleted", "sub", "file.go"),
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, root)
	require.Contains(t, resp.Content, "kept.go")
}

// An oversized screenshot is shrunk to fit instead of refused, and the model
// is told what happened.
func TestViewToolShrinksOversizedImage(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "large.png")
	var buf bytes.Buffer
	// Small enough for the hard cap, large enough to need the shrink.
	require.NoError(t, png.Encode(&buf, noisyImage(1600, 1200)))
	require.NoError(t, os.WriteFile(filePath, buf.Bytes(), 0o644))
	require.Greater(t, buf.Len(), MaxViewSize)
	require.Less(t, buf.Len(), maxImageFileSize)

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	ctx = context.WithValue(ctx, SupportsImagesContextKey, true)
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
	})

	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "image", resp.Type)
	require.Equal(t, "image/jpeg", resp.MediaType)
	require.LessOrEqual(t, len(resp.Data), MaxViewSize)
	require.Contains(t, resp.Content, "Image shrunk from 1600x1200 to ")
}

// An image file past the hard cap is refused from its size alone: reading
// hundreds of megabytes to discover it is an image would stall the turn.
func TestViewToolRefusesImageAboveHardLimit(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "huge.png")
	require.NoError(t, os.WriteFile(filePath, bytes.Repeat([]byte{0xff}, maxImageFileSize+1), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	ctx = context.WithValue(ctx, SupportsImagesContextKey, true)
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Image file is too large")
	require.Contains(t, resp.Content, "26214400", "the message names the hard cap")
	require.NotContains(t, resp.Content, "could not be shrunk", "the file must be refused before it is read")
}
