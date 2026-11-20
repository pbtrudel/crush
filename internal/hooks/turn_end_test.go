package hooks

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// turnEndRunner runs the given TurnEnd hooks and returns their aggregate.
func turnEndRunner(t *testing.T, cfgs ...HookConfig) AggregateResult {
	t.Helper()
	runner := NewRunner(Static(map[string][]HookConfig{EventTurnEnd: cfgs}), t.TempDir(), t.TempDir())
	return runner.Run(t.Context(), Event{Name: EventTurnEnd, SessionID: "sess"})
}

// A TurnEnd hook's answer becomes a request to continue the turn, a halt
// that refuses every continuation, or no opinion at all.
func TestTurnEndVerdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		hooks      []HookConfig
		wantReason string
		wantStop   bool
	}{
		{
			name:       "exit 2 with stderr",
			hooks:      []HookConfig{{Command: `echo "run the tests" >&2; exit 2`}},
			wantReason: "run the tests",
		},
		{
			name:       "decision block with a reason",
			hooks:      []HookConfig{{Command: `echo '{"decision":"block","reason":"tests are red"}'`}},
			wantReason: "tests are red",
		},
		{
			name:       "decision deny without a reason",
			hooks:      []HookConfig{{Command: `echo '{"decision":"deny"}'`}},
			wantReason: TurnEndDefaultReason,
		},
		{
			name:       "exit 2 without stderr",
			hooks:      []HookConfig{{Command: `exit 2`}},
			wantReason: TurnEndDefaultReason,
		},
		{
			name:       "additionalContext",
			hooks:      []HookConfig{{Command: `echo '{"hookSpecificOutput":{"additionalContext":"cite the file"}}'`}},
			wantReason: "cite the file",
		},
		{
			name:       "context as a string",
			hooks:      []HookConfig{{Command: `echo '{"context":"say what you ran"}'`}},
			wantReason: "say what you ran",
		},
		{
			name:       "context as an array",
			hooks:      []HookConfig{{Command: `echo '{"context":["one","two"]}'`}},
			wantReason: "one\ntwo",
		},
		{
			name:     "continue false halts",
			hooks:    []HookConfig{{Command: `echo '{"continue":false,"stopReason":"stop here"}'`}},
			wantStop: true,
		},
		{
			name:     "halt halts",
			hooks:    []HookConfig{{Command: `echo '{"halt":true}'`}},
			wantStop: true,
		},
		{
			name:     "exit 49 halts",
			hooks:    []HookConfig{{Command: `echo "no more" >&2; exit 49`}},
			wantStop: true,
		},
		{
			name:  "exit 0 with no output",
			hooks: []HookConfig{{Command: `true`}},
		},
		{
			name:  "allow",
			hooks: []HookConfig{{Command: `echo '{"decision":"allow"}'`}},
		},
		{
			name:  "exit 1",
			hooks: []HookConfig{{Command: `exit 1`}},
		},
		{
			name: "an asking hook and a halting hook",
			hooks: []HookConfig{
				{Command: `echo '{"decision":"block","reason":"keep going"}'`},
				{Command: `echo '{"halt":true}'`},
			},
			wantReason: "keep going",
			wantStop:   true,
		},
		{
			name: "two hooks asking",
			hooks: []HookConfig{
				{Command: `echo '{"decision":"block","reason":"first"}'`},
				{Command: `echo '{"decision":"block","reason":"second"}'`},
			},
			wantReason: "first\nsecond",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reason, stop := TurnEndVerdict(turnEndRunner(t, tt.hooks...))
			require.Equal(t, tt.wantStop, stop)
			require.Equal(t, tt.wantReason, reason)
		})
	}
}

// The reasons keep the hook each one came from, so the gate can label them
// with the display name.
func TestTurnEndReasonsKeepTheirHook(t *testing.T) {
	t.Parallel()

	agg := turnEndRunner(t,
		HookConfig{Name: "tests", Command: `echo '{"decision":"block","reason":"run the tests"}'`},
		HookConfig{Name: "cite", Command: `echo '{"context":"cite the file"}'`},
		HookConfig{Name: "quiet", Command: `true`},
	)

	reasons, stop := TurnEndReasons(agg)
	require.False(t, stop)
	require.Len(t, reasons, 2)
	require.Equal(t, TurnEndReason{Hook: "tests", Reason: "run the tests"}, reasons[0])
	require.Equal(t, TurnEndReason{Hook: "cite", Reason: "cite the file"}, reasons[1])
}

// A halting hook's reason is kept for the log and kept out of the text the
// model reads, because no continuation follows it.
func TestTurnEndReasonsMarkAHalt(t *testing.T) {
	t.Parallel()

	agg := turnEndRunner(t, HookConfig{Name: "guard", Command: `echo "hands off" >&2; exit 49`})
	reasons, stop := TurnEndReasons(agg)
	require.True(t, stop)
	require.Equal(t, []TurnEndReason{{Hook: "guard", Reason: "hands off", Halt: true}}, reasons)

	reason, stop := TurnEndVerdict(agg)
	require.True(t, stop)
	require.Empty(t, reason)
}

// A hook that names nothing is labelled with its command, as everywhere
// else a hook is shown.
func TestTurnEndReasonsFallBackToTheCommand(t *testing.T) {
	t.Parallel()

	command := `echo '{"decision":"block","reason":"run the tests"}'`
	reasons, _ := TurnEndReasons(turnEndRunner(t, HookConfig{Command: command}))
	require.Len(t, reasons, 1)
	require.Equal(t, command, reasons[0].Hook)
}

// A deny without a reason is the only case that gets the default text, and
// a hook's own wording is never replaced.
func TestTurnEndReasonsKeepTheHooksWording(t *testing.T) {
	t.Parallel()

	agg := turnEndRunner(t, HookConfig{Command: `echo "say what you did" >&2; exit 2`})
	reason, stop := TurnEndVerdict(agg)
	require.False(t, stop)
	require.Equal(t, "say what you did", reason)
	require.NotContains(t, reason, TurnEndDefaultReason)
}

// The payload carries the gate's fields on TurnEnd, and stop_hook_active is
// there even when it is false.
func TestBuildPayloadTurnEnd(t *testing.T) {
	t.Parallel()

	message := "done"
	left := 0
	active := false
	payload := string(BuildPayload(Event{
		Name:                 EventTurnEnd,
		SessionID:            "sess",
		LastAssistantMessage: &message,
		StopHookActive:       &active,
		TurnEnd:              "step_limit",
		ChangedFiles:         []string{"internal/agent/agent.go"},
		ContinuationsLeft:    &left,
	}, "/work"))

	require.Contains(t, payload, `"stop_hook_active":false`)
	require.Contains(t, payload, `"turn_end":"step_limit"`)
	require.Contains(t, payload, `"changed_files":["internal/agent/agent.go"]`)
	require.Contains(t, payload, `"continuations_left":0`)
	require.Contains(t, payload, `"last_assistant_message":"done"`)
	require.True(t, strings.HasPrefix(payload, "{"))

	// A continuation says so.
	active = true
	payload = string(BuildPayload(Event{Name: EventTurnEnd, StopHookActive: &active}, "/work"))
	require.Contains(t, payload, `"stop_hook_active":true`)
}

// A payload without the gate's fields does not carry them, so every other
// event is unchanged.
func TestBuildPayloadWithoutGateFields(t *testing.T) {
	t.Parallel()

	payload := string(BuildPayload(Event{Name: EventTurnStart, SessionID: "sess"}, "/work"))
	for _, field := range []string{"stop_hook_active", "turn_end", "changed_files", "continuations_left"} {
		require.NotContains(t, payload, field)
	}
}
