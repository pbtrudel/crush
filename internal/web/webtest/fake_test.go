package webtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"

	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// Empty fake collections must encode as {} and [] rather than null so a
// gateway answer matches the real implementations.
func TestFakeEmptyCollectionsEncodeAsEmptyJSON(t *testing.T) {
	t.Parallel()
	f := NewFake()

	children, err := f.ListChildSessions(t.Context(), "s1")
	require.NoError(t, err)
	require.NotNil(t, children)
	encoded, err := json.Marshal(children)
	require.NoError(t, err)
	require.Equal(t, "[]", string(encoded))

	diags := f.LSPDiagnosticsByFile(t.Context())
	require.NotNil(t, diags)
	encoded, err = json.Marshal(diags)
	require.NoError(t, err)
	require.Equal(t, "{}", string(encoded))
}

// TestEmitRacesCancel runs Emit while subscribers are cancelled; -race
// catches a send on a channel the cleanup closes.
func TestEmitRacesCancel(t *testing.T) {
	t.Parallel()
	f := NewFake()
	var wg sync.WaitGroup
	for range 50 {
		ctx, cancel := context.WithCancel(t.Context())
		ch := f.SubscribeEvents(ctx)
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range ch {
			}
		}()
		go func() {
			defer wg.Done()
			f.Emit(pubsub.Event[message.Message]{Type: pubsub.CreatedEvent})
			cancel()
		}()
	}
	wg.Wait()
}

// TestFakeHistoryMeta pins the fake's metadata projection: sizes come
// from the stored content and a missing version wraps sql.ErrNoRows.
func TestFakeHistoryMeta(t *testing.T) {
	t.Parallel()
	f := NewFake()
	f.AddHistory(history.File{ID: "h1", SessionID: "s1", Path: "/a", Content: "abc", Version: 0})

	metas, err := f.ListSessionHistoryMeta(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, []history.FileMeta{{ID: "h1", SessionID: "s1", Path: "/a", Size: 3}}, metas)

	got, err := f.GetHistoryVersion(t.Context(), "h1")
	require.NoError(t, err)
	require.Equal(t, "abc", got.Content)

	_, err = f.GetHistoryVersion(t.Context(), "nope")
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.Contains(t, f.CallLog(), "GetHistoryVersion:h1")
}

// TestFakeAgentAddNoteRecords pins the call log entry gateway tests
// assert on: the note text reaches the fake's log unchanged.
func TestFakeAgentAddNoteRecords(t *testing.T) {
	t.Parallel()
	f := NewFake()
	require.NoError(t, f.AgentAddNote(t.Context(), "s1", "prefer the small model"))
	require.Equal(t, []string{"AgentAddNote:s1:prefer the small model"}, f.CallLog())
}
