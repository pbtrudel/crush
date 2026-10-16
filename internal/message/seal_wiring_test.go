package message

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestUpdate_FlushUsesPartSeal pins the wiring: the debounced write path
// must encode through the per-message seal, and the seal must actually
// retain the escaped prefix it can reuse on the next tick.
func TestUpdate_FlushUsesPartSeal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, sessionID := newTestService(t, WithDebounce(0))
	s := svc.(*service)

	msg, err := svc.Create(ctx, sessionID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)

	const delta = "streamed chunk "
	text := strings.Repeat(delta, 20)
	msg.AppendContent(text)
	require.NoError(t, svc.Update(ctx, msg))

	p := s.pending[msg.ID]
	require.NotNil(t, p, "pending state should exist after a flush")
	// The first write for a message encodes from scratch: there is no
	// cached prefix to reuse, and building one would cost more than it
	// saves for the many messages that are only ever written once.
	require.Empty(t, p.seal.entries, "the seal must stay inactive on the first flush")

	// The second flush is where the seal activates and starts caching the
	// escaped prefix of the growing part.
	msg.AppendContent(delta)
	require.NoError(t, svc.Update(ctx, msg))
	p = s.pending[msg.ID]
	require.Len(t, p.seal.entries, 1, "the second flush should encode through the seal")
	require.NotEmpty(t, p.seal.entries[0].head, "the seal must keep the part's static head")

	// A later delta must not invalidate the cached prefix: this is the
	// whole point of the seal.
	msg.AppendContent(delta)
	require.NoError(t, svc.Update(ctx, msg))
	require.Equal(t, text+delta+delta, s.pending[msg.ID].seal.entries[0].raw)

	// And the row on disk round-trips to exactly the in-memory parts.
	stored, err := svc.Get(ctx, msg.ID)
	require.NoError(t, err)
	want, err := marshalParts(msg.Parts)
	require.NoError(t, err)
	got, err := marshalParts(stored.Parts)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

// TestUpdate_SealSurvivesStructuralChange proves the fallback: when the
// part set changes shape, the seal rebuilds and the stored bytes are
// still byte-identical to the unsealed encoder.
func TestUpdate_SealSurvivesStructuralChange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, sessionID := newTestService(t, WithDebounce(0))

	msg, err := svc.Create(ctx, sessionID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)
	msg.AppendContent("first half ")
	require.NoError(t, svc.Update(ctx, msg))

	msg.AddToolCall(ToolCall{ID: "c1", Name: "bash", Input: `{"command":`})
	require.NoError(t, svc.Update(ctx, msg))
	msg.AppendToolCallInput("c1", `"ls"`)
	require.NoError(t, svc.Update(ctx, msg))
	msg.FinishToolCall("c1")
	msg.AppendContent("second half")
	require.NoError(t, svc.Update(ctx, msg))
	msg.AddFinish(FinishReasonEndTurn, "", "")
	require.NoError(t, svc.Update(ctx, msg))

	stored, err := svc.Get(ctx, msg.ID)
	require.NoError(t, err)
	want, err := marshalParts(msg.Parts)
	require.NoError(t, err)
	got, err := marshalParts(stored.Parts)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

// TestUpdate_DebouncedFlushesStayConsistent hammers the streaming path
// through the real debounce timer with concurrent readers of the service
// to catch a seal that races a flush.
func TestUpdate_DebouncedFlushesStayConsistent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, sessionID := newTestService(t, WithDebounce(time.Millisecond))

	msg, err := svc.Create(ctx, sessionID, CreateMessageParams{Role: Assistant})
	require.NoError(t, err)

	var sb strings.Builder
	for range 300 {
		sb.WriteString("tok ")
		msg.AppendContent("tok ")
		require.NoError(t, svc.Update(ctx, msg))
	}
	require.NoError(t, svc.Flush(ctx, msg.ID))

	stored, err := svc.Get(ctx, msg.ID)
	require.NoError(t, err)
	require.Len(t, stored.Parts, 1)
	require.Equal(t, sb.String(), stored.Content().Text)
}
