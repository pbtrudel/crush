package web

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
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
	// maxDiffInputBytes bounds one side of a diff. A pair of files under it
	// is always diffed: the unified text is what a big answer falls back
	// to, so only a side larger than this gives up on the diff entirely.
	maxDiffInputBytes = 8 << 20
	// maxDiffInputLines bounds one side of a diff by lines. The diff splits
	// each side into a slice of lines, so a side of short lines costs far
	// more than its bytes: 200,000 lines is about 3 MB of slice headers per
	// side, about 7 MB for a pair, on top of the text itself.
	maxDiffInputLines = 200_000
	// maxTreeSessions bounds the child-session walk. A real tree is a few
	// dozen sessions; the cap only stops a runaway or cyclic parent chain.
	maxTreeSessions = 256
)

// FileDiagnostics is the LSP diagnostic count for one edited file.
type FileDiagnostics struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
}

// VersionTool names the tool call that produced one version of a file.
// A version no tool produced, such as a baseline of a file changed outside
// Crush, carries no id.
type VersionTool struct {
	SessionID  string `json:"session_id"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// FileSummary is one edited path across a session and every session below
// it. VersionIDs are positions in that tree's history, in creation order,
// not history.Version: a subagent's first edit of a path starts a version
// 0 of its own, so versions collide across sessions. VersionTools has one
// entry per version, in the same order.
type FileSummary struct {
	Path         string          `json:"path"`
	Versions     int             `json:"versions"`
	VersionIDs   []int64         `json:"version_ids"`
	VersionTools []VersionTool   `json:"version_tools"`
	Additions    int             `json:"additions"`
	Removals     int             `json:"removals"`
	Diagnostics  FileDiagnostics `json:"diagnostics"`
	UpdatedAt    int64           `json:"updated_at"`
	SessionIDs   []string        `json:"session_ids"`
	// StatsUnknown reports that a version is over the diff cap, so the
	// counts are not known. They are zero instead of misleading.
	StatsUnknown bool `json:"stats_unknown,omitempty"`
}

// FileDiff is the diff between two versions of one edited path. From and
// To are positions in the tree's history, the same numbering as
// FileSummary.VersionIDs. Truncated answers carry only the unified text.
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
// versions in creation order, without loading any content. Version numbers
// are per path and session, so two sessions can both hold a version 0 of
// one path: creation time decides the order, then the session, then the
// version, then the record id.
func (g *Gateway) treeHistory(ctx context.Context, root string) (map[string][]history.FileMeta, error) {
	ids, err := g.sessionTree(ctx, root)
	if err != nil {
		return nil, err
	}
	byPath := map[string][]history.FileMeta{}
	for _, id := range ids {
		files, err := g.ws.ListSessionHistoryMeta(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			byPath[f.Path] = append(byPath[f.Path], f)
		}
	}
	for _, versions := range byPath {
		// The session comes before the version so a session's versions stay
		// together and in their own order when two sessions write the same
		// path in the same second; the sort is stable so a full tie keeps
		// the order the workspace listed.
		slices.SortStableFunc(versions, func(a, b history.FileMeta) int {
			return cmp.Or(
				cmp.Compare(a.CreatedAt, b.CreatedAt),
				cmp.Compare(a.SessionID, b.SessionID),
				cmp.Compare(a.Version, b.Version),
				cmp.Compare(a.ID, b.ID),
			)
		})
	}
	return byPath, nil
}

// fileSummaries lists root's edited files, newest first. The counts are
// first against latest, the same numbers the TUI sidebar shows. Only those
// two versions of a path are ever loaded, and only on a stats cache miss.
func (g *Gateway) fileSummaries(ctx context.Context, root string) ([]FileSummary, error) {
	byPath, err := g.treeHistory(ctx, root)
	if err != nil {
		return nil, err
	}
	diags := g.ws.LSPDiagnosticsByFile(ctx)
	load := func(id string) (history.File, error) {
		return g.ws.GetHistoryVersion(ctx, id)
	}
	out := make([]FileSummary, 0, len(byPath))
	for path, versions := range byPath {
		last := versions[len(versions)-1]
		additions, removals, unknown := g.fileStats.totals(ctx, path, versions, load)
		ids := make([]int64, len(versions))
		tools := make([]VersionTool, len(versions))
		var sessions []string
		for i, v := range versions {
			ids[i] = int64(i)
			tools[i] = VersionTool{SessionID: v.SessionID, ToolCallID: v.ToolCallID}
			if !slices.Contains(sessions, v.SessionID) {
				sessions = append(sessions, v.SessionID)
			}
		}
		d := diags[path]
		out = append(out, FileSummary{
			Path: path, Versions: len(versions), VersionIDs: ids, VersionTools: tools,
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
	// slots bounds the diff work of a cache miss, together with the diffs
	// the browser asks for.
	slots *diffSlots
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

func newFileStatCache(slots *diffSlots) *fileStatCache {
	return &fileStatCache{entries: map[string]fileStat{}, slots: slots}
}

// totals returns the line counts between the first and the latest version
// of path, loading just those two through load. A side the metadata already
// reports over the per-side limits, or one that cannot be loaded, makes the
// counts unknown instead of failing the whole file list.
func (c *fileStatCache) totals(ctx context.Context, path string, metas []history.FileMeta, load func(id string) (history.File, error)) (additions, removals int, unknown bool) {
	var cacheable bool
	first, last := metas[0], metas[len(metas)-1]
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
	additions, removals, unknown, cacheable = c.compute(ctx, path, first, last, load)

	c.mu.Lock()
	// Only a deterministic outcome is stored. A transient load failure says
	// nothing about the versions, so caching it would keep the counts
	// unknown until the next version; a cancelled request says even less.
	if cacheable && ctx.Err() == nil {
		if !unknown {
			c.diffed++
		}
		c.entries[path] = fileStat{first: firstID, last: lastID, additions: additions, removals: removals, unknown: unknown}
	}
	c.mu.Unlock()
	return additions, removals, unknown
}

// compute loads the first and the latest version of a path and diffs them
// under a diff slot. Every failure is unknown: a stale or unreadable
// version must not fail the whole list. cacheable reports that the answer
// follows from the versions themselves, so a transient failure is retried
// on the next summary instead of being memoized.
func (c *fileStatCache) compute(ctx context.Context, path string, first, last history.FileMeta, load func(id string) (history.File, error)) (additions, removals int, unknown, cacheable bool) {
	if first.Size > maxDiffInputBytes || last.Size > maxDiffInputBytes {
		return 0, 0, true, true
	}
	// One version has nothing to diff, so there is no load and no diff.
	if versionIdentity(first) == versionIdentity(last) {
		return 0, 0, false, true
	}
	release, err := c.slots.acquire(ctx)
	if err != nil {
		return 0, 0, true, false
	}
	defer release()
	a, err := load(first.ID)
	if err != nil {
		slog.Debug("Failed to load a file version for stats", "path", path, "version", first.ID, "error", err)
		return 0, 0, true, false
	}
	b, err := load(last.ID)
	if err != nil {
		slog.Debug("Failed to load a file version for stats", "path", path, "version", last.ID, "error", err)
		return 0, 0, true, false
	}
	if tooManyLines(a.Content) || tooManyLines(b.Content) {
		return 0, 0, true, true
	}
	_, additions, removals = diff.GenerateDiff(a.Content, b.Content, path)
	return additions, removals, false, true
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
func versionIdentity(m history.FileMeta) string {
	if m.ID != "" {
		return m.ID
	}
	return m.SessionID + ":" + strconv.FormatInt(m.Version, 10)
}

// pickVersions resolves the two versions of a diff by their position in
// metas, the path's history. A nil bound picks the first or the latest
// version.
func pickVersions(metas []history.FileMeta, from, to *int64) (a, b history.FileMeta, fromIdx, toIdx int64, err error) {
	pick := func(want *int64, fallback int64) (history.FileMeta, int64, error) {
		i := fallback
		if want != nil {
			i = *want
		}
		if i < 0 || i >= int64(len(metas)) {
			return history.FileMeta{}, 0, errUnknownVersion
		}
		return metas[i], i, nil
	}
	a, fromIdx, err = pick(from, 0)
	if err != nil {
		return history.FileMeta{}, history.FileMeta{}, 0, 0, err
	}
	b, toIdx, err = pick(to, int64(len(metas)-1))
	if err != nil {
		return history.FileMeta{}, history.FileMeta{}, 0, 0, err
	}
	return a, b, fromIdx, toIdx, nil
}

// buildDiff diffs two loaded versions of one path, addressed by their
// position in the path's history.
func buildDiff(path string, a, b history.File, fromIdx, toIdx int64) FileDiff {
	d := FileDiff{Path: path, From: fromIdx, To: toIdx, Language: languageFor(path)}
	// Only a side over a per-side limit is refused: the diff of two such
	// files would cost far more than the drawer can render, and there is no
	// unified text to fall back to either.
	if len(a.Content) > maxDiffInputBytes || len(b.Content) > maxDiffInputBytes ||
		tooManyLines(a.Content) || tooManyLines(b.Content) {
		d.Truncated = true
		return d
	}
	unified, _, _ := diff.GenerateDiff(a.Content, b.Content, path)
	d.Old, d.New, d.Unified = a.Content, b.Content, unified
	if len(d.Old)+len(d.New)+len(d.Unified) > maxDiffBytes {
		d.Old, d.New, d.Truncated = "", "", true
		d.Unified = truncateLines(d.Unified, maxDiffBytes)
	}
	return d
}

// tooManyLines reports a side the diff would split into more lines than
// the cap allows.
func tooManyLines(c string) bool {
	return strings.Count(c, "\n") > maxDiffInputLines
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
	sid := r.PathValue("sid")
	if !g.sessionExists(w, r, sid) {
		return
	}
	files, err := g.fileSummaries(r.Context(), sid)
	if err != nil {
		writeInternal(w, "list_files", err)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// handleFileDiff answers only for paths in the session tree's history, and
// reads nothing but that history: a path is a lookup key, never a file to
// open. Just the two versions the answer shows are loaded.
func (g *Gateway) handleFileDiff(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	if !g.sessionExists(w, r, sid) {
		return
	}
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
	byPath, err := g.treeHistory(r.Context(), sid)
	if err != nil {
		writeInternal(w, "file_history", err)
		return
	}
	metas, ok := byPath[path]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_path", "the session has no history for this path")
		return
	}
	a, b, fromIdx, toIdx, err := pickVersions(metas, from, to)
	if errors.Is(err, errUnknownVersion) {
		writeError(w, http.StatusBadRequest, "bad_version", err.Error())
		return
	}
	// The metadata already carries both sizes: a side over the per-side
	// limit answers the truncated refusal without loading either version.
	if a.Size > maxDiffInputBytes || b.Size > maxDiffInputBytes {
		writeJSON(w, http.StatusOK, FileDiff{
			Path: path, From: fromIdx, To: toIdx,
			Language: languageFor(path), Truncated: true,
		})
		return
	}
	release, err := g.diffs.acquire(r.Context())
	if err != nil {
		// The client went away while the request waited for a diff slot.
		return
	}
	var once sync.Once
	releaseOnce := func() { once.Do(release) }
	defer releaseOnce()
	first, err := g.ws.GetHistoryVersion(r.Context(), a.ID)
	if err != nil {
		writeHistoryLoadError(w, r, "file_diff", err)
		return
	}
	last, err := g.ws.GetHistoryVersion(r.Context(), b.ID)
	if err != nil {
		writeHistoryLoadError(w, r, "file_diff", err)
		return
	}
	d := buildDiff(path, first, last, fromIdx, toIdx)
	// Free the slot before writing: a stalled reader must not pin diff work.
	releaseOnce()
	writeJSON(w, http.StatusOK, d)
}

// writeHistoryLoadError answers a version load that failed while the
// request was still live. A client that left mid-load gets no answer and
// no warning log: the failure is its own cancellation, not the store's.
func writeHistoryLoadError(w http.ResponseWriter, r *http.Request, code string, err error) {
	if r.Context().Err() != nil {
		return
	}
	writeHistoryError(w, code, err)
}

// writeHistoryError answers a version load that failed after the metadata
// listed it. A miss is the gateway's own answer, so a version deleted
// between the listing and the load reads as gone; anything else is the
// generic internal error, never the upstream message.
func writeHistoryError(w http.ResponseWriter, code string, err error) {
	if isNotFound(err) {
		writeError(w, http.StatusNotFound, "unknown_version", "a version of this file is no longer available")
		return
	}
	writeInternal(w, code, err)
}

// sessionExists answers 404 unknown_session for a session the workspace
// does not have, the same answer the messages route gives, and reports
// whether the handler may continue.
func (g *Gateway) sessionExists(w http.ResponseWriter, r *http.Request, sid string) bool {
	if _, err := g.ws.GetSession(r.Context(), sid); err != nil {
		if isSessionNotFound(err) {
			writeError(w, http.StatusNotFound, "unknown_session", "no such session")
			return false
		}
		writeInternal(w, "get_session", err)
		return false
	}
	return true
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
