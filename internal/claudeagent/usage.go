package claudeagent

import "charm.land/fantasy"

// usageCounts is the highest usage the CLI reported for one model response.
// The CLI repeats a response's usage several times while streaming, so the
// maximum per field is the response's real count.
type usageCounts struct {
	input      int64
	cacheRead  int64
	cacheWrite int64
	output     int64
	reasoning  int64
}

// usageTracker accumulates the usage of one step.
//
// The CLI reports the same response's usage repeatedly while streaming and
// then reports the whole turn again as a total. Summing those reports double
// counts and makes Crush compact far too early, so each response keeps its
// highest count and the step's context size comes from the last response
// only.
type usageTracker struct {
	responses map[string]*usageCounts
	last      string
}

// newUsageTracker returns a tracker ready for one step.
func newUsageTracker() *usageTracker {
	return &usageTracker{responses: make(map[string]*usageCounts)}
}

// update records one usage report for a model response, keeping the highest
// count per field. It is safe to call with a nil usage block.
func (u *usageTracker) update(messageID string, usage *wireUsage) {
	if u == nil || usage == nil {
		return
	}
	if u.responses == nil {
		u.responses = make(map[string]*usageCounts)
	}
	counts, ok := u.responses[messageID]
	if !ok {
		counts = &usageCounts{}
		u.responses[messageID] = counts
		u.last = messageID
	}
	counts.input = max(counts.input, usageInt64(usage.InputTokens))
	counts.cacheRead = max(counts.cacheRead, usageInt64(usage.CacheReadInputTokens))
	counts.cacheWrite = max(counts.cacheWrite, usageInt64(usage.CacheCreationInputTokens))
	counts.output = max(counts.output, usageInt64(usage.OutputTokens))
	if usage.OutputTokensDetails != nil {
		counts.reasoning = max(counts.reasoning, usageInt64(usage.OutputTokensDetails.ThinkingTokens))
	}
}

// applyTurnTotal folds a result message's turn total into the tracked usage.
// The total covers every model call in the turn, so it is only meaningful
// when the step saw at most one response.
func (u *usageTracker) applyTurnTotal(usage *wireUsage) {
	if u == nil || usage == nil || len(u.responses) > 1 {
		return
	}
	id := u.last
	if id == "" {
		id = "result"
	}
	u.update(id, usage)
}

// snapshot returns the step's usage. Input and cache tokens describe the most
// recent API call, while output tokens sum every response the step produced.
func (u *usageTracker) snapshot() fantasy.Usage {
	if u == nil || len(u.responses) == 0 {
		return fantasy.Usage{}
	}
	var output, reasoning int64
	for _, counts := range u.responses {
		output += counts.output
		reasoning += counts.reasoning
	}
	var usage fantasy.Usage
	if last, ok := u.responses[u.last]; ok {
		// InputTokens is the uncached part of the prompt. Crush adds the
		// cache counts back to size the context, so folding them in here
		// would count the same tokens twice and trigger compaction early.
		usage.InputTokens = last.input
		usage.CacheReadTokens = last.cacheRead
		usage.CacheCreationTokens = last.cacheWrite
	}
	usage.OutputTokens = output
	usage.ReasoningTokens = reasoning
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage
}

// usageInt64 dereferences an optional wire count.
func usageInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
