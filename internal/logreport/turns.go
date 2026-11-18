package logreport

import (
	"fmt"
	"io"
	"sort"

	"github.com/charmbracelet/crush/internal/message"
)

// Turns is the Turns section, for top-level sessions.
type Turns struct {
	Prompts       int `json:"prompts"`
	HiddenTurns   int `json:"hidden_turns"`
	Continuations int `json:"continuations"`
	// ChildRows counts the visible user rows of child sessions: the brief
	// each was started with and what was sent to it later. They are not
	// prompts.
	ChildRows int    `json:"child_session_rows"`
	Steps     Spread `json:"steps_per_prompt"`
	ToolCalls Spread `json:"tool_calls_per_prompt"`
	// TreeSteps and TreeToolCalls add each child session's steps to the
	// prompt that was open in its top-level session when it was written.
	TreeSteps     Spread `json:"steps_per_prompt_with_children"`
	TreeToolCalls Spread `json:"tool_calls_per_prompt_with_children"`
	// RunEnds counts runs by the turn_end stamp on their last step. Its
	// "unstamped" entry counts prompts instead: those whose steps carry
	// no stamp and come from a build that never stamps one.
	RunEnds map[string]int `json:"runs_by_turn_end"`
}

// Spread is a distribution: its median, 90th percentile and maximum.
type Spread struct {
	Median int `json:"median"`
	P90    int `json:"p90"`
	Max    int `json:"max"`
}

func spread(values []int) Spread {
	return Spread{
		Median: percentile(values, 50),
		P90:    percentile(values, 90),
		Max:    percentile(values, 100),
	}
}

// segment is what one prompt caused in its session: the steps from the
// prompt to the next prompt, summaries aside.
type segment struct {
	steps, calls         int
	treeSteps, treeCalls int
	// build is the build stamp of the segment's first step.
	build string
	// stamped is set when a step of the segment carries a turn end.
	stamped bool
}

// opening is when a prompt opened a segment of a top-level session.
type opening struct {
	at      int64
	segment int
}

// childStep is a step of a child session, kept until the pass is over,
// when the top-level session it belongs to is known.
type childStep struct {
	at    int64
	calls int
}

type turnsSection struct {
	turns    Turns
	segments []segment
	// turnStamping holds the builds that stamp turn ends.
	turnStamping map[string]bool
	// parents, openings and children are kept until the pass is over:
	// a child session can be read before its top-level session.
	parents  map[string]string
	openings map[string][]opening
	children map[string][]childStep
	// Per session.
	started, top bool
	open         int
	// lastEnd is the turn end on the session's last step, consumed by
	// the next user row that is not an injection.
	lastEnd message.TurnEnd
}

func newTurnsSection() *turnsSection {
	return &turnsSection{
		turns:        Turns{RunEnds: map[string]int{}},
		turnStamping: map[string]bool{},
		parents:      map[string]string{},
		openings:     map[string][]opening{},
		children:     map[string][]childStep{},
	}
}

func (s *turnsSection) message(p *pass, m *message.Message) {
	if !s.started {
		s.started, s.open = true, -1
		s.top = sessionKind(p.session, p.parent, "") == topLevelKind
		s.parents[p.session] = p.parent
	}
	switch {
	case m.Role == message.User:
		s.user(p, m)
	case m.Role == message.Assistant && !m.IsSummaryMessage:
		s.step(p, m)
	}
}

// continues reports whether a run that ended so is continued by a new
// run: the context was compacted, or a hook blocked the compaction, and
// the prompt was queued again.
func continues(end message.TurnEnd) bool {
	switch end {
	case message.TurnEndContextGuard, message.TurnEndContextOverflow, message.TurnEndCompactionBlocked:
		return true
	}
	return false
}

func (s *turnsSection) user(p *pass, m *message.Message) {
	row, _, _ := classifyUser(m)
	if row == injectionRow || row == otherRow {
		return
	}
	continued := continues(s.lastEnd)
	s.lastEnd = ""
	switch {
	case !s.top:
		if row == visibleRow {
			s.turns.ChildRows++
		}
	case continued:
		s.turns.Continuations++
	case row == hiddenTurnRow:
		s.turns.HiddenTurns++
	default:
		s.turns.Prompts++
		s.open = len(s.segments)
		s.segments = append(s.segments, segment{})
		s.openings[p.session] = append(s.openings[p.session], opening{at: m.CreatedAt, segment: s.open})
	}
}

func (s *turnsSection) step(p *pass, m *message.Message) {
	var end message.TurnEnd
	build := ""
	if f := m.FinishPart(); f != nil {
		end, build = f.TurnEnd, f.Build
	}
	s.lastEnd = end
	if end != "" && build != "" {
		s.turnStamping[build] = true
	}
	calls := len(m.ToolCalls())
	if !s.top {
		s.children[p.session] = append(s.children[p.session], childStep{at: m.CreatedAt, calls: calls})
		return
	}
	if end != "" {
		s.turns.RunEnds[string(end)]++
	}
	if s.open < 0 {
		return
	}
	seg := &s.segments[s.open]
	if seg.steps == 0 {
		seg.build = build
	}
	seg.steps++
	seg.calls += calls
	seg.stamped = seg.stamped || end != ""
}

func (s *turnsSection) endSession(*pass) {
	s.started, s.open, s.lastEnd = false, -1, ""
}

// root returns the top-most session above id that the pass read, or id.
func (s *turnsSection) root(id string) string {
	// The bound guards against a cycle in the parent links.
	for range 32 {
		parent := s.parents[id]
		if parent == "" {
			break
		}
		id = parent
	}
	return id
}

func (s *turnsSection) finish(_ *pass, r *Report) {
	for i := range s.segments {
		s.segments[i].treeSteps = s.segments[i].steps
		s.segments[i].treeCalls = s.segments[i].calls
	}
	for child, steps := range s.children {
		openings := s.openings[s.root(child)]
		for _, step := range steps {
			i := sort.Search(len(openings), func(i int) bool { return openings[i].at > step.at }) - 1
			if i < 0 {
				continue
			}
			seg := &s.segments[openings[i].segment]
			seg.treeSteps++
			seg.treeCalls += step.calls
		}
	}
	var steps, calls, treeSteps, treeCalls []int
	for _, seg := range s.segments {
		steps = append(steps, seg.steps)
		calls = append(calls, seg.calls)
		treeSteps = append(treeSteps, seg.treeSteps)
		treeCalls = append(treeCalls, seg.treeCalls)
		if seg.steps > 0 && !seg.stamped && !s.turnStamping[seg.build] {
			s.turns.RunEnds["unstamped"]++
		}
	}
	s.turns.Steps, s.turns.ToolCalls = spread(steps), spread(calls)
	s.turns.TreeSteps, s.turns.TreeToolCalls = spread(treeSteps), spread(treeCalls)
	r.Turns = s.turns
}

func (t Turns) writeText(out io.Writer) {
	fmt.Fprintln(out, "Turns (top-level sessions)")
	fmt.Fprintf(out, "  prompts\t%d\n", t.Prompts)
	fmt.Fprintf(out, "  hidden turns\t%d\n", t.HiddenTurns)
	fmt.Fprintf(out, "  continuations\t%d\n", t.Continuations)
	fmt.Fprintf(out, "  visible rows in child sessions\t%d\n", t.ChildRows)
	fmt.Fprintln(out, "  per prompt\tmedian\tp90\tmax")
	for _, row := range []struct {
		name string
		s    Spread
	}{
		{"steps", t.Steps},
		{"tool calls", t.ToolCalls},
		{"steps with child sessions", t.TreeSteps},
		{"tool calls with child sessions", t.TreeToolCalls},
	} {
		fmt.Fprintf(out, "  %s\t%d\t%d\t%d\n", row.name, row.s.Median, row.s.P90, row.s.Max)
	}
	fmt.Fprintf(out, "  runs by turn end\t%s\n", formatCounts(t.RunEnds))
	fmt.Fprintln(out)
}
