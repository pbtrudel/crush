package claudeagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Error codes the CLI attaches to assistant messages.
const (
	errorCodeRateLimit = "rate_limit"
	errorCodeBilling   = "billing_error"
)

// errorTracker keeps the request-local diagnostics the CLI reports before the
// terminal result message. A usage warning alone is not a failed request, and
// Claude Code owns retries, so nothing becomes an error until the result.
type errorTracker struct {
	code     string
	rejected *wireRateLimit
}

// newErrorTracker returns a tracker ready for one request.
func newErrorTracker() *errorTracker {
	return &errorTracker{}
}

// observe records the diagnostics carried by one stream envelope.
func (e *errorTracker) observe(env *envelope) {
	if e == nil || env == nil {
		return
	}
	switch env.Type {
	case msgRateLimitEvent:
		if env.RateLimitInfo != nil && env.RateLimitInfo.Status == "rejected" {
			e.rejected = env.RateLimitInfo
			return
		}
		e.rejected = nil
	case msgAssistant:
		// Sub-agent messages carry a parent tool use id and their
		// failures belong to that call, not to this request.
		if env.ParentToolUseID != nil {
			return
		}
		if env.Error != "" {
			e.code = env.Error
		}
	}
}

// result returns the error the turn failed with, or nil when it succeeded.
func (e *errorTracker) result(env *envelope) error {
	if env == nil || env.Type != msgResult {
		return nil
	}
	status := resultStatus(env)
	if env.Subtype == "success" && !env.IsError && (status == nil || *status < 400) {
		return nil
	}
	if text := e.failureText(env, status); text != "" {
		return errors.New(text)
	}
	if status != nil && *status != 0 {
		return fmt.Errorf("the Claude Code CLI could not finish its reply (HTTP %d)", *status)
	}
	return errors.New("the Claude Code CLI could not finish its reply")
}

// failureText builds the message shown to the user for a failed turn.
func (e *errorTracker) failureText(env *envelope, status *int) string {
	failure := resultText(env)
	if status != nil && *status >= 500 {
		if *status == 529 {
			failure = "Anthropic is overloaded (529)." +
				" Claude Code exhausted its retries. Try again shortly."
		} else {
			failure = fmt.Sprintf("Anthropic returned HTTP %d.", *status) +
				" Claude Code exhausted its retries. Try again shortly."
		}
	}
	limited := e.code == errorCodeRateLimit ||
		(e.rejected != nil && status != nil && *status == 429)
	text := failure
	if limited && text == "" {
		text = "Claude Code subscription limit reached"
		if window := e.limitWindow(); window != "" {
			text += " (" + window + ")"
		}
		text += "."
	}
	if limited && e.rejected != nil && e.rejected.ResetsAt > 0 {
		reset := time.Unix(e.rejected.ResetsAt, 0).UTC().Format(time.RFC3339)
		text += " Resets at " + reset + "."
	}
	if e.code == errorCodeBilling {
		text += " Check your Claude plan and extra-usage settings."
	}
	return text
}

// limitWindow names the exhausted subscription window, if the CLI reported
// one.
func (e *errorTracker) limitWindow() string {
	if e.rejected == nil {
		return ""
	}
	return strings.ReplaceAll(e.rejected.RateLimitType, "_", " ")
}

// resultStatus is the HTTP status of the API call behind the result. The CLI
// only reports it for successful turns; failures describe themselves through
// the errors list.
func resultStatus(env *envelope) *int {
	if env.Subtype != "success" {
		return nil
	}
	return env.APIErrorStatus
}

// resultText is the turn's own explanation, with the CLI's login hint
// rewritten to a command that works outside the CLI.
func resultText(env *envelope) string {
	var text string
	if env.Subtype == "success" {
		text = errorRawText(env.Result)
	} else {
		text = strings.Join(env.Errors, "\n")
	}
	return strings.ReplaceAll(text, "Please run /login", "Run `claude auth login` in a terminal")
}

// rawString decodes a JSON string payload, falling back to its literal text
// when the CLI sent something else.
func errorRawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return s
}
