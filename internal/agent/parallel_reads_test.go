package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"github.com/stretchr/testify/require"

	"github.com/charmbracelet/crush/internal/agent/tools"
)

func TestReadGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		announce [][2]string
		observe  []string
		want     bool
	}{
		{
			name:     "two reads open the gate",
			announce: [][2]string{{"call-1", tools.ViewToolName}, {"call-2", tools.GrepToolName}},
			observe:  []string{"call-1", "call-2"},
			want:     true,
		},
		{
			name:     "one call stays sequential",
			announce: [][2]string{{"call-1", tools.ViewToolName}},
			observe:  []string{"call-1"},
		},
		{
			name:     "a read and an edit stay sequential",
			announce: [][2]string{{"call-1", tools.ViewToolName}, {"call-2", tools.EditToolName}},
			observe:  []string{"call-1", "call-2"},
		},
		{
			name:     "one call announced twice is one call",
			announce: [][2]string{{"call-1", tools.ViewToolName}, {"call-1", tools.ViewToolName}},
			observe:  []string{"call-1"},
		},
		{
			name: "an empty step stays sequential",
		},
	}

	// Every read tool of the turn is in the set, so a call to any opens the
	// gate.
	for _, name := range []string{tools.ViewToolName, tools.ReadToolName, tools.GrepToolName, tools.GlobToolName, tools.LSToolName} {
		require.Contains(t, readOnlyTools, name)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var gate readGate
			for _, announced := range tt.announce {
				gate.announce(announced[0], announced[1])
			}
			for _, id := range tt.observe {
				gate.observe(id)
			}
			require.Equal(t, tt.want, gate.allReadOnly())
		})
	}
}

// An unannounced call closes the gate from then on, but it cannot recall the
// verdict fantasy already read for the calls before it.
func TestReadGateCannotRecallAVerdict(t *testing.T) {
	t.Parallel()

	var gate readGate
	gate.announce("call-1", tools.ViewToolName)
	gate.announce("call-2", tools.ViewToolName)
	require.True(t, gate.allReadOnly(), "two announced reads are parallel")

	gate.observe("call-3")
	require.False(t, gate.allReadOnly(), "an unannounced call closes the gate from then on")
}

// A new step must not inherit the calls or the verdict of the step before
// it.
func TestReadGateReset(t *testing.T) {
	t.Parallel()

	var gate readGate
	gate.announce("call-1", tools.ViewToolName)
	gate.announce("call-2", tools.ViewToolName)
	gate.observe("call-3")
	require.False(t, gate.allReadOnly(), "an unseen call closes the gate")

	gate.reset()
	require.False(t, gate.allReadOnly(), "a reset step has no calls")
	gate.announce("call-1", tools.ViewToolName)
	gate.announce("call-2", tools.ViewToolName)
	gate.observe("call-1")
	gate.observe("call-2")
	require.True(t, gate.allReadOnly(), "the reset forgets the earlier unseen call")
}

// The wrapper answers for the whole step and forwards everything else. A tool
// outside the read set is left alone, so one that is already parallel stays
// parallel however the read calls go.
func TestParallelReadTool(t *testing.T) {
	t.Parallel()

	inner := textTool(tools.ViewToolName, false)
	shell := textTool(tools.BashToolName, true)
	var gate readGate
	wrapped := parallelReadTools([]fantasy.AgentTool{inner, shell}, &gate)

	require.Same(t, shell, wrapped[1], "a tool outside the read set is not wrapped")
	require.True(t, wrapped[1].Info().Parallel)

	info := wrapped[0].Info()
	require.Equal(t, tools.ViewToolName, info.Name, "the name is unchanged")
	require.False(t, info.Parallel, "one call in the step is not parallel")

	gate.announce("call-1", tools.ViewToolName)
	gate.announce("call-2", tools.GrepToolName)
	require.True(t, wrapped[0].Info().Parallel, "the wrapper defers to the step")

	opts := fantasy.ProviderOptions{anthropic.Name: &anthropic.ProviderCacheControlOptions{
		CacheControl: anthropic.CacheControl{Type: "ephemeral"},
	}}
	wrapped[0].SetProviderOptions(opts)
	require.Equal(t, opts, wrapped[0].ProviderOptions())
	require.Equal(t, opts, inner.ProviderOptions(), "options reach the inner tool")
}

// textTool builds a tool of the turn that answers with its own name.
func textTool(name string, parallel bool) fantasy.AgentTool {
	run := func(context.Context, struct{}, fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("read from " + name), nil
	}
	if parallel {
		return fantasy.NewParallelAgentTool(name, "", run)
	}
	return fantasy.NewAgentTool(name, "", run)
}

// TestGatedStep drives a real fantasy agent through a step, with tools that
// record whether two of them were ever inside at once.
func TestGatedStep(t *testing.T) {
	t.Parallel()

	// Two reads are dispatched at once, so each one finds the other.
	t.Run("two reads run at once", func(t *testing.T) {
		t.Parallel()
		barrier := &overlapBarrier{want: 2, timeout: 5 * time.Second}
		results := runBarrierStep(t, barrier, tools.ViewToolName, tools.GrepToolName)

		require.Equal(t, "met", results[tools.ViewToolName])
		require.Equal(t, "met", results[tools.GrepToolName])
		require.True(t, barrier.overlapped, "both reads were running at once")
	})

	// A step carrying an edit keeps the model's order, one call at a time,
	// so no read sees a half-written file. Only a read waits for a peer, and
	// it gives up quickly because the gate keeps the edit out of its
	// goroutines.
	t.Run("a mixed step stays sequential", func(t *testing.T) {
		t.Parallel()
		barrier := &overlapBarrier{want: 2, timeout: 100 * time.Millisecond}
		results := runBarrierStep(t, barrier, tools.ViewToolName, tools.EditToolName)

		require.Equal(t, "alone", results[tools.ViewToolName])
		require.Equal(t, "alone", results[tools.EditToolName])
		require.Equal(t, []string{tools.ViewToolName, tools.EditToolName}, barrier.names)
		require.False(t, barrier.overlapped, "a mixed step ran its calls one at a time")
	})
}

// runBarrierStep runs one step of the given tools. A read waits for its
// peers; anything else answers at once, so a mixed step costs one short
// timeout instead of the full one.
func runBarrierStep(t *testing.T, barrier *overlapBarrier, names ...string) map[string]string {
	t.Helper()
	list := make([]fantasy.AgentTool, len(names))
	var step []fantasy.StreamPart
	for i, name := range names {
		_, waits := readOnlyTools[name]
		list[i] = &barrierTool{name: name, barrier: barrier, waits: waits}
		id := fmt.Sprintf("call-%d", i+1)
		step = append(step,
			fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: id, ToolCallName: name},
			fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd, ID: id},
			// Only this part makes the call dispatchable, the way a
			// provider ends a streamed call.
			fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: id, ToolCallName: name, ToolCallInput: "{}"},
		)
	}
	step = append(step, fantasy.StreamPart{
		Type:         fantasy.StreamPartTypeFinish,
		FinishReason: fantasy.FinishReasonToolCalls,
	})
	// The answer that ends the run once the step's tools have run.
	answer := []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeTextStart, ID: "text-1"},
		{Type: fantasy.StreamPartTypeTextDelta, ID: "text-1", Delta: "done"},
		{Type: fantasy.StreamPartTypeTextEnd, ID: "text-1"},
		{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop},
	}
	return runGatedStep(t, list, step, answer)
}

// runGatedStep streams the given steps through a fantasy agent whose tools
// carry the gate the way the turn wires it, and returns each tool's text
// result.
func runGatedStep(t *testing.T, list []fantasy.AgentTool, steps ...[]fantasy.StreamPart) map[string]string {
	t.Helper()

	gate := &readGate{}
	// The first request plays the tool-call step, the next the answer.
	model := &fakeLanguageModel{}
	var requests atomic.Int32
	model.stream = func(yield func(fantasy.StreamPart) bool) {
		parts := steps[min(int(requests.Add(1))-1, len(steps)-1)]
		for _, part := range parts {
			if !yield(part) {
				return
			}
		}
	}
	// fantasy reports the results of a parallel step from several goroutines
	// at once.
	var mu sync.Mutex
	results := make(map[string]string)
	_, err := fantasy.NewAgent(model).Stream(t.Context(), fantasy.AgentStreamCall{
		Prompt: "go",
		PrepareStep: func(ctx context.Context, _ fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
			gate.reset()
			return ctx, fantasy.PrepareStepResult{Tools: parallelReadTools(list, gate)}, nil
		},
		OnToolInputStart: func(id string, toolName string) error {
			gate.announce(id, toolName)
			return nil
		},
		OnToolCall: func(call fantasy.ToolCallContent) error {
			gate.observe(call.ToolCallID)
			return nil
		},
		OnToolResult: func(result fantasy.ToolResultContent) error {
			text, ok := result.Result.(fantasy.ToolResultOutputContentText)
			if !ok {
				return nil
			}
			mu.Lock()
			results[result.ToolName] = text.Text
			mu.Unlock()
			return nil
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, results, "the step ran its tools")
	return results
}

// overlapBarrier records the order its tools arrive in and whether two of
// them were ever inside at the same time.
type overlapBarrier struct {
	mu         sync.Mutex
	inside     int
	want       int
	timeout    time.Duration
	names      []string
	overlapped bool
	ready      chan struct{}
	closed     bool
}

func (b *overlapBarrier) meet(name string, wait bool) bool {
	b.mu.Lock()
	if b.ready == nil {
		b.ready = make(chan struct{})
	}
	b.inside++
	b.names = append(b.names, name)
	if b.inside >= b.want {
		b.overlapped = true
		if !b.closed {
			b.closed = true
			close(b.ready)
		}
	}
	ready := b.ready
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.inside--
		b.mu.Unlock()
	}()
	if !wait {
		return false
	}
	select {
	case <-ready:
		return true
	case <-time.After(b.timeout):
		return false
	}
}

// barrierTool reports whether a peer reached it, and waits only when the test
// says it should, so a sequential step fails fast instead of hanging.
type barrierTool struct {
	name    string
	barrier *overlapBarrier
	waits   bool
}

func (t *barrierTool) Info() fantasy.ToolInfo { return fantasy.ToolInfo{Name: t.name} }

func (t *barrierTool) Run(context.Context, fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if t.barrier.meet(t.name, t.waits) {
		return fantasy.NewTextResponse("met"), nil
	}
	return fantasy.NewTextResponse("alone"), nil
}

func (t *barrierTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (t *barrierTool) SetProviderOptions(fantasy.ProviderOptions) {}

// TestToolNameAliasesStayOutsideTheReadSet pins that no alias target is a
// read tool: the gate never sees an aliased call, so a repaired name would
// otherwise reach a read tool the gate judged sequential.
func TestToolNameAliasesStayOutsideTheReadSet(t *testing.T) {
	t.Parallel()

	for alias, target := range toolNameAliases {
		require.NotContains(t, readOnlyTools, target, "alias %q points at a read tool", alias)
	}
}
