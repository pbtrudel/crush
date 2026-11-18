package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNewPermissionDeniedResponseKind(t *testing.T) {
	t.Parallel()

	resp := NewPermissionDeniedResponse()
	require.True(t, resp.IsError)
	require.True(t, resp.StopTurn)
	require.Equal(t, "User denied permission", resp.Content, "the text the model reads is unchanged")
	require.Equal(t, `{"error_kind":"permission_denied"}`, resp.Metadata)
}

func TestBashPermissionDeniedKind(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, false)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{Description: "denied", Command: "touch newfile"})
	require.Equal(t, 1, perms.requestCount)
	require.Equal(t, "User denied permission", resp.Content)
	require.Equal(t, string(ErrorKindPermissionDenied), gjson.Get(resp.Metadata, "error_kind").String())
}

// These six denials attach the tool's own metadata, which replaces the
// metadata whole, so each one is pinned here.
func TestPermissionDeniedKindSurvivesToolMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ownKey  string
		respond func(t *testing.T, edit editContext, existing, missing string) (fantasy.ToolResponse, error)
	}{
		{
			name:   "edit, create",
			ownKey: "new_content",
			respond: func(t *testing.T, edit editContext, _, missing string) (fantasy.ToolResponse, error) {
				return createNewFile(edit, missing, "gamma\n", fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "edit, delete",
			ownKey: "old_content",
			respond: func(t *testing.T, edit editContext, existing, _ string) (fantasy.ToolResponse, error) {
				return deleteContent(edit, existing, "beta\n", false, fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "edit, replace",
			ownKey: "new_content",
			respond: func(t *testing.T, edit editContext, existing, _ string) (fantasy.ToolResponse, error) {
				return replaceContent(edit, existing, "beta", "BETA", false, fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "multiedit, create",
			ownKey: "edits_applied",
			respond: func(t *testing.T, edit editContext, _, missing string) (fantasy.ToolResponse, error) {
				return processMultiEditWithCreation(edit, MultiEditParams{
					FilePath: missing,
					Edits:    []MultiEditOperation{{OldString: "", NewString: "gamma\n"}},
				}, fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "multiedit, existing file",
			ownKey: "edits_applied",
			respond: func(t *testing.T, edit editContext, existing, _ string) (fantasy.ToolResponse, error) {
				return processMultiEditExistingFile(edit, MultiEditParams{
					FilePath: existing,
					Edits:    []MultiEditOperation{{OldString: "beta", NewString: "BETA"}},
				}, fantasy.ToolCall{ID: "call"})
			},
		},
		{
			name:   "write",
			ownKey: "diff",
			respond: func(t *testing.T, edit editContext, existing, _ string) (fantasy.ToolResponse, error) {
				tool := NewWriteTool(nil, edit.permissions, edit.files, edit.filetracker, edit.workingDir)
				input, err := json.Marshal(WriteParams{FilePath: existing, Content: "gamma\n"})
				require.NoError(t, err)
				return tool.Run(edit.ctx, fantasy.ToolCall{ID: "call", Name: WriteToolName, Input: string(input)})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			existing := filepath.Join(dir, "a.txt")
			require.NoError(t, os.WriteFile(existing, []byte("alpha\nbeta\n"), 0o644))
			missing := filepath.Join(dir, "new.txt")
			edit := editContextForKind(t, dir, time.Now().Add(time.Second))
			edit.permissions = &mockPermissionService{deny: true}

			resp, err := tt.respond(t, edit, existing, missing)
			require.NoError(t, err)
			require.True(t, resp.IsError)
			require.True(t, resp.StopTurn)
			require.Equal(t, "User denied permission", resp.Content, "the text the model reads is unchanged")
			require.Equal(t, string(ErrorKindPermissionDenied), gjson.Get(resp.Metadata, "error_kind").String())
			require.True(t, gjson.Get(resp.Metadata, tt.ownKey).Exists(), "the tool's own metadata is kept: %s", resp.Metadata)

			content, err := os.ReadFile(existing)
			require.NoError(t, err)
			require.Equal(t, "alpha\nbeta\n", string(content), "a denied call changes nothing")
			require.NoFileExists(t, missing)
		})
	}
}
