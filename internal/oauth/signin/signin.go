// Package signin runs the provider OAuth sign-in flows without a UI, so
// the terminal dialogs and the web gateway share one implementation.
package signin

import (
	"context"
	"errors"

	"github.com/charmbracelet/crush/internal/oauth"
)

// Challenge is what the user does to finish a sign-in: type UserCode at
// VerificationURL (device flows) or open VerificationURL (browser flows).
// DeviceCode is the poll secret; it never leaves the process.
type Challenge struct {
	DeviceCode      string
	UserCode        string
	VerificationURL string
	ExpiresIn       int
	Interval        int
}

// Flow is one provider's OAuth sign-in.
type Flow interface {
	// Name is the provider name the dialogs show, such as "GitHub Copilot".
	Name() string
	// Start begins the sign-in and returns the first challenge.
	Start(ctx context.Context) (Challenge, error)
	// Wait blocks until the token arrives. A flow that falls back to
	// another method returns the next challenge to show instead; the
	// caller shows it and calls Wait again with it.
	Wait(ctx context.Context, c Challenge) (tok *oauth.Token, next *Challenge, err error)
	// CodeEntry reports whether the sign-in can finish with a code pasted
	// back from the authorization page. It is a capability, not live
	// state: a flow that fell back to a device challenge still reports
	// true, and SubmitCode still tries the browser flow, so the caller
	// decides from the challenge in force rather than from this.
	CodeEntry() bool
	// SubmitCode exchanges a pasted code or callback URL for a token.
	SubmitCode(ctx context.Context, code string) (*oauth.Token, error)
	// Close releases the callback listener of a browser flow.
	Close()
}

var (
	// ErrUnsupported means the provider signs in with an API key only.
	ErrUnsupported = errors.New("this provider signs in with an API key")
	// ErrNoBrowserFlow means a code was submitted with no browser flow running.
	ErrNoBrowserFlow = errors.New("no browser flow in progress")
)

// New returns the flow for a provider id: hyper, copilot, openai or xai.
func New(providerID string) (Flow, error) {
	switch providerID {
	case "hyper":
		return NewHyper(), nil
	case "copilot":
		return NewCopilot(), nil
	case "openai":
		return NewOpenAI(), nil
	case "xai":
		return NewGrok(), nil
	default:
		return nil, ErrUnsupported
	}
}

// browserFlow is the loopback flow a browser sign-in holds, kept small so
// tests can substitute it without opening a listener.
type browserFlow interface {
	// StartURL is the local handoff page to open in a browser.
	StartURL() string
	// Wait blocks until the browser delivers the authorization result.
	Wait(ctx context.Context) (*oauth.Token, error)
	// CompleteWithCode finishes the flow with a pasted code.
	CompleteWithCode(ctx context.Context, code string) (*oauth.Token, error)
	// Close releases the callback listener.
	Close()
}
