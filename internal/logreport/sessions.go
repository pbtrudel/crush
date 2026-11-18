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
