package webtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/stretchr/testify/require"
)

func TestScriptedPlaysAReply(t *testing.T) {
	t.Parallel()
	f := NewFake()
	events := f.SubscribeEvents(t.Context())
	require.NoError(t, Scripted(f)(t.Context(), "s1", "hello", nil))
	var last message.Message
	var sess session.Session
	for len(events) > 0 {
		switch e := (<-events).(type) {
		case pubsub.Event[message.Message]:
			last = e.Payload
		case pubsub.Event[session.Session]:
			sess = e.Payload
		}
	}
	require.Contains(t, last.Content().Text, "Signal received. hello")
	require.EqualValues(t, 1200, sess.PromptTokens)
}

func TestScriptedIgnoresStaleDecision(t *testing.T) {
	t.Parallel()
	f := NewFake()
	// A grant left over from an earlier run.
	f.PermissionGrant(permission.PermissionRequest{ID: "old"})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { done <- Scripted(f)(ctx, "s1", "run permission", nil) }()
	select {
	case <-done:
		t.Fatal("a stale grant resolved the new request")
	case <-time.After(400 * time.Millisecond):
	}
	f.PermissionGrant(permission.PermissionRequest{ID: "new"})
	require.NoError(t, <-done)
}

func TestSeedHolodeckWritesTeamFiles(t *testing.T) {
	t.Parallel()
	f := NewFake()
	dir := t.TempDir()
	require.NoError(t, SeedHolodeck(f, dir, time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)))
	children, err := f.ListChildSessions(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, children, 2)
	files, err := f.ListSessionHistory(t.Context(), "team-m-1")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	s, err := f.GetSession(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, s.Todos, 3)
	team, err := os.ReadFile(filepath.Join(dir, "team.json"))
	require.NoError(t, err)
	require.True(t, strings.Contains(string(team), `"alice"`))
}

// TestScriptedDelegates pins the delegation prompt: the subagent starts an
// edit call, writes version 3 of parser.go past the seeded versions and
// publishes both the message and the file.
func TestScriptedDelegates(t *testing.T) {
	t.Parallel()
	f := NewFake()
	require.NoError(t, SeedHolodeck(f, t.TempDir(), time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)))
	events := f.SubscribeEvents(t.Context())
	require.NoError(t, Scripted(f)(t.Context(), "s1", "delegate the tokenizer", nil))

	var fileEvent *history.File
	var call message.ToolCall
	for len(events) > 0 {
		switch e := (<-events).(type) {
		case pubsub.Event[history.File]:
			file := e.Payload
			fileEvent = &file
		case pubsub.Event[message.Message]:
			for _, part := range e.Payload.Parts {
				if c, ok := part.(message.ToolCall); ok && c.Name == "edit" {
					call = c
				}
			}
		}
	}
	require.Equal(t, "edit", call.Name)
	require.NotNil(t, fileEvent)
	require.Equal(t, "sub-1", fileEvent.SessionID)
	require.Equal(t, f.parserPath(), fileEvent.Path)
	require.EqualValues(t, 3, fileEvent.Version)

	files, err := f.ListSessionHistory(t.Context(), "sub-1")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, f.parserPath(), files[0].Path)
	require.EqualValues(t, 3, files[0].Version)
	require.Greater(t, files[0].CreatedAt, int64(0))
}
