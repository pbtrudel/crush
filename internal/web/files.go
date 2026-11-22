package web

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/diff"
	"github.com/charmbracelet/crush/internal/history"
)

const (
	// maxDiffBytes caps a diff answer (spec 2.7: 1 MB). Above it the old
	// and new texts are dropped and the unified diff is cut, so one
	// generated file cannot stall the tab.
	maxDiffBytes = 1 << 20
	// maxTreeSessions bounds the child-session walk. A real tree is a few
	// dozen sessions; the cap only stops a runaway or cyclic parent chain.
	maxTreeSessions = 256
)

// FileDiagnostics is the LSP diagnostic count for one edited file.
type FileDiagnostics struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
}

// FileSummary is one edited path across a session and every session below
// it.
type FileSummary struct {
	Path        string          `json:"path"`
	Versions    int             `json:"versions"`
	VersionIDs  []int64         `json:"version_ids"`
	Additions   int             `json:"additions"`
	Removals    int             `json:"removals"`
	Diagnostics FileDiagnostics `json:"diagnostics"`
	UpdatedAt   int64           `json:"updated_at"`
	SessionIDs  []string        `json:"session_ids"`
	// StatsUnknown reports that a version is over the diff cap, so the
	// counts are not known. They are zero instead of misleading.
	StatsUnknown bool `json:"stats_unknown,omitempty"`
}

// FileDiff is the diff between two versions of one edited path. Truncated
// answers carry only the unified text.
type FileDiff struct {
	Path      string `json:"path"`
	From      int64  `json:"from"`
	To        int64  `json:"to"`
	Old       string `json:"old"`
	New       string `json:"new"`
	Unified   string `json:"unified"`
	Language  string `json:"language"`
	Truncated bool   `json:"truncated"`
}

var errUnknownVersion = errors.New("version is not in this file's history")

// sessionTree returns root and every session below it, breadth first.
func (g *Gateway) sessionTree(ctx context.Context, root string) ([]string, error) {
	seen := map[string]bool{root: true}
	order := []string{root}
	for i := 0; i < len(order); i++ {
		children, err := g.ws.ListChildSessions(ctx, order[i])
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			if seen[c.ID] || len(order) >= maxTreeSessions {
				continue
			}
			seen[c.ID] = true
			order = append(order, c.ID)
		}
	}
	return order, nil
}

// treeHistory groups the file history of root's tree by path, each path's
// versions in version order.
func (g *Gateway) treeHistory(ctx context.Context, root string) (map[string][]history.File, error) {
	ids, err := g.sessionTree(ctx, root)
	if err != nil {
		return nil, err
	}
	byPath := map[string][]history.File{}
	for _, id := range ids {
		files, err := g.ws.ListSessionHistory(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			byPath[f.Path] = append(byPath[f.Path], f)
		}
	}
	for _, versions := range byPath {
		slices.SortFunc(versions, func(a, b history.File) int {
			return cmp.Or(cmp.Compare(a.Version, b.Version), cmp.Compare(a.CreatedAt, b.CreatedAt))
		})
	}
	return byPath, nil
}

// fileSummaries lists root's edited files, newest first. The counts are
// first against latest, the same numbers the TUI sidebar shows.
func (g *Gateway) fileSummaries(ctx context.Context, root string) ([]FileSummary, error) {
	byPath, err := g.treeHistory(ctx, root)
	if err != nil {
		return nil, err
	}
	diags := g.ws.LSPDiagnosticsByFile(ctx)
	out := make([]FileSummary, 0, len(byPath))
	for path, versions := range byPath {
		last := versions[len(versions)-1]
		additions, removals, unknown := g.fileStats.totals(path, versions)
		ids := make([]int64, len(versions))
		var sessions []string
		for i, v := range versions {
			ids[i] = v.Version
			if !slices.Contains(sessions, v.SessionID) {
				sessions = append(sessions, v.SessionID)
			}
		}
		d := diags[path]
		out = append(out, FileSummary{
			Path: path, Versions: len(versions), VersionIDs: ids,
			Additions: additions, Removals: removals,
			Diagnostics: FileDiagnostics{Error: d.Error, Warning: d.Warning},
			UpdatedAt:   last.UpdatedAt, SessionIDs: sessions,
			StatsUnknown: unknown,
		})
	}
	slices.SortFunc(out, func(a, b FileSummary) int {
		return cmp.Or(cmp.Compare(b.UpdatedAt, a.UpdatedAt), cmp.Compare(a.Path, b.Path))
	})
	return out, nil
}

// fileStatCache memoizes the first-to-latest line counts of edited paths.
// Every snapshot and every file list rebuilds the summaries, and diffing
// the same two versions again can only repeat work: the counts change
// only when a version does. It holds one entry per path, replaced when
// the first or the latest version moves.
type fileStatCache struct {
	mu      sync.Mutex
	entries map[string]fileStat
	// diffed counts the diffs computed, so a test can tell a hit from a
	// recompute.
	diffed int
}

type fileStat struct {
	first, last string
	additions   int
	removals    int
	unknown     bool
}

func newFileStatCache() *fileStatCache {
	return &fileStatCache{entries: map[string]fileStat{}}
}

// totals returns the line counts between the first and the latest version
// of path. A version over the diff cap makes the counts unknown instead of
// paying for a diff whose spans the drawer cannot use.
func (c *fileStatCache) totals(path string, versions []history.File) (additions, removals int, unknown bool) {
	first, last := versions[0], versions[len(versions)-1]
	firstID, lastID := versionIdentity(first), versionIdentity(last)
	c.mu.Lock()
	if entry, ok := c.entries[path]; ok && entry.first == firstID && entry.last == lastID {
		c.mu.Unlock()
		return entry.additions, entry.removals, entry.unknown
	}
	c.mu.Unlock()

	// Diff outside the lock: a megabyte file must not block the snapshots
	// of every other tab. A racing identical lookup recomputes the same
	// numbers and stores them again.
	if len(first.Content) > maxDiffBytes || len(last.Content) > maxDiffBytes {
		unknown = true
	} else {
		_, additions, removals = diff.GenerateDiff(first.Content, last.Content, path)
	}

	c.mu.Lock()
	if !unknown {
		c.diffed++
	}
	c.entries[path] = fileStat{first: firstID, last: lastID, additions: additions, removals: removals, unknown: unknown}
	c.mu.Unlock()
	return additions, removals, unknown
}

// diffCount reports how many diffs the cache has computed.
func (c *fileStatCache) diffCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.diffed
}

// versionIdentity names one history record for the stats cache. The record
// id is unique per version; a record without one falls back to its session
// and version, which is unique for a path.
func versionIdentity(v history.File) string {
	if v.ID != "" {
		return v.ID
	}
	return v.SessionID + ":" + strconv.FormatInt(v.Version, 10)
}

// buildDiff diffs two versions of one path. A nil bound picks the first or
// the latest version.
func buildDiff(path string, versions []history.File, from, to *int64) (FileDiff, error) {
	pick := func(want *int64, fallback history.File) (history.File, error) {
		if want == nil {
			return fallback, nil
		}
		for _, v := range versions {
			if v.Version == *want {
				return v, nil
			}
		}
		return history.File{}, errUnknownVersion
	}
	a, err := pick(from, versions[0])
	if err != nil {
		return FileDiff{}, err
	}
	b, err := pick(to, versions[len(versions)-1])
	if err != nil {
		return FileDiff{}, err
	}
	unified, _, _ := diff.GenerateDiff(a.Content, b.Content, path)
	d := FileDiff{
		Path: path, From: a.Version, To: b.Version,
		Old: a.Content, New: b.Content, Unified: unified, Language: languageFor(path),
	}
	if len(d.Old)+len(d.New)+len(d.Unified) > maxDiffBytes {
		d.Old, d.New, d.Truncated = "", "", true
		d.Unified = truncateLines(d.Unified, maxDiffBytes)
	}
	return d, nil
}

// truncateLines cuts s to at most limit bytes, at the last line break when
// there is one and never inside a UTF-8 sequence.
func truncateLines(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if i := strings.LastIndexByte(s[:limit], '\n'); i >= 0 {
		return s[:i+1]
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

var languages = map[string]string{
	".go": "go", ".ts": "typescript", ".mts": "typescript", ".cts": "typescript", ".tsx": "tsx",
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "jsx",
	".json": "json", ".css": "css", ".html": "html", ".htm": "html",
	".md": "markdown", ".py": "python", ".yaml": "yaml", ".yml": "yaml",
}

// languageFor names the syntax the diff view highlights; the browser maps
// each name to a lazily loaded CodeMirror language.
func languageFor(path string) string {
	if l, ok := languages[strings.ToLower(filepath.Ext(path))]; ok {
		return l
	}
	return "text"
}

func (g *Gateway) handleFiles(w http.ResponseWriter, r *http.Request) {
	files, err := g.fileSummaries(r.Context(), r.PathValue("sid"))
	if err != nil {
		writeInternal(w, "list_files", err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// handleFileDiff answers only for paths in the session tree's history, and
// reads nothing but that history: a path is a lookup key, never a file to
// open.
func (g *Gateway) handleFileDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	path := q.Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "missing_path", "path is required")
		return
	}
	from, okFrom := versionParam(q.Get("from"))
	to, okTo := versionParam(q.Get("to"))
	if !okFrom || !okTo {
		writeError(w, http.StatusBadRequest, "bad_version", "from and to must be version numbers")
		return
	}
	byPath, err := g.treeHistory(r.Context(), r.PathValue("sid"))
	if err != nil {
		writeInternal(w, "file_history", err)
		return
	}
	versions, ok := byPath[path]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_path", "the session has no history for this path")
		return
	}
	d, err := buildDiff(path, versions, from, to)
	if errors.Is(err, errUnknownVersion) {
		writeError(w, http.StatusBadRequest, "bad_version", err.Error())
		return
	}
	if err != nil {
		writeInternal(w, "build_diff", err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func versionParam(s string) (*int64, bool) {
	if s == "" {
		return nil, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return nil, false
	}
	return &n, true
}
