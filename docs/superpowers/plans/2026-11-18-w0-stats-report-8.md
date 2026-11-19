# Stats Report (Turns, Sessions, Injections, Compactions, By model, By build) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the sections Turns, Sessions, Injections, Compactions, By model and By build to `crush stats report`, in both output formats (delivery item 8 of the measurement design).

**Architecture:** Every section is one more accumulator behind the `section` interface of `internal/logreport` (item 7): one type, one entry in `newSections`, one field on `Report`, one line in `WriteText`. The one hand-written query gains a `LEFT JOIN` on `sessions` so the pass knows each session's parent; it stays one query, read row by row. User rows are classified once (`classifyUser`: prompt-like visible row, injection, hidden turn, other) and the classification is shared by Injections and Turns. Turns cuts each top-level session into segments, one per prompt; By model and By build read those segments after the pass. Child sessions, session kinds and the child steps that belong to a top-level prompt are resolved after the pass, because the pass reads sessions in id order and a child can come before its parent.

**Tech Stack:** Go 1.27, `database/sql`, `github.com/tidwall/gjson` (already a direct dependency), `text/tabwriter`, testify `require`.

**Spec:** `docs/superpowers/specs/2026-11-17-measurement-design.md`, section 2 (Report: the Turns, Sessions, Injections, Compactions, By model and By build rows, every definition, the unstamped rule, the injection tags), Evidence 6, Failure behavior, Validation (the report bullet and the live check), Scope, Delivery item 8. It builds on the item 7 plan, `docs/superpowers/plans/2026-11-18-w0-stats-report.md`, whose code is merged before this work starts. Code references are to origin/main `ba4c11fe` plus that plan.

## Global Constraints

- Delivery item 8 is split in four pull requests, each merged on its own before the next starts: part 1 (Injections and the producers' tags; Tasks 1 and 2), part 2 (Sessions and Compactions; Tasks 3 to 5), part 3 (Turns; Tasks 6 to 8), part 4 (By model and By build; Tasks 9 and 10).
- For each part, run `git fetch origin`, then work in a fresh worktree created from `origin/main` after the previous part has merged (`worktree create` with name `w0-stats-report-8a`, `w0-stats-report-8b`, `w0-stats-report-8c`, `w0-stats-report-8d`, base `origin/main`). Paths below are relative to its root.
- Files this plan touches: `internal/logreport/` (new files and the item 7 files `report.go`, `output.go`, `testlog_test.go`, `privacy_test.go`), `internal/cmd/stats_report.go` (`Long` only), and three new test files: `internal/agent/injection_tags_test.go`, `internal/agent/prompt/injection_tag_test.go`, `internal/automation/injection_tag_test.go`. Do not touch `internal/app/`, `internal/cmd/run.go`, `internal/agent/coordinator.go`, `internal/agent/session_tree.go`, `internal/agent/team.go`, `internal/agentteam/`, any file not listed above, or any SQL or migration file: items 4 to 6 are changing those files in parallel, and the producers of the injected text do not change.
- Privacy rule (spec section 2): the output is counts, rates and the names of tools, models, providers, kinds and builds only. It never prints message text, titles, paths or session ids, in either format. Session ids and parent ids are held in memory to resolve kinds and never printed. An injection kind is printed only when it is a tag from the report's own list; an unknown tag is never printed. By model and By build name no tool, because an `unknown_tool` result carries a name the model invented.
- The database is opened read-only, as item 7 does; nothing here opens it otherwise.
- Definitions, exactly:
  - User rows are classified in this order. A row with a `SubAgentReport` part is an injection of kind `subagent_report`. A row with a text part that is not hidden is a visible row. A row whose first hidden text part opens with `<tag>` for a tag in `injectionTags` (`system_reminder`, `background_job_notice`, `env_snapshot`, and the retired `background_subagent_notice`) is an injection of that kind. Any other row with hidden text is a hidden turn. A row with neither (a bang-mode shell command) is none of these.
  - Injection characters: the rune count of the row's hidden texts, or of each report's `PromptText()`.
  - Injections and Compactions cover every session. Turns covers top-level sessions only, its runs by turn end included.
  - Session kind, from the id and the parent: `team-` prefix `teammate`; `title-` prefix `title`; an id containing `$$` is `subagent` when the call after `$$` is an `agent` or `agent_dispatch` call in the window, `agentic_fetch` when it is an `agentic_fetch` call, else `unknown`; no parent `top_level`; else `unknown`. Kinds are resolved after the pass.
  - Steps are assistant rows that are not summaries.
  - Continuation: the first user row after a step stamped `context_guard`, `context_overflow` or `compaction_blocked`, that is not an injection or an other row, hidden or not. Summaries and injections between them do not matter. Each step resets what the next row sees, and each counted row consumes it. A cancel that drops the queued prompt leaves no trace in the log, so the next row after such a step is counted as a continuation.
  - Prompt: any other visible row in a top-level session. It opens a segment that runs to the next prompt in the session. A hidden turn or a continuation stays in the open segment. Steps before the session's first prompt in the window are in no segment. A visible row in a child session is a child-session row, not a prompt.
  - Segment figures: steps and tool calls (`tool_call` parts) of the session alone; and with child sessions, adding each child step to the segment of the child's top-level ancestor that was open at the step's `created_at` (the last segment whose prompt has `created_at` at or before it). The ancestor is found through the parents of the sessions read in the window; a child step with no such segment is left out. Median, 90th percentile and maximum, nearest rank (`percentile`, item 7).
  - Runs by turn end: top-level steps with a `turn_end` stamp, by value, listed under the value's own name. The entry `unstamped` counts prompts, not runs: segments with at least one step, no stamped step, and a first step whose build never stamps `turn_end` in the window (no build counts as never).
  - Compactions: summary rows that carry `CompactionStats`, by `trigger` (empty reads `unstamped`); median `tokens_before` per trigger and overall. Failed: summary rows without statistics whose finish reason is `error`. A summary still being written (no statistics, no error) is neither.
  - By model (model and provider of the message) and By build (`build` stamp, empty reads `unstamped`): prompts and median steps per prompt from the segments with at least one step, by their first step; tool calls and edit calls (`edit`, `multiedit`) by the step that made them; error results, failed edits and shell results by the step that made their call, and a result whose call is not in the window is skipped. Rates: tool error rate is error results over calls, edit failure rate failed edit calls over edit calls, shell failure rate failed foreground commands (`exit_code` non-zero or `interrupted`) over foreground commands. In text the shell column reads `unstamped` when the group has shell results but no foreground command, and `-` when it has none.
- JSON: `schema` stays 1; fields are only added. Rates and shares are fractions in JSON and percentages with two decimals in text, as in item 7.
- Style: gofumpt (`/home/pbtrudel/.local/bin/gofumpt`); comments on their own line start with a capital, end with a period, wrap at 78 columns; no em dashes anywhere; testify `require`; log messages start with a capital letter (this plan adds none).
- Commits: one per task, semantic subjects, no attribution lines. Commits are authorized for this work.
- Never open or print any `crush.json` or `crushrc` file. Never print message text, titles, paths or session ids from the real database; the live checks print only the report's own output.
- Size: part 1 about 330 changed lines, part 2 about 350, part 3 about 425, part 4 about 300, tests included. Part 3 is slightly above the 400-line target because the Turns section and its figures with child sessions ship together; each PR description states its size.

## Review Focus

1. A child session read before its parent, and a child of a child: the pass reads sessions in id order, so `a$$call` comes before its lead and a subagent of a teammate has no direct link to the lead. Kinds and the with-children figures must still resolve. Pinned in Task 3 (`TestSessionsByKind`) and Task 7 (`TestTurnsWithChildSessions`).
2. A hidden row that opens with a tag the report does not know (a newer build, or text a model wrote): it is a hidden turn, and its tag is never printed as a kind. Pinned in Task 1 (`TestInjectionsByKind`, `TestClassifyUser` and the privacy fixture).
3. A continuation separated from its stamped step by a summary and injections, a blocked compaction that still continues, a failed one that does not: none of these may cut a prompt in two or lose a prompt. Pinned in Task 6 (`TestTurnsPromptsHiddenTurnsAndContinuations`).
4. A window or a session that does not start with a prompt: steps before the first prompt, a child step before the lead's first prompt, a child whose parent has no row in the window. They belong to no segment and must not inflate one. Pinned in Task 6 (`TestTurnsWithoutStamps`) and Task 7.
5. An invented tool name: By model and By build must never print one. Pinned in Task 9 (`TestByModelAndByBuild`).

---

# Part 1: Injections and the producers' tags

Branch `w0-stats-report-8a`. When merged, the report has an Injections section, and each package that writes an injected text has a test that its text carries the report's tag.

### Task 1: The Injections section and the producers' tags

**Files:**
- Create: `internal/logreport/injections.go`
- Modify: `internal/logreport/report.go` (`Report`, `newSections`), `internal/logreport/output.go` (`WriteText`), `internal/cmd/stats_report.go` (`Long`)
- Test: `internal/logreport/injections_test.go` (new), `internal/logreport/testlog_test.go` (two helpers), `internal/logreport/privacy_test.go` (fixture), `internal/agent/injection_tags_test.go` (new), `internal/agent/prompt/injection_tag_test.go` (new), `internal/automation/injection_tag_test.go` (new)

**Interfaces:**
- Consumes: `section`, `pass`, `newSections`, `Report`, `newTestLog`, `(*testLog).add`, `marker` (item 7, Task 1); `message.SubAgentReport.PromptText`; the producers `todoReminder`, `repeatToolReminder`, `backgroundJobNotice` (`internal/agent`), `SessionEnvSnapshot` and the test helper `fixedSnapshotTime` (`internal/agent/prompt`), `GoalPrompt`, `LoopPrompt` (`internal/automation`).
- Produces:
  - `func InjectionTag(text string) string` (exported, used by the producer tests); `var injectionTags []string`; `const subagentReportKind = "subagent_report"`.
  - `type userRow int` with `otherRow`, `visibleRow`, `injectionRow`, `hiddenTurnRow`; `func classifyUser(m *message.Message) (row userRow, kind string, chars int)` (Turns uses it in Task 6).
  - `Report.Injections Injections` (JSON `injections`); `type Injections struct { ByKind []Injection }`; `type Injection struct { Kind string; Rows, Sessions, Chars int }`; `func newInjectionsSection() *injectionsSection`.
  - Test helpers: `(*testLog).summary(sessionID string, parts ...message.ContentPart)` (a compaction summary row), `(*testLog).child(id, parent string)` (a session with a parent).

- [ ] **Step 1: Write the failing tests**

Create `internal/logreport/injections_test.go`:

```go
package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestInjectionsByKind(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.session("s2", "title")
	hidden := func(session, text string) {
		log.add(session, message.User, message.TextContent{Text: text, Hidden: true})
	}
	hidden("s1", "<env_snapshot>\nToday's date: 1/1/2026")
	log.add("s1", message.User, message.TextContent{Text: "a prompt"})
	hidden("s1", "<system_reminder>abc</system_reminder>")
	// Characters, not bytes: "é" is one character.
	hidden("s1", "<system_reminder>dé</system_reminder>")
	hidden("s1", "<background_job_notice>x</background_job_notice>")
	log.add("s1", message.User, message.SubAgentReport{Label: "l", Output: "o"})
	// A goal opens with no tag: it is a hidden turn, not an injection.
	hidden("s1", "You are pursuing a standing goal across multiple turns.")
	// A tag the report does not know is no injection, and its name is
	// never printed.
	hidden("s1", "<a_tag_from_a_later_build>x")
	// A report is only an injection on a user row.
	log.add("s1", message.Assistant, message.SubAgentReport{Label: "l", Output: "o"})
	hidden("s2", "<system_reminder>abc</system_reminder>")

	require.Equal(t, []Injection{
		{Kind: "system_reminder", Rows: 3, Sessions: 2, Chars: 38 + 37 + 38},
		{Kind: "background_job_notice", Rows: 1, Sessions: 1, Chars: 48},
		{Kind: "env_snapshot", Rows: 1, Sessions: 1, Chars: 37},
		// The report as the model reads it, tags included.
		{Kind: "subagent_report", Rows: 1, Sessions: 1, Chars: 48},
	}, log.report(Options{}).Injections.ByKind)
}

func TestClassifyUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts []message.ContentPart
		row   userRow
		kind  string
	}{
		{"typed", []message.ContentPart{message.TextContent{Text: "fix it"}}, visibleRow, ""},
		{"attachment only", []message.ContentPart{message.TextContent{}, message.BinaryContent{Path: "a.png"}}, visibleRow, ""},
		{"visible beside hidden", []message.ContentPart{message.TextContent{Text: "<system_reminder>x", Hidden: true}, message.TextContent{Text: "fix it"}}, visibleRow, ""},
		{"reminder", []message.ContentPart{message.TextContent{Text: "<system_reminder>x", Hidden: true}}, injectionRow, "system_reminder"},
		{"retired notice", []message.ContentPart{message.TextContent{Text: "<background_subagent_notice>x", Hidden: true}}, injectionRow, "background_subagent_notice"},
		{"report", []message.ContentPart{message.SubAgentReport{Label: "l"}}, injectionRow, "subagent_report"},
		{"plan handoff", []message.ContentPart{message.TextContent{Text: "Implement the plan.", Hidden: true}}, hiddenTurnRow, ""},
		{"tag not at the start", []message.ContentPart{message.TextContent{Text: "see <system_reminder>", Hidden: true}}, hiddenTurnRow, ""},
		{"bang-mode shell", []message.ContentPart{message.ShellCommand{Command: "ls"}}, otherRow, ""},
	}
	for _, tt := range tests {
		row, kind, _ := classifyUser(&message.Message{Role: message.User, Parts: tt.parts})
		require.Equal(t, tt.row, row, tt.name)
		require.Equal(t, tt.kind, kind, tt.name)
	}
}
```

In `internal/logreport/testlog_test.go`, replace

```go
// add writes a message with the given parts at the log's clock and moves
```

with

```go
// child creates a session started from parent.
func (l *testLog) child(id, parent string) {
	l.t.Helper()
	_, err := l.conn.ExecContext(l.t.Context(),
		`INSERT INTO sessions (id, parent_session_id, title, created_at, updated_at) VALUES (?, ?, 'child', 0, 0)`, id, parent)
	require.NoError(l.t, err)
}

// add writes a message with the given parts at the log's clock and moves
```

and replace

```go
// report opens the log read-only, as the command does, and runs the
```

with

```go
// summary writes a compaction summary: an assistant message marked as
// one.
func (l *testLog) summary(sessionID string, parts ...message.ContentPart) {
	l.t.Helper()
	l.add(sessionID, message.Assistant, parts...)
	_, err := l.conn.ExecContext(l.t.Context(),
		`UPDATE messages SET is_summary_message = 1 WHERE id = ?`, fmt.Sprintf("msg-%d", l.n))
	require.NoError(l.t, err)
}

// report opens the log read-only, as the command does, and runs the
```

In `internal/logreport/privacy_test.go`, replace

```go
	log.add(session, message.Assistant, message.SubAgentReport{Label: marker, Output: marker},
		message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.120"})

	r := log.report(Options{})
	require.Equal(t, 7, r.Window.Messages, "the fixture is read")
```

with

```go
	log.add(session, message.Assistant, message.SubAgentReport{Label: marker, Output: marker},
		message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.120"})
	// A hidden row with a tag the report does not know, a subagent
	// report, a compaction summary and a child session.
	log.add(session, message.User, message.TextContent{Text: "<" + marker + ">", Hidden: true})
	log.add(session, message.User, message.SubAgentReport{Label: marker, Output: marker, Model: marker})
	log.summary(session, message.TextContent{Text: marker},
		message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.120"},
		message.CompactionStats{TokensBefore: 10, Trigger: "guard"})
	child := "team-" + marker
	log.child(child, session)
	log.add(child, message.User, message.TextContent{Text: "brief " + marker})
	log.add(child, message.Assistant, message.TextContent{Text: marker},
		message.Finish{Reason: message.FinishReasonEndTurn, Build: "0.100.0-fork.120", TurnEnd: message.TurnEndCompleted})

	r := log.report(Options{})
	require.Equal(t, 12, r.Window.Messages, "the fixture is read")
```

Create `internal/agent/injection_tags_test.go`:

```go
package agent

import (
	"testing"

	"github.com/charmbracelet/crush/internal/logreport"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/shell"
	"github.com/stretchr/testify/require"
)

// The report names each hidden row the agent writes by the tag its text
// opens with (logreport.InjectionTag). Rewording a reminder or a notice
// must keep the tag, or the report would count it as a hidden turn.
func TestInjectedRowsCarryTheReportTags(t *testing.T) {
	t.Parallel()

	todos := []session.Todo{{Content: "write the test", Status: session.TodoStatusInProgress}}
	require.Equal(t, "system_reminder", logreport.InjectionTag(todoReminder(todos, 0)))
	require.Equal(t, "system_reminder", logreport.InjectionTag(todoReminder(todos, todoReminderEscalateAfter)))
	require.Equal(t, "system_reminder", logreport.InjectionTag(repeatToolReminder("bash", 3)))
	require.Equal(t, "background_job_notice", logreport.InjectionTag(backgroundJobNotice(shell.BackgroundJobEvent{ShellID: "001", Command: "make", ExitCode: 2})))
}
```

Create `internal/agent/prompt/injection_tag_test.go`:

```go
package prompt

import (
	"testing"

	"github.com/charmbracelet/crush/internal/logreport"
	"github.com/stretchr/testify/require"
)

// The report names the hidden environment snapshot by the tag it opens
// with (logreport.InjectionTag).
func TestEnvSnapshotCarriesTheReportTag(t *testing.T) {
	t.Parallel()

	snapshot := SessionEnvSnapshot(t.Context(), t.TempDir(), fixedSnapshotTime())
	require.Equal(t, "env_snapshot", logreport.InjectionTag(snapshot))
}
```

Create `internal/automation/injection_tag_test.go`:

```go
package automation

import (
	"testing"

	"github.com/charmbracelet/crush/internal/logreport"
	"github.com/stretchr/testify/require"
)

// A goal or a loop prompt starts a run the user did not type, so the
// report counts it as a hidden turn. Opening it with an injection tag
// would turn it into an injection.
func TestAutomationPromptsCarryNoInjectionTag(t *testing.T) {
	t.Parallel()

	require.Empty(t, logreport.InjectionTag(GoalPrompt("ship it", 0, 5, "")))
	require.Empty(t, logreport.InjectionTag(GoalPrompt("ship it", 2, 5, "tests fail")))
	require.Empty(t, logreport.InjectionTag(LoopPrompt(Task{ID: "t1", Kind: LoopFixed, Task: "check CI"}, 0)))
	require.Empty(t, logreport.InjectionTag(LoopPrompt(Task{ID: "t2", Kind: LoopSelfPaced, Task: "check CI"}, 1)))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/logreport/ ./internal/agent/ ./internal/agent/prompt/ ./internal/automation/ -run 'TestInjections|TestClassifyUser|TestReportNeverPrintsPrivateText|TestInjectedRowsCarryTheReportTags|TestEnvSnapshotCarriesTheReportTag|TestAutomationPromptsCarryNoInjectionTag' -count=1`
Expected: build failures, `undefined: Injection`, `undefined: classifyUser` (logreport) and `undefined: logreport.InjectionTag` (the three producer packages).

- [ ] **Step 3: Implement**

Create `internal/logreport/injections.go`:

```go
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
```

In `internal/logreport/report.go`, replace

```go
	Reads  Reads  `json:"reads"`
}
```

with

```go
	Reads  Reads  `json:"reads"`

	Injections Injections `json:"injections"`
}
```

and replace

```go
		&readsSection{seen: map[string]bool{}},
	}
```

with

```go
		&readsSection{seen: map[string]bool{}},
		newInjectionsSection(),
	}
```

In `internal/logreport/output.go`, replace

```go
	r.Reads.writeText(tw)
```

with

```go
	r.Reads.writeText(tw)
	r.Injections.writeText(tw)
```

In `internal/cmd/stats_report.go`, replace

```go
show none.`,
```

with

```go
show none.

Injections are the hidden user rows that open with a tag the report knows,
and subagent reports. Any other hidden user row is a hidden turn: a goal,
a loop iteration, a plan handoff.`,
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ ./internal/cmd/ ./internal/agent/prompt/ ./internal/automation/ -count=1 && go test ./internal/agent/ -run TestInjectedRowsCarryTheReportTags -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/ internal/cmd/stats_report.go internal/agent/injection_tags_test.go internal/agent/prompt/injection_tag_test.go internal/automation/injection_tag_test.go
git add internal/logreport/ internal/cmd/stats_report.go internal/agent/injection_tags_test.go internal/agent/prompt/injection_tag_test.go internal/automation/injection_tag_test.go
git commit -m "feat(logreport): report the rows the harness injects, by tag"
```

---

### Task 2: Part 1 verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything in part 1 (Task 1).
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/logreport/... ./internal/cmd/... ./internal/agent/... ./internal/automation/... && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=10m ./internal/logreport/... ./internal/cmd/... ./internal/agent/... ./internal/automation/...`, then `./scripts/check_log_capitalization.sh`, then `gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD`, then `git diff origin/main...HEAD | grep -cP '\x{2014}'`, then `git diff origin/main...HEAD`
Expected: about 330 changed lines, in `internal/logreport/`, `internal/cmd/stats_report.go` and the three `injection_tag*_test.go` files only; the em dash count is `0`; no debug output.

- [ ] **Step 4: Live check on the project database**

The report opens the real database read-only. It loads the project configuration to find the data directory and prints none of it. Do not print anything from the database other than the report's own output.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" .
export CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1
time "$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 > "$scratch/report.txt"
"$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 --json > "$scratch/report.json"
cat "$scratch/report.txt"
python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); print(d["schema"], d["window"]["messages"], d["tools"]["calls"], d["tools"]["errors"]); [print(k["kind"], k["rows"], k["sessions"], k["characters"]) for k in d["injections"]["by_kind"]]' "$scratch/report.json"
rm -rf "$scratch"
```

Expected: both commands exit 0, and the text and JSON figures agree with each other. Record the output and the run time in the pull request; the controller compares the figures below with the one-off scripts. The pull request is opened with the `pr-discipline` skill only when the user asks for it.

Expected figures, in addition to item 7's (`messages 44692`, tool calls `22295`, error results `628`): Injections, in this order, `background_job_notice 5272 49 6182207`, `system_reminder 291 27 100589`, `subagent_report 80 11`, `env_snapshot 48 48 19488`, `background_subagent_notice 2 1 3664` (rows, sessions, characters; `/tmp/crush-model-intel/w0/out_26_interventions.txt`, whose volume table measured the same hidden texts; the scripts did not measure the reports' characters, so record that figure). 5,693 injected rows plus the 2 hidden turns of Task 6 are the 5,695 harness rows of Evidence 6.

---

# Part 2: Sessions and Compactions

Branch `w0-stats-report-8b`, from `origin/main` after part 1 merged. When merged, the pass knows each session's parent, and the report has the Sessions and Compactions sections.

### Task 3: The parent in the pass and the Sessions section

**Files:**
- Create: `internal/logreport/sessions.go`
- Modify: `internal/logreport/report.go` (`Report`, `newSections`, `pass`, `messagesQuery`, `Run`), `internal/logreport/output.go` (`WriteText`)
- Test: `internal/logreport/sessions_test.go` (new)

**Interfaces:**
- Consumes: `section`, `pass`, `ratio`, `percent`, `newBuild`, `(*testLog).step`, `(*testLog).summary`, `(*testLog).child` (item 7 and Task 1).
- Produces:
  - On the pass: `pass.session`, `pass.parent` (the current session's id and its parent's id, `""` without one), set when a session starts and still set during its `endSession`.
  - `func sessionKind(id, parent, spawner string) string`; constants `topLevelKind = "top_level"`, `teammateKind`, `titleKind`, `subagentKind`, `agenticFetchKind`, `unknownKind`; `agentTool`, `agentDispatchTool`, `agenticFetchTool`.
  - `Report.Sessions Sessions` (JSON `sessions`); `type Sessions struct { ByKind []SessionKind }`; `type SessionKind struct { Kind string; Sessions, Steps int; StepShare float64 }`.

- [ ] **Step 1: Write the failing test**

Create `internal/logreport/sessions_test.go`:

```go
package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestSessionsByKind(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("lead", "title")
	// These ids sort before their parent's, so the pass reads them first
	// and resolves their kinds once it is over.
	log.child("a1$$call-fetch", "lead")
	log.child("a2$$call-agent", "lead")
	log.child("a3$$call-dispatch", "lead")
	// The call that created it is not in the window.
	log.child("a4$$call-gone", "lead")
	log.child("team-reviewer", "lead")
	log.child("title-lead", "lead")
	// A child whose id has none of the known forms.
	log.child("other", "lead")

	log.step("lead", newBuild,
		message.ToolCall{ID: "call-fetch", Name: "agentic_fetch"},
		message.ToolCall{ID: "call-agent", Name: "agent"},
		message.ToolCall{ID: "call-dispatch", Name: "agent_dispatch"},
	)
	log.add("lead", message.User, message.TextContent{Text: "go on"})
	log.step("lead", newBuild)
	// A summary is not a step.
	log.summary("lead", message.TextContent{Text: "summary"})
	log.step("a1$$call-fetch", newBuild)
	log.step("a2$$call-agent", newBuild)
	log.step("a2$$call-agent", newBuild)
	log.step("a3$$call-dispatch", newBuild)
	log.add("a4$$call-gone", message.User, message.TextContent{Text: "brief"})
	log.step("team-reviewer", newBuild)
	log.step("team-reviewer", newBuild)
	log.step("team-reviewer", newBuild)
	log.step("title-lead", newBuild)
	log.add("other", message.User, message.TextContent{Text: "brief"})

	require.Equal(t, []SessionKind{
		{Kind: "subagent", Sessions: 2, Steps: 3, StepShare: 0.3},
		{Kind: "unknown", Sessions: 2},
		{Kind: "agentic_fetch", Sessions: 1, Steps: 1, StepShare: 0.1},
		{Kind: "teammate", Sessions: 1, Steps: 3, StepShare: 0.3},
		{Kind: "title", Sessions: 1, Steps: 1, StepShare: 0.1},
		{Kind: "top_level", Sessions: 1, Steps: 2, StepShare: 0.2},
	}, log.report(Options{}).Sessions.ByKind)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/logreport/ -run TestSessionsByKind -count=1`
Expected: build failure, `undefined: SessionKind`, `log.report(Options{}).Sessions undefined (type *Report has no field or method Sessions)`.

- [ ] **Step 3: Implement**

Create `internal/logreport/sessions.go`:

```go
package logreport

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/charmbracelet/crush/internal/message"
)

// Session kinds.
const (
	topLevelKind     = "top_level"
	teammateKind     = "teammate"
	titleKind        = "title"
	subagentKind     = "subagent"
	agenticFetchKind = "agentic_fetch"
	unknownKind      = "unknown"
)

// Tools that start a <message>$$<tool call> session.
const (
	agentTool         = "agent"
	agentDispatchTool = "agent_dispatch"
	agenticFetchTool  = "agentic_fetch"
)

// sessionKind names a session's kind from its id and its parent's.
// spawner is the name of the tool call a <message>$$<tool call> id names,
// or "" when that call is not in the window.
func sessionKind(id, parent, spawner string) string {
	switch {
	case strings.HasPrefix(id, "team-"):
		return teammateKind
	case strings.HasPrefix(id, "title-"):
		return titleKind
	case strings.Contains(id, "$$"):
		switch spawner {
		case agentTool, agentDispatchTool:
			return subagentKind
		case agenticFetchTool:
			return agenticFetchKind
		}
		return unknownKind
	case parent == "":
		return topLevelKind
	default:
		return unknownKind
	}
}

// Sessions is the Sessions section: the sessions with a message in the
// window, by kind.
type Sessions struct {
	// ByKind is sorted by sessions, most first, ties by kind.
	ByKind []SessionKind `json:"by_kind"`
}

// SessionKind is one kind of session and its share of the window's steps,
// summaries aside.
type SessionKind struct {
	Kind      string  `json:"kind"`
	Sessions  int     `json:"sessions"`
	Steps     int     `json:"steps"`
	StepShare float64 `json:"step_share"`
}

// sessionRead is a session the pass read, kept until its kind can be
// resolved: a child can be read before the call that created it.
type sessionRead struct {
	id, parent string
	steps      int
}

type sessionsSection struct {
	read []sessionRead
	// spawners maps the ids of the calls that start sessions to their
	// tool names.
	spawners map[string]string
	steps    int
}

func (s *sessionsSection) message(_ *pass, m *message.Message) {
	if m.Role != message.Assistant || m.IsSummaryMessage {
		return
	}
	s.steps++
	for _, tc := range m.ToolCalls() {
		if tc.Name == agentTool || tc.Name == agentDispatchTool || tc.Name == agenticFetchTool {
			s.spawners[tc.ID] = tc.Name
		}
	}
}

func (s *sessionsSection) endSession(p *pass) {
	s.read = append(s.read, sessionRead{id: p.session, parent: p.parent, steps: s.steps})
	s.steps = 0
}

func (s *sessionsSection) finish(_ *pass, r *Report) {
	kinds := map[string]*SessionKind{}
	total := 0
	for _, read := range s.read {
		spawner := ""
		if _, callID, ok := strings.Cut(read.id, "$$"); ok {
			spawner = s.spawners[callID]
		}
		kind := sessionKind(read.id, read.parent, spawner)
		k := kinds[kind]
		if k == nil {
			k = &SessionKind{Kind: kind}
			kinds[kind] = k
		}
		k.Sessions++
		k.Steps += read.steps
		total += read.steps
	}
	r.Sessions.ByKind = []SessionKind{}
	for _, k := range kinds {
		k.StepShare = ratio(k.Steps, total)
		r.Sessions.ByKind = append(r.Sessions.ByKind, *k)
	}
	slices.SortFunc(r.Sessions.ByKind, func(a, b SessionKind) int {
		return cmp.Or(cmp.Compare(b.Sessions, a.Sessions), cmp.Compare(a.Kind, b.Kind))
	})
}

func (s Sessions) writeText(out io.Writer) {
	fmt.Fprintln(out, "Sessions")
	fmt.Fprintln(out, "  kind\tsessions\tsteps\tshare of steps")
	for _, k := range s.ByKind {
		fmt.Fprintf(out, "  %s\t%d\t%d\t%s\n", k.Kind, k.Sessions, k.Steps, percent(k.StepShare))
	}
	fmt.Fprintln(out)
}
```

In `internal/logreport/report.go`, replace

```go
	Injections Injections `json:"injections"`
}
```

with

```go
	Sessions   Sessions   `json:"sessions"`
	Injections Injections `json:"injections"`
}
```

replace

```go
		newInjectionsSection(),
```

with

```go
		&sessionsSection{spawners: map[string]string{}},
		newInjectionsSection(),
```

replace

```go
	// stamping holds the builds whose tool results carry a stamp:
	// exit_code or interrupted on a shell result, error_kind on any.
	stamping map[string]bool
}
```

with

```go
	// stamping holds the builds whose tool results carry a stamp:
	// exit_code or interrupted on a shell result, error_kind on any.
	stamping map[string]bool
	// session and parent are the current session's id and its parent's,
	// empty for a session without one. Neither is ever printed.
	session, parent string
}
```

replace

```go
const messagesQuery = `SELECT session_id, role, parts, COALESCE(model, ''),
	COALESCE(provider, ''), created_at, is_summary_message
FROM messages
WHERE created_at >= ? AND created_at < ?
ORDER BY session_id, created_at, rowid`
```

with

```go
const messagesQuery = `SELECT m.session_id, COALESCE(s.parent_session_id, ''),
	m.role, m.parts, COALESCE(m.model, ''), COALESCE(m.provider, ''),
	m.created_at, m.is_summary_message
FROM messages m LEFT JOIN sessions s ON s.id = m.session_id
WHERE m.created_at >= ? AND m.created_at < ?
ORDER BY m.session_id, m.created_at, m.rowid`
```

replace

```go
		var (
			m     message.Message
			role  string
			parts []byte
		)
		if err := rows.Scan(&m.SessionID, &role, &parts, &m.Model, &m.Provider, &m.CreatedAt, &m.IsSummaryMessage); err != nil {
```

with

```go
		var (
			m      message.Message
			parent string
			role   string
			parts  []byte
		)
		if err := rows.Scan(&m.SessionID, &parent, &role, &parts, &m.Model, &m.Provider, &m.CreatedAt, &m.IsSummaryMessage); err != nil {
```

and replace

```go
			current = m.SessionID
			p.sessions++
```

with

```go
			current = m.SessionID
			p.session, p.parent = m.SessionID, parent
			p.sessions++
```

In `internal/logreport/output.go`, replace

```go
	r.Injections.writeText(tw)
```

with

```go
	r.Sessions.writeText(tw)
	r.Injections.writeText(tw)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ ./internal/cmd/ -count=1`
Expected: PASS, the item 7 tests included: the join changes no other figure.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/
git add internal/logreport/
git commit -m "feat(logreport): report sessions by kind and their share of steps"
```

---

### Task 4: The Compactions section

**Files:**
- Create: `internal/logreport/compactions.go`
- Modify: `internal/logreport/report.go` (`Report`, `newSections`), `internal/logreport/output.go` (`WriteText`)
- Test: `internal/logreport/compactions_test.go` (new)

**Interfaces:**
- Consumes: `section`, `percentile` (item 7, Task 8), `(*testLog).summary` (Task 1); `(*message.Message).CompactionStatsPart`, `(*message.Message).FinishReason`.
- Produces: `Report.Compactions Compactions` (JSON `compactions`); `type Compactions struct { Count, MedianTokensBefore int; ByTrigger []Compaction; Failed int }`; `type Compaction struct { Trigger string; Count, MedianTokensBefore int }`; `type compactionsSection struct { before map[string][]int; failed int }`.

- [ ] **Step 1: Write the failing test**

Create `internal/logreport/compactions_test.go`:

```go
package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestCompactionsByTrigger(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("s1", "title")
	log.session("s2", "title")
	compacted := func(session, trigger string, before int64) {
		log.summary(session, message.TextContent{Text: "summary"},
			message.Finish{Reason: message.FinishReasonEndTurn},
			message.CompactionStats{TokensBefore: before, Trigger: trigger})
	}
	compacted("s1", "guard", 140_000)
	compacted("s1", "guard", 100_000)
	compacted("s1", "threshold", 120_000)
	compacted("s2", "guard", 160_000)
	// Written before triggers were recorded.
	compacted("s2", "", 90_000)
	// A failed compaction keeps its summary row, with an error and no
	// statistics; one still being written has neither.
	log.summary("s2", message.Finish{Reason: message.FinishReasonError, Message: "Summarization Error"})
	log.summary("s2", message.TextContent{Text: "partial"})
	// Statistics on a message that is not a summary are not a compaction.
	log.add("s2", message.Assistant, message.CompactionStats{TokensBefore: 1})

	require.Equal(t, Compactions{
		Count:              5,
		MedianTokensBefore: 120_000,
		ByTrigger: []Compaction{
			{Trigger: "guard", Count: 3, MedianTokensBefore: 140_000},
			{Trigger: "threshold", Count: 1, MedianTokensBefore: 120_000},
			{Trigger: "unstamped", Count: 1, MedianTokensBefore: 90_000},
		},
		Failed: 1,
	}, log.report(Options{}).Compactions)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/logreport/ -run TestCompactionsByTrigger -count=1`
Expected: build failure, `undefined: Compactions`, `undefined: Compaction`.

- [ ] **Step 3: Implement**

Create `internal/logreport/compactions.go`:

```go
package logreport

import (
	"cmp"
	"fmt"
	"io"
	"slices"

	"github.com/charmbracelet/crush/internal/message"
)

// Compactions is the Compactions section: the summaries that carry
// compaction statistics, in every session.
type Compactions struct {
	Count              int `json:"count"`
	MedianTokensBefore int `json:"median_tokens_before"`
	// ByTrigger is sorted by count, most first, ties by trigger. A summary
	// written before triggers were recorded reads "unstamped".
	ByTrigger []Compaction `json:"by_trigger"`
	// Failed counts the summaries that ended with an error and carry no
	// statistics. A failed compaction records no trigger.
	Failed int `json:"failed"`
}

// Compaction is the compactions one trigger started.
type Compaction struct {
	Trigger            string `json:"trigger"`
	Count              int    `json:"count"`
	MedianTokensBefore int    `json:"median_tokens_before"`
}

type compactionsSection struct {
	// before holds the tokens before each compaction, by trigger.
	before map[string][]int
	failed int
}

func (s *compactionsSection) message(_ *pass, m *message.Message) {
	if !m.IsSummaryMessage {
		return
	}
	stats, ok := m.CompactionStatsPart()
	if !ok {
		if m.FinishReason() == message.FinishReasonError {
			s.failed++
		}
		return
	}
	trigger := cmp.Or(stats.Trigger, "unstamped")
	s.before[trigger] = append(s.before[trigger], int(stats.TokensBefore))
}

func (s *compactionsSection) endSession(*pass) {}

func (s *compactionsSection) finish(_ *pass, r *Report) {
	c := Compactions{ByTrigger: []Compaction{}, Failed: s.failed}
	var all []int
	for trigger, before := range s.before {
		all = append(all, before...)
		c.ByTrigger = append(c.ByTrigger, Compaction{
			Trigger:            trigger,
			Count:              len(before),
			MedianTokensBefore: percentile(before, 50),
		})
	}
	c.Count = len(all)
	c.MedianTokensBefore = percentile(all, 50)
	slices.SortFunc(c.ByTrigger, func(a, b Compaction) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Trigger, b.Trigger))
	})
	r.Compactions = c
}

func (c Compactions) writeText(out io.Writer) {
	fmt.Fprintln(out, "Compactions (all sessions)")
	fmt.Fprintf(out, "  compactions\t%d\tmedian tokens before %d\n", c.Count, c.MedianTokensBefore)
	for _, t := range c.ByTrigger {
		fmt.Fprintf(out, "  trigger %s\t%d\tmedian tokens before %d\n", t.Trigger, t.Count, t.MedianTokensBefore)
	}
	fmt.Fprintf(out, "  failed\t%d\n", c.Failed)
	fmt.Fprintln(out)
}
```

In `internal/logreport/report.go`, replace

```go
	Sessions   Sessions   `json:"sessions"`
	Injections Injections `json:"injections"`
}
```

with

```go
	Sessions    Sessions    `json:"sessions"`
	Injections  Injections  `json:"injections"`
	Compactions Compactions `json:"compactions"`
}
```

and replace

```go
		newInjectionsSection(),
	}
```

with

```go
		newInjectionsSection(),
		&compactionsSection{before: map[string][]int{}},
	}
```

In `internal/logreport/output.go`, replace

```go
	r.Injections.writeText(tw)
```

with

```go
	r.Injections.writeText(tw)
	r.Compactions.writeText(tw)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/
git add internal/logreport/
git commit -m "feat(logreport): report compactions by trigger"
```

---

### Task 5: Part 2 verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything in part 2 (Tasks 3 and 4).
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/logreport/... ./internal/cmd/... && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=10m ./internal/logreport/... ./internal/cmd/...`, then `./scripts/check_log_capitalization.sh`, then `gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD`, then `git diff origin/main...HEAD | grep -cP '\x{2014}'`, then `git diff origin/main...HEAD`
Expected: about 350 changed lines, in `internal/logreport/` only; the em dash count is `0`; no debug output.

- [ ] **Step 4: Live check on the project database**

The report opens the real database read-only. It loads the project configuration to find the data directory and prints none of it. Do not print anything from the database other than the report's own output.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" .
export CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1
time "$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 > "$scratch/report.txt"
"$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 --json > "$scratch/report.json"
cat "$scratch/report.txt"
python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); print(d["window"]["messages"], d["tools"]["calls"]); [print(k["kind"], k["sessions"], k["steps"], round(100 * k["step_share"], 2)) for k in d["sessions"]["by_kind"]]; print(d["compactions"])' "$scratch/report.json"
rm -rf "$scratch"
```

Expected: both commands exit 0, and the text and JSON figures agree with each other. Record the output and the run time in the pull request; the controller compares the figures below with the one-off scripts. The pull request is opened with the `pr-discipline` skill only when the user asks for it.

Expected figures, in addition to item 7's and Task 2's: Sessions, in this order, `agentic_fetch 143 986 6.12%`, `subagent 48 1816 11.28%`, `top_level 48 9151 56.83%`, `teammate 35 4148 25.76%` (sessions, steps, share of steps; the sessions match `/tmp/crush-model-intel/w0/out_32_session_kinds.txt`, where `subagent` is its 44 `agent_dispatch` and 4 `agent` sessions; the steps add up to 16,101, the 16,112 assistant messages less the 11 summaries). Compactions `compactions 11`, one line `trigger unstamped 11`, `failed 0` (`out_20_totals.txt`: 11 summaries, 11 with statistics; the window predates the trigger stamp). The scripts did not measure the tokens before a compaction, so record the medians.

---

# Part 3: Turns

Branch `w0-stats-report-8c`, from `origin/main` after part 2 merged. When merged, the report has the Turns section: prompts, hidden turns, continuations, steps and tool calls per prompt with and without child sessions, and runs by turn end.

### Task 6: The Turns section

**Files:**
- Create: `internal/logreport/turns.go`
- Modify: `internal/logreport/report.go` (`Report`, `newSections`), `internal/logreport/output.go` (`WriteText`), `internal/cmd/stats_report.go` (`Long`)
- Test: `internal/logreport/testlog_test.go` (one helper), `internal/logreport/turns_test.go` (new)

**Interfaces:**
- Consumes: `classifyUser`, `injectionRow`, `otherRow`, `visibleRow`, `hiddenTurnRow` (Task 1); `pass.session`, `pass.parent`, `sessionKind`, `topLevelKind` (Task 3); `percentile`, `formatCounts`, `newBuild`, `oldBuild`, `(*testLog).step`, `(*testLog).summary`, `(*testLog).child`.
- Produces:
  - `Report.Turns Turns` (JSON `turns`); `type Turns struct { Prompts, HiddenTurns, Continuations, ChildRows int; Steps, ToolCalls Spread; RunEnds map[string]int }`; `type Spread struct { Median, P90, Max int }`; `func spread(values []int) Spread`.
  - `type segment struct { steps, calls int; build string; stamped bool }`; `type turnsSection struct` with field `segments []segment` (By model and By build read it in Task 9); `func newTurnsSection() *turnsSection`; `func continues(end message.TurnEnd) bool`.
  - Test helper: `(*testLog).lastStep(sessionID, build string, end message.TurnEnd, calls ...message.ToolCall)`.

- [ ] **Step 1: Write the failing tests**

In `internal/logreport/testlog_test.go`, replace

```go
// result writes a tool message carrying one result.
```

with

```go
// lastStep writes the last step of a run, stamped with how the run
// ended.
func (l *testLog) lastStep(sessionID, build string, end message.TurnEnd, calls ...message.ToolCall) {
	l.t.Helper()
	parts := []message.ContentPart{message.TextContent{Text: "done"}}
	for _, c := range calls {
		parts = append(parts, c)
	}
	parts = append(parts, message.Finish{Reason: message.FinishReasonEndTurn, Build: build, TurnEnd: end})
	l.add(sessionID, message.Assistant, parts...)
}

// result writes a tool message carrying one result.
```

Create `internal/logreport/turns_test.go`:

```go
package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestTurnsPromptsHiddenTurnsAndContinuations(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("lead", "title")
	log.child("team-x", "lead")
	prompt := func(text string) {
		log.add("lead", message.User, message.TextContent{Text: text})
	}
	hidden := func(session, text string) {
		log.add(session, message.User, message.TextContent{Text: text, Hidden: true})
	}
	call := message.ToolCall{ID: "c", Name: "glob"}

	hidden("lead", "<env_snapshot>")
	prompt("one")
	log.step("lead", newBuild, call, call)
	// Injections and hidden turns stay inside the prompt's segment.
	hidden("lead", "<system_reminder>x")
	log.step("lead", newBuild, call)
	hidden("lead", "Implement the plan.")
	log.lastStep("lead", newBuild, message.TurnEndContextGuard, call)
	// The continuation is known by the stamp on the last step before it,
	// looking past the summary and the injections.
	log.summary("lead", message.TextContent{Text: "summary"})
	hidden("lead", "<system_reminder>x")
	prompt("The previous session was interrupted because it got too long")
	log.lastStep("lead", newBuild, message.TurnEndCompleted)

	prompt("two")
	// A blocked compaction still queues the prompt again.
	log.lastStep("lead", newBuild, message.TurnEndCompactionBlocked, call, call, call)
	prompt("The previous session was interrupted because it got too long")
	log.lastStep("lead", newBuild, message.TurnEndCompleted)
	// Two prompts in a row: the first caused no step.
	prompt("three")
	prompt("four")
	log.lastStep("lead", newBuild, message.TurnEndCompactionFailed)
	// A failed compaction ends the turn, so the next row is a prompt.
	prompt("five")
	log.lastStep("lead", newBuild, message.TurnEndCompleted)

	// A child session's rows are not prompts, and its runs are not
	// top-level runs.
	log.add("team-x", message.User, message.TextContent{Text: "brief"})
	hidden("team-x", "Implement the plan.")
	log.lastStep("team-x", newBuild, message.TurnEndLoopGuard)

	turns := log.report(Options{}).Turns
	require.Equal(t, 5, turns.Prompts)
	require.Equal(t, 1, turns.HiddenTurns)
	require.Equal(t, 2, turns.Continuations)
	require.Equal(t, 1, turns.ChildRows)
	// Steps per prompt: 4, 2, 0, 1, 1. Tool calls: 4, 3, 0, 0, 0.
	require.Equal(t, Spread{Median: 1, P90: 4, Max: 4}, turns.Steps)
	require.Equal(t, Spread{Median: 0, P90: 4, Max: 4}, turns.ToolCalls)
	require.Equal(t, map[string]int{
		"context_guard":      1,
		"compaction_blocked": 1,
		"compaction_failed":  1,
		"completed":          3,
	}, turns.RunEnds)
}

func TestTurnsWithoutStamps(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("old", "title")
	log.session("new", "title")
	prompt := func(session, text string) {
		log.add(session, message.User, message.TextContent{Text: text})
	}
	// A session that a goal started: its steps come before any prompt and
	// belong to none.
	log.add("old", message.User, message.TextContent{Text: "You are pursuing a standing goal", Hidden: true})
	log.step("old", oldBuild)
	log.step("old", oldBuild)
	prompt("old", "one")
	log.step("old", oldBuild)
	log.step("old", oldBuild)
	// Written before the stamps, a continuation counts as a prompt.
	prompt("old", "The previous session was interrupted because it got too long")
	log.step("old", "")
	prompt("old", "no step follows")
	// A build that stamps turn ends left one run without a stamp, as a
	// crash does: that prompt is not unstamped.
	prompt("new", "one")
	log.lastStep("new", newBuild, message.TurnEndCompleted)
	prompt("new", "two")
	log.step("new", newBuild)

	turns := log.report(Options{}).Turns
	require.Equal(t, 5, turns.Prompts)
	require.Equal(t, 1, turns.HiddenTurns)
	require.Zero(t, turns.Continuations)
	// Steps per prompt: 2, 1, 0, 1, 1.
	require.Equal(t, Spread{Median: 1, P90: 2, Max: 2}, turns.Steps)
	require.Equal(t, map[string]int{"completed": 1, "unstamped": 2}, turns.RunEnds)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/logreport/ -run TestTurns -count=1`
Expected: build failure, `log.report(Options{}).Turns undefined (type *Report has no field or method Turns)`, `undefined: Spread`.

- [ ] **Step 3: Implement**

Create `internal/logreport/turns.go`:

```go
package logreport

import (
	"fmt"
	"io"

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
	steps, calls int
	// build is the build stamp of the segment's first step.
	build string
	// stamped is set when a step of the segment carries a turn end.
	stamped bool
}

type turnsSection struct {
	turns    Turns
	segments []segment
	// turnStamping holds the builds that stamp turn ends.
	turnStamping map[string]bool
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
	}
}

func (s *turnsSection) message(p *pass, m *message.Message) {
	if !s.started {
		s.started, s.open = true, -1
		s.top = sessionKind(p.session, p.parent, "") == topLevelKind
	}
	switch {
	case m.Role == message.User:
		s.user(m)
	case m.Role == message.Assistant && !m.IsSummaryMessage:
		s.step(m)
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

func (s *turnsSection) user(m *message.Message) {
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
	}
}

func (s *turnsSection) step(m *message.Message) {
	var end message.TurnEnd
	build := ""
	if f := m.FinishPart(); f != nil {
		end, build = f.TurnEnd, f.Build
	}
	s.lastEnd = end
	if end != "" && build != "" {
		s.turnStamping[build] = true
	}
	if !s.top {
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
	seg.calls += len(m.ToolCalls())
	seg.stamped = seg.stamped || end != ""
}

func (s *turnsSection) endSession(*pass) {
	s.started, s.open, s.lastEnd = false, -1, ""
}

func (s *turnsSection) finish(_ *pass, r *Report) {
	var steps, calls []int
	for _, seg := range s.segments {
		steps = append(steps, seg.steps)
		calls = append(calls, seg.calls)
		if seg.steps > 0 && !seg.stamped && !s.turnStamping[seg.build] {
			s.turns.RunEnds["unstamped"]++
		}
	}
	s.turns.Steps, s.turns.ToolCalls = spread(steps), spread(calls)
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
	} {
		fmt.Fprintf(out, "  %s\t%d\t%d\t%d\n", row.name, row.s.Median, row.s.P90, row.s.Max)
	}
	fmt.Fprintf(out, "  runs by turn end\t%s\n", formatCounts(t.RunEnds))
	fmt.Fprintln(out)
}
```

In `internal/logreport/report.go`, replace

```go
	Sessions    Sessions    `json:"sessions"`
```

with

```go
	Turns       Turns       `json:"turns"`
	Sessions    Sessions    `json:"sessions"`
```

and replace

```go
		&sessionsSection{spawners: map[string]string{}},
```

with

```go
		newTurnsSection(),
		&sessionsSection{spawners: map[string]string{}},
```

In `internal/logreport/output.go`, replace

```go
	r.Sessions.writeText(tw)
```

with

```go
	r.Turns.writeText(tw)
	r.Sessions.writeText(tw)
```

In `internal/cmd/stats_report.go`, replace

```go
a loop iteration, a plan handoff.`,
```

with

```go
a loop iteration, a plan handoff.

A prompt is a visible user row in a top-level session; in a child session
such a row is its brief, or a message sent to it. A row that follows a
step stamped context_guard, context_overflow or compaction_blocked
continues that prompt; when a cancel dropped the queued prompt, the next
prompt is counted as a continuation. Without the stamp a continuation
counts as a prompt, and runs by turn end read "unstamped", counted per
prompt.`,
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ ./internal/cmd/ -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/ internal/cmd/stats_report.go
git add internal/logreport/ internal/cmd/stats_report.go
git commit -m "feat(logreport): report prompts, continuations and steps per prompt"
```

---

### Task 7: Steps per prompt with child sessions

**Files:**
- Modify: `internal/logreport/turns.go` (whole file below)
- Test: `internal/logreport/turns_tree_test.go` (new)

**Interfaces:**
- Consumes: everything Task 6 produces; `pass.session`, `pass.parent` (Task 3); `(*testLog).child`.
- Produces: `Turns.TreeSteps`, `Turns.TreeToolCalls` (`Spread`, JSON `steps_per_prompt_with_children`, `tool_calls_per_prompt_with_children`); `segment.treeSteps`, `segment.treeCalls`; `type opening struct { at int64; segment int }`; `type childStep struct { at int64; calls int }`; `turnsSection.parents`, `openings`, `children`; `func (s *turnsSection) root(id string) string`. Task 6's names and signatures are unchanged except that `user` and `step` now take `p *pass` first.

- [ ] **Step 1: Write the failing test**

Create `internal/logreport/turns_tree_test.go`:

```go
package logreport

import (
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestTurnsWithChildSessions(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("lead", "title")
	// "a$$c1" sorts before "lead", so the pass reads it first.
	log.child("a$$c1", "lead")
	log.child("team-x", "lead")
	// A subagent of the teammate counts toward the lead's prompt.
	log.child("b$$c2", "team-x")
	// The parent of this one has no message in the window.
	log.child("c$$c3", "gone")
	call := message.ToolCall{ID: "c", Name: "glob"}

	// Written before the lead's first prompt: in no segment.
	log.step("a$$c1", newBuild, call)
	log.add("lead", message.User, message.TextContent{Text: "one"})
	log.step("lead", newBuild, message.ToolCall{ID: "c1", Name: "agent"})
	log.step("a$$c1", newBuild, call, call)
	log.step("a$$c1", newBuild)
	log.step("team-x", newBuild, call)
	log.add("lead", message.User, message.TextContent{Text: "two"})
	log.step("b$$c2", newBuild, call, call, call)
	log.step("team-x", newBuild)
	log.step("lead", newBuild)
	log.step("c$$c3", newBuild, call)

	turns := log.report(Options{}).Turns
	require.Equal(t, Spread{Median: 1, P90: 1, Max: 1}, turns.Steps)
	require.Equal(t, Spread{Median: 0, P90: 1, Max: 1}, turns.ToolCalls)
	// With child sessions, steps per prompt: 1 + 3 = 4, then 1 + 2 = 3.
	// Tool calls: 1 + 3 = 4, then 0 + 3 = 3.
	require.Equal(t, Spread{Median: 3, P90: 4, Max: 4}, turns.TreeSteps)
	require.Equal(t, Spread{Median: 3, P90: 4, Max: 4}, turns.TreeToolCalls)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/logreport/ -run TestTurnsWithChildSessions -count=1`
Expected: build failure, `turns.TreeSteps undefined (type Turns has no field or method TreeSteps)`.

- [ ] **Step 3: Implement**

Replace the contents of `internal/logreport/turns.go` with:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ -count=1`
Expected: PASS, Task 6's tests included.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/
git add internal/logreport/
git commit -m "feat(logreport): add child sessions' steps to the prompt they served"
```

---

### Task 8: Part 3 verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything in part 3 (Tasks 6 and 7).
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/logreport/... ./internal/cmd/... && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=10m ./internal/logreport/... ./internal/cmd/...`, then `./scripts/check_log_capitalization.sh`, then `gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD`, then `git diff origin/main...HEAD | grep -cP '\x{2014}'`, then `git diff origin/main...HEAD`
Expected: about 425 changed lines, in `internal/logreport/` and `internal/cmd/stats_report.go` only; the em dash count is `0`; no debug output.

- [ ] **Step 4: Live check on the project database**

The report opens the real database read-only. It loads the project configuration to find the data directory and prints none of it. Do not print anything from the database other than the report's own output.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" .
export CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1
time "$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 > "$scratch/report.txt"
"$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 --json > "$scratch/report.json"
cat "$scratch/report.txt"
python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); t = d["turns"]; print(d["window"]["messages"], t["prompts"], t["hidden_turns"], t["continuations"], t["child_session_rows"], t["prompts"] + t["child_session_rows"]); [print(k, t[k]) for k in ("steps_per_prompt", "tool_calls_per_prompt", "steps_per_prompt_with_children", "tool_calls_per_prompt_with_children", "runs_by_turn_end")]' "$scratch/report.json"
rm -rf "$scratch"
```

Expected: both commands exit 0, and the text and JSON figures agree with each other. Record the output and the run time in the pull request; the controller compares the figures below with the one-off scripts. The pull request is opened with the `pr-discipline` skill only when the user asks for it.

Expected figures, in addition to item 7's and Tasks 2 and 5: Turns `prompts 320`, `hidden turns 2`, `continuations 0`, `visible rows in child sessions 270`; per prompt, `steps 15 67 278`, `tool calls 21 95 466` (median, p90, max); `runs by turn end unstamped 318` (the 320 prompts less the 2 that caused no step; the window predates the stamp).

The spec's live-check figure, 590 prompts, is the one-off scripts' count of every user row with a visible text part in every session (`/tmp/crush-model-intel/w0/out_27_turn_shape.txt`: "590 prompts in 274 sessions"). Section 2 of the spec counts only top-level sessions as prompts, so the report prints 320 prompts and 270 child-session rows: 320 + 270 = 590 is the check. The figures above were computed twice from the privacy-reduced snapshot, by an independent script and by this code on a database rebuilt from it. The snapshot cannot link a teammate to its lead, so for the figures with child sessions it gives only a floor: each of `steps with child sessions` and `tool calls with child sessions` must be at least `15 80 807` and `21 105 1180`. Report any other difference rather than patching it.

---

# Part 4: By model and By build

Branch `w0-stats-report-8d`, from `origin/main` after part 3 merged.

### Task 9: The By model and By build sections

**Files:**
- Create: `internal/logreport/groups.go`
- Modify: `internal/logreport/report.go` (`Report`, `newSections`, `call`, `observe`), `internal/logreport/turns.go` (`segment`, `step`), `internal/logreport/output.go` (`WriteText`)
- Test: `internal/logreport/testlog_test.go` (`testLog`, `newTestLog`, `raw`), `internal/logreport/groups_test.go` (new)

**Interfaces:**
- Consumes: `pass.calls`, `call` (item 7, Task 5); `turnsSection.segments`, `newTurnsSection` (Tasks 6 and 7); `ratio`, `percent`, `percentile`, `bashTool`, `editTool`, `multiEditTool`; `(*testLog).step`, `(*testLog).result`, `(*testLog).lastStep`, `(*testLog).child`.
- Produces: `Report.ByModel []Group` (JSON `by_model`), `Report.ByBuild []Group` (JSON `by_build`); `type Group struct { Model, Provider, Build string; Prompts, MedianSteps, ToolCalls int; ToolErrorRate float64; Edits int; EditFailureRate float64; ShellResults, ShellCommands int; ShellFailureRate float64 }`; `func writeGroups(out io.Writer, title string, groups []Group, byBuild bool)`; `call.model`, `call.provider`; `segment.model`, `segment.provider`; test fields `testLog.model`, `testLog.provider` (the next message's, default `model-a` and `provider-a`).

- [ ] **Step 1: Write the failing test**

In `internal/logreport/testlog_test.go`, replace

```go
	// at is the time of the next message; each message advances it.
	at time.Time
}
```

with

```go
	// at is the time of the next message; each message advances it.
	at time.Time
	// model and provider are those of the next message.
	model, provider string
}
```

replace

```go
		at:   time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local),
	}
```

with

```go
		at:   time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local),

		model:    "model-a",
		provider: "provider-a",
	}
```

and replace

```go
		VALUES (?, ?, ?, ?, 'model-a', 'provider-a', ?, ?)`,
		fmt.Sprintf("msg-%d", l.n), sessionID, string(role), parts, l.at.Unix(), l.at.Unix())
```

with

```go
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("msg-%d", l.n), sessionID, string(role), parts, l.model, l.provider, l.at.Unix(), l.at.Unix())
```

Create `internal/logreport/groups_test.go`:

```go
package logreport

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/stretchr/testify/require"
)

func TestByModelAndByBuild(t *testing.T) {
	t.Parallel()

	log := newTestLog(t)
	log.session("lead", "title")
	log.child("team-x", "lead")
	prompt := func(text string) {
		log.add("lead", message.User, message.TextContent{Text: text})
	}
	result := func(session, id, name string, failed bool, metadata string) {
		log.result(session, message.ToolResult{ToolCallID: id, Name: name, IsError: failed, Metadata: metadata})
	}

	prompt("one")
	log.step("lead", newBuild,
		message.ToolCall{ID: "e1", Name: "edit"},
		message.ToolCall{ID: "b1", Name: "bash"},
		message.ToolCall{ID: "g1", Name: "glob"},
	)
	result("lead", "e1", "edit", true, `{"error_kind":"text_not_found"}`)
	result("lead", "b1", "bash", false, `{"exit_code":1}`)
	result("lead", "g1", "glob", false, "")
	log.lastStep("lead", newBuild, message.TurnEndCompleted)
	// A child session's calls count for its model, not its prompts.
	log.step("team-x", newBuild, message.ToolCall{ID: "b2", Name: "bash"})
	result("team-x", "b2", "bash", false, `{"exit_code":0}`)

	log.model, log.provider = "model-b", "provider-b"
	prompt("two")
	log.step("lead", oldBuild,
		message.ToolCall{ID: "b3", Name: "bash"},
		message.ToolCall{ID: "e2", Name: "edit"},
		message.ToolCall{ID: "m1", Name: "multiedit"},
		message.ToolCall{ID: "u1", Name: "made_up_tool"},
	)
	result("lead", "b3", "bash", false, "")
	result("lead", "e2", "edit", false, "")
	result("lead", "m1", "multiedit", true, "")
	result("lead", "u1", "made_up_tool", true, `{"error_kind":"unknown_tool"}`)
	// A result whose call is not in the window is attributed to no one.
	result("lead", "gone", "edit", true, "")
	prompt("three")
	log.step("lead", "")
	log.step("lead", "")
	log.step("lead", oldBuild)

	r := log.report(Options{})
	require.Equal(t, []Group{
		{
			Model: "model-b", Provider: "provider-b", Prompts: 2, MedianSteps: 1,
			ToolCalls: 4, ToolErrorRate: 0.5, Edits: 2, EditFailureRate: 0.5,
			ShellResults: 1,
		},
		{
			Model: "model-a", Provider: "provider-a", Prompts: 1, MedianSteps: 2,
			ToolCalls: 4, ToolErrorRate: 0.25, Edits: 1, EditFailureRate: 1,
			ShellResults: 2, ShellCommands: 2, ShellFailureRate: 0.5,
		},
	}, r.ByModel)
	require.Equal(t, []Group{
		{
			Build: newBuild, Prompts: 1, MedianSteps: 2,
			ToolCalls: 4, ToolErrorRate: 0.25, Edits: 1, EditFailureRate: 1,
			ShellResults: 2, ShellCommands: 2, ShellFailureRate: 0.5,
		},
		{
			Build: oldBuild, Prompts: 1, MedianSteps: 1,
			ToolCalls: 4, ToolErrorRate: 0.5, Edits: 2, EditFailureRate: 0.5,
			ShellResults: 1,
		},
		// The third prompt's first step carries no build.
		{Build: "unstamped", Prompts: 1, MedianSteps: 3},
	}, r.ByBuild)

	// The columns name models, providers and builds, never a tool: a
	// model can invent any tool name.
	var text bytes.Buffer
	writeGroups(&text, "By model", r.ByModel, false)
	writeGroups(&text, "By build", r.ByBuild, true)
	require.NotContains(t, text.String(), "made_up_tool")
	require.Contains(t, text.String(), "unstamped", "a shell rate without stamps")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/logreport/ -run TestByModelAndByBuild -count=1`
Expected: build failure, `undefined: Group`, `undefined: writeGroups`, `r.ByModel undefined`.

- [ ] **Step 3: Implement**

Create `internal/logreport/groups.go`:

```go
package logreport

import (
	"cmp"
	"fmt"
	"io"
	"slices"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// Group is one model and provider, or one build: its prompts and how its
// tool calls fared. It names no tool, since a model can invent any name.
type Group struct {
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	Build    string `json:"build,omitempty"`
	// Prompts are those whose first step it made, and MedianSteps their
	// median steps, child sessions aside.
	Prompts         int     `json:"prompts"`
	MedianSteps     int     `json:"median_steps_per_prompt"`
	ToolCalls       int     `json:"tool_calls"`
	ToolErrorRate   float64 `json:"tool_error_rate"`
	Edits           int     `json:"edits"`
	EditFailureRate float64 `json:"edit_failure_rate"`
	// ShellCommands are the shell results that carry an exit stamp, out of
	// ShellResults; the failure rate is over the commands.
	ShellResults     int     `json:"shell_results"`
	ShellCommands    int     `json:"shell_commands"`
	ShellFailureRate float64 `json:"shell_failure_rate"`
}

// groupKey is a model and provider, or a build.
type groupKey struct {
	model, provider, build string
}

type groupCounts struct {
	steps                             []int
	calls, errors, edits, editsFailed int
	shellResults, shellCommands       int
	shellFailed                       int
}

type groupsSection struct {
	turns  *turnsSection
	groups map[groupKey]*groupCounts
}

// both returns the counts of a model and provider and those of a build.
func (s *groupsSection) both(model, provider, build string) [2]*groupCounts {
	keys := [2]groupKey{{model: model, provider: provider}, {build: cmp.Or(build, "unstamped")}}
	var out [2]*groupCounts
	for i, key := range keys {
		if s.groups[key] == nil {
			s.groups[key] = &groupCounts{}
		}
		out[i] = s.groups[key]
	}
	return out
}

func (s *groupsSection) message(p *pass, m *message.Message) {
	switch m.Role {
	case message.Assistant:
		build := ""
		if f := m.FinishPart(); f != nil {
			build = f.Build
		}
		for _, tc := range m.ToolCalls() {
			for _, g := range s.both(m.Model, m.Provider, build) {
				g.calls++
				if tc.Name == editTool || tc.Name == multiEditTool {
					g.edits++
				}
			}
		}
	case message.Tool:
		for _, tr := range m.ToolResults() {
			c, ok := p.calls[tr.ToolCallID]
			if !ok {
				continue
			}
			code := gjson.Get(tr.Metadata, "exit_code")
			interrupted := gjson.Get(tr.Metadata, "interrupted").Bool()
			for _, g := range s.both(c.model, c.provider, c.build) {
				if tr.IsError {
					g.errors++
					if tr.Name == editTool || tr.Name == multiEditTool {
						g.editsFailed++
					}
				}
				if tr.Name != bashTool {
					continue
				}
				g.shellResults++
				if interrupted || code.Exists() {
					g.shellCommands++
					if interrupted || code.Int() != 0 {
						g.shellFailed++
					}
				}
			}
		}
	}
}

func (s *groupsSection) endSession(*pass) {}

func (s *groupsSection) finish(_ *pass, r *Report) {
	for _, seg := range s.turns.segments {
		if seg.steps == 0 {
			continue
		}
		for _, g := range s.both(seg.model, seg.provider, seg.build) {
			g.steps = append(g.steps, seg.steps)
		}
	}
	r.ByModel, r.ByBuild = []Group{}, []Group{}
	for key, c := range s.groups {
		g := Group{
			Model:            key.model,
			Provider:         key.provider,
			Build:            key.build,
			Prompts:          len(c.steps),
			MedianSteps:      percentile(c.steps, 50),
			ToolCalls:        c.calls,
			ToolErrorRate:    ratio(c.errors, c.calls),
			Edits:            c.edits,
			EditFailureRate:  ratio(c.editsFailed, c.edits),
			ShellResults:     c.shellResults,
			ShellCommands:    c.shellCommands,
			ShellFailureRate: ratio(c.shellFailed, c.shellCommands),
		}
		if key.build != "" {
			r.ByBuild = append(r.ByBuild, g)
		} else {
			r.ByModel = append(r.ByModel, g)
		}
	}
	byPrompts := func(a, b Group) int {
		return cmp.Or(cmp.Compare(b.Prompts, a.Prompts), cmp.Compare(b.ToolCalls, a.ToolCalls),
			cmp.Compare(a.Model, b.Model), cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.Build, b.Build))
	}
	slices.SortFunc(r.ByModel, byPrompts)
	slices.SortFunc(r.ByBuild, byPrompts)
}

// writeGroups writes the By model or the By build section.
func writeGroups(out io.Writer, title string, groups []Group, byBuild bool) {
	fmt.Fprintln(out, title)
	name := "model\tprovider"
	if byBuild {
		name = "build"
	}
	fmt.Fprintf(out, "  %s\tprompts\tmedian steps\ttool errors\tedit failures\tshell failures\n", name)
	for _, g := range groups {
		name = g.Model + "\t" + g.Provider
		if byBuild {
			name = g.Build
		}
		shell := "-"
		switch {
		case g.ShellCommands > 0:
			shell = percent(g.ShellFailureRate)
		case g.ShellResults > 0:
			shell = "unstamped"
		}
		fmt.Fprintf(out, "  %s\t%d\t%d\t%s\t%s\t%s\n", name, g.Prompts, g.MedianSteps,
			percent(g.ToolErrorRate), percent(g.EditFailureRate), shell)
	}
	fmt.Fprintln(out)
}
```

In `internal/logreport/report.go`, replace

```go
	Compactions Compactions `json:"compactions"`
}
```

with

```go
	Compactions Compactions `json:"compactions"`
	ByModel     []Group     `json:"by_model"`
	ByBuild     []Group     `json:"by_build"`
}
```

replace

```go
// newSections returns the report's sections in the order they print.
func newSections() []section {
	return []section{
```

with

```go
// newSections returns the report's sections in the order they print.
func newSections() []section {
	turns := newTurnsSection()
	return []section{
```

replace

```go
		newTurnsSection(),
```

with

```go
		turns,
```

replace

```go
		&compactionsSection{before: map[string][]int{}},
	}
```

with

```go
		&compactionsSection{before: map[string][]int{}},
		&groupsSection{turns: turns, groups: map[groupKey]*groupCounts{}},
	}
```

replace

```go
type call struct {
	name string
	// build is the build stamp of the step that made the call.
	build string
}
```

with

```go
type call struct {
	name string
	// build is the build stamp of the step that made the call, and model
	// and provider the step's.
	build, model, provider string
}
```

and replace

```go
			p.calls[tc.ID] = call{name: tc.Name, build: build}
```

with

```go
			p.calls[tc.ID] = call{name: tc.Name, build: build, model: m.Model, provider: m.Provider}
```

In `internal/logreport/turns.go`, replace

```go
	// build is the build stamp of the segment's first step.
	build string
```

with

```go
	// model, provider and build are those of the segment's first step.
	model, provider, build string
```

and replace

```go
		seg.build = build
```

with

```go
		seg.model, seg.provider, seg.build = m.Model, m.Provider, build
```

In `internal/logreport/output.go`, replace

```go
	r.Compactions.writeText(tw)
```

with

```go
	r.Compactions.writeText(tw)
	writeGroups(tw, "By model", r.ByModel, false)
	writeGroups(tw, "By build", r.ByBuild, true)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/logreport/ ./internal/cmd/ -count=1`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofumpt -w internal/logreport/
git add internal/logreport/
git commit -m "feat(logreport): report prompts and failure rates by model and by build"
```

---

### Task 10: Part 4 verification and live check

**Files:**
- None changed.

**Interfaces:**
- Consumes: everything in part 4 (Task 9).
- Produces: the evidence for the pull request's Testing section.

- [ ] **Step 1: Build, vet and test everything**

Run: `go build ./... && go vet ./internal/logreport/... ./internal/cmd/... && go test ./...`
Expected: build and vet print nothing; every package reports `ok` or `no test files`.

- [ ] **Step 2: Lint and format**

Run: `/home/pbtrudel/go/bin/golangci-lint run --path-mode=abs --config=.golangci.yml --timeout=10m ./internal/logreport/... ./internal/cmd/...`, then `./scripts/check_log_capitalization.sh`, then `gofumpt -l internal/`
Expected: `0 issues.`, exit 0, and no files listed.

- [ ] **Step 3: Check the size and read the diff**

Run: `git diff --stat origin/main...HEAD`, then `git diff origin/main...HEAD | grep -cP '\x{2014}'`, then `git diff origin/main...HEAD`
Expected: about 300 changed lines, in `internal/logreport/` only; the em dash count is `0`; no debug output.

- [ ] **Step 4: Live check on the project database**

The report opens the real database read-only. It loads the project configuration to find the data directory and prints none of it. Do not print anything from the database other than the report's own output.

```bash
scratch=$(mktemp -d) && go build -o "$scratch/crush" .
export CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1
time "$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 > "$scratch/report.txt"
"$scratch/crush" stats report --cwd /home/pbtrudel/git/crush --since 2026-10-21 --until 2026-11-17 --json > "$scratch/report.json"
cat "$scratch/report.txt"
python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); [print(g["model"], g["provider"], g["prompts"], g["median_steps_per_prompt"], g["tool_calls"], round(100 * g["tool_error_rate"], 2), round(100 * g["edit_failure_rate"], 2), g["shell_commands"], g["shell_results"]) for g in d["by_model"][:5]]; [print(g["build"], g["prompts"], g["median_steps_per_prompt"], g["tool_calls"], g["edits"]) for g in d["by_build"]]' "$scratch/report.json"
rm -rf "$scratch"
```

Expected: both commands exit 0, and the text and JSON figures agree with each other. Record the output and the run time in the pull request; the controller compares the figures below with the one-off scripts. The pull request is opened with the `pr-discipline` skill only when the user asks for it.

Expected figures, in addition to item 7's and Tasks 2, 5 and 8: By model, the first five lines `deepseek-v4-1-flash-1017 mistral 196 16 2.57% 8.62% unstamped`, `deepseek-v4.1-flash-1017 hyper 33 20 2.79% 17.96% unstamped`, `deepseek/deepseek-v4.1-flash commandcode 25 10 4.83% 12.32% unstamped`, `deepseek-v4.1-flash neuralwatt 19 5 2.39% 12.50% unstamped`, `deepseek-v4.1-flash-1017 pgsgrove 16 15 1.73% 15.38% unstamped` (model, provider, prompts, median steps, tool errors, edit failures, shell failures). Prompts and median steps match a recount from the snapshot by the spec's definition; the rates are those of this code on the database rebuilt from it. By build, one line `unstamped 318 15 2.82% 10.12% unstamped`: 628 of 22,295 tool calls failed and 184 of 1,818 edits (item 7's Tools and Edits figures). No line names a tool. Report any difference rather than patching it.

---

## Self-review

1. **Spec coverage.** Turns: prompts, hidden turns, continuations (Task 6), steps and tool calls per prompt without child sessions (Task 6) and with them (Task 7), median, 90th percentile and maximum by nearest rank (Tasks 6 and 7, `percentile` from item 7), runs by `turn_end` (Task 6). Sessions by kind with each kind's share of steps, kinds resolved after the pass from a map of tool call ids (Task 3). Injections by kind with count, sessions and characters; the report owns the tag list, and a test in each producing package asserts its tag (Task 1). Compactions by trigger with the median tokens before (Task 4). By model and By build with prompts, median steps per prompt, tool error rate, edit failure rate and shell failure rate (Task 9). The unstamped rule: turn endings and compaction triggers read `unstamped` (Tasks 4 and 6), a continuation written before the stamps counts as a prompt (Task 6), shell failure rates read `unstamped` (Task 9), builds read `unstamped` (Task 9). A stamp value the report does not know is listed under its own name (`RunEnds`, `ByTrigger`). The prompt figure of the live check is reconciled in Task 8. The privacy test renders every section with markers in the new inputs (Task 1). The single query, read row by row, keeps one statement (Task 3).
2. **Placeholder scan.** Every code step carries its full code; every modify step names the exact text it replaces.
3. **Type consistency.** `classifyUser`, `userRow`, `sessionKind`, `segment`, `turnsSection`, `Spread`, `Group` and the helpers `summary`, `child`, `lastStep` keep one name and signature across tasks. Task 7 changes `user` and `step` to take `p *pass` first and says so; Task 9 widens `segment` and `call` and says so.
4. **Review Focus.** Each of the five lines has its test in the task named beside it.
