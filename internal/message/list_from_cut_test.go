package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListFromCut(t *testing.T) {
	t.Parallel()

	svc, sessionID := newTestService(t)

	ids := make([]string, 0, 5)
	for range 5 {
		msg, err := svc.Create(t.Context(), sessionID, CreateMessageParams{
			Role:  User,
			Parts: []ContentPart{TextContent{Text: "hi"}},
		})
		require.NoError(t, err)
		ids = append(ids, msg.ID)
	}

	t.Run("empty cut returns the whole session", func(t *testing.T) {
		t.Parallel()

		got, err := svc.ListFromCut(t.Context(), sessionID, "")
		require.NoError(t, err)
		require.Len(t, got, len(ids))
	})

	// Unlike ListFromSummary, the cut query orders by insertion (rowid),
	// so same-second messages cannot slip ahead of the cut point.
	t.Run("returns the cut message and everything after it in order", func(t *testing.T) {
		t.Parallel()

		got, err := svc.ListFromCut(t.Context(), sessionID, ids[2])
		require.NoError(t, err)
		require.Len(t, got, 3)
		for i, id := range ids[2:] {
			require.Equal(t, id, got[i].ID)
		}
	})

	// The caller falls back to the summary-only view when the cut is
	// gone, so an empty result is the correct signal here.
	t.Run("dangling cut returns nothing", func(t *testing.T) {
		t.Parallel()

		got, err := svc.ListFromCut(t.Context(), sessionID, "does-not-exist")
		require.NoError(t, err)
		require.Empty(t, got)
	})
}
