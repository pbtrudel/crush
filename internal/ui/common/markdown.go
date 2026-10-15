package common

import (
	"image/color"
	"sync"

	"charm.land/glamour/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/ui/xchroma"
)

const formatterName = "crush"

func init() {
	// NOTE: Glamour does not offer us an option to pass the formatter
	// implementation directly. We need to register and use by name.
	var zero color.Color
	formatters.Register(formatterName, xchroma.Formatter(zero, nil))
}

// markdownRendererCacheMax bounds each per-width glamour renderer
// cache. Only the current width is hot; a few extra entries absorb
// resize oscillation without rebuilding a renderer per frame.
const markdownRendererCacheMax = 4

// mdCacheMu guards mdCache, quietMDCache, userMDCache, and planMDCache.
//
// Lock ordering: when both mdCacheMu and rendererLocksMu are
// needed (in InvalidateMarkdownRendererCache and when evicting
// renderers), acquire mdCacheMu FIRST, then rendererLocksMu. No
// other call site may hold rendererLocksMu while acquiring mdCacheMu.
var (
	mdCacheMu    sync.Mutex
	mdCache      = newLRUCache[int, *glamour.TermRenderer](markdownRendererCacheMax)
	quietMDCache = newLRUCache[int, *glamour.TermRenderer](markdownRendererCacheMax)
	userMDCache  = newLRUCache[int, *glamour.TermRenderer](markdownRendererCacheMax)
	planMDCache  = newLRUCache[int, *glamour.TermRenderer](markdownRendererCacheMax)
)

// MarkdownRenderer returns a glamour [glamour.TermRenderer] configured with
// the given styles and width. Renderers are memoized per width and shared
// across callers; call InvalidateMarkdownRendererCache when the active
// styles change.
//
// The returned renderer is NOT safe for concurrent Render calls
// (goldmark's BlockStack carries state across the public Render
// API). Crush's TUI is single-threaded so production never
// contends, but parallel callers (most notably parallel tests)
// must serialize via [LockMarkdownRenderer]. Treat the renderer
// as effectively pinned to one goroutine at a time.
func MarkdownRenderer(sty *styles.Styles, width int) *glamour.TermRenderer {
	mdCacheMu.Lock()
	defer mdCacheMu.Unlock()
	if r, ok := mdCache.get(width); ok {
		return r
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(sty.Markdown),
		glamour.WithWordWrap(width),
		glamour.WithChromaFormatter(formatterName),
	)
	evictRendererLocks(mdCache.put(width, r))
	return r
}

// UserMarkdownRenderer returns a glamour [glamour.TermRenderer] configured like
// [MarkdownRenderer] but with single line breaks preserved. Renderers are
// memoized per width and shared across callers; call
// InvalidateMarkdownRendererCache when the active styles change. Same
// concurrency contract as [MarkdownRenderer]: serialize via
// [LockMarkdownRenderer].
//
// User input is authored in a plain textarea, not written as Markdown source,
// so a lone newline is a line the user deliberately typed. Standard Markdown
// treats it as a soft break and joins the lines when rendering, which makes a
// submitted message display differently from what was typed (see
// charmbracelet/crush#3502). Preserving newlines keeps the display faithful.
//
// This is deliberately NOT applied to [MarkdownRenderer]: assistant output and
// dialog copy are genuine Markdown, where soft-wrapping a paragraph across
// source lines is normal and collapsing it is the correct rendering.
func UserMarkdownRenderer(sty *styles.Styles, width int) *glamour.TermRenderer {
	mdCacheMu.Lock()
	defer mdCacheMu.Unlock()
	if r, ok := userMDCache.get(width); ok {
		return r
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(sty.Markdown),
		glamour.WithWordWrap(width),
		glamour.WithChromaFormatter(formatterName),
		glamour.WithPreservedNewLines(),
	)
	evictRendererLocks(userMDCache.put(width, r))
	return r
}

// QuietMarkdownRenderer returns a glamour [glamour.TermRenderer] with no colors
// (plain text with structure) and the given width. Renderers are memoized per
// width and shared across callers. Same concurrency contract as
// [MarkdownRenderer]: serialize via [LockMarkdownRenderer].
func QuietMarkdownRenderer(sty *styles.Styles, width int) *glamour.TermRenderer {
	mdCacheMu.Lock()
	defer mdCacheMu.Unlock()
	if r, ok := quietMDCache.get(width); ok {
		return r
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(sty.QuietMarkdown),
		glamour.WithWordWrap(width),
		glamour.WithChromaFormatter(formatterName),
	)
	evictRendererLocks(quietMDCache.put(width, r))
	return r
}

// PlanMarkdownRenderer returns a glamour [glamour.TermRenderer] that keeps the
// rich markdown colors but paints the plan-card background under every
// primitive, for the final plan message card. Renderers are memoized per width
// and shared across callers. Same concurrency contract as [MarkdownRenderer]:
// serialize via [LockMarkdownRenderer].
func PlanMarkdownRenderer(sty *styles.Styles, width int) *glamour.TermRenderer {
	mdCacheMu.Lock()
	defer mdCacheMu.Unlock()
	if r, ok := planMDCache.get(width); ok {
		return r
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(sty.PlanMarkdown),
		glamour.WithWordWrap(width),
		glamour.WithChromaFormatter(formatterName),
	)
	evictRendererLocks(planMDCache.put(width, r))
	return r
}

// InvalidateMarkdownRendererCache drops every cached renderer
// AND every per-renderer mutex in a single atomic critical
// section so the two maps cannot disagree mid-toggle. Call this
// whenever the active styles change so subsequent renderers
// pick up the new ansi.StyleConfig.
//
// Existing holders of an old mutex (mid-Render goroutines) keep
// their reference safely; new renderers minted after the
// invalidation get freshly minted mutexes.
//
// Lock ordering: mdCacheMu is acquired first, then
// rendererLocksMu — see the comments on each mutex.
func InvalidateMarkdownRendererCache() {
	mdCacheMu.Lock()
	defer mdCacheMu.Unlock()
	rendererLocksMu.Lock()
	defer rendererLocksMu.Unlock()

	mdCache.reset()
	quietMDCache.reset()
	userMDCache.reset()
	planMDCache.reset()
	rendererLocks = map[*glamour.TermRenderer]*sync.Mutex{}
}

// evictRendererLocks drops the per-renderer mutexes of evicted
// renderers so rendererLocks stays bounded with the caches. Callers
// hold mdCacheMu (lock ordering: mdCacheMu before rendererLocksMu).
func evictRendererLocks(renderers []*glamour.TermRenderer) {
	if len(renderers) == 0 {
		return
	}
	rendererLocksMu.Lock()
	defer rendererLocksMu.Unlock()
	for _, r := range renderers {
		delete(rendererLocks, r)
	}
}

// rendererLocksMu guards rendererLocks. We key per-renderer
// mutexes by pointer so the lock granularity matches the
// renderer cache granularity (one mutex per (width, palette)
// renderer instance, not one mutex for the entire cache).
//
// Lock ordering: when both mdCacheMu and rendererLocksMu are
// needed (only in InvalidateMarkdownRendererCache), acquire
// mdCacheMu FIRST, then rendererLocksMu.
var (
	rendererLocksMu sync.Mutex
	rendererLocks   = map[*glamour.TermRenderer]*sync.Mutex{}
)

// LockMarkdownRenderer returns the per-renderer mutex used to
// serialize concurrent Render calls on a shared
// [glamour.TermRenderer] instance. The returned [*sync.Mutex] is
// stable for the lifetime of the renderer (i.e. until
// [InvalidateMarkdownRendererCache] is called).
//
// Callers that issue more than one Render call in the same
// logical operation should hold the mutex for the entire
// sequence so other goroutines do not interleave their own
// Render calls and corrupt the renderer state. F8's
// streamingMarkdown is the immediate consumer; other call
// sites that today issue exactly one Render call per item
// render are safe without locking under the single-threaded
// TUI Update loop, but should adopt this lock if they ever run
// in parallel (e.g. background prerender workers).
func LockMarkdownRenderer(r *glamour.TermRenderer) *sync.Mutex {
	rendererLocksMu.Lock()
	defer rendererLocksMu.Unlock()
	if mu, ok := rendererLocks[r]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	rendererLocks[r] = mu
	return mu
}
