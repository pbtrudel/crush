package signin

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/crush/internal/oauth"
	"github.com/charmbracelet/crush/internal/oauth/hyper"
)

// hyperDeps are the provider calls the Hyper flow makes, so tests can run
// it without a network.
type hyperDeps struct {
	initiate   func(ctx context.Context) (*hyper.DeviceAuthResponse, error)
	poll       func(ctx context.Context, deviceCode string, expiresIn int) (string, error)
	exchange   func(ctx context.Context, refreshToken string) (*oauth.Token, error)
	introspect func(ctx context.Context, accessToken string) (*hyper.IntrospectTokenResponse, error)
}

// NewHyper returns the Hyper device sign-in.
func NewHyper() Flow {
	return newHyper(hyperDeps{
		initiate:   hyper.InitiateDeviceAuth,
		poll:       hyper.PollForToken,
		exchange:   hyper.ExchangeToken,
		introspect: hyper.IntrospectToken,
	})
}

func newHyper(deps hyperDeps) Flow {
	return &hyperFlow{deps: deps}
}

type hyperFlow struct {
	deps hyperDeps
}

func (f *hyperFlow) Name() string { return "Hyper" }

func (f *hyperFlow) Start(ctx context.Context) (Challenge, error) {
	resp, err := f.deps.initiate(ctx)
	if err != nil {
		return Challenge{}, fmt.Errorf("failed to initiate device auth: %w", err)
	}
	return Challenge{
		DeviceCode:      resp.DeviceCode,
		UserCode:        resp.UserCode,
		VerificationURL: resp.VerificationURL,
		ExpiresIn:       resp.ExpiresIn,
	}, nil
}

func (f *hyperFlow) Wait(ctx context.Context, c Challenge) (*oauth.Token, *Challenge, error) {
	refreshToken, err := f.deps.poll(ctx, c.DeviceCode, c.ExpiresIn)
	if err != nil {
		return nil, nil, err
	}

	tok, err := f.deps.exchange(ctx, refreshToken)
	if err != nil {
		return nil, nil, fmt.Errorf("token exchange failed: %w", err)
	}

	introspection, err := f.deps.introspect(ctx, tok.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("token introspection failed: %w", err)
	}
	if !introspection.Active {
		return nil, nil, errors.New("access token is not active")
	}

	return tok, nil, nil
}

func (f *hyperFlow) CodeEntry() bool { return false }

func (f *hyperFlow) SubmitCode(context.Context, string) (*oauth.Token, error) {
	return nil, ErrNoBrowserFlow
}

func (f *hyperFlow) Close() {}
