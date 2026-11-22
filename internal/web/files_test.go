package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

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
		{ID: "f1", SessionID: "sub-1", Path: "/repo/a.go", Content: "a\nb\n", Version: 1, CreatedAt: 11, UpdatedAt: 11},
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
// diff cap is never diffed, and its counts are reported as unknown so the
// drawer does not show a misleading zero.
func TestFileSummariesSkipOversizedDiff(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	fake.AddHistory(history.File{
		ID: "big-0", SessionID: "s1", Path: "/repo/big.txt", Version: 0, CreatedAt: 1, UpdatedAt: 1,
		Content: strings.Repeat("o", maxDiffBytes+1),
	})
	fake.AddHistory(history.File{
		ID: "big-1", SessionID: "s1", Path: "/repo/big.txt", Version: 1, CreatedAt: 2, UpdatedAt: 2,
		Content: "small\n",
	})
	got, err := g.fileSummaries(t.Context(), "s1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got[0].StatsUnknown)
	require.Zero(t, got[0].Additions)
	require.Zero(t, got[0].Removals)
	require.Zero(t, g.fileStats.diffCount(), "the oversized version must not be diffed")
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

func TestFileDiffAcrossChildSessions(t *testing.T) {
	t.Parallel()
	g, fake := startTest(t)
	seedTree(fake)
	c := authedClient(t, g)

	rsp, body := doJSON(t, c, http.MethodGet, diffURL(g, "s1", "/repo/a.go", ""), "")
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
		require.Equal(t, http.StatusNotFound, rsp.StatusCode, p)
		require.Contains(t, string(body), "unknown_path")
	}
	rsp, _ := doJSON(t, c, http.MethodGet, g.URL()+"/api/sessions/s1/files/diff", "")
	require.Equal(t, http.StatusBadRequest, rsp.StatusCode)
}

func TestBuildDiffTruncatesLargePayload(t *testing.T) {
	t.Parallel()
	line := func(c string) string { return strings.Repeat(c, 1023) + "\n" }
	old := strings.Repeat(line("o"), 600)
	next := strings.Repeat(line("n"), 600)
	d, err := buildDiff("/repo/big.txt", []history.File{{Version: 0, Content: old}, {Version: 1, Content: next}}, nil, nil)
	require.NoError(t, err)
	require.True(t, d.Truncated)
	require.Empty(t, d.Old)
	require.Empty(t, d.New)
	require.LessOrEqual(t, len(d.Unified), maxDiffBytes)
	require.True(t, strings.HasSuffix(d.Unified, "\n"), "the cut lands on a line boundary")
	require.Equal(t, "text", d.Language)
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
