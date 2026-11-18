package logreport

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/message"
)

// injectionTags are the tags the harness opens its hidden user rows with.
// The report owns the list: a test in each package that writes one of
// them asserts, through InjectionTag, that its text carries the tag. No
// code writes background_subagent_notice any more; it stays so that older
// rows read the same.
var injectionTags = []string{
	"system_reminder",
	"background_job_notice",
	"env_snapshot",
	"background_subagent_notice",
}

// subagentReportKind is the kind of a user row that carries a subagent
// report.
const subagentReportKind = "subagent_report"

// InjectionTag returns the tag a hidden user text opens with when the
// report knows it, and "" otherwise.
func InjectionTag(text string) string {
	for _, tag := range injectionTags {
		if strings.HasPrefix(text, "<"+tag+">") {
			return tag
		}
	}
	return ""
}

// userRow is what a user row is to the report.
type userRow int

const (
	// otherRow has no text and no report, such as a bang-mode shell
	// command.
	otherRow userRow = iota
	// visibleRow carries a visible text part: a prompt, a continuation or
	// a message to a child session.
	visibleRow
	// injectionRow is a hidden row that opens with a known tag, or a
	// subagent report.
	injectionRow
	// hiddenTurnRow is any other hidden row: a goal, a loop iteration, a
	// plan handoff.
	hiddenTurnRow
)

// classifyUser says what a user row is. For an injection it also returns
// its kind and the characters it adds to the history.
func classifyUser(m *message.Message) (row userRow, kind string, chars int) {
	if reports := m.SubAgentReports(); len(reports) > 0 {
		for _, r := range reports {
			chars += utf8.RuneCountInString(r.PromptText())
		}
		return injectionRow, subagentReportKind, chars
	}
	first, hidden := "", false
	for _, part := range m.Parts {
		text, ok := part.(message.TextContent)
		if !ok {
			continue
		}
		if !text.Hidden {
			return visibleRow, "", 0
		}
		if !hidden {
			first, hidden = text.Text, true
		}
		chars += utf8.RuneCountInString(text.Text)
	}
	if !hidden {
		return otherRow, "", 0
	}
	if tag := InjectionTag(first); tag != "" {
		return injectionRow, tag, chars
	}
	return hiddenTurnRow, "", 0
}

// Injections is the Injections section: the rows the harness writes into
// the history of every session, by kind.
type Injections struct {
	// ByKind is sorted by rows, most first, ties by kind.
	ByKind []Injection `json:"by_kind"`
}

// Injection is one kind of injected row.
type Injection struct {
	Kind     string `json:"kind"`
	Rows     int    `json:"rows"`
	Sessions int    `json:"sessions"`
	// Chars counts the characters the rows add to the history.
	Chars int `json:"characters"`
}

type injectionsSection struct {
	kinds map[string]*Injection
	// seen holds the kinds the current session has had.
	seen map[string]bool
}

func newInjectionsSection() *injectionsSection {
	return &injectionsSection{kinds: map[string]*Injection{}, seen: map[string]bool{}}
}

func (s *injectionsSection) message(_ *pass, m *message.Message) {
	if m.Role != message.User {
		return
	}
	row, kind, chars := classifyUser(m)
	if row != injectionRow {
		return
	}
	in := s.kinds[kind]
	if in == nil {
		in = &Injection{Kind: kind}
		s.kinds[kind] = in
	}
	in.Rows++
	in.Chars += chars
	if !s.seen[kind] {
		s.seen[kind] = true
		in.Sessions++
	}
}

func (s *injectionsSection) endSession(*pass) {
	clear(s.seen)
}

func (s *injectionsSection) finish(_ *pass, r *Report) {
	r.Injections.ByKind = []Injection{}
	for _, in := range s.kinds {
		r.Injections.ByKind = append(r.Injections.ByKind, *in)
	}
	slices.SortFunc(r.Injections.ByKind, func(a, b Injection) int {
		return cmp.Or(cmp.Compare(b.Rows, a.Rows), cmp.Compare(a.Kind, b.Kind))
	})
}

func (in Injections) writeText(out io.Writer) {
	fmt.Fprintln(out, "Injections (all sessions)")
	fmt.Fprintln(out, "  kind\trows\tsessions\tcharacters")
	for _, k := range in.ByKind {
		fmt.Fprintf(out, "  %s\t%d\t%d\t%d\n", k.Kind, k.Rows, k.Sessions, k.Chars)
	}
	fmt.Fprintln(out)
}
