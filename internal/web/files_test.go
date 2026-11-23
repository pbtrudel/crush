package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/client"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/web/webtest"
	"github.com/stretchr/testify/require"
)

// seedTree gives s1 a subagent (sub-1) that spawned its own (sub-2), and an
// unrelated session (s9) whose edits must never show up under s1.
func seedTree(fake *webtest.Fake) {
	fake.AddSession(session.Session{ID: "sub-1", ParentSessionID: "s1", Title: "Subagent", CreatedAt: 2})
	fake.AddSession(session.Session{ID: "sub-2", ParentSessionID: "sub-1", Title: "Nested", CreatedAt: 3})
	fake.AddSession(session.Session{ID: "s9", Title: "Other"})
	for _, f := range []history.File{
		{ID: "f0", SessionID: "s1", Path: "/repo/a.go", Content: "a\n", Version: 0, CreatedAt: 10, UpdatedAt: 10},
		// A subagent's first edit to a path starts a version 0 of its own:
		// versions are unique per (path, session), not per path.
		{ID: "f1", SessionID: "sub-1", Path: "/repo/a.go", Content: "a\nb\n", Version: 0, CreatedAt: 11, UpdatedAt: 11},
		{ID: "f2", SessionID: "s1", Path: "/repo/a.go", Content: "a\nb\nc\n", Version: 2, CreatedAt: 12, UpdatedAt: 12},
		{ID: "f3", SessionID: "sub-2", Path: "/repo/b.go", Content: "", Version: 0, CreatedAt: 13, UpdatedAt: 13},
		{ID: "f4", SessionID: "sub-2", Path: "/repo/b.go", Content: "x\n", Version: 1, CreatedAt: 14, UpdatedAt: 14},
		{ID: "f5", SessionID: "s9", Path: "/repo/c.go", Content: "c\n", Version: 0, CreatedAt: 15, UpdatedAt: 15},
	} {
		fake.AddHistory(f)
	}
	fake.SetDiagnostics(map[string]lsp.DiagnosticCounts{"/repo/a.go": {Error: 1, Warning: 2, Hint: 4}})
}

func diffURL(g *Gateway, sid, path, extra string) string {
	return g.URL() + "/api/sessions/" + sid + "/files/diff?path=" + url.QueryEscape(path) + extra
}

func TestFileSummariesAggregateChildSessions(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	seedTree(fake)
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, g.URL()+"/api/sessions/s1/files", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var got []FileSummary
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, []FileSummary{
		{Path: "/repo/b.go", Versions: 2, VersionIDs: []int64{0, 1}, Additions: 1, UpdatedAt: 14, SessionIDs: []string{"sub-2"}},
		{
			Path: "/repo/a.go", Versions: 3, VersionIDs: []int64{0, 1, 2}, Additions: 2,
			Diagnostics: FileDiagnostics{Error: 1, Warning: 2}, UpdatedAt: 12, SessionIDs: []string{"s1", "sub-1"},
		},
	}, got)
}

// TestFileSummariesSkipOversizedDiff pins ruling I3: a version above the
// per-side diff limit is never diffed, and its counts are reported as
// unknown so the drawer does not show a misleading zero. Either end of the
// pair counts, and the limit is the diff limit, not the answer cap.
func TestFileSummariesSkipOversizedDiff(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("o", maxDiffInputBytes+1)
	for _, tc := range []struct {
		name        string
		first, last string
	}{
		{"oversized first version", huge, "small\n"},
		{"oversized last version", "small\n", huge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g, fake := startTest(t)
			fake.AddHistory(history.File{
				ID: "big-0", SessionID: "s1", Path: "/repo/big.txt", Version: 0, CreatedAt: 1, UpdatedAt: 1,
				Content: tc.first,
			})
			fake.AddHistory(history.File{
				ID: "big-1", SessionID: "s1", Path: "/repo/big.txt", Version: 1, CreatedAt: 2, UpdatedAt: 2,
				Content: tc.last,
			})
			got, err := g.fileSummaries(t.Context(), "s1")
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.True(t, got[0].StatsUnknown)
			require.Zero(t, got[0].Additions)
			require.Zero(t, got[0].Removals)
			require.Zero(t, g.fileStats.diffCount(), "the oversized version must not be diffed")
		})
	}
}

// TestFileSummariesMemoizeStats pins ruling I3: an unchanged file is not
// diffed again, and a new latest version replaces the cached entry.
func TestFileSummariesMemoizeStats(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	seedTree(fake)
	for range 2 {
		got, err := g.fileSummaries(t.Context(), "s1")
		require.NoError(t, err)
		require.Len(t, got, 2)
	}
	require.Equal(t, 2, g.fileStats.diffCount(), "one diff per file across two calls")

	fake.AddHistory(history.File{
		ID: "f6", SessionID: "s1", Path: "/repo/a.go", Content: "a\nb\nc\nd\n", Version: 3, CreatedAt: 20, UpdatedAt: 20,
	})
	got, err := g.fileSummaries(t.Context(), "s1")
	require.NoError(t, err)
	require.Equal(t, 3, g.fileStats.diffCount(), "a new latest version recomputes")
	require.Len(t, got, 2)
	require.Equal(t, "/repo/a.go", got[0].Path)
	require.Equal(t, 3, got[0].Additions)
}

// TestFileSummariesMergeCollidingVersions pins the cross-session version
// collision: a subagent's first edit to a path is its own version 0, so
// versions must be addressed by position in the chronologically merged
// list, and the stats diff the first against the latest in time.
func TestFileSummariesMergeCollidingVersions(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddSession(session.Session{ID: "child", ParentSessionID: "s1", CreatedAt: 2})
	for _, f := range []history.File{
		{ID: "p0", SessionID: "s1", Path: "/repo/x.go", Content: "one\n", Version: 0, CreatedAt: 10, UpdatedAt: 10},
		{ID: "p1", SessionID: "s1", Path: "/repo/x.go", Content: "one\ntwo\n", Version: 1, CreatedAt: 11, UpdatedAt: 11},
		{ID: "c0", SessionID: "child", Path: "/repo/x.go", Content: "one\ntwo\nthree\n", Version: 0, CreatedAt: 12, UpdatedAt: 12},
		{ID: "c1", SessionID: "child", Path: "/repo/x.go", Content: "one\ntwo\nthree\nfour\n", Version: 1, CreatedAt: 13, UpdatedAt: 13},
	} {
		fake.AddHistory(f)
	}
	got, err := g.fileSummaries(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, []int64{0, 1, 2, 3}, got[0].VersionIDs, "versions are positions, not history.Version")
	require.Equal(t, 3, got[0].Additions, "first against the latest in time")
	require.Zero(t, got[0].Removals)
	require.Equal(t, int64(13), got[0].UpdatedAt)
	require.Equal(t, []string{"s1", "child"}, got[0].SessionIDs)

	c := authedClient(t, g)
	rsp, body := doJSON(t, c, http.MethodGet, diffURL(g, "s1", "/repo/x.go", ""), "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var d FileDiff
	require.NoError(t, json.Unmarshal(body, &d))
	require.Equal(t, int64(0), d.From)
	require.Equal(t, int64(3), d.To)
	require.Equal(t, "one\n", d.Old)
	require.Equal(t, "one\ntwo\nthree\nfour\n", d.New)

	rsp, body = doJSON(t, c, http.MethodGet, diffURL(g, "s1", "/repo/x.go", "&from=1&to=2"), "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.NoError(t, json.Unmarshal(body, &d))
	require.Equal(t, "one\ntwo\n", d.Old, "position 1 is the parent's version 1 at t=11")
	require.Equal(t, "one\ntwo\nthree\n", d.New, "position 2 is the child's version 0 at t=12")

	rsp, body = doJSON(t, c, http.MethodGet, diffURL(g, "s1", "/repo/x.go", "&from=4"), "")
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode, string(body))
	require.Contains(t, string(body), "bad_version")
}

// TestTreeHistoryOrdersTies pins the sort keys: creation time first, then
// version, then record id, so a colliding version still has one order.
func TestTreeHistoryOrdersTies(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	for _, f := range []history.File{
		{ID: "b", SessionID: "s1", Path: "/repo/t.go", Version: 2, CreatedAt: 10},
		{ID: "a", SessionID: "s1", Path: "/repo/t.go", Version: 2, CreatedAt: 10},
		{ID: "c", SessionID: "s1", Path: "/repo/t.go", Version: 1, CreatedAt: 10},
		{ID: "d", SessionID: "s1", Path: "/repo/t.go", Version: 3, CreatedAt: 5},
	} {
		fake.AddHistory(f)
	}
	byPath, err := g.treeHistory(t.Context(), "s1")
	require.NoError(t, err)
	var ids []string
	for _, f := range byPath["/repo/t.go"] {
		ids = append(ids, f.ID)
	}
	require.Equal(t, []string{"d", "c", "a", "b"}, ids)
}

// TestTreeHistoryKeepsSameSecondSessionsTogether pins the tie-break that
// matters when two sessions write one path in the same second: each
// session's versions stay together and in their own order, so a session's
// v0 never lands after its v1 just because another session is in between.
func TestTreeHistoryKeepsSameSecondSessionsTogether(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddSession(session.Session{ID: "s1", Title: "Root", CreatedAt: 1})
	fake.AddSession(session.Session{ID: "child", ParentSessionID: "s1", Title: "Child", CreatedAt: 2})
	for _, f := range []history.File{
		// Written out of version order, as the store sorts by id alone.
		{ID: "c1", SessionID: "child", Path: "/repo/t.go", Version: 1, CreatedAt: 10},
		{ID: "p1", SessionID: "s1", Path: "/repo/t.go", Version: 1, CreatedAt: 10},
		{ID: "c0", SessionID: "child", Path: "/repo/t.go", Version: 0, CreatedAt: 10},
		{ID: "p0", SessionID: "s1", Path: "/repo/t.go", Version: 0, CreatedAt: 10},
	} {
		fake.AddHistory(f)
	}
	byPath, err := g.treeHistory(t.Context(), "s1")
	require.NoError(t, err)
	var ids []string
	for _, f := range byPath["/repo/t.go"] {
		ids = append(ids, f.ID)
	}
	require.Equal(t, []string{"c0", "c1", "p0", "p1"}, ids,
		"each session's versions stay together and in version order")
}

// TestFileRoutesRejectUnknownSession pins that both routes answer the same
// 404 as the messages route for a session the workspace does not have.
func TestFileRoutesRejectUnknownSession(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	seedTree(fake)
	c := authedClient(t, g)
	for _, target := range []string{
		g.URL() + "/api/sessions/nope/files",
		diffURL(g, "nope", "/repo/a.go", ""),
	} {
		rsp, body := doJSON(t, c, http.MethodGet, target, "")
		rsp.Body.Close()
		require.Equal(t, http.StatusNotFound, rsp.StatusCode, target)
		require.Contains(t, string(body), "unknown_session", target)
	}
}

func TestFileDiffAcrossChildSessions(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	seedTree(fake)
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodGet, diffURL(g, "s1", "/repo/a.go", ""), "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var d FileDiff
	require.NoError(t, json.Unmarshal(body, &d))
	require.Equal(t, int64(0), d.From)
	require.Equal(t, int64(2), d.To)
	require.Equal(t, "a\n", d.Old)
	require.Equal(t, "a\nb\nc\n", d.New)
	require.Contains(t, d.Unified, "+b")
	require.Contains(t, d.Unified, "+c")
	require.Equal(t, "go", d.Language)
	require.False(t, d.Truncated)

	rsp, body = doJSON(t, c, http.MethodGet, diffURL(g, "s1", "/repo/a.go", "&from=1&to=2"), "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.NoError(t, json.Unmarshal(body, &d))
	require.Equal(t, "a\nb\n", d.Old, "version 1 is the subagent's edit")
}

func TestFileDiffRejectsUnknownVersion(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	seedTree(fake)
	c := authedClient(t, g)
	for _, extra := range []string{"&from=5", "&to=abc", "&from=-1"} {
		rsp, body := doJSON(t, c, http.MethodGet, diffURL(g, "s1", "/repo/a.go", extra), "")
		rsp.Body.Close()
		require.Equal(t, http.StatusBadRequest, rsp.StatusCode, extra)
		require.Contains(t, string(body), "bad_version")
	}
}

func TestFileDiffRejectsPathOutsideTree(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	seedTree(fake)
	c := authedClient(t, g)
	for _, p := range []string{"/repo/c.go", "../../etc/passwd", "/etc/passwd"} {
		rsp, body := doJSON(t, c, http.MethodGet, diffURL(g, "s1", p, ""), "")
		rsp.Body.Close()
		require.Equal(t, http.StatusNotFound, rsp.StatusCode, p)
		require.Contains(t, string(body), "unknown_path")
	}
	rsp, _ := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/files/diff", "")
	rsp.Body.Close()
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
}

func lines(c string, n int) string { return strings.Repeat(strings.Repeat(c, 1023)+"\n", n) }

// TestFileDiffTruncatesLargePair pins spec 2.7 for a large pair: two texts
// whose sum is over the cap are still diffed, and the answer keeps the
// unified text alone. A 600 KB file edited by one line is far under the
// per-side limit, so the pair is diffed and only the texts are dropped.
func TestFileDiffTruncatesLargePair(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddHistory(history.File{
		ID: "big-0", SessionID: "s1", Path: "/repo/big.txt", Version: 0, CreatedAt: 1, UpdatedAt: 1,
		Content: lines("o", 600),
	})
	fake.AddHistory(history.File{
		ID: "big-1", SessionID: "s1", Path: "/repo/big.txt", Version: 1, CreatedAt: 2, UpdatedAt: 2,
		Content: lines("o", 599) + "one more line\n",
	})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, diffURL(g, "s1", "/repo/big.txt", ""), "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var d FileDiff
	require.NoError(t, json.Unmarshal(body, &d))
	require.True(t, d.Truncated)
	require.Empty(t, d.Old)
	require.Empty(t, d.New)
	require.NotEmpty(t, d.Unified, "a large pair still gets its unified diff")
	require.Contains(t, d.Unified, "+one more line")
	require.LessOrEqual(t, len(d.Unified), maxDiffBytes)
	require.Equal(t, "text", d.Language)
}

// TestBuildDiffKeepsUnifiedForLargePair is the unit-level twin: the old and
// new texts are dropped once the payload is over the cap, and the unified
// text stays.
func TestBuildDiffKeepsUnifiedForLargePair(t *testing.T) {
	t.Parallel()
	old := lines("o", 600)
	next := lines("o", 599) + "one more line\n"
	require.Greater(t, len(old)+len(next), maxDiffBytes)
	d := buildDiff("/repo/big.txt",
		history.File{Version: 0, Content: old},
		history.File{Version: 1, Content: next}, 0, 1)
	require.True(t, d.Truncated)
	require.Empty(t, d.Old)
	require.Empty(t, d.New)
	require.Contains(t, d.Unified, "+one more line")
	require.Equal(t, "text", d.Language)
}

// TestBuildDiffRefusesAHugeSide pins the only answer with no unified text:
// a single side over the per-side limit is never diffed, because the diff
// of two such files would cost far more than the render can hold.
func TestBuildDiffRefusesAHugeSide(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("x", maxDiffInputBytes+1)
	for _, tc := range []struct {
		name        string
		first, last string
	}{
		{"huge first side", huge, "small\n"},
		{"huge last side", "small\n", huge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := buildDiff("/repo/huge.txt",
				history.File{Version: 0, Content: tc.first},
				history.File{Version: 1, Content: tc.last}, 0, 1)
			require.True(t, d.Truncated)
			require.Empty(t, d.Unified)
			require.Empty(t, d.Old)
			require.Empty(t, d.New)
		})
	}
}

// TestBuildDiffCutsUnifiedAtLineBoundary keeps the cut path honest: a pair
// that fits the cap can still produce a unified diff over it, and the cut
// then lands on a line and a rune boundary.
func TestBuildDiffCutsUnifiedAtLineBoundary(t *testing.T) {
	t.Parallel()
	// 512 lines of 1024 bytes each keep the two texts at exactly the cap,
	// while the unified diff carries more than a byte of prefix per line.
	old, next := lines("o", 512), lines("n", 512)
	require.LessOrEqual(t, len(old)+len(next), maxDiffBytes)
	d := buildDiff("/repo/big.txt",
		history.File{Version: 0, Content: old},
		history.File{Version: 1, Content: next}, 0, 1)
	require.True(t, d.Truncated)
	require.Empty(t, d.Old)
	require.Empty(t, d.New)
	require.LessOrEqual(t, len(d.Unified), maxDiffBytes)
	require.True(t, strings.HasSuffix(d.Unified, "\n"), "the cut lands on a line boundary")
	require.True(t, utf8.ValidString(d.Unified))
	require.Equal(t, "text", d.Language)
}

// TestFileDiffKeepsAngleBrackets pins the wire encoding: a diff of markup
// is already near the cap, so escaping <, > and & would only inflate it.
func TestFileDiffKeepsAngleBrackets(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddHistory(history.File{
		ID: "h-0", SessionID: "s1", Path: "/repo/page.html", Version: 0, CreatedAt: 1, UpdatedAt: 1,
		Content: "<p>a</p>\n",
	})
	fake.AddHistory(history.File{
		ID: "h-1", SessionID: "s1", Path: "/repo/page.html", Version: 1, CreatedAt: 2, UpdatedAt: 2,
		Content: "<p>a&b</p>\n",
	})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, diffURL(g, "s1", "/repo/page.html", ""), "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.NotContains(t, string(body), `\u003c`, "the body is not HTML-escaped")
	require.NotContains(t, string(body), `\u0026`)
	require.Contains(t, string(body), "+<p>a&b</p>")
	var d FileDiff
	require.NoError(t, json.Unmarshal(body, &d))
	require.Contains(t, d.Unified, "+<p>a&b</p>")
}

func TestTruncateLinesKeepsRunes(t *testing.T) {
	t.Parallel()
	got := truncateLines(strings.Repeat("é", 10), 5)
	require.True(t, utf8.ValidString(got))
	require.Equal(t, "éé", got)
	require.Equal(t, "ab\n", truncateLines("ab\ncd\nef", 5))
	require.Equal(t, "short", truncateLines("short", 50))
}

func TestEncodeStripsFileContent(t *testing.T) {
	t.Parallel()
	typ, raw, ok := encode(pubsub.Event[history.File]{Type: pubsub.CreatedEvent, Payload: history.File{
		ID: "f1", SessionID: "s1", Path: "/repo/a.go", Content: strings.Repeat("x", 4096), Version: 3,
	}})
	require.True(t, ok)
	require.Equal(t, "file", typ)
	require.NotContains(t, string(raw), "xxxx")
	require.Contains(t, string(raw), `"path":"/repo/a.go"`)
	require.Contains(t, string(raw), `"version":3`)
}

func TestSnapshotIncludesFiles(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	seedTree(fake)
	c := dialStream(t, g, "")
	f := readFrame(t, c)
	var snap Snapshot
	require.NoError(t, json.Unmarshal(f.Payload, &snap))
	require.Len(t, snap.Files, 2)
	require.Equal(t, "/repo/b.go", snap.Files[0].Path)
}

func TestLanguageFor(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		"/repo/a.go":         "go",
		"/repo/a.ts":         "typescript",
		"/repo/a.mts":        "typescript",
		"/repo/a.cts":        "typescript",
		"/repo/a.tsx":        "tsx",
		"/repo/a.js":         "javascript",
		"/repo/a.mjs":        "javascript",
		"/repo/a.cjs":        "javascript",
		"/repo/a.jsx":        "jsx",
		"/repo/a.json":       "json",
		"/repo/a.css":        "css",
		"/repo/a.html":       "html",
		"/repo/a.htm":        "html",
		"/repo/a.md":         "markdown",
		"/repo/a.py":         "python",
		"/repo/a.yaml":       "yaml",
		"/repo/a.yml":        "yaml",
		"/repo/A.GO":         "go",
		"/repo/a.txt":        "text",
		"/repo/Makefile":     "text",
		"/repo/a.tar.gz":     "text",
		"/repo/no-extension": "text",
	} {
		require.Equal(t, want, languageFor(path), path)
	}
}

// TestBuildDiffRefusesTooManyLines pins the line cap: a side of short
// lines stays under the byte cap but would split into millions of lines,
// so it is refused before any diff runs.
func TestBuildDiffRefusesTooManyLines(t *testing.T) {
	t.Parallel()
	lines := strings.Repeat("\n", maxDiffInputLines+1)
	d := buildDiff("/repo/lock.json",
		history.File{Content: "a\n"}, history.File{Content: lines}, 0, 1)
	require.True(t, d.Truncated)
	require.Empty(t, d.Unified, "a side over the line cap is not diffed")
	require.Empty(t, d.Old)
	require.Empty(t, d.New)
}

// TestFileSummariesLoadOnlyFirstAndLast pins that a summary reads the
// metadata, not the content: only the first and the latest version are
// loaded, and a second summary with no new version loads nothing.
func TestFileSummariesLoadOnlyFirstAndLast(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	for i := range 5 {
		f.AddHistory(history.File{
			ID: fmt.Sprintf("v%d", i), SessionID: "s1", Path: "/repo/a.go",
			Content: strings.Repeat("x\n", i+1), Version: int64(i), CreatedAt: int64(100 + i),
		})
	}
	got, err := g.fileSummaries(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 4, got[0].Additions)
	require.ElementsMatch(t, []string{"GetHistoryVersion:v0", "GetHistoryVersion:v4"}, loads(f))
	// A second summary with no new version hits the stats cache: no load.
	_, err = g.fileSummaries(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, loads(f), 2)
}

// TestFileSummariesSkipOversizedByMetadata pins that the size in the
// metadata decides: an oversized side is never loaded just to learn that
// it cannot be diffed.
func TestFileSummariesSkipOversizedByMetadata(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddHistory(history.File{ID: "v0", SessionID: "s1", Path: "/repo/big.bin", Content: "a", CreatedAt: 1})
	f.AddHistory(history.File{
		ID: "v1", SessionID: "s1", Path: "/repo/big.bin",
		Content: strings.Repeat("b", maxDiffInputBytes+1), Version: 1, CreatedAt: 2,
	})
	files, err := g.fileSummaries(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, files[0].StatsUnknown)
	require.Empty(t, loads(f), "the size in the metadata decides; no content is loaded")
}

// TestFileDiffLoadsOnlyTheTwoVersions pins that a diff loads just the
// versions it shows, not the whole history of the path.
func TestFileDiffLoadsOnlyTheTwoVersions(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	for i := range 4 {
		f.AddHistory(history.File{
			ID: fmt.Sprintf("v%d", i), SessionID: "s1", Path: "/repo/a.go",
			Content: fmt.Sprintf("line %d\n", i), Version: int64(i), CreatedAt: int64(100 + i),
		})
	}
	f.AddSession(session.Session{ID: "s1", Title: "Root"})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, diffURL(g, "s1", "/repo/a.go", "&from=1&to=2"), "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	require.ElementsMatch(t, []string{"GetHistoryVersion:v1", "GetHistoryVersion:v2"}, loads(f))
}

// TestFileDiffRefusesOversizedSideByMetadata pins that a side whose size
// the metadata already reports as over the cap answers the truncated
// refusal without loading either version.
func TestFileDiffRefusesOversizedSideByMetadata(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddSession(session.Session{ID: "s1", Title: "Root"})
	f.AddHistory(history.File{ID: "v0", SessionID: "s1", Path: "/repo/big.txt", Content: "a\n", CreatedAt: 1})
	f.AddHistory(history.File{
		ID: "v1", SessionID: "s1", Path: "/repo/big.txt",
		Content: strings.Repeat("b", maxDiffInputBytes+1), Version: 1, CreatedAt: 2,
	})
	rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, diffURL(g, "s1", "/repo/big.txt", ""), "")
	rsp.Body.Close()
	require.Equal(t, http.StatusOK, rsp.StatusCode, string(body))
	var d FileDiff
	require.NoError(t, json.Unmarshal(body, &d))
	require.True(t, d.Truncated)
	require.Empty(t, d.Unified)
	require.Empty(t, d.Old)
	require.Empty(t, d.New)
	require.Empty(t, loads(f), "an oversized side is refused before either version is loaded")
}

// versionErrorWorkspace fails every history version load, the way a
// deleted version or a failing server reaches the gateway.
type versionErrorWorkspace struct {
	*webtest.Fake
	err error
}

func (w versionErrorWorkspace) GetHistoryVersion(context.Context, string) (history.File, error) {
	return history.File{}, w.err
}

// TestFileDiffVersionLoadErrors pins the answers to a load that fails
// after the metadata listed the version: a miss is the gateway's own 404,
// and any other error is the generic 500 that never carries the upstream
// message.
func TestFileDiffVersionLoadErrors(t *testing.T) {
	t.Parallel()
	boom := errors.New("get history version h0: open /home/user/.crush/crush.db: disk I/O error")
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"missing version", fmt.Errorf("get history version h0: %w", sql.ErrNoRows), http.StatusNotFound, "unknown_version"},
		{"missing version over the client", fmt.Errorf("status code 404: %w", client.ErrNotFound), http.StatusNotFound, "unknown_version"},
		{"unexpected error", boom, http.StatusInternalServerError, "file_diff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := webtest.NewFake()
			fake.AddSession(session.Session{ID: "s1", Title: "Root"})
			fake.AddHistory(history.File{
				ID: "h0", SessionID: "s1", Path: "/repo/a.go", Version: 0, CreatedAt: 1, Content: "a\n",
			})
			g, err := Start(t.Context(), Options{Workspace: versionErrorWorkspace{Fake: fake, err: tc.err}})
			require.NoError(t, err)
			t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
			rsp, body := doJSON(t, authedClient(t, g), http.MethodGet, diffURL(g, "s1", "/repo/a.go", ""), "")
			rsp.Body.Close()
			require.Equal(t, tc.wantStatus, rsp.StatusCode, string(body))
			require.Contains(t, string(body), tc.wantCode)
			require.NotContains(t, string(body), "/home/user", "an upstream error never reaches the browser")
		})
	}
}

// loads lists the content fetches the fake saw, in order.
func loads(f *webtest.Fake) []string {
	var out []string
	for _, c := range f.CallLog() {
		if strings.HasPrefix(c, "GetHistoryVersion:") {
			out = append(out, c)
		}
	}
	return out
}

// TestFileSummariesDoNotCacheACancelledRequest pins that a request whose
// tab went away leaves no entry behind: the next summary still loads the
// two versions and reports real counts.
func TestFileSummariesDoNotCacheACancelledRequest(t *testing.T) {
	t.Parallel()
	g, f := startTest(t)
	f.AddHistory(history.File{ID: "v0", SessionID: "s1", Path: "/repo/a.go", Content: "a\n", Version: 0, CreatedAt: 1})
	f.AddHistory(history.File{ID: "v1", SessionID: "s1", Path: "/repo/a.go", Content: "a\nb\n", Version: 1, CreatedAt: 2})
	// Hold every slot, so the cancelled request gives up waiting for one.
	releaseFirst, err := g.diffs.acquire(t.Context())
	require.NoError(t, err)
	releaseLast, err := g.diffs.acquire(t.Context())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := g.fileSummaries(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got[0].StatsUnknown, "a cancelled request gives up its wait for a slot")
	require.Empty(t, loads(f), "a cancelled request loads nothing")
	releaseFirst()
	releaseLast()

	got, err = g.fileSummaries(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.False(t, got[0].StatsUnknown)
	require.Equal(t, 1, got[0].Additions)
	require.ElementsMatch(t, []string{"GetHistoryVersion:v0", "GetHistoryVersion:v1"}, loads(f))
}
