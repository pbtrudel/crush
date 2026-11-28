package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestListUnfinishedAssistantsReturnsOnlyThoseRows pins the first step of a
// settle pass: only assistant rows that never finished, never a summary and
// never a tool row.
func TestListUnfinishedAssistantsReturnsOnlyThoseRows(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)

	unfinished, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  Assistant,
		Parts: []ContentPart{ToolCall{ID: "call_open", Name: "bash", Input: "{}", Finished: true}},
	})
	require.NoError(t, err)
	// A user turn and its tool reply are never settle candidates.
	_, err = svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  User,
		Parts: []ContentPart{TextContent{Text: "run it"}},
	})
	require.NoError(t, err)
	_, err = svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  Tool,
		Parts: []ContentPart{ToolResult{ToolCallID: "call_open", Name: "bash", Content: "ok"}},
	})
	require.NoError(t, err)
	// A turn that finished is not a candidate either. Update is what writes
	// finished_at, so this is the only way a row leaves the set.
	finished, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role: Assistant,
		Parts: []ContentPart{
			ToolCall{ID: "call_done", Name: "bash", Input: "{}", Finished: true},
			Finish{Reason: FinishReasonEndTurn, Time: 10},
		},
	})
	require.NoError(t, err)
	require.NoError(t, svc.Update(t.Context(), finished))
	require.NoError(t, svc.FlushAll(t.Context()))

	got, err := svc.ListUnfinishedAssistants(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, []string{unfinished.ID}, messageIDs(got))
}

// TestListToolMessagesFromBoundsTheRead pins the second step: only the tool
// rows created at or after the bound come back, and same-second rows are not
// lost to created_at's one-second resolution.
func TestListToolMessagesFromBoundsTheRead(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)
	assistant, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  Assistant,
		Parts: []ContentPart{ToolCall{ID: "call_open", Name: "bash", Input: "{}", Finished: true}},
	})
	require.NoError(t, err)

	older, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  Tool,
		Parts: []ContentPart{ToolResult{ToolCallID: "call_old", Name: "bash", Content: "old"}},
	})
	require.NoError(t, err)
	newer, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
		Role:  Tool,
		Parts: []ContentPart{ToolResult{ToolCallID: "call_new", Name: "bash", Content: "new"}},
	})
	require.NoError(t, err)

	// Both rows were created in the same second as the assistant row, so
	// the bound has to be inclusive to keep them.
	got, err := svc.ListToolMessagesFrom(t.Context(), sessionID, assistant.CreatedAt)
	require.NoError(t, err)
	require.Equal(t, []string{older.ID, newer.ID}, messageIDs(got))

	// A row before the bound is out of reach of this pass.
	got, err = svc.ListToolMessagesFrom(t.Context(), sessionID, newer.CreatedAt+1)
	require.NoError(t, err)
	require.Empty(t, got)
}
