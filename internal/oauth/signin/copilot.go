package signin

import (
	"context"
	"fmt"

	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/copilot"
)

// copilotDeps are the provider calls the Copilot flow makes, so tests can
// run it without a network.
type copilotDeps struct {
	requestDevice func(ctx context.Context) (*copilot.DeviceCode, error)
	poll          func(ctx context.Context, dc *copilot.DeviceCode) (*oauth.Token, error)
}

// NewCopilot returns the GitHub Copilot device sign-in.
func NewCopilot() Flow {
	return newCopilot(copilotDeps{
		requestDevice: copilot.RequestDeviceCode,
		poll:          copilot.PollForToken,
	})
}

func newCopilot(deps copilotDeps) Flow {
	return &copilotFlow{deps: deps}
}

type copilotFlow struct {
	deps       copilotDeps
	deviceCode *copilot.DeviceCode
}

func (f *copilotFlow) Name() string { return "GitHub Copilot" }

func (f *copilotFlow) Start(ctx context.Context) (Challenge, error) {
	dc, err := f.deps.requestDevice(ctx)
	if err != nil {
		return Challenge{}, fmt.Errorf("failed to initiate device auth: %w", err)
	}
	f.deviceCode = dc
	return Challenge{
		DeviceCode:      dc.DeviceCode,
		UserCode:        dc.UserCode,
		VerificationURL: dc.VerificationURI,
		ExpiresIn:       dc.ExpiresIn,
		Interval:        dc.Interval,
	}, nil
}

func (f *copilotFlow) Wait(ctx context.Context, _ Challenge) (*oauth.Token, *Challenge, error) {
	tok, err := f.deps.poll(ctx, f.deviceCode)
	if err != nil {
		return nil, nil, err
	}
	return tok, nil, nil
}

func (f *copilotFlow) CodeEntry() bool { return false }

func (f *copilotFlow) SubmitCode(context.Context, string) (*oauth.Token, error) {
	return nil, ErrNoBrowserFlow
}

func (f *copilotFlow) Close() {}
