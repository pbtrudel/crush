package logreport

import (
	"cmp"
	"fmt"
	"io"
	"slices"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// Tools is the Tools section: calls and error results per tool.
type Tools struct {
	Calls  int     `json:"calls"`
	Errors int     `json:"errors"`
	Rate   float64 `json:"error_rate"`
	// ByTool is sorted by calls, most first, ties by name.
	ByTool []ToolStats `json:"by_tool"`
	// ViewRead adds up view and read, one tool under two names.
	ViewRead ToolStats `json:"view_and_read"`
}

// ToolStats is one tool's calls, error results and their kinds.
type ToolStats struct {
	Name   string         `json:"name"`
	Calls  int            `json:"calls"`
	Errors int            `json:"errors"`
	Rate   float64        `json:"error_rate"`
	Kinds  map[string]int `json:"error_kinds,omitempty"`
}

// unrecognizedTool is the only name the report prints for a tool whose
// calls never ran under a known name: an invented name is model text.
const unrecognizedTool = "unrecognized"

// kindKey is a failure's kind, or its call's build when it has no kind:
// whether that reads "other" or "unstamped" is known after the pass.
type kindKey struct {
	kind  string
	build string
}

type kindCounts map[kindKey]int

func (k kindCounts) add(kind, build string) {
	if kind != "" {
		build = ""
	}
	k[kindKey{kind: kind, build: build}]++
}

func (k kindCounts) resolve(p *pass) map[string]int {
	if len(k) == 0 {
		return nil
	}
	out := map[string]int{}
	for key, n := range k {
		out[p.kindName(key.kind, key.build)] += n
	}
	return out
}

type toolsSection struct {
	calls  map[string]int
	errors map[string]int
	kinds  map[string]kindCounts
	// recognized holds the names whose results show a tool ran under
	// that name: anything else is folded into unrecognizedTool.
	recognized map[string]bool
}

func newToolsSection() *toolsSection {
	return &toolsSection{
		calls:      map[string]int{},
		errors:     map[string]int{},
		kinds:      map[string]kindCounts{},
		recognized: map[string]bool{},
	}
}

func (s *toolsSection) message(p *pass, m *message.Message) {
	switch m.Role {
	case message.Assistant:
		for _, tc := range m.ToolCalls() {
			s.calls[tc.Name]++
		}
	case message.Tool:
		for _, tr := range m.ToolResults() {
			kind := gjson.Get(tr.Metadata, "error_kind").String()
			if !tr.IsError || (kind != "" && kind != "unknown_tool") {
				s.recognized[tr.Name] = true
			}
			if !tr.IsError {
				continue
			}
			s.errors[tr.Name]++
			if s.kinds[tr.Name] == nil {
				s.kinds[tr.Name] = kindCounts{}
			}
			s.kinds[tr.Name].add(kind, p.calls[tr.ToolCallID].build)
		}
	}
}

func (s *toolsSection) endSession(*pass) {}

func (s *toolsSection) finish(p *pass, r *Report) {
	t := Tools{ByTool: []ToolStats{}, ViewRead: ToolStats{Name: viewTool + "+" + readTool}}
	names := map[string]bool{}
	for name := range s.calls {
		names[name] = true
	}
	for name := range s.errors {
		names[name] = true
	}
	// A name that is not recognized is counted under unrecognizedTool, so
	// several invented names share one line.
	folded := map[string]*ToolStats{}
	for name := range names {
		printed := name
		if !s.recognized[name] {
			printed = unrecognizedTool
		}
		stats, ok := folded[printed]
		if !ok {
			stats = &ToolStats{Name: printed}
			folded[printed] = stats
		}
		stats.Calls += s.calls[name]
		stats.Errors += s.errors[name]
		for kind, n := range s.kinds[name].resolve(p) {
			if stats.Kinds == nil {
				stats.Kinds = map[string]int{}
			}
			stats.Kinds[kind] += n
		}
	}
	for _, stats := range folded {
		stats.Rate = ratio(stats.Errors, stats.Calls)
		t.ByTool = append(t.ByTool, *stats)
		t.Calls += stats.Calls
		t.Errors += stats.Errors
		if stats.Name == viewTool || stats.Name == readTool {
			t.ViewRead.Calls += stats.Calls
			t.ViewRead.Errors += stats.Errors
			for kind, n := range stats.Kinds {
				if t.ViewRead.Kinds == nil {
					t.ViewRead.Kinds = map[string]int{}
				}
				t.ViewRead.Kinds[kind] += n
			}
		}
	}
	t.Rate = ratio(t.Errors, t.Calls)
	t.ViewRead.Rate = ratio(t.ViewRead.Errors, t.ViewRead.Calls)
	slices.SortFunc(t.ByTool, func(a, b ToolStats) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Name, b.Name))
	})
	r.Tools = t
}

func (t Tools) writeText(out io.Writer) {
	fmt.Fprintln(out, "Tools")
	fmt.Fprintln(out, "  tool\tcalls\terrors\trate\tkinds")
	for _, s := range slices.Concat(t.ByTool, []ToolStats{t.ViewRead}) {
		fmt.Fprintf(out, "  %s\t%d\t%d\t%s\t%s\n", s.Name, s.Calls, s.Errors, percent(s.Rate), formatCounts(s.Kinds))
	}
	fmt.Fprintf(out, "  all\t%d\t%d\t%s\t\n", t.Calls, t.Errors, percent(t.Rate))
	fmt.Fprintln(out)
}

func percent(r float64) string {
	return fmt.Sprintf("%.2f%%", 100*r)
}
