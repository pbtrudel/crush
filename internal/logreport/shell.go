package logreport

import (
	"fmt"
	"io"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/tidwall/gjson"
)

// Shell is the Shell section. A foreground command is a shell result that
// carries the exit_code or the interrupted stamp; a command moved to the
// background carries neither and is not counted.
type Shell struct {
	Commands    int     `json:"commands"`
	NonZero     int     `json:"non_zero_exits"`
	Interrupted int     `json:"interrupted"`
	FailureRate float64 `json:"failure_rate"`
	// Unstamped counts the shell results without either stamp written by
	// builds that do not stamp them.
	Unstamped int `json:"unstamped"`
}

type shellSection struct {
	shell Shell
	// bare counts the results without either stamp by their call's build.
	bare map[string]int
}

func (s *shellSection) message(p *pass, m *message.Message) {
	if m.Role != message.Tool {
		return
	}
	for _, tr := range m.ToolResults() {
		if tr.Name != bashTool {
			continue
		}
		code := gjson.Get(tr.Metadata, "exit_code")
		interrupted := gjson.Get(tr.Metadata, "interrupted").Bool()
		switch {
		case interrupted:
			s.shell.Commands++
			s.shell.Interrupted++
		case code.Exists():
			s.shell.Commands++
			if code.Int() != 0 {
				s.shell.NonZero++
			}
		default:
			s.bare[p.calls[tr.ToolCallID].build]++
		}
	}
}

func (s *shellSection) endSession(*pass) {}

func (s *shellSection) finish(p *pass, r *Report) {
	for build, n := range s.bare {
		if p.kindName("", build) == "unstamped" {
			s.shell.Unstamped += n
		}
	}
	s.shell.FailureRate = ratio(s.shell.NonZero+s.shell.Interrupted, s.shell.Commands)
	r.Shell = s.shell
}

func (s Shell) writeText(out io.Writer) {
	fmt.Fprintln(out, "Shell")
	fmt.Fprintf(out, "  foreground commands\t%d\n", s.Commands)
	fmt.Fprintf(out, "  non-zero exits\t%d\n", s.NonZero)
	fmt.Fprintf(out, "  interrupted\t%d\n", s.Interrupted)
	fmt.Fprintf(out, "  failure rate\t%s\n", percent(s.FailureRate))
	fmt.Fprintf(out, "  unstamped results\t%d\n", s.Unstamped)
	fmt.Fprintln(out)
}
