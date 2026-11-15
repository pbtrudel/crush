package agent

import (
	"context"

	"charm.land/fantasy"
)

// boundedOutputTool truncates a tool's text result where the tool returns
// it.
//
// The session log has always stored a bounded copy (see truncateToolOutput),
// but the running turn kept the full text, because fantasy carries the
// tool's own response into the following requests. The turn paid for the
// full output on every later step, and the next turn replayed different
// text at that position, so everything after it missed the provider's
// prompt cache. Bounding the response itself makes both copies one string.
type boundedOutputTool struct {
	inner fantasy.AgentTool
	bound func(string) string
}

func (t boundedOutputTool) Info() fantasy.ToolInfo { return t.inner.Info() }

func (t boundedOutputTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp, err := t.inner.Run(ctx, call)
	if err != nil {
		return resp, err
	}
	// A media result's text is only its caption; the payload is budgeted
	// separately (see applyImageBudget).
	if len(resp.Data) == 0 {
		resp.Content = t.bound(resp.Content)
	}
	return resp, nil
}

func (t boundedOutputTool) ProviderOptions() fantasy.ProviderOptions {
	return t.inner.ProviderOptions()
}

func (t boundedOutputTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	t.inner.SetProviderOptions(opts)
}

// boundToolOutputs wraps every tool of a turn in a boundedOutputTool.
func (a *sessionAgent) boundToolOutputs(tools []fantasy.AgentTool) []fantasy.AgentTool {
	bounded := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		bounded[i] = boundedOutputTool{inner: tool, bound: a.truncateToolOutput}
	}
	return bounded
}
