package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// TestReadToolAliasesView pins the read alias: it must advertise the same
// description as view and produce the same response for the same input, so
// models that call "read" get a working file read instead of an unknown-tool
// error.
func TestReadToolAliasesView(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("line 1\nline 2"), 0o644))

	permissions := &mockViewPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	tool := NewReadTool(nil, permissions, mockFileTracker{}, nil, tmpDir)

	require.Equal(t, "read", tool.Info().Name)
	require.Equal(t, viewDescription(), tool.Info().Description,
		"the read alias must advertise the same behavior as view")

	input, err := json.Marshal(ViewParams{FilePath: filePath})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "read-call", Name: ReadToolName, Input: string(input)})
	require.NoError(t, err)
	require.Contains(t, resp.Content, "line 2")
}

// TestReadToolPermissionRequestUsesAliasName pins that the outside-workdir
// permission prompt names the tool the model actually called, so the dialog
// and any persisted grant refer to "read" rather than "view".
func TestReadToolPermissionRequestUsesAliasName(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("outside"), 0o644))

	permissions := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  true,
	}
	tool := NewReadTool(nil, permissions, mockFileTracker{}, nil, workingDir)

	input, err := json.Marshal(ViewParams{FilePath: outsideFile})
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	_, err = tool.Run(ctx, fantasy.ToolCall{ID: "read-call", Name: ReadToolName, Input: string(input)})
	require.NoError(t, err)

	require.Equal(t, 1, permissions.requestCount)
	require.Equal(t, ReadToolName, permissions.lastRequest.ToolName)
}
