package db

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestQueries opens a fresh database in a temp dir for CRUD tests.
func newTestQueries(t *testing.T) (*Queries, *sql.DB) {
	t.Helper()
	conn, err := Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return New(conn), conn
}

// setSessionCreatedAt pins a session's created_at so stats queries keyed on
// date buckets are deterministic.
func setSessionCreatedAt(t *testing.T, conn *sql.DB, id string, unix int64) {
	t.Helper()
	_, err := conn.ExecContext(t.Context(),
		`UPDATE sessions SET created_at = ?, updated_at = ? WHERE id = ?`, unix, unix, id)
	require.NoError(t, err)
}

func TestSessionCRUD(t *testing.T) {
	t.Parallel()
	q, conn := newTestQueries(t)
	ctx := t.Context()

	created, err := q.CreateSession(ctx, CreateSessionParams{
		ID:               "s1",
		Title:            "first",
		MessageCount:     2,
		PromptTokens:     100,
		CompletionTokens: 50,
		Cost:             0.5,
	})
	require.NoError(t, err)
	assert.Equal(t, "s1", created.ID)
	assert.Equal(t, "first", created.Title)
	assert.NotZero(t, created.CreatedAt)

	got, err := q.GetSessionByID(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)

	_, err = q.GetSessionByID(ctx, "missing")
	require.ErrorIs(t, err, sql.ErrNoRows)

	_, err = q.CreateSession(ctx, CreateSessionParams{ID: "s2", Title: "second"})
	require.NoError(t, err)
	// created_at has one-second resolution; pin explicit values so
	// GetLastSession's ordering is deterministic.
	setSessionCreatedAt(t, conn, "s1", 1700000000)
	setSessionCreatedAt(t, conn, "s2", 1700000100)

	sessions, err := q.ListSessions(ctx)
	require.NoError(t, err)
	require.Len(t, sessions, 2)

	last, err := q.GetLastSession(ctx)
	require.NoError(t, err)
	// The update_sessions_updated_at trigger forces updated_at to "now" on
	// every update, so both sessions end up in the same second and the
	// ordering between them is undefined; only membership can be asserted.
	assert.Contains(t, []string{"s1", "s2"}, last.ID)

	require.NoError(t, q.RenameSession(ctx, RenameSessionParams{ID: "s1", Title: "renamed"}))
	renamed, err := q.GetSessionByID(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "renamed", renamed.Title)

	channel, err := q.SetSessionChannel(ctx, SetSessionChannelParams{ID: "s1", Channel: sql.NullString{String: "chan", Valid: true}})
	require.NoError(t, err)
	require.NotNil(t, channel.Channel)
	assert.Equal(t, "chan", channel.Channel.String)

	channel, err = q.SetSessionChannel(ctx, SetSessionChannelParams{ID: "s1", Channel: sql.NullString{}})
	require.NoError(t, err)
	assert.False(t, channel.Channel.Valid, "channel should be clearable")

	todos := sql.NullString{String: `[]`, Valid: true}
	summary := sql.NullString{String: "m1", Valid: true}
	updated, err := q.UpdateSession(ctx, UpdateSessionParams{
		ID:               "s1",
		Title:            "updated",
		PromptTokens:     10,
		CompletionTokens: 20,
		Cost:             0.25,
		Todos:            todos,
		SummaryMessageID: summary,
	})
	require.NoError(t, err)
	assert.Equal(t, "updated", updated.Title)
	assert.Equal(t, int64(10), updated.PromptTokens)
	assert.Equal(t, int64(20), updated.CompletionTokens)
	assert.Equal(t, 0.25, updated.Cost)
	assert.Equal(t, todos, updated.Todos)
	assert.Equal(t, summary, updated.SummaryMessageID)

	require.NoError(t, q.UpdateSessionTitleAndUsage(ctx, UpdateSessionTitleAndUsageParams{
		ID:               "s1",
		Title:            "auto-title",
		PromptTokens:     1,
		CompletionTokens: 2,
		Cost:             0.125,
	}))
	titled, err := q.GetSessionByID(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "auto-title", titled.Title)
	// The usage is added on top of what the session already had (this is
	// how auto-titling records the titling call's own usage).
	assert.Equal(t, int64(11), titled.PromptTokens)
	assert.Equal(t, int64(22), titled.CompletionTokens)

	require.NoError(t, q.DeleteSession(ctx, "s2"))
	_, err = q.GetSessionByID(ctx, "s2")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestMessageCRUD(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueries(t)
	ctx := t.Context()

	_, err := q.CreateSession(ctx, CreateSessionParams{ID: "s1", Title: "t"})
	require.NoError(t, err)

	model := sql.NullString{String: "m", Valid: true}
	provider := sql.NullString{String: "p", Valid: true}
	created, err := q.CreateMessage(ctx, CreateMessageParams{
		ID:        "m1",
		SessionID: "s1",
		Role:      "assistant",
		Parts:     `[{"type":"text","data":{"text":"hi"}}]`,
		Model:     model,
		Provider:  provider,
	})
	require.NoError(t, err)
	assert.Equal(t, "assistant", created.Role)
	assert.True(t, created.Model.Valid)
	assert.False(t, created.IsSummaryMessage != 0)

	got, err := q.GetMessage(ctx, "m1")
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)

	for _, msg := range []CreateMessageParams{
		{ID: "u1", SessionID: "s1", Role: "user", Parts: `[]`},
		{ID: "a2", SessionID: "s1", Role: "assistant", Parts: `[]`, Model: model, Provider: provider},
		{ID: "sum", SessionID: "s1", Role: "assistant", Parts: `[]`, IsSummaryMessage: 1},
		{ID: "a3", SessionID: "s1", Role: "assistant", Parts: `[]`, Model: model, Provider: provider},
	} {
		_, err := q.CreateMessage(ctx, msg)
		require.NoError(t, err)
	}

	bySession, err := q.ListMessagesBySession(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, bySession, 5)

	userMsgs, err := q.ListUserMessagesBySession(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, userMsgs, 1)
	assert.Equal(t, "u1", userMsgs[0].ID)

	allUsers, err := q.ListAllUserMessages(ctx)
	require.NoError(t, err)
	require.Len(t, allUsers, 1)
	assert.Equal(t, "u1", allUsers[0].ID)

	lastAssistant, err := q.GetLastAssistantMessageBySession(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "a3", lastAssistant.ID)

	// FromSummary returns the summary and everything created after it;
	// second-resolution timestamps may pull in messages created in the same
	// second, so only assert the summary is present and ordered first.
	fromSummary, err := q.ListMessagesBySessionFromSummary(ctx, ListMessagesBySessionFromSummaryParams{SessionID: "s1", ID: "sum"})
	require.NoError(t, err)
	require.NotEmpty(t, fromSummary)
	// created_at has one-second resolution, so earlier same-second messages
	// can come back too (the caller slices from the summary by ID); the
	// summary itself and later messages must be present.
	ids := map[string]bool{}
	for _, m := range fromSummary {
		ids[m.ID] = true
	}
	assert.True(t, ids["sum"], "summary must be included")
	assert.True(t, ids["a3"], "messages after the summary must be included")

	require.NoError(t, q.UpdateMessage(ctx, UpdateMessageParams{
		ID:                      "m1",
		Parts:                   `[{"type":"text","data":{"text":"edited"}}]`,
		PrismModelID:            sql.NullString{String: "pm", Valid: true},
		PrismModelName:          sql.NullString{String: "pmn", Valid: true},
		PrismHypercreditSavings: sql.NullFloat64{Float64: 0.5, Valid: true},
		PrismDollarSavings:      sql.NullFloat64{Float64: 1.5, Valid: true},
		FinishedAt:              sql.NullInt64{Int64: 999, Valid: true},
	}))
	edited, err := q.GetMessage(ctx, "m1")
	require.NoError(t, err)
	assert.Equal(t, `[{"type":"text","data":{"text":"edited"}}]`, edited.Parts)
	assert.True(t, edited.FinishedAt.Valid)
	assert.Equal(t, int64(999), edited.FinishedAt.Int64)
	assert.True(t, edited.PrismHypercreditSavings.Valid)
	assert.Equal(t, 0.5, edited.PrismHypercreditSavings.Float64)

	require.NoError(t, q.DeleteMessage(ctx, "sum"))
	afterDelete, err := q.ListMessagesBySession(ctx, "s1")
	require.NoError(t, err)
	assert.Len(t, afterDelete, 4)

	require.NoError(t, q.DeleteSessionMessages(ctx, "s1"))
	empty, err := q.ListMessagesBySession(ctx, "s1")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestFileCRUD(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueries(t)
	ctx := t.Context()

	_, err := q.CreateSession(ctx, CreateSessionParams{ID: "s1", Title: "t"})
	require.NoError(t, err)

	file, err := q.CreateFile(ctx, CreateFileParams{
		ID:        "f1",
		SessionID: "s1",
		Path:      "/tmp/a.txt",
		Content:   "v1",
		Version:   1,
	})
	require.NoError(t, err)
	assert.Equal(t, "v1", file.Content)
	assert.NotZero(t, file.CreatedAt)

	got, err := q.GetFile(ctx, "f1")
	require.NoError(t, err)
	assert.Equal(t, file.ID, got.ID)

	_, err = q.GetFile(ctx, "missing")
	require.ErrorIs(t, err, sql.ErrNoRows)

	_, err = q.CreateFile(ctx, CreateFileParams{ID: "f2", SessionID: "s1", Path: "/tmp/a.txt", Content: "v2", Version: 2})
	require.NoError(t, err)
	_, err = q.CreateFile(ctx, CreateFileParams{ID: "f3", SessionID: "s1", Path: "/tmp/b.txt", Content: "b", Version: 1})
	require.NoError(t, err)

	bySession, err := q.ListFilesBySession(ctx, "s1")
	require.NoError(t, err)
	assert.Len(t, bySession, 3)

	byPath, err := q.ListFilesByPath(ctx, "/tmp/a.txt")
	require.NoError(t, err)
	require.Len(t, byPath, 2)

	latest, err := q.ListLatestSessionFiles(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, latest, 2, "only the latest version of each path")
	contents := map[string]string{}
	for _, f := range latest {
		contents[f.Path] = f.Content
	}
	assert.Equal(t, map[string]string{"/tmp/a.txt": "v2", "/tmp/b.txt": "b"}, contents)

	byPathAndSession, err := q.GetFileByPathAndSession(ctx, GetFileByPathAndSessionParams{Path: "/tmp/a.txt", SessionID: "s1"})
	require.NoError(t, err)
	assert.Equal(t, "v2", byPathAndSession.Content)

	require.NoError(t, q.DeleteFile(ctx, "f2"))
	require.NoError(t, q.DeleteSessionFiles(ctx, "s1"))
	empty, err := q.ListFilesBySession(ctx, "s1")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestReadFiles(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueries(t)
	ctx := t.Context()

	// read_files has a foreign key on sessions.
	_, err := q.CreateSession(ctx, CreateSessionParams{ID: "s1", Title: "t"})
	require.NoError(t, err)

	err = q.RecordFileRead(ctx, RecordFileReadParams{SessionID: "s1", Path: "/a"})
	require.NoError(t, err)
	err = q.RecordFileRead(ctx, RecordFileReadParams{SessionID: "s1", Path: "/b"})
	require.NoError(t, err)

	read, err := q.GetFileRead(ctx, GetFileReadParams{SessionID: "s1", Path: "/a"})
	require.NoError(t, err)
	assert.Equal(t, "/a", read.Path)
	assert.NotZero(t, read.ReadAt)

	_, err = q.GetFileRead(ctx, GetFileReadParams{SessionID: "s1", Path: "/missing"})
	require.ErrorIs(t, err, sql.ErrNoRows)

	listed, err := q.ListSessionReadFiles(ctx, "s1")
	require.NoError(t, err)
	// Both reads land in the same second, so the read_at DESC ordering
	// between them is undefined.
	require.Len(t, listed, 2)
	paths := map[string]bool{}
	for _, r := range listed {
		paths[r.Path] = true
	}
	assert.True(t, paths["/a"] && paths["/b"])
}

func TestMCPEnabledDisabledServers(t *testing.T) {
	t.Parallel()
	q, _ := newTestQueries(t)
	ctx := t.Context()

	err := q.InsertMCPDisabledServer(ctx, "a")
	require.NoError(t, err)
	err = q.InsertMCPDisabledServer(ctx, "b")
	require.NoError(t, err)

	disabled, err := q.ListMCPDisabledServers(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, disabled)

	err = q.DeleteMCPDisabledServer(ctx, "a")
	require.NoError(t, err)
	disabled, err = q.ListMCPDisabledServers(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, disabled)

	err = q.InsertMCPEnabledServer(ctx, "c")
	require.NoError(t, err)
	enabled, err := q.ListMCPEnabledServers(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"c"}, enabled)

	err = q.DeleteMCPEnabledServer(ctx, "c")
	require.NoError(t, err)
	enabled, err = q.ListMCPEnabledServers(ctx)
	require.NoError(t, err)
	assert.Empty(t, enabled)
}

// seedStatsData creates one root session, one child session and messages
// with known timestamps and token counts for the stats aggregations.
func seedStatsData(t *testing.T, q *Queries, conn *sql.DB) {
	t.Helper()
	ctx := t.Context()

	_, err := q.CreateSession(ctx, CreateSessionParams{
		ID: "root", Title: "root", MessageCount: 2, PromptTokens: 100, CompletionTokens: 50, Cost: 2.0,
	})
	require.NoError(t, err)
	setSessionCreatedAt(t, conn, "root", 1700000000) // 2023-11-14, a Tuesday, 22:00 UTC

	_, err = q.CreateSession(ctx, CreateSessionParams{
		ID: "child", ParentSessionID: sql.NullString{String: "root", Valid: true},
	})
	require.NoError(t, err)

	parts := `[{"type":"tool_call","data":{"name":"bash"}}]`
	model := sql.NullString{String: "test-model", Valid: true}
	provider := sql.NullString{String: "test-provider", Valid: true}
	_, err = q.CreateMessage(ctx, CreateMessageParams{ID: "a1", SessionID: "root", Role: "assistant", Parts: parts, Model: model, Provider: provider})
	require.NoError(t, err)
	// finished_at - created_at = 5s for the response-time average.
	_, err = conn.ExecContext(ctx, `UPDATE messages SET finished_at = created_at + 5 WHERE id = 'a1'`)
	require.NoError(t, err)
}

func TestStatsQueries(t *testing.T) {
	t.Parallel()
	q, conn := newTestQueries(t)
	seedStatsData(t, q, conn)
	ctx := t.Context()

	totals, err := q.GetTotalStats(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, totals.TotalSessions, "child sessions are excluded")
	assert.EqualValues(t, 100, totals.TotalPromptTokens)
	assert.EqualValues(t, 50, totals.TotalCompletionTokens)
	assert.EqualValues(t, 2.0, totals.TotalCost)
	// Root was seeded with message_count 2; the message insert trigger
	// bumped it to 3.
	assert.EqualValues(t, 3, totals.TotalMessages)

	byDay, err := q.GetUsageByDay(ctx)
	require.NoError(t, err)
	require.Len(t, byDay, 1)
	assert.Equal(t, "2023-11-14", byDay[0].Day)
	assert.Equal(t, int64(1), byDay[0].SessionCount)

	byHour, err := q.GetUsageByHour(ctx)
	require.NoError(t, err)
	require.Len(t, byHour, 1)
	assert.Equal(t, int64(22), byHour[0].Hour)

	byDow, err := q.GetUsageByDayOfWeek(ctx)
	require.NoError(t, err)
	require.Len(t, byDow, 1)
	assert.Equal(t, int64(2), byDow[0].DayOfWeek, "2023-11-14 is a Tuesday")

	heatmap, err := q.GetHourDayHeatmap(ctx)
	require.NoError(t, err)
	require.Len(t, heatmap, 1)
	assert.Equal(t, int64(2), heatmap[0].DayOfWeek)
	assert.Equal(t, int64(22), heatmap[0].Hour)

	activity, err := q.GetRecentActivity(ctx)
	// The seeded session is from 2023, outside the 30-day window.
	require.NoError(t, err)
	assert.Empty(t, activity)

	avg, err := q.GetAverageResponseTime(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(5), avg)

	tools, err := q.GetToolUsage(ctx)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "bash", tools[0].ToolName)
	assert.Equal(t, int64(1), tools[0].CallCount)
}
