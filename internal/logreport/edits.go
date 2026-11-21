package logreport

import (
	"fmt"
	"hash/fnv"
	"io"
	"strings"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// Edits is the Edits section, for edit, multiedit and apply_patch together.
type Edits struct {
	Calls  int            `json:"calls"`
	Failed int            `json:"failed"`
	Rate   float64        `json:"failure_rate"`
	Kinds  map[string]int `json:"failed_by_kind,omitempty"`
	// BatchFailures counts the failed edits inside multiedit calls that
	// otherwise succeeded. They do not enter the rate.
	BatchFailures     int            `json:"batch_failures"`
	BatchFailureKinds map[string]int `json:"batch_failures_by_kind,omitempty"`
	AfterRefusal      AfterRefusal   `json:"after_read_guard_refusal"`
	Runs              FailureRuns    `json:"failure_runs"`
}

// AfterRefusal is what followed each refusal by the read guard: the next
// edit or multiedit of the same file in the session is the retry, with
// the same text or a different one. An edit of another file first, or a
// write to the same file, means the model moved on.
type AfterRefusal struct {
	Refusals      int `json:"refusals"`
	RetrySameText int `json:"retry_same_text"`
	RetryNewText  int `json:"retry_different_text"`
	MovedOn       int `json:"moved_on"`
	NoRetry       int `json:"no_retry"`
}

// FailureRuns are runs of consecutive failed edit or multiedit results in
// a session with no successful one between them.
type FailureRuns struct {
	Runs   int `json:"runs"`
	Failed int `json:"failed_edits"`
	// ByLength counts runs of length 1, 2, 3, 4, and 5 or more.
	ByLength [5]int `json:"by_length"`
	Max      int    `json:"max"`
}

// editCall is an edit, multiedit or write call: its file, and a hash of
// its old_string values so that the text itself is never held.
type editCall struct {
	name string
	path string
	text uint64
}

type editsSection struct {
	edits      Edits
	kinds      kindCounts
	batchKinds kindCounts
	// Per session.
	calls   map[string]editCall
	pending []editCall
	run     int
}

func newEditsSection() *editsSection {
	return &editsSection{kinds: kindCounts{}, batchKinds: kindCounts{}, calls: map[string]editCall{}}
}

func (s *editsSection) message(p *pass, m *message.Message) {
	switch m.Role {
	case message.Assistant:
		for _, tc := range m.ToolCalls() {
			if tc.Name != editTool && tc.Name != multiEditTool && tc.Name != writeTool && tc.Name != applyPatchTool {
				continue
			}
			c := newEditCall(tc)
			s.follow(c)
			s.calls[tc.ID] = c
			if c.name != writeTool {
				s.edits.Calls++
			}
		}
	case message.Tool:
		for _, tr := range m.ToolResults() {
			if tr.Name == editTool || tr.Name == multiEditTool || tr.Name == applyPatchTool {
				s.result(p, tr)
			}
		}
	}
}

func newEditCall(tc message.ToolCall) editCall {
	h := fnv.New64a()
	olds := gjson.Get(tc.Input, "edits.#.old_string").Array()
	path := gjson.Get(tc.Input, "file_path").String()
	switch tc.Name {
	case editTool:
		olds = []gjson.Result{gjson.Get(tc.Input, "old_string")}
	case applyPatchTool:
		// The patch is the text, so an identical patch is the same
		// retry, and the section path is the first file it touches.
		patch := gjson.Get(tc.Input, "patch")
		olds = []gjson.Result{patch}
		path = patchPath(patch.String())
	}
	for _, old := range olds {
		h.Write([]byte(old.String()))
		h.Write([]byte{0})
	}
	return editCall{name: tc.Name, path: path, text: h.Sum64()}
}

// patchPath reads the first file an apply_patch call touches, so the
// after-refusal report can tell a retry of the same file from a move on.
func patchPath(patch string) string {
	for line := range strings.SplitSeq(patch, "\n") {
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if path, found := strings.CutPrefix(line, prefix); found {
				return strings.TrimSpace(path)
			}
		}
	}
	return ""
}

// follow settles the pending refusals that call c answers.
func (s *editsSection) follow(c editCall) {
	kept := s.pending[:0]
	for _, r := range s.pending {
		switch {
		case c.path == r.path && c.name == writeTool:
			s.edits.AfterRefusal.MovedOn++
		case c.path == r.path && c.text == r.text:
			s.edits.AfterRefusal.RetrySameText++
		case c.path == r.path:
			s.edits.AfterRefusal.RetryNewText++
		case c.name != writeTool:
			s.edits.AfterRefusal.MovedOn++
		default:
			kept = append(kept, r)
		}
	}
	s.pending = kept
}

func (s *editsSection) result(p *pass, tr message.ToolResult) {
	build := p.calls[tr.ToolCallID].build
	if !tr.IsError {
		s.endRun()
		for _, failed := range gjson.Get(tr.Metadata, "edits_failed").Array() {
			s.edits.BatchFailures++
			s.batchKinds.add(failed.Get("error_kind").String(), build)
		}
		return
	}
	s.run++
	s.edits.Failed++
	kind := gjson.Get(tr.Metadata, "error_kind").String()
	s.kinds.add(kind, build)
	if kind == "not_read" || kind == "changed_since_read" {
		s.edits.AfterRefusal.Refusals++
		if c, ok := s.calls[tr.ToolCallID]; ok {
			s.pending = append(s.pending, c)
		} else {
			s.edits.AfterRefusal.NoRetry++
		}
	}
}

func (s *editsSection) endRun() {
	if s.run == 0 {
		return
	}
	runs := &s.edits.Runs
	runs.Runs++
	runs.Failed += s.run
	runs.ByLength[min(s.run, 5)-1]++
	runs.Max = max(runs.Max, s.run)
	s.run = 0
}

func (s *editsSection) endSession(*pass) {
	s.endRun()
	s.edits.AfterRefusal.NoRetry += len(s.pending)
	s.pending = s.pending[:0]
	clear(s.calls)
}

func (s *editsSection) finish(p *pass, r *Report) {
	s.edits.Rate = ratio(s.edits.Failed, s.edits.Calls)
	s.edits.Kinds = s.kinds.resolve(p)
	s.edits.BatchFailureKinds = s.batchKinds.resolve(p)
	r.Edits = s.edits
}

func (e Edits) writeText(out io.Writer) {
	a, runs := e.AfterRefusal, e.Runs
	fmt.Fprintln(out, "Edits (edit, multiedit and apply_patch)")
	fmt.Fprintf(out, "  calls\t%d\n", e.Calls)
	fmt.Fprintf(out, "  failed\t%d\t%s\t%s\n", e.Failed, percent(e.Rate), formatCounts(e.Kinds))
	fmt.Fprintf(out, "  failed inside batches\t%d\t\t%s\n", e.BatchFailures, formatCounts(e.BatchFailureKinds))
	fmt.Fprintf(out, "  read-guard refusals\t%d\tretry same text %d, retry different text %d, moved on %d, no retry %d\n",
		a.Refusals, a.RetrySameText, a.RetryNewText, a.MovedOn, a.NoRetry)
	fmt.Fprintf(out, "  runs of failed edits\t%d\tlength 1: %d, 2: %d, 3: %d, 4: %d, 5 or more: %d, max %d\n",
		runs.Runs, runs.ByLength[0], runs.ByLength[1], runs.ByLength[2], runs.ByLength[3], runs.ByLength[4], runs.Max)
	fmt.Fprintln(out)
}
