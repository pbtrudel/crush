package history_test

import (
	"testing"

	"github.com/charmbracelet/crush/internal/history"
	"github.com/stretchr/testify/require"
)

// TestCreateVersionRecordsTheToolCall pins the drawer's link from a file
// version to the tool call that produced it: a version written with a
// tagged context carries the id, a version without one carries none, and
// both the returned file and the metadata list report it.
func TestCreateVersionRecordsTheToolCall(t *testing.T) {
	t.Parallel()
	svc, sessionID := newHistoryTestService(t)
	ctx := t.Context()
	_, err := svc.Create(ctx, sessionID, "a.go", "")
	require.NoError(t, err)
	f, err := svc.CreateVersion(history.WithToolCallID(ctx, "tc-7"), sessionID, "a.go", "package a")
	require.NoError(t, err)
	require.Equal(t, "tc-7", f.ToolCallID)
	metas, err := svc.ListMetaBySession(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, []string{"", "tc-7"}, []string{metas[0].ToolCallID, metas[1].ToolCallID})
}

// TestWithToolCallIDIgnoresAnEmptyID pins that an empty id leaves the
// context unchanged, so a caller that passes "" cannot tag a version with
// an empty tool call.
func TestWithToolCallIDIgnoresAnEmptyID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	require.Same(t, ctx, history.WithToolCallID(ctx, ""))
	require.NotSame(t, ctx, history.WithToolCallID(ctx, "tc-7"))
}
