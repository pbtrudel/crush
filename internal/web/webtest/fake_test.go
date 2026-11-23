package webtest

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

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
