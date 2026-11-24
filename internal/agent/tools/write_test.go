package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/home"
	"github.com/charmbracelet/crush/internal/permission/testutil"
	"github.com/stretchr/testify/require"
)

type mockFileTrackerService struct{}

func (m mockFileTrackerService) RecordRead(ctx context.Context, sessionID, path string) {}

func (m mockFileTrackerService) LastReadTime(ctx context.Context, sessionID, path string) time.Time {
	return time.Now()
}

func (m mockFileTrackerService) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return nil, nil
}

func TestWriteToolWritesEmptyNewFile(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tool := NewWriteTool(nil, &testutil.MockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, workingDir)

	input, err := json.Marshal(WriteParams{FilePath: "empty.txt", Content: ""})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  WriteToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	b, err := os.ReadFile(filepath.Join(workingDir, "empty.txt"))
	require.NoError(t, err)
	require.Equal(t, "", string(b))
}

func TestWriteToolExpandsHomeDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	tool := NewWriteTool(nil, &mockPermissionService{}, &mockHistoryService{}, mockFileTrackerService{}, t.TempDir())

	input, err := json.Marshal(WriteParams{FilePath: "~/tilde-write.txt", Content: "home content"})
	require.NoError(t, err)

	resp, err := tool.Run(ctx, fantasy.ToolCall{
		ID:    "test-call",
		Name:  WriteToolName,
		Input: string(input),
	})
	require.NoError(t, err)
	require.False(t, resp.IsError)

	b, err := os.ReadFile(filepath.Join(home.Dir(), "tilde-write.txt"))
	require.NoError(t, err)
	require.Equal(t, "home content", string(b))
}

func TestWriteRefusalShowsFileAndRecordsRead(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "f.go")
	require.NoError(t, os.WriteFile(filePath, []byte("alpha\nbeta\n"), 0o644))

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	tracker := &refusalTracker{}
	tool := NewWriteTool(nil, &testutil.MockPermissionService{}, &mockHistoryService{}, tracker, workingDir)

	write := func() fantasy.ToolResponse {
		input, err := json.Marshal(WriteParams{FilePath: filePath, Content: "gamma\n"})
		require.NoError(t, err)
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "test-call", Name: WriteToolName, Input: string(input)})
		require.NoError(t, err)
		return resp
	}

	resp := write()
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "has not been read in this session")
	require.Contains(t, resp.Content, "    1|alpha")
	require.Equal(t, []string{filePath}, tracker.reads)

	resp = write()
	require.False(t, resp.IsError, resp.Content)
	b, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "gamma\n", string(b))
}

func TestWriteRefusalOfAPartialWindowStaysRefused(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "big.go")
	var lines []string
	for i := 1; i <= 1000; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(lines, "\n")+"\n"), 0o644))

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	tracker := &refusalTracker{}
	tool := NewWriteTool(nil, &testutil.MockPermissionService{}, &mockHistoryService{}, tracker, workingDir)

	write := func() fantasy.ToolResponse {
		input, err := json.Marshal(WriteParams{FilePath: filePath, Content: "gamma\n"})
		require.NoError(t, err)
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "test-call", Name: WriteToolName, Input: string(input)})
		require.NoError(t, err)
		return resp
	}

	resp := write()
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "has not been read in this session")
	require.Contains(t, resp.Content, "this shows lines 1-200 of 1000; view the rest, or use edit for a targeted change")
	require.Empty(t, tracker.reads, "a partial window must not stand in for the whole file")

	resp = write()
	require.True(t, resp.IsError, "the write must stay refused until the rest is read too")
}

// Write refuses before it asks for any permission, so a refusal for a file
// outside the working directory must not show the file or record a read.
func TestWriteRefusalOutsideWorkingDirShowsNoContent(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("token=abc\n"), 0o644))

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	tracker := &refusalTracker{}
	tool := NewWriteTool(nil, &testutil.MockPermissionService{}, &mockHistoryService{}, tracker, workingDir)
	input, err := json.Marshal(WriteParams{FilePath: outside, Content: "x\n"})
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "test-call", Name: WriteToolName, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.NotContains(t, resp.Content, "token=abc")
	require.Empty(t, tracker.reads)
}
