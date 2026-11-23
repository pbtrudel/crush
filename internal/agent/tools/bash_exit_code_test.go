package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"mvdan.cc/sh/v3/interp"
)

func TestCommandOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantCode    *int
		interrupted bool
	}{
		{name: "success", err: nil, wantCode: new(0)},
		{name: "exit status", err: interp.ExitStatus(2), wantCode: new(2)},
		{name: "other error", err: errors.New("boom"), wantCode: new(1)},
		{name: "cancelled", err: context.Canceled, interrupted: true},
		{name: "deadline", err: fmt.Errorf("run: %w", context.DeadlineExceeded), interrupted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, interrupted := commandOutcome(tt.err)
			require.Equal(t, tt.wantCode, code)
			require.Equal(t, tt.interrupted, interrupted)
		})
	}
}

func TestBashTool_ExitCodeInMetadata(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	tests := []struct {
		name   string
		params BashParams
		want   int64
	}{
		{name: "success", params: BashParams{Description: "succeeds", Command: "echo ok"}, want: 0},
		{name: "failure", params: BashParams{Description: "fails", Command: "exit 3"}, want: 3},
		{name: "background command finished in the probe window", params: BashParams{Description: "fails fast", Command: "exit 4", RunInBackground: true}, want: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := runBashTool(t, tool, ctx, tt.params)
			if shellID := gjson.Get(resp.Metadata, "shell_id").String(); shellID != "" {
				defer shell.GetBackgroundShellManager().Kill(context.Background(), shellID)
			}

			code := gjson.Get(resp.Metadata, "exit_code")
			require.True(t, code.Exists(), "a finished command records its exit code, zero included: %s", resp.Metadata)
			require.Equal(t, tt.want, code.Int())
			require.False(t, gjson.Get(resp.Metadata, "interrupted").Exists())
			if tt.want != 0 {
				require.Contains(t, resp.Content, fmt.Sprintf("Exit code %d", tt.want), "the text the model reads is unchanged")
			}
		})
	}
}

func TestBashTool_MovedToBackgroundHasNoExitCode(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:         "moved to background",
		Command:             "sleep 1.5 && exit 5",
		AutoBackgroundAfter: 1,
	})

	require.Contains(t, resp.Content, "moved to background")
	require.False(t, gjson.Get(resp.Metadata, "exit_code").Exists(), "a background job reports through its notice")
	require.False(t, gjson.Get(resp.Metadata, "interrupted").Exists())
	require.NoError(t, shell.GetBackgroundShellManager().Kill(t.Context(), gjson.Get(resp.Metadata, "shell_id").String()))
}

func TestBashTool_CancelledCommandIsInterrupted(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), SessionIDContextKey, "test-session"))
	time.AfterFunc(300*time.Millisecond, cancel)

	input, err := json.Marshal(BashParams{Description: "cancelled", Command: "sleep 5"})
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "test-call", Name: BashToolName, Input: string(input)})

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, resp.Content, "the model reads the error, as before")
	require.True(t, gjson.Get(resp.Metadata, "interrupted").Bool(), "a cancelled command is recorded as aborted: %s", resp.Metadata)
	require.False(t, gjson.Get(resp.Metadata, "exit_code").Exists())
}
