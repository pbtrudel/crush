package logreport

import (
	"fmt"
	"io"
	"slices"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// defaultReadLines is what view returns when a call sets no limit
// (DefaultReadLimit in internal/agent/tools/view.go).
const defaultReadLines = 200

// Reads is the Reads section, for view and read together.
type Reads struct {
	Calls       int     `json:"calls"`
	WithLimit   int     `json:"with_limit"`
	LimitShare  float64 `json:"limit_share"`
	MedianLines int     `json:"median_lines_requested"`
	// ReReads touch a path already requested in the session.
	ReReads     int     `json:"re_reads"`
	ReReadShare float64 `json:"re_read_share"`
	// Paging reads set an offset or a limit, and the previous view or
	// read in the session touched the same path.
	Paging      int     `json:"paging"`
	PagingShare float64 `json:"paging_share"`
}

type readsSection struct {
	reads Reads
	lines []int
	// Per session.
	seen map[string]bool
	prev []string
}

func (s *readsSection) message(_ *pass, m *message.Message) {
	if m.Role != message.Assistant {
		return
	}
	for _, tc := range m.ToolCalls() {
		if tc.Name == viewTool || tc.Name == readTool {
			s.read(tc.Input)
		}
	}
}

func (s *readsSection) read(input string) {
	s.reads.Calls++
	limit, offset := gjson.Get(input, "limit"), gjson.Get(input, "offset")
	explicit := limit.Type == gjson.Number
	lines := defaultReadLines
	if explicit {
		s.reads.WithLimit++
		if n := int(limit.Int()); n > 0 {
			lines = n
		}
	}
	s.lines = append(s.lines, lines)

	var paths []string
	if p := gjson.Get(input, "file_path").String(); p != "" {
		paths = append(paths, p)
	}
	for _, p := range gjson.Get(input, "file_paths").Array() {
		paths = append(paths, p.String())
	}
	if len(paths) == 0 {
		return
	}
	reread, paging := false, false
	for _, p := range paths {
		reread = reread || s.seen[p]
		paging = paging || slices.Contains(s.prev, p)
		s.seen[p] = true
	}
	if reread {
		s.reads.ReReads++
	}
	if paging && (explicit || offset.Type == gjson.Number) {
		s.reads.Paging++
	}
	s.prev = paths
}

func (s *readsSection) endSession(*pass) {
	clear(s.seen)
	s.prev = nil
}

func (s *readsSection) finish(_ *pass, r *Report) {
	s.reads.LimitShare = ratio(s.reads.WithLimit, s.reads.Calls)
	s.reads.ReReadShare = ratio(s.reads.ReReads, s.reads.Calls)
	s.reads.PagingShare = ratio(s.reads.Paging, s.reads.Calls)
	s.reads.MedianLines = percentile(s.lines, 50)
	r.Reads = s.reads
}

// percentile returns the nearest-rank p-th percentile of values, which it
// sorts, or zero when there are none.
func percentile(values []int, p int) int {
	if len(values) == 0 {
		return 0
	}
	slices.Sort(values)
	rank := (p*len(values) + 99) / 100
	return values[max(rank, 1)-1]
}

func (r Reads) writeText(out io.Writer) {
	fmt.Fprintln(out, "Reads (view and read)")
	fmt.Fprintf(out, "  calls\t%d\n", r.Calls)
	fmt.Fprintf(out, "  with a limit\t%d\t%s\n", r.WithLimit, percent(r.LimitShare))
	fmt.Fprintf(out, "  median lines requested\t%d\n", r.MedianLines)
	fmt.Fprintf(out, "  re-reads\t%d\t%s\n", r.ReReads, percent(r.ReReadShare))
	fmt.Fprintf(out, "  paging\t%d\t%s\n", r.Paging, percent(r.PagingShare))
	fmt.Fprintln(out)
}
