package agent

import "charm.land/fantasy"

// stepInjection is a group of messages a turn added ahead of one step:
// queued follow-up prompts, reminders, and background notices.
//
// fantasy rebuilds every step's messages from the turn's initial prompt plus
// its own responses, so a message appended in PrepareStep reaches the model
// for that one request only. The session log keeps it, though, so the next
// turn replays it in the middle of this turn's messages: the model loses a
// follow-up it was just given, and the replay stops matching what the
// provider cached. Remembering where each group went lets every later step
// put it back in the same place.
type stepInjection struct {
	// at is how many of fantasy's messages preceded the group.
	at       int
	messages []fantasy.Message
}

// withInjections returns base with every injection group restored at the
// position it was first added. Groups are in the order they were added, so
// their positions never decrease.
func withInjections(base []fantasy.Message, injections []stepInjection) []fantasy.Message {
	if len(injections) == 0 {
		return base
	}
	extra := 0
	for _, injection := range injections {
		extra += len(injection.messages)
	}
	merged := make([]fantasy.Message, 0, len(base)+extra)
	next := 0
	for _, injection := range injections {
		at := min(injection.at, len(base))
		merged = append(merged, base[next:at]...)
		merged = append(merged, injection.messages...)
		next = at
	}
	return append(merged, base[next:]...)
}
