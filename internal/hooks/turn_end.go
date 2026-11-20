package hooks

import "strings"

// TurnEndDefaultReason is what a TurnEnd hook that denies without saying why
// asks the model to do. It replaces the tool-shaped text the runner
// substitutes for an exit-2 hook with empty stderr.
const TurnEndDefaultReason = "A TurnEnd hook asked you to continue before finishing."

// legacyDenyReason is the reason the runner substitutes for an exit-2 hook
// with empty stderr. It describes a tool call, so a TurnEnd verdict replaces
// it.
const legacyDenyReason = "blocked by hook"

// TurnEndReason is one hook's answer to an ending turn.
type TurnEndReason struct {
	// Hook is the hook's display name.
	Hook string
	// Reason is what the hook said: what the model should do, or why it
	// halted the turn.
	Reason string
	// Halt reports that this hook refused every continuation. Its Reason
	// is then for the log, not for the model.
	Halt bool
}

// TurnEndReasons reads the aggregated hooks as a TurnEnd verdict: a deny or
// a context asks for one more run of the prompt, a halt refuses every
// continuation, and any other answer is no opinion. A deny with no reason of
// its own gets TurnEndDefaultReason.
//
// One reason per hook comes back, in config order, so the gate can label
// each with its hook. A hook that answers with both a reason and a context
// contributes both.
func TurnEndReasons(agg AggregateResult) (reasons []TurnEndReason, stop bool) {
	for i, hook := range agg.Hooks {
		if hook.Halt {
			stop = true
			if reason := strings.TrimSpace(hook.Reason); reason != "" {
				reasons = append(reasons, TurnEndReason{Hook: hook.Name, Reason: reason, Halt: true})
			}
			continue
		}
		if hook.Decision == DecisionDeny.String() {
			reason := strings.TrimSpace(hook.Reason)
			if reason == "" || reason == legacyDenyReason {
				reason = TurnEndDefaultReason
			}
			reasons = append(reasons, TurnEndReason{Hook: hook.Name, Reason: reason})
		}
		if i >= len(agg.contexts) {
			continue
		}
		if context := strings.TrimSpace(agg.contexts[i]); context != "" {
			reasons = append(reasons, TurnEndReason{Hook: hook.Name, Reason: context})
		}
	}
	return reasons, stop
}

// TurnEndVerdict reads the aggregated hooks as one answer for an ending
// turn: the text to continue with, and whether a hook halted the turn. A
// halt outranks every reason.
func TurnEndVerdict(agg AggregateResult) (reason string, stop bool) {
	reasons, stop := TurnEndReasons(agg)
	texts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if r.Halt {
			continue
		}
		texts = append(texts, r.Reason)
	}
	return strings.Join(texts, "\n"), stop
}
