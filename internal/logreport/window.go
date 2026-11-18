package logreport

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
)

// Window is the Window section: what the pass read.
type Window struct {
	Since    *time.Time     `json:"since,omitempty"`
	Until    *time.Time     `json:"until,omitempty"`
	First    *time.Time     `json:"first_message,omitempty"`
	Last     *time.Time     `json:"last_message,omitempty"`
	Messages int            `json:"messages"`
	ByRole   map[string]int `json:"messages_by_role"`
	Sessions int            `json:"sessions"`
	// Skipped counts the messages whose parts do not decode. They count
	// as messages and are left out of every other figure.
	Skipped int     `json:"skipped_rows"`
	Builds  []Build `json:"builds"`
}

// Build is one build seen on the messages' finish parts, or "unstamped"
// for the assistant messages that carry none.
type Build struct {
	Build    string    `json:"build"`
	Messages int       `json:"messages"`
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
}

type windowSection struct {
	builds map[string]*Build
}

func (s *windowSection) message(_ *pass, m *message.Message) {
	build := ""
	if f := m.FinishPart(); f != nil {
		build = f.Build
	}
	if build == "" && m.Role != message.Assistant {
		return
	}
	if build == "" {
		build = "unstamped"
	}
	at := time.Unix(m.CreatedAt, 0)
	b, ok := s.builds[build]
	if !ok {
		b = &Build{Build: build, First: at, Last: at}
		s.builds[build] = b
	}
	b.Messages++
	if at.Before(b.First) {
		b.First = at
	}
	if at.After(b.Last) {
		b.Last = at
	}
}

func (s *windowSection) endSession(*pass) {}

func (s *windowSection) finish(p *pass, r *Report) {
	w := Window{ByRole: p.roles, Sessions: p.sessions, Skipped: p.skipped}
	if !p.opts.Since.IsZero() {
		w.Since = &p.opts.Since
	}
	if !p.opts.Until.IsZero() {
		w.Until = &p.opts.Until
	}
	for _, n := range p.roles {
		w.Messages += n
	}
	if w.Messages > 0 {
		first, last := time.Unix(p.first, 0), time.Unix(p.last, 0)
		w.First, w.Last = &first, &last
	}
	w.Builds = []Build{}
	for _, b := range s.builds {
		w.Builds = append(w.Builds, *b)
	}
	slices.SortFunc(w.Builds, func(a, b Build) int {
		return cmp.Or(a.First.Compare(b.First), cmp.Compare(a.Build, b.Build))
	})
	r.Window = w
}

func (w Window) writeText(out io.Writer) {
	fmt.Fprintln(out, "Window")
	fmt.Fprintf(out, "  since\t%s\n", formatTime(w.Since))
	fmt.Fprintf(out, "  until\t%s\n", formatTime(w.Until))
	fmt.Fprintf(out, "  messages\t%d\t%s\n", w.Messages, formatCounts(w.ByRole))
	fmt.Fprintf(out, "  sessions\t%d\n", w.Sessions)
	fmt.Fprintf(out, "  skipped rows\t%d\n", w.Skipped)
	for _, b := range w.Builds {
		fmt.Fprintf(out, "  build %s\t%d\t%s to %s\n", b.Build, b.Messages,
			b.First.Format(time.DateOnly), b.Last.Format(time.DateOnly))
	}
	fmt.Fprintln(out)
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "open"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// formatCounts lists counts as "name n", largest first, ties by name.
func formatCounts(counts map[string]int) string {
	names := slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + " " + strconv.Itoa(counts[name])
	}
	return strings.Join(parts, ", ")
}

// ParseBound reads a --since or --until value: a date such as 2026-11-09,
// meaning the start of that day in now's location, or an age such as 7d
// or 12h, meaning that long before now. An empty value is an open bound.
func ParseBound(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, value, now.Location()); err == nil {
		// A day whose midnight a daylight saving change skips parses as
		// the hour before, so take the start of the next zone period.
		if t.Format(time.DateOnly) != value {
			_, t = t.ZoneBounds()
		}
		return t, nil
	}
	if days, ok := strings.CutSuffix(value, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n >= 0 {
			return now.AddDate(0, 0, -n), nil
		}
	}
	if d, err := time.ParseDuration(value); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("%q is neither a date such as 2026-11-09 nor an age such as 7d", value)
}
