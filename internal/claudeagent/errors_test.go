package claudeagent

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// errorRawJSON encodes a string the way the CLI writes it on the wire.
func errorRawJSON(s string) json.RawMessage {
	return json.RawMessage(strconv.Quote(s))
}

// errorAssistantEnvelope is an assistant message the CLI could not complete.
func errorAssistantEnvelope(code string) *envelope {
	return &envelope{
		Type:    msgAssistant,
		Message: json.RawMessage(`{"id":"msg_1"}`),
		Error:   code,
	}
}

func errorStatus(v int) *int { return &v }

func TestErrorResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		observed []*envelope
		result   *envelope
		want     string
	}{
		{
			name:   "successful turn",
			result: &envelope{Type: msgResult, Subtype: "success", Result: errorRawJSON("done")},
		},
		{
			name: "usage warning alone is not a failure",
			observed: []*envelope{{
				Type:          msgRateLimitEvent,
				RateLimitInfo: &wireRateLimit{Status: "allowed_warning", RateLimitType: "five_hour"},
			}},
			result: &envelope{Type: msgResult, Subtype: "success", Result: errorRawJSON("done")},
		},
		{
			name:   "failed turn uses the errors list",
			result: &envelope{Type: msgResult, Subtype: "error_during_execution", IsError: true, Errors: []string{"first", "second"}},
			want:   "first\nsecond",
		},
		{
			name:   "login hint is rewritten",
			result: &envelope{Type: msgResult, Subtype: "success", IsError: true, Result: errorRawJSON("Please run /login")},
			want:   "Run `claude auth login` in a terminal",
		},
		{
			name:   "client error keeps the result text",
			result: &envelope{Type: msgResult, Subtype: "success", APIErrorStatus: errorStatus(400), Result: errorRawJSON("Invalid request")},
			want:   "Invalid request",
		},
		{
			name:   "server error",
			result: &envelope{Type: msgResult, Subtype: "success", APIErrorStatus: errorStatus(500), Result: errorRawJSON("boom")},
			want:   "Anthropic returned HTTP 500. Claude Code exhausted its retries. Try again shortly.",
		},
		{
			name:   "overloaded",
			result: &envelope{Type: msgResult, Subtype: "success", APIErrorStatus: errorStatus(529)},
			want:   "Anthropic is overloaded (529). Claude Code exhausted its retries. Try again shortly.",
		},
		{
			name: "rejected rate limit with window and reset",
			observed: []*envelope{{
				Type: msgRateLimitEvent,
				RateLimitInfo: &wireRateLimit{
					Status:        "rejected",
					RateLimitType: "five_hour",
					ResetsAt:      1700000000,
				},
			}},
			result: &envelope{Type: msgResult, Subtype: "success", APIErrorStatus: errorStatus(429)},
			want:   "Claude Code subscription limit reached (five hour). Resets at 2023-11-14T22:13:20Z.",
		},
		{
			name: "rejected rate limit without window or reset",
			observed: []*envelope{{
				Type:          msgRateLimitEvent,
				RateLimitInfo: &wireRateLimit{Status: "rejected"},
			}},
			result: &envelope{Type: msgResult, Subtype: "success", APIErrorStatus: errorStatus(429)},
			want:   "Claude Code subscription limit reached.",
		},
		{
			name:     "rate limit code",
			observed: []*envelope{errorAssistantEnvelope(errorCodeRateLimit)},
			result:   &envelope{Type: msgResult, Subtype: "success", APIErrorStatus: errorStatus(429)},
			want:     "Claude Code subscription limit reached.",
		},
		{
			name:     "billing error",
			observed: []*envelope{errorAssistantEnvelope(errorCodeBilling)},
			result:   &envelope{Type: msgResult, Subtype: "success", IsError: true, Result: errorRawJSON("Your credit balance is too low.")},
			want:     "Your credit balance is too low. Check your Claude plan and extra-usage settings.",
		},
		{
			name: "sub-agent failure does not set the request code",
			observed: []*envelope{{
				Type:            msgAssistant,
				ParentToolUseID: new(string),
				Error:           errorCodeRateLimit,
			}},
			result: &envelope{Type: msgResult, Subtype: "success", APIErrorStatus: errorStatus(429)},
			want:   "the Claude Code CLI could not finish its reply (HTTP 429)",
		},
		{
			name:   "empty failure with a status",
			result: &envelope{Type: msgResult, Subtype: "success", IsError: true, APIErrorStatus: errorStatus(200)},
			want:   "the Claude Code CLI could not finish its reply (HTTP 200)",
		},
		{
			name:   "empty failure without a status",
			result: &envelope{Type: msgResult, Subtype: "error_during_execution", IsError: true},
			want:   "the Claude Code CLI could not finish its reply",
		},
		{
			name:   "status is ignored for failed turns",
			result: &envelope{Type: msgResult, Subtype: "error_during_execution", IsError: true, APIErrorStatus: errorStatus(529), Errors: []string{"tool exploded"}},
			want:   "tool exploded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tracker := newErrorTracker()
			for _, env := range tt.observed {
				tracker.observe(env)
			}

			err := tracker.result(tt.result)
			if tt.want == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.want)
		})
	}
}

func TestErrorObserveIgnoresOtherEnvelopes(t *testing.T) {
	t.Parallel()

	tracker := newErrorTracker()
	tracker.observe(nil)
	tracker.observe(&envelope{Type: msgUser})
	tracker.observe(&envelope{Type: msgStreamEvent})

	require.NoError(t, tracker.result(&envelope{Type: msgResult, Subtype: "success"}))
}

func TestErrorResultIgnoresNonResultEnvelopes(t *testing.T) {
	t.Parallel()

	tracker := newErrorTracker()
	require.NoError(t, tracker.result(nil))
	require.NoError(t, tracker.result(&envelope{Type: msgAssistant, IsError: true}))
}

func TestErrorRejectionClearedByLaterEvent(t *testing.T) {
	t.Parallel()

	tracker := newErrorTracker()
	tracker.observe(&envelope{
		Type:          msgRateLimitEvent,
		RateLimitInfo: &wireRateLimit{Status: "rejected", RateLimitType: "five_hour"},
	})
	tracker.observe(&envelope{
		Type:          msgRateLimitEvent,
		RateLimitInfo: &wireRateLimit{Status: "allowed"},
	})

	// Without the rejection the 429 has no explanation of its own.
	require.EqualError(t,
		tracker.result(&envelope{Type: msgResult, Subtype: "success", APIErrorStatus: errorStatus(429)}),
		"the Claude Code CLI could not finish its reply (HTTP 429)",
	)
}

func TestErrorAssistantLineDecodesCode(t *testing.T) {
	t.Parallel()

	// The CLI writes the code next to the message body, so it has to
	// survive the transport's decode for the tracker to see it.
	line := []byte(`{"type":"assistant","message":{"id":"msg_1"},"error":"rate_limit","parent_tool_use_id":null}`)
	var env envelope
	require.NoError(t, json.Unmarshal(line, &env))

	tracker := newErrorTracker()
	tracker.observe(&env)

	require.EqualError(t,
		tracker.result(&envelope{Type: msgResult, Subtype: "success", APIErrorStatus: errorStatus(429)}),
		"Claude Code subscription limit reached.",
	)
}

func TestErrorNilTracker(t *testing.T) {
	t.Parallel()

	var tracker *errorTracker
	require.NotPanics(t, func() {
		tracker.observe(&envelope{Type: msgRateLimitEvent})
		require.NoError(t, tracker.result(&envelope{Type: msgResult, Subtype: "success"}))
	})
}
