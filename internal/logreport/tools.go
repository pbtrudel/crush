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
}

func newToolsSection() *toolsSection {
	return &toolsSection{calls: map[string]int{}, errors: map[string]int{}, kinds: map[string]kindCounts{}}
}

func (s *toolsSection) message(p *pass, m *message.Message) {
	switch m.Role {
	case message.Assistant:
		for _, tc := range m.ToolCalls() {
			s.calls[tc.Name]++
		}
	case message.Tool:
		for _, tr := range m.ToolResults() {
			if !tr.IsError {
				continue
			}
			s.errors[tr.Name]++
			if s.kinds[tr.Name] == nil {
				s.kinds[tr.Name] = kindCounts{}
			}
			s.kinds[tr.Name].add(gjson.Get(tr.Metadata, "error_kind").String(), p.calls[tr.ToolCallID].build)
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
	for name := range names {
		stats := ToolStats{
			Name:   name,
			Calls:  s.calls[name],
			Errors: s.errors[name],
			Rate:   ratio(s.errors[name], s.calls[name]),
			Kinds:  s.kinds[name].resolve(p),
		}
		t.ByTool = append(t.ByTool, stats)
		t.Calls += stats.Calls
		t.Errors += stats.Errors
		if name == viewTool || name == readTool {
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
