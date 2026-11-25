package dialog

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestOAuthFlowStartingACommandCancelsThePrevious pins that a poll and a
// code submit never run side by side: the second command cancels the
// first, so a dismiss after a failed attempt stops every goroutine instead
// of leaving the older one polling until it answers, and the older
// command's answer is dropped.
func TestOAuthFlowStartingACommandCancelsThePrevious(t *testing.T) {
	t.Parallel()
	flow := &fakeFlow{entered: make(chan struct{}, 2)}
	m := newOAuthFlow(flow, 0)

	polled := make(chan any, 1)
	go func() { polled <- m.startPolling("dc", 60)() }()
	<-flow.entered
	pollCtx := flow.waitCtx
	require.NoError(t, pollCtx.Err(), "the poll starts on a live context")

	submitted := make(chan any, 1)
	go func() { submitted <- m.submitCode("code")() }()
	<-flow.entered
	require.ErrorIs(t, pollCtx.Err(), context.Canceled,
		"a starting submit cancels the poll it replaces")
	select {
	case msg := <-polled:
		require.Nil(t, msg, "a cancelled poll reports nothing")
	case <-time.After(time.Second):
		t.Fatal("the replaced poll never returned")
	}

	// The submit owns the cancel func now, so a dismiss stops it.
	m.stopPolling()
	select {
	case msg := <-submitted:
		require.Nil(t, msg, "a cancelled submit reports nothing")
	case <-time.After(time.Second):
		t.Fatal("the dismiss did not cancel the submit")
	}
}
