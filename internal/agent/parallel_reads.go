package agent

import (
	"context"
	"sync"

	"charm.land/fantasy"

	"github.com/charmbracelet/crush/internal/agent/tools"
)

// readOnlyTools is the set of tools that only read: a step made of nothing
// else can run every one of its calls at once without any of them seeing a
// half-finished change from another.
var readOnlyTools = map[string]struct{}{
	tools.ViewToolName: {},
	tools.ReadToolName: {},
	tools.GrepToolName: {},
	tools.GlobToolName: {},
	tools.LSToolName:   {},
}

// readGate decides, per step, whether the step's read-only calls may run
// concurrently.
//
// fantasy starts a call whose Info().Parallel is true on a goroutine and runs
// every other call inline without waiting for the goroutines already started,
// so one mislabelled call lets an edit or a shell command race a read. It
// reads Info() once the whole step has streamed, which is the window this
// gate uses, and the gate leans on one property of the provider: that it
// announces every call of the step with an input-start event before the step
// ends. Every provider in fantasy v0.48.1 does. A call that reaches observe
// without one is invisible to the stream and its verdict cannot be recalled,
// so the gate is sound for the announced calls only; a step where such a call
// follows announced reads still dispatches those reads at once, and observe
// just keeps the calls after it inline.
//
// Two choices lean conservative. A provider-executed call announces a name
// like any other, and OnToolInputStart carries no ProviderExecuted flag, so
// such a call counts as a name outside the read set and keeps the whole step
// sequential. And the gate trusts the announced name to be the dispatched
// one. Crush now wires a RepairToolCall, but a repair only ever maps an alias
// onto a tool outside the read set, and the announced alias is itself outside
// the read set, so a step holding a repaired call already stays sequential.
type readGate struct {
	mu     sync.Mutex
	names  []string
	seen   map[string]struct{}
	unseen bool
}

// reset forgets the step that ended, so the next one starts from no calls and
// an open verdict.
func (g *readGate) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.names = nil
	g.seen = nil
	g.unseen = false
}

// announce records a call the provider started streaming. A second
// announcement of the same call would otherwise count as a second call and
// open the gate on its own.
func (g *readGate) announce(id, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen == nil {
		g.seen = make(map[string]struct{}, 2)
	}
	if _, repeated := g.seen[id]; repeated {
		return
	}
	g.seen[id] = struct{}{}
	g.names = append(g.names, name)
}

// observe records a call that reached its tool call. A call the provider
// never announced makes the step opaque from here on, which keeps the calls
// after it inline; the verdict fantasy already read for the earlier calls
// stands.
func (g *readGate) observe(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.seen[id]; !ok {
		g.unseen = true
	}
}

// allReadOnly reports whether the step that just streamed is two or more
// read-only calls. A single call has nothing to run beside, so it stays on
// the inline path and keeps the simple error and permission flow.
func (g *readGate) allReadOnly() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.unseen || len(g.names) < 2 {
		return false
	}
	for _, name := range g.names {
		if _, ok := readOnlyTools[name]; !ok {
			return false
		}
	}
	return true
}

// parallelReadTool reports the gate's verdict for one read-only tool, so the
// step runs its reads at once only when every call in it is a read.
type parallelReadTool struct {
	inner fantasy.AgentTool
	gate  *readGate
}

func (t parallelReadTool) Info() fantasy.ToolInfo {
	info := t.inner.Info()
	info.Parallel = t.gate.allReadOnly()
	return info
}

func (t parallelReadTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	return t.inner.Run(ctx, call)
}

func (t parallelReadTool) ProviderOptions() fantasy.ProviderOptions {
	return t.inner.ProviderOptions()
}

func (t parallelReadTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	t.inner.SetProviderOptions(opts)
}

// parallelReadTools wraps the read-only tools of a step in parallelReadTool
// and leaves every other tool as it is. It runs per step rather than per turn
// because a step can adopt a tool the turn did not carry, and an adopted read
// has to ask the same gate as the rest.
func parallelReadTools(list []fantasy.AgentTool, gate *readGate) []fantasy.AgentTool {
	wrapped := make([]fantasy.AgentTool, len(list))
	for i, tool := range list {
		if _, ok := readOnlyTools[tool.Info().Name]; ok {
			wrapped[i] = parallelReadTool{inner: tool, gate: gate}
			continue
		}
		wrapped[i] = tool
	}
	return wrapped
}
