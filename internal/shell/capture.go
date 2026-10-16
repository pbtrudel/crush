package shell

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// maxCaptureRetain bounds how much command output the capture holds in
// memory before it starts keeping only the ends of the stream. At or below
// it the captured text is exactly what the command printed, so the persist
// path behaves as it always has — including the case where output crosses
// the budget only because of escape sequences. Past it, a command that
// prints hundreds of megabytes costs a fixed few hundred KB instead of the
// same number again in RAM, which matters because that output arrives while
// the agent is holding the rest of the session in memory.
const maxCaptureRetain = 8 << 20

// Retention windows, in raw bytes, for the two ends kept after overflow.
// Stripping only removes bytes, so a raw window always contains the
// stripped content the renderer needs from that end.
const (
	headWindow = maxPersistedBytes
	tailWindow = maxPersistedBytes
)

// Render budgets for the overflow path. The rendered result is deliberately
// left inside [withinBudget] so [TruncateForPersist] passes an
// already-bounded rendering through untouched rather than truncating it a
// second time and pointing the agent at a truncated spill file.
const (
	sideBytes = maxPersistedBytes/2 - 48
	sideLines = maxPersistedLines/2 - 2
)

// boundedCapture is an io.Writer that accumulates command output without
// letting the output size dictate memory use. It is safe for concurrent
// use, because stdout and stderr are wired to the same writer.
type boundedCapture struct {
	mu         sync.Mutex
	onProgress func(string)

	// full holds the stream verbatim until it exceeds maxCaptureRetain.
	full []byte

	// head and tail take over once the stream is too large to hold: the
	// first headWindow bytes and a sliding window of the last tailWindow.
	head     []byte
	tail     *byteRing
	overflow bool

	totalLines int
	totalBytes int
}

func newBoundedCapture(onProgress func(string)) *boundedCapture {
	return &boundedCapture{onProgress: onProgress}
}

func (c *boundedCapture) Write(p []byte) (int, error) {
	// Live output is reported in full regardless of retention: the UI
	// shows the command as it runs, and the retention decision is about
	// what gets stored, not what gets displayed.
	if c.onProgress != nil && len(p) > 0 {
		c.onProgress(string(p))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalBytes += len(p)
	c.totalLines += strings.Count(string(p), "\n")

	if !c.overflow {
		c.full = append(c.full, p...)
		if len(c.full) > maxCaptureRetain {
			c.startOverflowLocked()
		}
		return len(p), nil
	}

	c.tail.Write(p)
	return len(p), nil
}

// startOverflowLocked converts a full retention into head plus tail and
// releases the accumulated buffer so its capacity is not pinned.
func (c *boundedCapture) startOverflowLocked() {
	c.overflow = true
	c.head = append([]byte(nil), c.full[:min(len(c.full), headWindow)]...)
	c.tail = newByteRing(tailWindow)
	if len(c.full) > headWindow {
		c.tail.Write(c.full[headWindow:])
	}
	c.full = nil
}

// String renders what the command produced, either verbatim or, for a
// stream that outgrew retention, as head and tail around a note about what
// was dropped.
func (c *boundedCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.overflow {
		return string(c.full)
	}

	head, headLines := clipHeadLines(c.head, sideLines, sideBytes)
	tail, tailLines := clipTailLines(c.tail.Bytes(), sideLines, sideBytes)

	omitted := c.totalLines - headLines - tailLines
	note := "output truncated"
	if omitted > 0 {
		note = fmt.Sprintf("%d lines omitted", omitted)
	}
	note += fmt.Sprintf(" (output exceeded %d MiB and was not saved)", maxCaptureRetain>>20)

	return head + "\n\n" + note + "\n\n" + tail
}

// clipHeadLines keeps whole lines from the front of a retained window and
// strips styling. Cutting on line boundaries is what keeps a partial escape
// sequence at the cut point from bleeding into the chat.
func clipHeadLines(raw []byte, maxLines, maxBytes int) (string, int) {
	s := string(raw)
	lines := strings.Split(s, "\n")

	kept := 0
	var b strings.Builder
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			break // trailing newline, not an extra line
		}
		if kept == maxLines || b.Len()+len(line)+1 > maxBytes {
			break
		}
		if kept > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		kept++
	}
	if kept == 0 {
		// A first line longer than the budget fits nowhere; keep as much
		// of it as the budget allows rather than returning nothing.
		out := clipHead(s, maxBytes)
		return trimTrailingEscape(ansi.Strip(out)), 1
	}
	return trimTrailingEscape(ansi.Strip(b.String())), kept
}

// clipTailLines keeps whole lines from the back of a retained window. The
// window can start in the middle of a line, so that fragment is dropped
// before anything else is considered.
func clipTailLines(raw []byte, maxLines, maxBytes int) (string, int) {
	s := string(raw)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	} else {
		// The window holds a single line: too little to trust its start.
		return "", 0
	}

	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	kept := 0
	var picked []string
	total := 0
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if kept == maxLines || total+len(line)+1 > maxBytes {
			break
		}
		if kept > 0 {
			total++ // the joining newline
		}
		total += len(line)
		picked = append(picked, line)
		kept++
	}
	if kept == 0 {
		out := clipTail(s, maxBytes)
		return ansi.Strip(out), 1
	}
	slices.Reverse(picked)
	return ansi.Strip(strings.Join(picked, "\n")), kept
}

// trimTrailingEscape drops an escape sequence cut in half by the retention
// window, which stripping alone cannot recognize.
func trimTrailingEscape(s string) string {
	if i := strings.LastIndexByte(s, 0x1b); i >= 0 {
		return s[:i]
	}
	return s
}

// byteRing keeps the last cap bytes written to it.
type byteRing struct {
	buf    []byte
	pos    int // next write index, and the oldest byte once full
	filled int // valid bytes, capped at len(buf)
}

func newByteRing(cap int) *byteRing {
	return &byteRing{buf: make([]byte, cap)}
}

func (r *byteRing) Write(p []byte) {
	if len(p) >= len(r.buf) {
		copy(r.buf, p[len(p)-len(r.buf):])
		r.pos = 0
		r.filled = len(r.buf)
		return
	}
	n := copy(r.buf[r.pos:], p)
	if n < len(p) {
		copy(r.buf, p[n:])
	}
	r.pos = (r.pos + len(p)) % len(r.buf)
	if r.filled < len(r.buf) {
		r.filled = min(len(r.buf), r.filled+len(p))
	}
}

// Bytes returns the retained bytes in arrival order.
func (r *byteRing) Bytes() []byte {
	if r.filled < len(r.buf) {
		return r.buf[:r.filled]
	}
	out := make([]byte, 0, len(r.buf))
	out = append(out, r.buf[r.pos:]...)
	out = append(out, r.buf[:r.pos]...)
	return out
}
