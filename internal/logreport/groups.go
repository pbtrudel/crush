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
	ShellResults  int `json:"shell_results"`
	ShellCommands int `json:"shell_commands"`
	// ShellUnstamped are the shell results from builds that did not stamp
	// exit codes, as in the Shell section.
	ShellUnstamped   int     `json:"shell_unstamped"`
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
	// bare counts the shell results without either stamp by the build of
	// their call's step.
	bare map[string]int
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
				switch {
				case interrupted || code.Exists():
					g.shellCommands++
					if interrupted || code.Int() != 0 {
						g.shellFailed++
					}
				// An error kind comes from a build that stamps: neither a
				// command nor a bare result.
				case !gjson.Get(tr.Metadata, "error_kind").Exists():
					if g.bare == nil {
						g.bare = map[string]int{}
					}
					g.bare[c.build]++
				}
			}
		}
	}
}

func (s *groupsSection) endSession(*pass) {}

func (s *groupsSection) finish(p *pass, r *Report) {
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
		// A bare result is unstamped only when its build never stamps.
		unstamped := 0
		for build, n := range c.bare {
			if p.kindName("", build) == "unstamped" {
				unstamped += n
			}
		}
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
			ShellUnstamped:   unstamped,
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
		case g.ShellUnstamped > 0:
			shell = "unstamped"
		}
		fmt.Fprintf(out, "  %s\t%d\t%d\t%s\t%s\t%s\n", name, g.Prompts, g.MedianSteps,
			percent(g.ToolErrorRate), percent(g.EditFailureRate), shell)
	}
	fmt.Fprintln(out)
}
