package signin

import (
	"context"
	"fmt"

	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/grok"
)

// grokDeps are the provider calls the Grok flow makes, so tests can run it
// without a network or a browser.
type grokDeps struct {
	startBrowser  func() (browserFlow, error)
	requestDevice func(ctx context.Context) (*grok.DeviceCode, error)
	pollDevice    func(ctx context.Context, deviceCode string, expiresIn int) (*oauth.Token, error)
}

// NewGrok returns the Grok sign-in: the loopback browser flow first, with
// the device flow as a fallback.
func NewGrok() Flow {
	return newGrok(grokDeps{
		startBrowser: func() (browserFlow, error) {
			flow, err := grok.StartBrowserFlow()
			if err != nil {
				return nil, err
			}
			return flow, nil
		},
		requestDevice: grok.RequestDeviceCode,
		pollDevice:    grok.PollForToken,
	})
}

func newGrok(deps grokDeps) Flow {
	return &grokFlow{deps: deps}
}

type grokFlow struct {
	deps grokDeps
	flow browserFlow
}

func (f *grokFlow) Name() string { return "Grok" }

func (f *grokFlow) Start(ctx context.Context) (Challenge, error) {
	flow, err := f.deps.startBrowser()
	if err == nil {
		f.flow = flow
		return Challenge{VerificationURL: flow.StartURL()}, nil
	}

	// The loopback callback listener could not start (port unavailable,
	// remote machine): go straight to the device flow, where the user
	// opens the verification page and enters the code themselves.
	dc, deviceErr := f.deps.requestDevice(ctx)
	if deviceErr != nil {
		return Challenge{}, fmt.Errorf("failed to start browser auth: %w (device auth: %v)", err, deviceErr)
	}
	return grokChallenge(dc), nil
}

func (f *grokFlow) Wait(ctx context.Context, c Challenge) (*oauth.Token, *Challenge, error) {
	if c.DeviceCode != "" {
		tok, err := f.deps.pollDevice(ctx, c.DeviceCode, c.ExpiresIn)
		if err != nil {
			return nil, nil, err
		}
		return tok, nil, nil
	}

	if f.flow == nil {
		return nil, nil, ErrNoBrowserFlow
	}
	tok, err := f.flow.Wait(ctx)
	if err == nil {
		return tok, nil, nil
	}
	if ctx.Err() != nil {
		return nil, nil, err
	}

	// The browser permission was declined (or the callback never
	// arrived): offer the device flow instead, whose code the user
	// enters on accounts.x.ai to finish signing in.
	dc, deviceErr := f.deps.requestDevice(ctx)
	if deviceErr != nil {
		return nil, nil, err
	}
	next := grokChallenge(dc)
	return nil, &next, nil
}

// CodeEntry is a capability, not a state: the TUI dialog builds its paste
// field from it once, before Start runs, and SubmitCode reports
// ErrNoBrowserFlow while the device flow is in force. The device branch
// shows a user code, which turns code entry off in the dialog anyway.
func (f *grokFlow) CodeEntry() bool { return true }

func (f *grokFlow) SubmitCode(ctx context.Context, code string) (*oauth.Token, error) {
	if f.flow == nil {
		return nil, ErrNoBrowserFlow
	}
	return f.flow.CompleteWithCode(ctx, code)
}

func (f *grokFlow) Close() {
	if f.flow != nil {
		f.flow.Close()
	}
}

// grokChallenge turns a device authorization into the challenge the user
// sees.
func grokChallenge(dc *grok.DeviceCode) Challenge {
	return Challenge{
		DeviceCode:      dc.DeviceCode,
		UserCode:        dc.UserCode,
		VerificationURL: dc.VerificationURI,
		ExpiresIn:       dc.ExpiresIn,
		Interval:        dc.Interval,
	}
}
