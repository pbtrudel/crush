package webtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/signin"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
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

// TestFakeListSessionsNewestFirst pins the fake's order to the gateway's:
// newest updated first, with the ID breaking ties. The fake iterates a
// map, so without this it would hand back a random order.
func TestFakeListSessionsNewestFirst(t *testing.T) {
	t.Parallel()
	f := NewFake()
	f.AddSession(session.Session{ID: "s2", UpdatedAt: 10})
	f.AddSession(session.Session{ID: "s3", UpdatedAt: 20})
	f.AddSession(session.Session{ID: "s0", UpdatedAt: 20})

	list, err := f.ListSessions(t.Context())
	require.NoError(t, err)
	ids := make([]string, len(list))
	for i, s := range list {
		ids[i] = s.ID
	}
	require.Equal(t, []string{"s0", "s3", "s2", "s1"}, ids)
}

// TestFakeListSessionsSkipsChildren pins the fake's list to the real
// service's: ListSessions answers top-level sessions only, so a subagent's
// session never reaches the sidebar. GetSession still finds the child,
// which is what a subscribe needs.
func TestFakeListSessionsSkipsChildren(t *testing.T) {
	t.Parallel()
	f := NewFake()
	f.AddSession(session.Session{ID: "child", ParentSessionID: "s1", Title: "Sub"})

	list, err := f.ListSessions(t.Context())
	require.NoError(t, err)
	for _, s := range list {
		require.NotEqual(t, "child", s.ID, "a child session leaked into the list")
	}

	child, err := f.GetSession(t.Context(), "child")
	require.NoError(t, err)
	require.Equal(t, "s1", child.ParentSessionID)
}

// TestFakeCreateSessionIDsNeverRepeat pins that deleting a session cannot
// make the next created ID collide with one still in the map.
func TestFakeCreateSessionIDsNeverRepeat(t *testing.T) {
	t.Parallel()
	f := NewFake()
	first, err := f.CreateSession(t.Context(), "one")
	require.NoError(t, err)
	require.NoError(t, f.DeleteSession(t.Context(), first.ID))
	second, err := f.CreateSession(t.Context(), "two")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	_, err = f.GetSession(t.Context(), "s1")
	require.NoError(t, err, "the default session survives")
}

// TestFakeFlowWaitReturnsNextWithoutBlocking pins that a fallback
// challenge reaches the caller at once, once, and that the following
// Wait waits for the flow to finish.
func TestFakeFlowWaitReturnsNextWithoutBlocking(t *testing.T) {
	t.Parallel()
	f := NewFakeFlow(t)
	f.Next = &signin.Challenge{UserCode: "NEXT"}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tok, next, err := f.Wait(ctx, signin.Challenge{})
	require.NoError(t, err)
	require.Nil(t, tok)
	require.NotNil(t, next)
	require.Equal(t, "NEXT", next.UserCode)

	type waitResult struct {
		tok  *oauth.Token
		next *signin.Challenge
		err  error
	}
	res := make(chan waitResult, 1)
	go func() {
		tok, next, err := f.Wait(context.Background(), signin.Challenge{})
		res <- waitResult{tok: tok, next: next, err: err}
	}()
	select {
	case <-res:
		t.Fatal("the second Wait returned before Finish")
	case <-time.After(50 * time.Millisecond):
	}
	f.Finish()
	select {
	case got := <-res:
		require.NoError(t, got.err)
		require.Nil(t, got.next)
		require.Nil(t, got.tok)
	case <-time.After(time.Second):
		t.Fatal("Wait kept blocking after Finish")
	}
}

// TestFakeFlowFinishIsIdempotent pins that a second Finish, which is the
// t.Cleanup call in most tests, does not panic. Close is idempotent too:
// a finished sign-in and Shutdown both close the flow.
func TestFakeFlowFinishIsIdempotent(t *testing.T) {
	t.Parallel()
	f := NewFakeFlow(t)
	f.Finish()
	f.Finish()
	require.False(t, f.Closed())
	f.Close()
	f.Close()
	require.True(t, f.Closed())
}

// TestFakeFlowForFinishesWhenDoneCloses pins the constructor webfake uses,
// which has no *testing.T to register a cleanup with.
func TestFakeFlowForFinishesWhenDoneCloses(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	f := NewFakeFlowFor(done)
	f.Token = &oauth.Token{AccessToken: "tok-done"}
	close(done)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	tok, next, err := f.Wait(ctx, signin.Challenge{})
	require.NoError(t, err)
	require.Nil(t, next)
	require.Equal(t, "tok-done", tok.AccessToken)
}
